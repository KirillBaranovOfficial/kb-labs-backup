package engine

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/kb-labs/devkit/internal/cache"
	"github.com/kb-labs/devkit/internal/config"
	"github.com/kb-labs/devkit/internal/workspace"
)

func TestRunE2ECachesAndRestoresOutputs(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceRoot(t, root, []string{"packages/*"})
	writePkg(t, filepath.Join(root, "packages", "app"), `{"name":"@kb/app"}`)
	writeEngineFile(t, filepath.Join(root, "packages", "app", "input.txt"), "hello\n")

	cfg := testConfig([]config.NamedCategory{
		{Name: "libs", Category: config.CategoryConfig{Match: []string{"packages/*"}, Preset: "node-lib"}},
	})
	cfg.Tasks = map[string]config.TaskConfig{
		"build": {
			{
				Command: `sh -c "mkdir -p dist && cp input.txt dist/output.txt"`,
				Inputs:  []string{"input.txt"},
				Outputs: []string{"dist/**"},
			},
		},
	}

	ws := mustWorkspace(t, root, cfg)
	cacheRoot := filepath.Join(root, ".kb", "devkit-cache")

	first, err := Run(ws, cfg, RunOptions{
		Tasks:     []string{"build"},
		WSRoot:    root,
		CacheRoot: cacheRoot,
	})
	if err != nil {
		t.Fatalf("first Run error: %v", err)
	}
	if !first.OK || len(first.Results) != 1 || first.Results[0].Cached {
		t.Fatalf("unexpected first result: %+v", first)
	}

	outputPath := filepath.Join(root, "packages", "app", "dist", "output.txt")
	if _, err := os.Stat(outputPath); err != nil {
		t.Fatalf("expected output after first run: %v", err)
	}
	if err := os.Remove(outputPath); err != nil {
		t.Fatalf("remove output: %v", err)
	}

	second, err := Run(ws, cfg, RunOptions{
		Tasks:     []string{"build"},
		WSRoot:    root,
		CacheRoot: cacheRoot,
	})
	if err != nil {
		t.Fatalf("second Run error: %v", err)
	}
	if !second.OK || len(second.Results) != 1 || !second.Results[0].Cached {
		t.Fatalf("unexpected second result: %+v", second)
	}

	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read restored output: %v", err)
	}
	if string(data) != "hello\n" {
		t.Fatalf("restored output = %q, want %q", data, "hello\n")
	}

	summary := second.Summary()
	if summary.Scheduled != 1 || summary.Cached != 1 || summary.Passed != 0 || summary.Failed != 0 || summary.Blocked != 0 || summary.NotAffected != 0 {
		t.Fatalf("unexpected summary: %+v", summary)
	}
}

// case: S-261.4
func TestRunE2ECachedOutputsPreserveFileModes(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceRoot(t, root, []string{"packages/*"})
	pkgDir := filepath.Join(root, "packages", "app")
	writePkg(t, pkgDir, `{"name":"@kb/app"}`)

	cfg := testConfig([]config.NamedCategory{
		{Name: "libs", Category: config.CategoryConfig{Match: []string{"packages/*"}, Preset: "node-lib"}},
	})
	cfg.Tasks = map[string]config.TaskConfig{
		"build": {{
			Command: `sh -c "mkdir -p dist && printf regular > dist/regular.txt && printf executable > dist/run.sh && chmod 644 dist/regular.txt && chmod 755 dist/run.sh"`,
			Outputs: []string{"dist/**"},
		}},
	}
	ws := mustWorkspace(t, root, cfg)
	cacheRoot := filepath.Join(root, ".kb", "devkit-cache")
	run := func() RunResult {
		t.Helper()
		result, err := Run(ws, cfg, RunOptions{Tasks: []string{"build"}, WSRoot: root, CacheRoot: cacheRoot})
		if err != nil {
			t.Fatalf("Run error: %v", err)
		}
		return result
	}
	if result := run(); !result.OK || result.Results[0].Cached {
		t.Fatalf("first build = %+v, want fresh successful build", result)
	}
	for name := range map[string]os.FileMode{"regular.txt": 0o644, "run.sh": 0o755} {
		if err := os.Remove(filepath.Join(pkgDir, "dist", name)); err != nil {
			t.Fatalf("remove %s: %v", name, err)
		}
	}
	result := run()
	if !result.OK || !result.Results[0].Cached {
		t.Fatalf("second build = %+v, want cached successful build", result)
	}
	for name, want := range map[string]os.FileMode{"regular.txt": 0o644, "run.sh": 0o755} {
		info, err := os.Stat(filepath.Join(pkgDir, "dist", name))
		if err != nil {
			t.Fatalf("stat restored %s: %v", name, err)
		}
		if got := info.Mode().Perm(); got != want {
			t.Errorf("restored %s mode = %04o, want %04o", name, got, want)
		}
	}
	if err := filepath.WalkDir(filepath.Join(cacheRoot, "tasks"), func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var manifest map[string]any
		if err := json.Unmarshal(data, &manifest); err != nil {
			return err
		}
		outputs, ok := manifest["outputs"].([]any)
		if !ok {
			return nil
		}
		for _, output := range outputs {
			if ref, ok := output.(map[string]any); ok {
				delete(ref, "mode")
			}
		}
		data, err = json.Marshal(manifest)
		if err != nil {
			return err
		}
		return os.WriteFile(path, data, 0o644)
	}); err != nil {
		t.Fatalf("remove mode metadata from cached manifest: %v", err)
	}
	for _, name := range []string{"regular.txt", "run.sh"} {
		if err := os.Remove(filepath.Join(pkgDir, "dist", name)); err != nil {
			t.Fatalf("remove %s for legacy cache check: %v", name, err)
		}
	}
	legacy := run()
	if !legacy.OK || legacy.Results[0].Cached {
		t.Fatalf("legacy manifest run = %+v, want a successful rebuild after cache miss", legacy)
	}
}

