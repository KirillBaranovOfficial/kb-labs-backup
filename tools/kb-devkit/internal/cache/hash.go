// Package cache implements content-addressable caching for task execution.
package cache

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

//go:embed tool-inputs.mjs
var toolInputReporter []byte

// HashInputs computes a deterministic SHA256 hash over all files matching
// the given glob patterns.
//
// Patterns without a leading "^" are resolved relative to pkgDir.
// Patterns with a leading "^" are resolved relative to each dir in depDirs —
// this makes the hash sensitive to workspace dependency outputs (e.g. dist/).
// depDirs may be nil; "^" patterns are then no-ops (no files matched).
//
// Hash keys for dep files are expressed as paths relative to pkgDir
// (e.g. "../../../core/dist/index.js") so the hash is machine-independent
// and safe to use with future remote cache backends.
//
// Hash input: sorted list of (unique path key + content) for every matched file.
// Skips node_modules, .git, dist, .turbo, coverage automatically.
func HashInputs(pkgDir string, depDirs []string, patterns []string) (string, error) {
	return HashInputsWithFingerprint(pkgDir, depDirs, patterns, "")
}

// HashInputsWithFingerprint is HashInputs with an additional stable task
// fingerprint. The fingerprint is used for inputs which aren't files in a
// package (the command, cache format, and toolchain). Keeping it in the same
// digest prevents a remote CAS entry from surviving a semantic task change.
func HashInputsWithFingerprint(pkgDir string, depDirs []string, patterns []string, fingerprint string) (string, error) {
	return hashInputs(pkgDir, depDirs, patterns, fingerprint, toolInputContext{})
}

type toolInputContext struct {
	WorkspaceRoot string
	Language      string
	TaskName      string
}

// HashInputsWithToolInputs extends the task digest with files resolved by the
// task's compiler or test runner, using the same content hash as declared files.
func HashInputsWithToolInputs(pkgDir, workspaceRoot string, depDirs []string, patterns []string, fingerprint, language, taskName string) (string, error) {
	return hashInputs(pkgDir, depDirs, patterns, fingerprint, toolInputContext{
		WorkspaceRoot: workspaceRoot,
		Language:      language,
		TaskName:      taskName,
	})
}

