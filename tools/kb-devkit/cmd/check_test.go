package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/kb-labs/clikit/diag"
	"github.com/kb-labs/devkit/internal/checks"
	"github.com/kb-labs/devkit/internal/config"
	"github.com/kb-labs/devkit/internal/workspace"
)

// case: S-179.27
func TestCheckReportsConfiguredChecksPerCategory(t *testing.T) {
	cfg := &config.DevkitConfig{Presets: map[string]config.Preset{"node-lib": {Language: "typescript"}}}
	ws := &workspace.Workspace{Packages: []workspace.Package{{Name: "@acme/demo", Category: "ts-lib", Preset: "node-lib"}}}
	counts := selectedCheckCounts(ws, cfg, checks.Default(), nil, "")
	if counts["ts-lib"] != len(checks.Default().RulesFor(cfg.Presets["node-lib"])) {
		t.Fatalf("checksByCategory = %#v, want one count for each applicable rule", counts)
	}
}

// case: S-179.27
func TestCandidateFixturePackagesUseFixturePreset(t *testing.T) {
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not locate check test source")
	}
	moduleDir := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), ".."))
	workspaceRoot := filepath.Clean(filepath.Join(moduleDir, "../.."))
	cfg, err := config.LoadFile(filepath.Join(workspaceRoot, "devkit.yaml"))
	if err != nil {
		t.Fatalf("load workspace devkit config: %v", err)
	}
	ws, err := workspace.New(workspaceRoot, cfg)
	if err != nil {
		t.Fatalf("discover workspace: %v", err)
	}
	fixtures := 0
	for _, pkg := range ws.Packages {
		if pkg.Category == "fixture" {
			fixtures++
			if pkg.Preset != "candidate-fixture" {
				t.Errorf("fixture package %s uses preset %q, want candidate-fixture", pkg.Name, pkg.Preset)
			}
		}
		if pkg.Preset == "candidate-fixture" && pkg.Category != "fixture" {
			t.Errorf("non-fixture package %s uses candidate-fixture preset", pkg.Name)
		}
	}
	if fixtures == 0 {
		t.Fatal("workspace discovery found no fixture-category packages")
	}
}

// case: S-179.28
func TestCheckRejectsUnknownAndUnmatchedFilters(t *testing.T) {
	cfg := &config.DevkitConfig{Presets: map[string]config.Preset{"go": {Language: "go"}}}
	ws := &workspace.Workspace{Packages: []workspace.Package{{Name: "@acme/go", Category: "go-binary", Preset: "go"}}}
	registry := checks.Default()

	for _, test := range []struct {
		name string
		only []string
	}{
		{name: "unknown", only: []string{"not-a-check"}},
		{name: "known but unmatched", only: []string{"package_json"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := validateCheckSelection(ws, cfg, registry, test.only, "")
			var failure *diag.Diag
			if !errors.As(err, &failure) || failure.Code != "KB_RUNTIME_INPUT_INVALID" {
				t.Fatalf("error = %v, want typed KB_RUNTIME_INPUT_INVALID", err)
			}
		})
	}
}

// case: S-179.28
func TestCheckRejectsCategoryMismatchWithApplicableCategories(t *testing.T) {
	cfg := &config.DevkitConfig{
		Presets: map[string]config.Preset{"node-lib": {Language: "typescript"}},
		Custom:  []config.CustomCheck{{Name: "cli-naming", Run: "true", On: []string{"check"}, Categories: []string{"plugin-entry"}}},
	}
	ws := &workspace.Workspace{Packages: []workspace.Package{{Name: "@kb-labs/viewer/core", Category: "ts-lib", Preset: "node-lib"}}}
	err := validateCheckSelection(ws, cfg, checks.Build(cfg, ".", "check"), []string{"cli-naming"}, "@kb-labs/viewer/core")
	var failure *diag.Diag
	if !errors.As(err, &failure) || failure.Code != "KB_RUNTIME_INPUT_INVALID" {
		t.Fatalf("error = %v, want typed KB_RUNTIME_INPUT_INVALID", err)
	}
	if !strings.Contains(failure.Message, "does not apply") || !strings.Contains(failure.Hint, "plugin-entry") {
		t.Fatalf("failure = %#v, want applicability reason and matching categories", failure)
	}
}