// case: S-261.5
func TestAffectedBuildSelectsChangedInputsAfterMerge(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceRoot(t, root, []string{"packages/*"})
	coreDir := filepath.Join(root, "packages", "core")
	runtimeDir := filepath.Join(root, "packages", "runtime")
	appDir := filepath.Join(root, "packages", "app")
	writePkg(t, coreDir, `{"name":"@fixture/core"}`)
	writePkg(t, runtimeDir, `{"name":"@fixture/runtime","dependencies":{"@fixture/core":"workspace:*"}}`)
	writePkg(t, appDir, `{"name":"@fixture/app","dependencies":{"@fixture/runtime":"workspace:*"}}`)
	for _, dir := range []string{coreDir, runtimeDir, appDir} {
		writeEngineFile(t, filepath.Join(dir, "src", "input.txt"), "initial\n")
	}
	writeEngineFile(t, filepath.Join(coreDir, "README.md"), "package notes\n")
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("**/dist/\n.kb/\n"), 0o644); err != nil {
		t.Fatalf("write .gitignore: %v", err)
	}
	if err := os.WriteFile(filepath.Join(coreDir, "src", "input.txt"), []byte("initial\n"), 0o644); err != nil {
		t.Fatalf("write core input: %v", err)
	}
	for _, args := range [][]string{{"init", "-b", "main"}, {"config", "user.email", "devkit@example.test"}, {"config", "user.name", "kb-devkit test"}, {"add", "."}, {"commit", "-m", "initial packages"}} {
		runGitCommand(t, root, args...)
	}

	cfg := testConfig([]config.NamedCategory{
		{Name: "packages", Category: config.CategoryConfig{Match: []string{"packages/*"}, Preset: "node-lib"}},
	})
	cfg.Tasks = map[string]config.TaskConfig{
		"build": {{
			Command: `sh -c "mkdir -p dist && cat src/input.txt > dist/output.txt"`,
			Inputs:  []string{"src/**"},
			Outputs: []string{"dist/**"},
			Deps:    []string{"^build"},
		}},
	}
	ws := mustWorkspace(t, root, cfg)
	cacheRoot := filepath.Join(root, ".kb", "devkit")
	first, err := Run(ws, cfg, RunOptions{Tasks: []string{"build"}, WSRoot: root, CacheRoot: cacheRoot, Concurrency: 1})
	if err != nil || !first.OK {
		t.Fatalf("initial build = %+v, error = %v", first, err)
	}
	runGitCommand(t, root, "checkout", "-b", "feature")
	if err := os.WriteFile(filepath.Join(coreDir, "src", "input.txt"), []byte("merged change\n"), 0o644); err != nil {
		t.Fatalf("change core input: %v", err)
	}
	runGitCommand(t, root, "add", "packages/core/src/input.txt")
	runGitCommand(t, root, "commit", "-m", "change core input")
	runGitCommand(t, root, "checkout", "main")
	runGitCommand(t, root, "merge", "--no-ff", "feature", "-m", "merge feature")

	changed, err := AffectedPackages(ws, cfg)
	if err != nil {
		t.Fatalf("AffectedPackages: %v", err)
	}
	if len(changed) != 0 {
		t.Fatalf("committed merge has Git-diff packages %v, want none", packageNames(changed))
	}
	selected, err := AffectedRunPackages(ws, cfg, []string{"build"}, cacheRoot, false)
	if err != nil {
		t.Fatalf("AffectedRunPackages: %v", err)
	}
	want := []string{"@fixture/core", "@fixture/runtime", "@fixture/app"}
	got := packageNames(selected)
	sort.Strings(got)
	sort.Strings(want)
	if len(got) != len(want) || strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("--affected selected %v (git changes %v), want %v", got, packageNames(changed), want)
	}
	completed, err := Run(ws, cfg, RunOptions{Tasks: []string{"build"}, WSRoot: root, CacheRoot: cacheRoot, Concurrency: 1})
	if err != nil || !completed.OK {
		t.Fatalf("build after merged change = %+v, error = %v", completed, err)
	}
	if err := os.WriteFile(filepath.Join(coreDir, "README.md"), []byte("notes outside declared build inputs\n"), 0o644); err != nil {
		t.Fatalf("change file outside task inputs: %v", err)
	}
	diffOnly, err := AffectedPackages(ws, cfg)
	if err != nil {
		t.Fatalf("AffectedPackages after README change: %v", err)
	}
	diffOnlySelection, err := AffectedRunPackages(ws, cfg, []string{"build"}, cacheRoot, true)
	if err != nil {
		t.Fatalf("AffectedRunPackages with diff-only: %v", err)
	}
	unchangedInputs, err := AffectedRunPackages(ws, cfg, []string{"build"}, cacheRoot, false)
	if err != nil {
		t.Fatalf("AffectedRunPackages after README change: %v", err)
	}
	if len(diffOnly) == 0 || len(diffOnlySelection) != len(diffOnly) || len(unchangedInputs) != 0 {
		t.Fatalf("Git diff = %v, diff-only selection = %v, input-hash selection = %v; want a selected Git diff and empty input-hash selection", packageNames(diffOnly), packageNames(diffOnlySelection), packageNames(unchangedInputs))
	}
}