func hashInputs(pkgDir string, depDirs []string, patterns []string, fingerprint string, toolContext toolInputContext) (string, error) {
	type entry struct {
		key  string // sort key written into hash (unique across local + dep files)
		path string // absolute path for reading
	}

	seen := make(map[string]entry)

	// Partition patterns once to avoid per-pattern WalkDir calls.
	var localPatterns, depPatterns []string
	for _, p := range patterns {
		if strings.HasPrefix(p, "^") {
			depPatterns = append(depPatterns, strings.TrimPrefix(p, "^"))
		} else {
			localPatterns = append(localPatterns, p)
		}
	}

	// One expandGlobs call for all local patterns (single WalkDir over pkgDir).
	if len(localPatterns) > 0 {
		rels, err := expandGlobs(pkgDir, localPatterns, true)
		if err != nil {
			return "", err
		}
		for _, rel := range rels {
			if _, dup := seen[rel]; !dup {
				seen[rel] = entry{key: rel, path: filepath.Join(pkgDir, rel)}
			}
		}
	}

	// Hash each direct workspace dependency's published/importable surface in
	// addition to declared generated outputs. package.json is the producer's
	// contract for which files consumers can read.
	for _, depDir := range depDirs {
		rels, err := packageSurfaceFiles(depDir)
		if err != nil {
			return "", err
		}
		for _, rel := range rels {
			absPath := filepath.Join(depDir, rel)
			relKey, err := filepath.Rel(pkgDir, absPath)
			if err != nil {
				relKey = absPath
			}
			relKey = filepath.ToSlash(relKey)
			if _, dup := seen[relKey]; !dup {
				seen[relKey] = entry{key: relKey, path: absPath}
			}
		}
	}

	// Tool reporters use the compiler and test runner's resolved input data.
	// Their results enter the same digest as declared files and dependency surfaces.
	toolInputs, err := reportToolInputs(pkgDir, patterns, toolContext)
	if err != nil {
		return "", err
	}
	for _, absPath := range toolInputs {
		relKey, err := filepath.Rel(pkgDir, absPath)
		if err != nil {
			relKey = absPath
		}
		relKey = filepath.ToSlash(relKey)
		if _, dup := seen[relKey]; !dup {
			seen[relKey] = entry{key: relKey, path: absPath}
		}
	}

	// One expandGlobs call per dep dir for declared dependency output patterns.
	for _, depDir := range depDirs {
		if len(depPatterns) == 0 {
			break
		}
		rels, err := expandGlobs(depDir, depPatterns, false)
		if err != nil {
			return "", err
		}
		for _, rel := range rels {
			absPath := filepath.Join(depDir, rel)
			// Key is relative to pkgDir — machine-independent across checkouts.
			// Falls back to absPath only if the two dirs are on different drives (Windows).
			relKey, err := filepath.Rel(pkgDir, absPath)
			if err != nil {
				relKey = absPath
			}
			relKey = filepath.ToSlash(relKey)
			if _, dup := seen[relKey]; !dup {
				seen[relKey] = entry{key: relKey, path: absPath}
			}
		}
	}

	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	h := sha256.New()
	if fingerprint != "" {
		_, _ = io.WriteString(h, "task-fingerprint\x00")
		_, _ = io.WriteString(h, fingerprint)
		_, _ = io.WriteString(h, "\x00")
	}
	for _, key := range keys {
		e := seen[key]
		_, _ = io.WriteString(h, e.key+"\x00")

		f, err := os.Open(e.path)
		if err != nil {
			return "", err
		}
		_, err = io.Copy(h, f)
		f.Close()
		if err != nil {
			return "", err
		}
		_, _ = io.WriteString(h, "\x00")
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

type toolInputReport struct {
	Tool          string   `json:"tool"`
	WorkspaceRoot string   `json:"workspaceRoot"`
	PackageRoot   string   `json:"packageRoot"`
	ConfigFiles   []string `json:"configFiles,omitempty"`
}

type cachedToolReportEntry struct {
	files     []string
	signature string
}

var toolReportCache = struct {
	sync.Mutex
	reports map[string]cachedToolReportEntry
}{reports: make(map[string]cachedToolReportEntry)}

func reportToolInputs(pkgDir string, patterns []string, context toolInputContext) ([]string, error) {
	var inputs []string
	goInputs, err := goBuildInputFiles(pkgDir)
	if err != nil {
		return nil, err
	}
	inputs = append(inputs, goInputs...)
	if context.Language != "typescript" {
		return inputs, nil
	}

	for _, pattern := range patterns {
		if filepath.Ext(pattern) != ".json" || !strings.ContainsAny(pattern, "*?[") {
			continue
		}
		roots, err := expandGlobs(pkgDir, []string{pattern}, true)
		if err != nil {
			return nil, err
		}
		for _, root := range roots {
			root = filepath.Join(pkgDir, root)
			files, err := cachedToolReport("typescript\x00"+root, func() ([]string, error) {
				return runToolInputReporter(toolInputReport{
					Tool:          "typescript",
					WorkspaceRoot: context.WorkspaceRoot,
					PackageRoot:   pkgDir,
					ConfigFiles:   []string{root},
				}, root)
			})
			if err != nil {
				return nil, err
			}
			inputs = append(inputs, files...)
		}
	}

	if context.TaskName == "test" || context.TaskName == "test:coverage" {
		files, err := cachedToolReport("vitest\x00"+pkgDir, func() ([]string, error) {
			return runToolInputReporter(toolInputReport{
				Tool:          "vitest",
				WorkspaceRoot: context.WorkspaceRoot,
				PackageRoot:   pkgDir,
			}, pkgDir)
		})
		if err != nil {
			return nil, err
		}
		inputs = append(inputs, files...)
	}
	return inputs, nil
}

func cachedToolReport(key string, resolve func() ([]string, error)) ([]string, error) {
	toolReportCache.Lock()
	defer toolReportCache.Unlock()
	if cached, ok := toolReportCache.reports[key]; ok {
		signature, err := fileSetSignature(cached.files)
		if err == nil && signature == cached.signature {
			return append([]string(nil), cached.files...), nil
		}
	}
	files, err := resolve()
	if err != nil {
		return nil, err
	}
	files = uniqueSorted(files)
	signature, err := fileSetSignature(files)
	if err != nil {
		return nil, err
	}
	toolReportCache.reports[key] = cachedToolReportEntry{files: files, signature: signature}
	return append([]string(nil), files...), nil
}

func fileSetSignature(files []string) (string, error) {
	files = uniqueSorted(files)
	h := sha256.New()
	for _, file := range files {
		content, err := os.ReadFile(file)
		if err != nil {
			return "", err
		}
		_, _ = io.WriteString(h, file+"\x00")
		_, _ = h.Write(content)
		_, _ = io.WriteString(h, "\x00")
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func runToolInputReporter(request toolInputReport, signatureRoot string) ([]string, error) {
	requestBytes, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	command := exec.Command("node", "--input-type=module", "-e", string(toolInputReporter))
	command.Dir = request.WorkspaceRoot
	command.Stdin = strings.NewReader(string(requestBytes))
	output, err := command.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
			return nil, fmt.Errorf("%s input report failed: %w: %s", request.Tool, err, strings.TrimSpace(string(exitErr.Stderr)))
		}
		return nil, fmt.Errorf("%s input report failed: %w", request.Tool, err)
	}
	if request.Tool == "vitest" {
		var files []string
		if err := json.Unmarshal(output, &files); err != nil {
			return nil, err
		}
		return files, nil
	}
	var result map[string][]string
	if err := json.Unmarshal(output, &result); err != nil {
		return nil, err
	}
	files, ok := result[signatureRoot]
	if !ok {
		return nil, os.ErrNotExist
	}
	return files, nil
}

func uniqueSorted(files []string) []string {
	seen := make(map[string]struct{}, len(files))
	result := make([]string, 0, len(files))
	for _, file := range files {
		file = filepath.Clean(file)
		if _, ok := seen[file]; ok {
			continue
		}
		seen[file] = struct{}{}
		result = append(result, file)
	}
	sort.Strings(result)
	return result
}

type packageManifest struct {
	Exports json.RawMessage `json:"exports"`
	Files   []string        `json:"files"`
}

func packageSurfaceFiles(root string) ([]string, error) {
	manifestPath := filepath.Join(root, "package.json")
	content, err := os.ReadFile(manifestPath)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var manifest packageManifest
	if err := json.Unmarshal(content, &manifest); err != nil {
		return nil, err
	}
	patterns := append([]string(nil), manifest.Files...)
	if len(manifest.Exports) > 0 {
		if err := collectExportTargets(manifest.Exports, &patterns); err != nil {
			return nil, err
		}
	}
	if len(patterns) == 0 {
		return []string{"package.json"}, nil
	}
	patterns = append(patterns, "package.json")
	for index, pattern := range patterns {
		pattern = filepath.ToSlash(strings.TrimPrefix(pattern, "./"))
		if filepath.IsAbs(pattern) || pattern == ".." || strings.HasPrefix(pattern, "../") {
			return nil, os.ErrInvalid
		}
		if info, statErr := os.Stat(filepath.Join(root, filepath.FromSlash(pattern))); statErr == nil && info.IsDir() {
			pattern = strings.TrimSuffix(pattern, "/") + "/**"
		}
		patterns[index] = pattern
	}
	return expandSurfaceFiles(root, patterns)
}

func collectExportTargets(raw json.RawMessage, targets *[]string) error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var target string
	if err := json.Unmarshal(raw, &target); err == nil {
		if strings.HasPrefix(target, "./") {
			*targets = append(*targets, target)
		}
		return nil
	}
	var values []json.RawMessage
	if err := json.Unmarshal(raw, &values); err == nil {
		for _, value := range values {
			if err := collectExportTargets(value, targets); err != nil {
				return err
			}
		}
		return nil
	}
	var conditions map[string]json.RawMessage
	if err := json.Unmarshal(raw, &conditions); err != nil {
		return err
	}
	for _, value := range conditions {
		if err := collectExportTargets(value, targets); err != nil {
			return err
		}
	}
	return nil
}

func expandSurfaceFiles(root string, patterns []string) ([]string, error) {
	matched := make(map[string]struct{})
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && path != root && (entry.Name() == ".git" || entry.Name() == "node_modules") {
			return filepath.SkipDir
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		for _, pattern := range patterns {
			if matchGlob(pattern, filepath.ToSlash(rel)) {
				matched[rel] = struct{}{}
				break
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	result := make([]string, 0, len(matched))
	for rel := range matched {
		result = append(result, rel)
	}
	sort.Strings(result)
	return result, nil
}

func goBuildInputFiles(pkgDir string) ([]string, error) {
	if _, err := os.Stat(filepath.Join(pkgDir, "go.mod")); os.IsNotExist(err) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	command := exec.Command("go", "list", "-deps", "-json", "./...")
	command.Dir = pkgDir
	output, err := command.Output()
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(strings.NewReader(string(output)))
	var inputs []string
	for {
		var pkg struct {
			Dir    string `json:"Dir"`
			Module *struct {
				Main    bool   `json:"Main"`
				Dir     string `json:"Dir"`
				Replace *struct {
					Dir string `json:"Dir"`
				} `json:"Replace"`
			} `json:"Module"`
			GoFiles    []string `json:"GoFiles"`
			CgoFiles   []string `json:"CgoFiles"`
			CFiles     []string `json:"CFiles"`
			CXXFiles   []string `json:"CXXFiles"`
			MFiles     []string `json:"MFiles"`
			HFiles     []string `json:"HFiles"`
			FFiles     []string `json:"FFiles"`
			SFiles     []string `json:"SFiles"`
			SwigFiles  []string `json:"SwigFiles"`
			SysoFiles  []string `json:"SysoFiles"`
			EmbedFiles []string `json:"EmbedFiles"`
		}
		if err := decoder.Decode(&pkg); err == io.EOF {
			break
		} else if err != nil {
			return nil, err
		}
		localModuleDir := ""
		if pkg.Module != nil && pkg.Module.Main {
			localModuleDir = pkg.Module.Dir
		} else if pkg.Module != nil && pkg.Module.Replace != nil {
			localModuleDir = pkg.Module.Replace.Dir
		}
		if localModuleDir == "" {
			continue
		}
		if pkg.Module != nil && pkg.Module.Replace != nil && pkg.Module.Replace.Dir != "" {
			for _, manifest := range []string{"go.mod", "go.sum"} {
				path := filepath.Join(pkg.Module.Replace.Dir, manifest)
				if _, err := os.Stat(path); err == nil {
					inputs = append(inputs, path)
				} else if !os.IsNotExist(err) {
					return nil, err
				}
			}
		}
		for _, files := range [][]string{pkg.GoFiles, pkg.CgoFiles, pkg.CFiles, pkg.CXXFiles, pkg.MFiles, pkg.HFiles, pkg.FFiles, pkg.SFiles, pkg.SwigFiles, pkg.SysoFiles, pkg.EmbedFiles} {
			for _, file := range files {
				inputs = append(inputs, filepath.Join(pkg.Dir, file))
			}
		}
	}
	return inputs, nil
}

// expandGlobs returns sorted relative paths of all files matching any pattern.
// Patterns use simple glob syntax: ** matches any number of path segments,
// * matches within one segment.
// skipDist controls whether the "dist" directory is pruned during traversal.
// Use true for local package dirs (dist is a generated output, not an input),
// false for dep dirs (dist is exactly what we want to hash via "^dist/**").
func expandGlobs(root string, patterns []string, skipDist bool) ([]string, error) {
	seen := make(map[string]bool)

	for _, pattern := range patterns {
		err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}

			rel, _ := filepath.Rel(root, path)

			if d.IsDir() {
				if shouldSkipDir(d.Name(), skipDist) {
					return filepath.SkipDir
				}
				return nil
			}

			if matchGlob(pattern, rel) {
				seen[rel] = true
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}

	result := make([]string, 0, len(seen))
	for rel := range seen {
		result = append(result, rel)
	}
	sort.Strings(result)
	return result, nil
}

// matchGlob matches a path against a glob pattern.
// Supports: *, **, literal segments.
// Uses / as separator regardless of OS.
func matchGlob(pattern, path string) bool {
	// Normalize to forward slashes.
	pattern = filepath.ToSlash(pattern)
	path = filepath.ToSlash(path)

	return matchGlobParts(strings.Split(pattern, "/"), strings.Split(path, "/"))
}

func matchGlobParts(patParts, pathParts []string) bool {
	for len(patParts) > 0 {
		pat := patParts[0]
		patParts = patParts[1:]

		if pat == "**" {
			// ** matches zero or more path segments.
			// Try matching the rest of pattern against every suffix of pathParts.
			for i := 0; i <= len(pathParts); i++ {
				if matchGlobParts(patParts, pathParts[i:]) {
					return true
				}
			}
			return false
		}

		if len(pathParts) == 0 {
			return false
		}

		if !matchSingleSeg(pat, pathParts[0]) {
			return false
		}
		pathParts = pathParts[1:]
	}

	return len(pathParts) == 0
}

// matchSingleSeg matches a single path segment against a pattern segment.
// Supports * as wildcard within one segment.
func matchSingleSeg(pat, seg string) bool {
	if pat == "*" {
		return true
	}
	// filepath.Match handles * within a single segment.
	ok, _ := filepath.Match(pat, seg)
	return ok
}

func shouldSkipDir(name string, skipDist bool) bool {
	switch name {
	case "node_modules", ".git", ".turbo", "coverage", "__pycache__", ".cache":
		return true
	case "dist":
		return skipDist
	}
	return strings.HasPrefix(name, ".")
}
