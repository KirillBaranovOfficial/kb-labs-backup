package engine

import (
	"os"
	"path/filepath"
	"testing"

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
	if summary.Scheduled != 1 || summary.Cached != 1 || summary.Passed != 0 || summary.Failed != 0 || summary.Skipped != 0 {
		t.Fatalf("unexpected summary: %+v", summary)
	}
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
	if summary := second.Summary(); summary.Scheduled != 1 || summary.Passed != 0 || summary.Failed != 1 || summary.Cached != 0 || summary.Skipped != 0 {
		t.Fatalf("second summary = %+v, want total=1 passed=0 failed=1 cached=0", summary)
	}
	if err := os.WriteFile(filepath.Join(root, "packages", "dependency.ready"), nil, 0o644); err != nil {
		t.Fatalf("write dependency artifact: %v", err)
	}

	third := run()
	if !third.OK || len(third.Results) != 1 || third.Results[0].Cached {
		t.Fatalf("third run = %+v, want a fresh successful execution", third)
	}
	if summary := third.Summary(); summary.Scheduled != 1 || summary.Passed != 1 || summary.Failed != 0 || summary.Cached != 0 || summary.Skipped != 0 {
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
	app := filepath.Join(root, "packages", "app")

	cfg := testConfig([]config.NamedCategory{
		{Name: "core", Category: config.CategoryConfig{Match: []string{"packages/core"}, Preset: "node-lib"}},
		{Name: "libraries", Category: config.CategoryConfig{Match: []string{"packages/middle", "packages/app"}, Preset: "node-lib"}},
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
		if pkg.Dir == app {
			selected = append(selected, pkg)
		}
	}
	if len(selected) != 1 {
		t.Fatalf("selected app packages = %+v; want one", selected)
	}

	result, err := Run(ws, cfg, RunOptions{
		Tasks:     []string{"build"},
		Packages:  selected,
		WSRoot:    root,
		CacheRoot: filepath.Join(root, ".kb", "devkit-cache"),
	})
	if err != nil || result.OK || len(result.Results) != 1 {
		t.Fatalf("failed dependency run = %+v, error = %v; want one failed root task", result, err)
	}
	summary := result.Summary()
	if summary.Scheduled != 3 || summary.Passed != 0 || summary.Cached != 0 || summary.Failed != 1 || summary.Skipped != 2 {
		t.Fatalf("failed dependency summary = %+v; want scheduled=3 failed=1 skipped=2", summary)
	}
	if summary.Passed+summary.Cached+summary.Failed+summary.Skipped != summary.Scheduled {
		t.Fatalf("summary counts do not partition scheduled tasks: %+v", summary)
	}
	if len(summary.BlockedTasks) != 1 || summary.BlockedTasks[0].Package != "@fixture/core" ||
		summary.BlockedTasks[0].Task != "build" || summary.BlockedTasks[0].Dependents != 2 {
		t.Fatalf("blocked task details = %+v; want @fixture/core:build blocked 2 dependents", summary.BlockedTasks)
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

	// Phase 2: core fails → app (in a later layer) never runs.
	// app was CLEAN; scheduler hard stop must mark it DIRTY.
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
	// app: was CLEAN, skipped by scheduler hard stop → must be DIRTY
	if !ss.IsDirty("@kb/app", "build") {
		t.Fatal("@kb/app:build should be DIRTY (was CLEAN but skipped due to hard stop)")
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
	dirty := DirtyPackages(ws, []string{"build"}, cacheRoot)
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
	dirty2 := DirtyPackages(ws, []string{"build"}, cacheRoot)
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