func runGitCommand(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}

func packageNames(packages []workspace.Package) []string {
	names := make([]string, len(packages))
	for i, pkg := range packages {
		names[i] = pkg.Name
	}
	return names
}

// case: S-179.18
func TestRunE2EDoesNotReplayCachedFailure(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceRoot(t, root, []string{"packages/*"})
	writePkg(t, filepath.Join(root, "packages", "app"), `{"name":"@kb/app"}`)

	cfg := testConfig([]config.NamedCategory{
		{Name: "libs", Category: config.CategoryConfig{Match: []string{"packages/*"}, Preset: "node-lib"}},
	})
	cfg.Tasks = map[string]config.TaskConfig{
		"build": {{
			Command: `sh -c "test -f ../dependency.ready"`,
			Outputs: []string{"dist/**"},
		}},
	}

	ws := mustWorkspace(t, root, cfg)
	cacheRoot := filepath.Join(root, ".kb", "devkit-cache")
	run := func() RunResult {
		t.Helper()
		result, err := Run(ws, cfg, RunOptions{
			Tasks:     []string{"build"},
			WSRoot:    root,
			CacheRoot: cacheRoot,
		})
		if err != nil {
			t.Fatalf("Run error: %v", err)
		}
		return result
	}

	first := run()
	if first.OK || first.Summary().Failed != 1 {
		t.Fatalf("first run = %+v, want one failed task", first)
	}

	second := run()
	if second.OK || len(second.Results) != 1 || second.Results[0].Cached {
		t.Fatalf("second run = %+v, want a fresh failed execution", second)
	}
	if summary := second.Summary(); summary.Scheduled != 1 || summary.Passed != 0 || summary.Failed != 1 || summary.Cached != 0 || summary.Blocked != 0 || summary.NotAffected != 0 {
		t.Fatalf("second summary = %+v, want total=1 passed=0 failed=1 cached=0", summary)
	}
	if err := os.WriteFile(filepath.Join(root, "packages", "dependency.ready"), nil, 0o644); err != nil {
		t.Fatalf("write dependency artifact: %v", err)
	}

	third := run()
	if !third.OK || len(third.Results) != 1 || third.Results[0].Cached {
		t.Fatalf("third run = %+v, want a fresh successful execution", third)
	}
	if summary := third.Summary(); summary.Scheduled != 1 || summary.Passed != 1 || summary.Failed != 0 || summary.Cached != 0 || summary.Blocked != 0 || summary.NotAffected != 0 {
		t.Fatalf("third summary = %+v, want total=1 passed=1 failed=0 cached=0", summary)
	}

	fourth := run()
	if !fourth.OK || len(fourth.Results) != 1 || !fourth.Results[0].Cached {
		t.Fatalf("fourth run = %+v, want the successful task cached", fourth)
	}
}