// case: S-179.28
func TestCheckCLIRejectsReviewerCategoryMismatch(t *testing.T) {
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not locate check test source")
	}
	moduleDir := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), ".."))
	workspaceRoot := filepath.Clean(filepath.Join(moduleDir, "../.."))
	tempDir := t.TempDir()
	binary := filepath.Join(tempDir, "kb-devkit")
	build := exec.Command("go", "build", "-o", binary, ".")
	build.Dir = moduleDir
	build.Env = isolatedCheckEnv(t, tempDir)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build kb-devkit: %v\n%s", err, output)
	}

	command := exec.Command(binary, "check", "--package", "@kb-labs/viewer/core", "--only", "cli-naming", "--json")
	command.Dir = workspaceRoot
	command.Env = isolatedCheckEnv(t, tempDir)
	output, err := command.CombinedOutput()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() == 0 {
		t.Fatalf("command exit = %v, want nonzero typed refusal; output: %s", err, output)
	}
	var response struct {
		OK    bool `json:"ok"`
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
			Hint    string `json:"hint"`
		} `json:"error"`
	}
	if err := json.Unmarshal(output, &response); err != nil {
		t.Fatalf("decode command response: %v; output: %s", err, output)
	}
	if response.OK || response.Error.Code != "KB_RUNTIME_INPUT_INVALID" || !strings.Contains(response.Error.Message, "does not apply") || !strings.Contains(response.Error.Hint, "plugin-entry") {
		t.Fatalf("response = %#v, want typed category-mismatch refusal", response)
	}
}