// case: S-179.23, S-179.37
func TestRunSummaryPartitionsScheduledTasksAndCountsBlockedDependents(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceRoot(t, root, []string{"packages/*"})
	writePkg(t, filepath.Join(root, "packages", "core"), `{"name":"@fixture/core"}`)
	writePkg(t, filepath.Join(root, "packages", "middle"), `{"name":"@fixture/middle","dependencies":{"@fixture/core":"workspace:*"}}`)
	writePkg(t, filepath.Join(root, "packages", "app"), `{"name":"@fixture/app","dependencies":{"@fixture/middle":"workspace:*"}}`)
	writePkg(t, filepath.Join(root, "packages", "independent"), `{"name":"@fixture/independent"}`)
	writePkg(t, filepath.Join(root, "packages", "untouched"), `{"name":"@fixture/untouched"}`)

	cfg := testConfig([]config.NamedCategory{
		{Name: "core", Category: config.CategoryConfig{Match: []string{"packages/core"}, Preset: "node-lib"}},
		{Name: "libraries", Category: config.CategoryConfig{Match: []string{"packages/middle", "packages/app", "packages/independent", "packages/untouched"}, Preset: "node-lib"}},
	})
	cfg.Tasks = map[string]config.TaskConfig{
		"build": {
			{Categories: []string{"core"}, Command: "false"},
			{Categories: []string{"libraries"}, Command: "true", Deps: []string{"^build"}},
		},
	}
	ws := mustWorkspace(t, root, cfg)
	var selected []workspace.Package
	for _, pkg := range ws.Packages {
		if pkg.Dir != filepath.Join(root, "packages", "untouched") {
			selected = append(selected, pkg)
		}
	}
	if len(selected) != 4 {
		t.Fatalf("selected packages = %+v; want core, middle, app, and independent", selected)
	}

	result, err := Run(ws, cfg, RunOptions{
		Tasks:     []string{"build"},
		Packages:  selected,
		Affected:  true,
		WSRoot:    root,
		CacheRoot: filepath.Join(root, ".kb", "devkit-cache"),
	})
	if err != nil || result.OK || len(result.Results) != 2 {
		t.Fatalf("failed dependency run = %+v, error = %v; want failed root and independent result", result, err)
	}
	summary := result.Summary()
	if summary.Scheduled != 4 || summary.Passed != 1 || summary.Cached != 0 || summary.Failed != 1 || summary.Blocked != 2 || summary.NotAffected != 1 {
		t.Fatalf("failed dependency summary = %+v; want scheduled=4 passed=1 failed=1 blocked=2 notAffected=1", summary)
	}
	if summary.Passed+summary.Cached+summary.Failed+summary.Blocked != summary.Scheduled {
		t.Fatalf("summary counts do not partition scheduled tasks: %+v", summary)
	}
	blocked := make(map[string]string, len(summary.BlockedTasks))
	for _, task := range summary.BlockedTasks {
		blocked[task.Package] = task.BlockedByPackage + ":" + task.BlockedByTask
	}
	if blocked["@fixture/middle"] != "@fixture/core:build" || blocked["@fixture/app"] != "@fixture/core:build" {
		t.Fatalf("blocked task details = %+v; want middle and app blocked-by @fixture/core:build", summary.BlockedTasks)
	}
	independentPassed := false
	for _, task := range result.Results {
		if task.Package == "@fixture/independent" && task.OK {
			independentPassed = true
		}
	}
	if result.NotAffected != 1 || !independentPassed {
		t.Fatalf("result = %+v; want independent branch passed and one not-affected task", result)
	}
}

// case: S-179.19
func TestRunIncludesDeclaredTaskDependenciesWhenOnlyTheDependentWasRequested(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceRoot(t, root, []string{"packages/*"})
	writePkg(t, filepath.Join(root, "packages", "package"), `{"name":"@fixture/package"}`)
	cfg := testConfig([]config.NamedCategory{
		{Name: "package", Category: config.CategoryConfig{Match: []string{"packages/package"}, Preset: "node-lib"}},
	})
	cfg.Tasks = map[string]config.TaskConfig{
		"prepare": {{Command: `sh -c "printf ready > prepared.txt"`}},
		"build":   {{Command: `sh -c "test -f prepared.txt"`, Deps: []string{"prepare"}}},
	}
	ws := mustWorkspace(t, root, cfg)
	nodes, err := buildDAG(ws.Packages, []string{"build"}, cfg, ws)
	if err != nil {
		t.Fatalf("buildDAG error: %v", err)
	}
	buildNode := nodes[nodeKey{pkg: "@fixture/package", task: "build"}]
	if len(buildNode.deps) != 1 || buildNode.deps[0] != (nodeKey{pkg: "@fixture/package", task: "prepare"}) {
		t.Fatalf("build dependencies = %+v; want same-package prepare task", buildNode.deps)
	}
	result, err := Run(ws, cfg, RunOptions{
		Tasks: []string{"build"}, WSRoot: root, CacheRoot: filepath.Join(root, ".kb", "devkit-cache"), Concurrency: 1,
	})
	if err != nil || !result.OK || len(result.Results) != 2 {
		t.Fatalf("Run = %+v, error = %v; want prepare then build", result, err)
	}
	if result.Results[0].Task != "prepare" || result.Results[1].Task != "build" {
		t.Fatalf("task results = %+v; want prepare before build", result.Results)
	}
}