// case: S-179.27
func TestCheckCLIStreamsNamedProgressAndCataloguedTimeoutThroughPTY(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("the macOS script utility provides the PTY contract exercised here")
	}
	script, err := exec.LookPath("script")
	if err != nil {
		t.Skip("script utility is unavailable")
	}
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not locate check test source")
	}
	moduleDir := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), ".."))
	tempDir := t.TempDir()
	fixtureRoot := filepath.Join(tempDir, "workspace")
	pkgDir := filepath.Join(fixtureRoot, "packages", "demo")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "package.json"), []byte(`{"name":"@acme/demo"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	config := `schemaVersion: 2
workspace:
  discovery: ["packages/*"]
  categories:
    demo:
      match: ["packages/*"]
      language: javascript
      preset: demo
presets:
  demo:
    language: javascript
checks:
  timeout_ms: 5000
custom_checks:
  - name: slow-check
    run: "exec sleep 3"
    on: [check]
    scope: workspace
`
	configPath := filepath.Join(fixtureRoot, "devkit.yaml")
	if err := os.WriteFile(configPath, []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}

	binary := filepath.Join(tempDir, "kb-devkit")
	build := exec.Command("go", "build", "-o", binary, ".")
	build.Dir = moduleDir
	build.Env = isolatedCheckEnv(t, tempDir)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build kb-devkit: %v\n%s", err, output)
	}
	home := filepath.Join(tempDir, "home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	transcript := filepath.Join(tempDir, "pty-transcript.txt")
	command := exec.CommandContext(ctx, script, "-q", "-F", transcript, binary, "--config", configPath, "check", "--only", "slow-check", "--json")
	command.Dir = fixtureRoot
	command.Env = isolatedCheckEnv(t, home)
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	if err := command.Start(); err != nil {
		t.Fatalf("start built check through PTY: %v", err)
	}
	t.Cleanup(func() {
		if command.Process != nil && command.ProcessState == nil {
			if err := command.Process.Kill(); err == nil {
				if waitErr := command.Wait(); waitErr != nil && !errors.Is(waitErr, os.ErrProcessDone) {
					t.Errorf("wait for stopped PTY check command: %v", waitErr)
				}
			} else if errors.Is(err, os.ErrProcessDone) {
				_ = command.Wait()
			} else {
				t.Errorf("stop PTY check command: %v", err)
			}
		}
	})
	deadline := time.After(10 * time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		transcriptBytes, readErr := os.ReadFile(transcript)
		transcriptText := string(transcriptBytes)
		if readErr == nil && strings.Contains(transcriptText, "[RUN] workspace:slow-check elapsed") {
			if strings.Contains(transcriptText, "[DONE] workspace:slow-check") {
				t.Fatalf("PTY transcript contains completion before the delay ends: %q", transcriptText)
			}
			break
		}
		select {
		case <-ticker.C:
		case <-deadline:
			t.Fatalf("PTY emitted no named progress before completion: %q (read error: %v)", transcriptText, readErr)
		}
	}
	if err := command.Wait(); err != nil {
		t.Fatalf("built check through PTY: %v", err)
	}
	transcriptBytes, err := os.ReadFile(transcript)
	if err != nil {
		t.Fatalf("read PTY transcript: %v", err)
	}
	if !strings.Contains(string(transcriptBytes), "[DONE] workspace:slow-check elapsed") {
		t.Fatalf("PTY transcript lacks completion line: %q", transcriptBytes)
	}

	if err := os.WriteFile(configPath, []byte(strings.Replace(config, "timeout_ms: 5000", "timeout_ms: 30", 1)), 0o644); err != nil {
		t.Fatalf("set short timeout: %v", err)
	}
	command = exec.CommandContext(ctx, binary, "--config", configPath, "check", "--only", "slow-check", "--json")
	command.Dir = fixtureRoot
	command.Env = isolatedCheckEnv(t, home)
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatal("timed-out check exited successfully")
	}
	for _, expected := range []string{"KB_DEVKIT_CHECK_EXECUTION_FAILED", "slow-check", "30ms"} {
		if !strings.Contains(string(output), expected) {
			t.Fatalf("timeout output lacks %q: %s", expected, output)
		}
	}
}

func isolatedCheckEnv(t *testing.T, home string) []string {
	t.Helper()
	env := make([]string, 0, len(os.Environ())+1)
	for _, item := range os.Environ() {
		if strings.HasPrefix(item, "KB_PLATFORM_ROOT=") || strings.HasPrefix(item, "KB_HOME=") || strings.HasPrefix(item, "KB_NET_OFFSET=") || strings.HasPrefix(item, "HOME=") {
			continue
		}
		env = append(env, item)
	}
	goEnv, err := exec.Command("go", "env", "GOPATH", "GOMODCACHE", "GOCACHE").Output()
	if err != nil {
		t.Fatalf("read Go cache locations: %v", err)
	}
	paths := strings.Split(strings.TrimSpace(string(goEnv)), "\n")
	if len(paths) != 3 {
		t.Fatalf("go env returned %d paths, want GOPATH, GOMODCACHE, GOCACHE", len(paths))
	}
	env = append(env, "HOME="+home, "GOPATH="+paths[0], "GOMODCACHE="+paths[1], "GOCACHE="+paths[2])
	return env
}

// case: S-179.27
func TestSelectedCheckCountsOnlyApplicablePackagePairs(t *testing.T) {
	cfg := &config.DevkitConfig{
		Presets: map[string]config.Preset{"node-lib": {Language: "typescript"}},
		Custom:  []config.CustomCheck{{Name: "cli-naming", Run: "true", On: []string{"check"}, Categories: []string{"plugin-entry"}}},
	}
	ws := &workspace.Workspace{Packages: []workspace.Package{
		{Name: "@acme/plugin-a", Category: "plugin-entry", Preset: "node-lib"},
		{Name: "@acme/plugin-b", Category: "plugin-entry", Preset: "node-lib"},
		{Name: "@acme/library", Category: "ts-lib", Preset: "node-lib"},
	}}
	registry := checks.Build(cfg, ".", "check")

	wholeWorkspace := selectedCheckCounts(ws, cfg, registry, []string{"cli-naming"}, "")
	if wholeWorkspace["plugin-entry"] != 2 || sumCheckCounts(wholeWorkspace) != 2 {
		t.Fatalf("whole-workspace counts = %#v, want exactly two applicable packages", wholeWorkspace)
	}
	wholeResults := checks.RunAll(ws, cfg, registry, []string{"cli-naming"}, nil)
	if len(wholeResults) != 2 {
		t.Fatalf("whole-workspace executed packages = %d, want the two hand-counted plugin-entry packages", len(wholeResults))
	}
	for _, name := range []string{"@acme/plugin-a", "@acme/plugin-b"} {
		if wholeResults[name].ChecksRun != len(registry.ApplicableRulesFor(wholeResults[name].Package, cfg.Presets["node-lib"], []string{"cli-naming"})) {
			t.Fatalf("%s execution count = %d, applicability owner returned a different count", name, wholeResults[name].ChecksRun)
		}
	}
	mixed := selectedCheckCounts(ws, cfg, registry, []string{"cli-naming", "package_json"}, "")
	if mixed["plugin-entry"] != 2*2 || mixed["ts-lib"] != 1 || sumCheckCounts(mixed) != 5 {
		t.Fatalf("mixed counts = %#v, want 4 plugin-entry pairs and 1 ts-lib pair", mixed)
	}
	mixedResults := checks.RunAll(ws, cfg, registry, []string{"cli-naming", "package_json"}, nil)
	if len(mixedResults) != 3 || mixedResults["@acme/plugin-a"].ChecksRun != 2 || mixedResults["@acme/plugin-b"].ChecksRun != 2 || mixedResults["@acme/library"].ChecksRun != 1 {
		t.Fatalf("mixed execution = %#v, want only the five applicable pairs", mixedResults)
	}
}

// case: S-179.27
func TestWorkspaceScopedCheckCountIsAnchoredOnce(t *testing.T) {
	cfg := &config.DevkitConfig{
		Presets: map[string]config.Preset{"node-lib": {Language: "typescript"}},
		Custom:  []config.CustomCheck{{Name: "workspace-audit", Run: "true", On: []string{"check"}, Language: "any", Scope: "workspace"}},
	}
	ws := &workspace.Workspace{Packages: []workspace.Package{
		{Name: "@acme/plugin-a", Category: "plugin-entry", Preset: "node-lib"},
		{Name: "@acme/library", Category: "ts-lib", Preset: "node-lib"},
	}}
	counts := selectedCheckCounts(ws, cfg, checks.Build(cfg, ".", "check"), []string{"workspace-audit"}, "")
	if counts["workspace"] != 1 || sumCheckCounts(counts) != 1 {
		t.Fatalf("workspace count = %#v, want one anchored check", counts)
	}
	results := checks.RunAll(ws, cfg, checks.Build(cfg, ".", "check"), []string{"workspace-audit"}, nil)
	if results["workspace"].ChecksRun != 1 || len(results) != 1 {
		t.Fatalf("workspace execution = %#v, want one anchored run independent of package count", results)
	}
	emptyWorkspace := &workspace.Workspace{Root: t.TempDir()}
	registry := checks.Build(cfg, ".", "check")
	if err := validateCheckSelection(emptyWorkspace, cfg, registry, []string{"workspace-audit"}, ""); err != nil {
		t.Fatalf("workspace-only check selection returned %v, want one real anchored check", err)
	}
	if counts := selectedCheckCounts(emptyWorkspace, cfg, registry, []string{"workspace-audit"}, ""); counts["workspace"] != 1 {
		t.Fatalf("empty workspace counts = %#v, want one anchored check", counts)
	}
}

// case: S-179.29
func TestCheckFailureEnvelopeUsesStandardsViolationCodeAndData(t *testing.T) {
	previousJSONMode := jsonMode
	t.Cleanup(func() { jsonMode = previousJSONMode })
	jsonMode = true
	err := outputCheckJSON(map[string]checks.PackageResult{
		"@acme/demo": {
			Package: workspace.Package{Name: "@acme/demo", RelPath: "packages/demo", Category: "ts-lib"},
			Issues:  []checks.Issue{{Check: "package_json", Severity: checks.SeverityError, Message: "missing field"}},
		},
	}, &config.DevkitConfig{}, map[string]int{"ts-lib": 1}, 1)
	var failure *diag.Diag
	if !errors.As(err, &failure) || failure.Code != "KB_DEVKIT_STANDARDS_VIOLATIONS" || failure.Data == nil {
		t.Fatalf("error = %#v, want standards-violation diagnostic with result data", err)
	}
}

// case: S-179.30
func TestCheckExecutionFailureIsTypedAndActionable(t *testing.T) {
	failures := []externalCheckFailure{{Check: "adapter-manifest", Cause: "external command exited with status 1"}}
	err := externalCheckFailureError(failures, nil)
	var failure *diag.Diag
	if !errors.As(err, &failure) || failure.Code != "KB_DEVKIT_CHECK_EXECUTION_FAILED" {
		t.Fatalf("error = %#v, want typed external-check failure", err)
	}
	if failure.Hint == "" || failure.Cause != "adapter-manifest: external command exited with status 1" {
		t.Fatalf("failure = %#v, want safe cause and rerun hint", failure)
	}
	envelope, err := diag.ToEnvelope(failure)
	if err != nil {
		t.Fatalf("ToEnvelope() error = %v, want catalogued check detail", err)
	}
	if envelope.Message != "Workspace check adapter-manifest could not complete." {
		t.Fatalf("envelope message = %q, want the failing check name", envelope.Message)
	}
	if envelope.Hint != "Rerun this check alone with `kb-devkit check --only adapter-manifest`, then correct the check's execution problem." {
		t.Fatalf("envelope hint = %q, want catalogued next command", envelope.Hint)
	}
	if len(checkExecutionFailures(map[string]checks.PackageResult{
		"a": {Issues: []checks.Issue{{Check: "browser-node-builtins", CheckFailure: true, Cause: "exit 1"}}},
		"b": {Issues: []checks.Issue{{Check: "adapter-manifest", CheckFailure: true, Cause: "exit 2"}}},
	})) != 2 {
		t.Fatal("checkExecutionFailures dropped a crashed check")
	}
}