func TestRunE2ERespectsCrossPackageDependencies(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceRoot(t, root, []string{"packages/*"})
	writePkg(t, filepath.Join(root, "packages", "core"), `{"name":"@kb/core"}`)
	writePkg(t, filepath.Join(root, "packages", "app"), `{"name":"@kb/app","dependencies":{"@kb/core":"workspace:*"}}`)

	cfg := testConfig([]config.NamedCategory{
		{Name: "libs", Category: config.CategoryConfig{Match: []string{"packages/*"}, Preset: "node-lib"}},
	})
	cfg.Tasks = map[string]config.TaskConfig{
		"build": {
			{
				Command: `sh -c "printf built > build.txt"`,
				Outputs: []string{"build.txt"},
			},
		},
		"verify": {
			{
				Command: `sh -c "test -f build.txt && test -f ../core/build.txt"`,
				Deps:    []string{"build", "^build"},
			},
		},
	}

	ws := mustWorkspace(t, root, cfg)

	var seen []string
	result, err := Run(ws, cfg, RunOptions{
		Tasks:       []string{"build", "verify"},
		WSRoot:      root,
		CacheRoot:   filepath.Join(root, ".kb", "devkit-cache"),
		Concurrency: 1,
		OnResult: func(r TaskResult, done, total int) {
			seen = append(seen, r.Package+":"+r.Task)
		},
	})
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if !result.OK {
		t.Fatalf("RunResult not OK: %+v", result)
	}
	if len(result.Results) != 4 {
		t.Fatalf("results count = %d, want 4", len(result.Results))
	}
	if len(seen) != 4 {
		t.Fatalf("OnResult count = %d, want 4", len(seen))
	}

	if _, err := os.Stat(filepath.Join(root, "packages", "core", "build.txt")); err != nil {
		t.Fatalf("core build artifact missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "packages", "app", "build.txt")); err != nil {
		t.Fatalf("app build artifact missing: %v", err)
	}
}

// case: S-179.37
func TestRunAffectedDependencyChainCompletesBeforeDependentTasks(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceRoot(t, root, []string{"packages/*"})
	coreDir := filepath.Join(root, "packages", "core")
	runtimeDir := filepath.Join(root, "packages", "runtime")
	appDir := filepath.Join(root, "packages", "app")
	writePkg(t, coreDir, `{"name":"@fixture/core"}`)
	writePkg(t, runtimeDir, `{"name":"@fixture/runtime","dependencies":{"@fixture/core":"workspace:*"}}`)
	writePkg(t, appDir, `{"name":"@fixture/app","dependencies":{"@fixture/runtime":"workspace:*"}}`)

	cfg := testConfig([]config.NamedCategory{
		{Name: "core", Category: config.CategoryConfig{Match: []string{"packages/core"}, Preset: "node-lib"}},
		{Name: "runtime", Category: config.CategoryConfig{Match: []string{"packages/runtime"}, Preset: "node-lib"}},
		{Name: "applications", Category: config.CategoryConfig{Match: []string{"packages/app"}, Preset: "node-lib"}},
	})
	cfg.Tasks = map[string]config.TaskConfig{
		"build": {
			{Categories: []string{"core"}, Command: `sh -c "mkdir -p dist && printf core > dist/ready"`},
			{Categories: []string{"runtime"}, Command: `sh -c "test -f ../core/dist/ready && mkdir -p dist && printf runtime > dist/ready"`, Deps: []string{"^build"}},
			{Categories: []string{"applications"}, Command: `sh -c "test -f ../runtime/dist/ready"`, Deps: []string{"^build"}},
		},
	}
	ws := mustWorkspace(t, root, cfg)
	selected := ws.FilterByName([]string{"@fixture/runtime", "@fixture/app"})
	var completed []string
	result, err := Run(ws, cfg, RunOptions{
		Tasks:       []string{"build"},
		Packages:    selected,
		WSRoot:      root,
		CacheRoot:   filepath.Join(root, ".kb", "devkit-cache"),
		Concurrency: 3,
		OnResult: func(result TaskResult, _, _ int) {
			completed = append(completed, result.Package)
		},
	})
	if err != nil || !result.OK {
		t.Fatalf("affected dependency build = %+v, error = %v", result, err)
	}
	want := []string{"@fixture/core", "@fixture/runtime", "@fixture/app"}
	if len(completed) != len(want) {
		t.Fatalf("completed tasks = %v, want %v", completed, want)
	}
	for i := range want {
		if completed[i] != want[i] {
			t.Fatalf("completed task order = %v, want %v", completed, want)
		}
	}
}

func TestRunE2EStopsAfterFailure(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceRoot(t, root, []string{"packages/*"})
	writePkg(t, filepath.Join(root, "packages", "app"), `{"name":"@kb/app"}`)

	cfg := testConfig([]config.NamedCategory{
		{Name: "libs", Category: config.CategoryConfig{Match: []string{"packages/*"}, Preset: "node-lib"}},
	})
	cfg.Tasks = map[string]config.TaskConfig{
		"build": {
			{Command: `sh -c "exit 7"`},
		},
		"verify": {
			{Command: `sh -c "touch should-not-run"`, Deps: []string{"build"}},
		},
	}

	ws := mustWorkspace(t, root, cfg)
	result, err := Run(ws, cfg, RunOptions{
		Tasks:     []string{"build", "verify"},
		WSRoot:    root,
		CacheRoot: filepath.Join(root, ".kb", "devkit-cache"),
	})
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if result.OK {
		t.Fatalf("RunResult OK = true, want false: %+v", result)
	}
	if len(result.Results) != 1 || result.Results[0].Task != "build" || result.Results[0].ExitCode != 7 {
		t.Fatalf("unexpected results after failure: %+v", result.Results)
	}
	if _, err := os.Stat(filepath.Join(root, "packages", "app", "should-not-run")); !os.IsNotExist(err) {
		t.Fatalf("verify task should not have run, stat err = %v", err)
	}
}

func TestRunMarksFailedPackagesAsDirty(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceRoot(t, root, []string{"packages/*"})
	writePkg(t, filepath.Join(root, "packages", "app"), `{"name":"@kb/app"}`)

	cfg := testConfig([]config.NamedCategory{
		{Name: "libs", Category: config.CategoryConfig{Match: []string{"packages/*"}, Preset: "node-lib"}},
	})
	cfg.Tasks = map[string]config.TaskConfig{
		"build": {{Command: `sh -c "exit 1"`}},
	}

	ws := mustWorkspace(t, root, cfg)
	cacheRoot := filepath.Join(root, ".kb", "devkit-cache")

	result, err := Run(ws, cfg, RunOptions{
		Tasks:     []string{"build"},
		WSRoot:    root,
		CacheRoot: cacheRoot,
	})
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if result.OK {
		t.Fatal("expected RunResult.OK = false")
	}

	if !cache.NewStateStore(cacheRoot).IsDirty("@kb/app", "build") {
		t.Fatal("@kb/app:build should be DIRTY after failure")
	}
}

func TestRunMarksSuccessPackagesAsClean(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceRoot(t, root, []string{"packages/*"})
	writePkg(t, filepath.Join(root, "packages", "app"), `{"name":"@kb/app"}`)

	cfg := testConfig([]config.NamedCategory{
		{Name: "libs", Category: config.CategoryConfig{Match: []string{"packages/*"}, Preset: "node-lib"}},
	})
	cfg.Tasks = map[string]config.TaskConfig{
		"build": {{Command: `sh -c "exit 0"`}},
	}

	ws := mustWorkspace(t, root, cfg)
	cacheRoot := filepath.Join(root, ".kb", "devkit-cache")

	result, err := Run(ws, cfg, RunOptions{
		Tasks:     []string{"build"},
		WSRoot:    root,
		CacheRoot: cacheRoot,
	})
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if !result.OK {
		t.Fatal("expected RunResult.OK = true")
	}

	if cache.NewStateStore(cacheRoot).IsDirty("@kb/app", "build") {
		t.Fatal("@kb/app:build should be CLEAN after success")
	}
}

func TestRunMarksNotExecutedPackagesAsDirty(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceRoot(t, root, []string{"packages/*"})
	writePkg(t, filepath.Join(root, "packages", "core"), `{"name":"@kb/core"}`)
	writePkg(t, filepath.Join(root, "packages", "app"), `{"name":"@kb/app","dependencies":{"@kb/core":"workspace:*"}}`)

	cfg := testConfig([]config.NamedCategory{
		{Name: "libs", Category: config.CategoryConfig{Match: []string{"packages/*"}, Preset: "node-lib"}},
	})
	cacheRoot := filepath.Join(root, ".kb", "devkit-cache")

	// Phase 1: both packages build successfully → both CLEAN.
	cfg.Tasks = map[string]config.TaskConfig{
		"build": {{Command: `sh -c "exit 0"`, Deps: []string{"^build"}}},
	}
	ws := mustWorkspace(t, root, cfg)
	r1, err := Run(ws, cfg, RunOptions{Tasks: []string{"build"}, WSRoot: root, CacheRoot: cacheRoot, Concurrency: 1})
	if err != nil || !r1.OK {
		t.Fatalf("phase 1 Run should succeed: err=%v ok=%v", err, r1.OK)
	}
	ss := cache.NewStateStore(cacheRoot)
	if ss.IsDirty("@kb/core", "build") || ss.IsDirty("@kb/app", "build") {
		t.Fatal("both packages should be CLEAN after phase 1")
	}

	// Phase 2: core fails → app is blocked and must be marked DIRTY for retry.
	cfg.Tasks["build"] = config.TaskConfig{{Command: `sh -c "exit 1"`, Deps: []string{"^build"}}}
	r2, err := Run(ws, cfg, RunOptions{
		Tasks: []string{"build"}, WSRoot: root, CacheRoot: cacheRoot, Concurrency: 1, NoCache: true,
	})
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if r2.OK {
		t.Fatal("expected RunResult.OK = false (core fails)")
	}

	// core: actually failed → DIRTY
	if !ss.IsDirty("@kb/core", "build") {
		t.Fatal("@kb/core:build should be DIRTY (actually failed)")
	}
	// app: was CLEAN, blocked by the failed dependency → must be DIRTY
	if !ss.IsDirty("@kb/app", "build") {
		t.Fatal("@kb/app:build should be DIRTY (was CLEAN but blocked by dependency failure)")
	}
}

func TestDirtyPackagesCoversRetryAfterFailure(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceRoot(t, root, []string{"packages/*"})
	writePkg(t, filepath.Join(root, "packages", "app"), `{"name":"@kb/app"}`)

	cfg := testConfig([]config.NamedCategory{
		{Name: "libs", Category: config.CategoryConfig{Match: []string{"packages/*"}, Preset: "node-lib"}},
	})
	cfg.Tasks = map[string]config.TaskConfig{
		"build": {{Command: `sh -c "exit 1"`}},
	}

	ws := mustWorkspace(t, root, cfg)
	cacheRoot := filepath.Join(root, ".kb", "devkit-cache")

	// First run — fails
	Run(ws, cfg, RunOptions{ //nolint:errcheck
		Tasks: []string{"build"}, WSRoot: root, CacheRoot: cacheRoot,
	})

	// DirtyPackages must return @kb/app even though no files changed
	dirty := DirtyPackages(ws, cfg, []string{"build"}, cacheRoot)
	if len(dirty) != 1 || dirty[0].Name != "@kb/app" {
		t.Fatalf("DirtyPackages after failure = %v, want [@kb/app]", dirty)
	}

	// Fix: run succeeds. NoCache:true avoids hitting the cached failure manifest
	// (same inputHash since inputs are empty in this test), ensuring a real execution
	// that writes CLEAN state.
	cfg.Tasks["build"] = config.TaskConfig{{Command: `sh -c "exit 0"`}}
	Run(ws, cfg, RunOptions{ //nolint:errcheck
		Tasks: []string{"build"}, WSRoot: root, CacheRoot: cacheRoot, NoCache: true,
	})

	// Now DirtyPackages must be empty
	dirty2 := DirtyPackages(ws, cfg, []string{"build"}, cacheRoot)
	if len(dirty2) != 0 {
		t.Fatalf("DirtyPackages after fix = %v, want []", dirty2)
	}
}

func mustWorkspace(t *testing.T, root string, cfg *config.DevkitConfig) *workspace.Workspace {
	t.Helper()
	ws, err := workspace.New(root, cfg)
	if err != nil {
		t.Fatalf("workspace.New error: %v", err)
	}
	return ws
}

func testConfig(categories []config.NamedCategory) *config.DevkitConfig {
	return &config.DevkitConfig{
		Workspace: config.WorkspaceConfig{
			Categories: categories,
		},
	}
}

func writeWorkspaceRoot(t *testing.T, root string, workspaces []string) {
	t.Helper()
	var list string
	for i, w := range workspaces {
		if i > 0 {
			list += ","
		}
		list += `"` + w + `"`
	}
	content := `{"workspaces":[` + list + `]}`
	writeEngineFile(t, filepath.Join(root, "package.json"), content)
}

func writePkg(t *testing.T, dir, pkgJSON string) {
	t.Helper()
	writeEngineFile(t, filepath.Join(dir, "package.json"), pkgJSON)
}

func writeEngineFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// case: S-179.41
func TestRunEmitsTaskResultBeforeSlowPeerCompletes(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceRoot(t, root, []string{"packages/*"})
	writePkg(t, filepath.Join(root, "packages", "slow"), `{"name":"@fixture/slow"}`)
	writePkg(t, filepath.Join(root, "packages", "fast"), `{"name":"@fixture/fast"}`)
	cfg := testConfig([]config.NamedCategory{
		{Name: "slow", Category: config.CategoryConfig{Match: []string{"packages/slow"}, Preset: "node-lib"}},
		{Name: "fast", Category: config.CategoryConfig{Match: []string{"packages/fast"}, Preset: "node-lib"}},
	})
	cfg.Tasks = map[string]config.TaskConfig{
		"build": {
			{Categories: []string{"slow"}, Command: `sh -c "sleep 12"`},
			{Categories: []string{"fast"}, Command: `sh -c "sleep 0.05"`},
		},
	}
	ws := mustWorkspace(t, root, cfg)
	resultReader, resultWriter := io.Pipe()
	completionLines := make(chan string, 2)
	go func() {
		scanner := bufio.NewScanner(resultReader)
		for scanner.Scan() {
			completionLines <- scanner.Text()
		}
		close(completionLines)
	}()
	slowFinished := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, err := Run(ws, cfg, RunOptions{
			Tasks: []string{"build"}, WSRoot: root, CacheRoot: filepath.Join(root, ".kb", "devkit-cache"), Concurrency: 2,
			OnResult: func(result TaskResult, _, _ int) {
				if _, err := fmt.Fprintf(resultWriter, "%s:%s\n", result.Package, result.Task); err != nil {
					t.Errorf("write completion line: %v", err)
				}
				if result.Package == "@fixture/slow" {
					close(slowFinished)
				}
			},
		})
		_ = resultWriter.Close()
		if err != nil {
			t.Errorf("Run error: %v", err)
		}
	}()
	t.Cleanup(func() { <-done })
	select {
	case line := <-completionLines:
		if line != "@fixture/fast:build" {
			t.Fatalf("first completion line = %q, want the fast task", line)
		}
		select {
		case <-slowFinished:
			t.Fatal("slow subprocess completed before the fast task's output line")
		default:
		}
	case <-done:
		t.Fatal("Run completed without emitting the fast task result while the slow task was still running")
	case <-time.After(8 * time.Second):
		t.Fatal("fast task completion output was not emitted before the slow subprocess completed")
	}
	<-done
}

// case: S-179.41
func TestExecutorEmitsHeartbeatBeforeSlowSubprocessCompletes(t *testing.T) {
	root := t.TempDir()
	store, err := cache.NewLocalStore(filepath.Join(root, ".kb", "devkit-cache"))
	if err != nil {
		t.Fatal(err)
	}
	exec := NewExecutor(store, cache.NewManifestStore(filepath.Join(root, ".kb", "devkit-cache")), cache.NewStateStore(filepath.Join(root, ".kb", "devkit-cache")), root, filepath.Join(root, ".kb", "devkit-cache"), false)
	heartbeat := make(chan string, 1)
	exec.onHeartbeat = func(packageName, taskName string, elapsed time.Duration) {
		select {
		case heartbeat <- fmt.Sprintf("%s:%s elapsed %s", packageName, taskName, elapsed.Round(time.Millisecond)):
		default:
		}
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _, _, _ = exec.runCommand(`sh -c "sleep 1"`, root, "@fixture/slow", "build", 100*time.Millisecond)
	}()
	select {
	case line := <-heartbeat:
		if line == "" {
			t.Fatal("heartbeat line was empty")
		}
	case <-done:
		t.Fatal("slow subprocess completed before its heartbeat")
	case <-time.After(700 * time.Millisecond):
		t.Fatal("heartbeat was not emitted before the slow subprocess completed")
	}
	<-done
}
