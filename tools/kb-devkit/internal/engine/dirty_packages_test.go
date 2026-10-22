package engine

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kb-labs/devkit/internal/cache"
	"github.com/kb-labs/devkit/internal/config"
	"github.com/kb-labs/devkit/internal/workspace"
)

// makeWS builds a minimal Workspace from a slice of package names.
// Packages are in the order provided — workspace order is preserved.
func makeWS(names ...string) *workspace.Workspace {
	pkgs := make([]workspace.Package, len(names))
	for i, n := range names {
		pkgs[i] = workspace.Package{Name: n, Dir: "/fake/" + n}
	}
	return &workspace.Workspace{Root: "/fake", Packages: pkgs}
}

func TestDirtyPackagesIncludesNeverBuilt(t *testing.T) {
	cacheRoot := filepath.Join(t.TempDir(), ".kb", "devkit")
	ws := makeWS("@kb/app")

	got := DirtyPackages(ws, &config.DevkitConfig{Tasks: map[string]config.TaskConfig{"build": {{Command: "true"}}}}, []string{"build"}, cacheRoot)
	if len(got) != 1 || got[0].Name != "@kb/app" {
		t.Fatalf("DirtyPackages = %v, want [@kb/app] (no state file = dirty)", got)
	}
}

func TestDirtyPackagesExcludesCleanState(t *testing.T) {
	root := t.TempDir()
	pkgDir := filepath.Join(root, "packages", "app")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "package.json"), []byte(`{"name":"@kb/app"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cacheRoot := filepath.Join(root, ".kb", "devkit")
	states := cache.NewStateStore(cacheRoot)
	def := TaskDef{Name: "build", Command: "true", Cache: true}
	inputHash, err := hashTaskInputs(workspace.Package{Name: "@kb/app", Dir: pkgDir}, def, nil, cache.RuntimeFingerprint())
	if err != nil {
		t.Fatal(err)
	}
	if err := states.SetClean("@kb/app", "build", inputHash); err != nil {
		t.Fatalf("SetClean: %v", err)
	}
	ws := &workspace.Workspace{Root: root, Packages: []workspace.Package{{Name: "@kb/app", Dir: pkgDir}}}
	cfg := &config.DevkitConfig{Tasks: map[string]config.TaskConfig{"build": {{Command: "true"}}}}

	got := DirtyPackages(ws, cfg, []string{"build"}, cacheRoot)
	if len(got) != 0 {
		t.Fatalf("DirtyPackages = %v, want [] (state is CLEAN)", got)
	}
}

func TestDirtyPackagesIncludesDirtyState(t *testing.T) {
	cacheRoot := filepath.Join(t.TempDir(), ".kb", "devkit")
	states := cache.NewStateStore(cacheRoot)
	if err := states.SetDirty("@kb/app", "build", "last_run_failed"); err != nil {
		t.Fatalf("SetDirty: %v", err)
	}
	ws := makeWS("@kb/app")

	got := DirtyPackages(ws, &config.DevkitConfig{Tasks: map[string]config.TaskConfig{"build": {{Command: "true"}}}}, []string{"build"}, cacheRoot)
	if len(got) != 1 || got[0].Name != "@kb/app" {
		t.Fatalf("DirtyPackages = %v, want [@kb/app] (state is DIRTY)", got)
	}
}

func TestDirtyPackagesSchedulesTaskWhenInputHashCannotBeRead(t *testing.T) {
	root := t.TempDir()
	pkgDir := filepath.Join(root, "packages", "app")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "package.json"), []byte(`{"name":"@kb/app"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cacheRoot := filepath.Join(root, ".kb", "devkit")
	if err := cache.NewStateStore(cacheRoot).SetClean("@kb/app", "build", "last-successful-hash"); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(pkgDir, "package.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(pkgDir); err != nil {
		t.Fatal(err)
	}
	ws := &workspace.Workspace{Root: root, Packages: []workspace.Package{{Name: "@kb/app", Dir: pkgDir}}}
	cfg := &config.DevkitConfig{Tasks: map[string]config.TaskConfig{"build": {{Command: "true"}}}}

	got := DirtyPackages(ws, cfg, []string{"build"}, cacheRoot)
	if len(got) != 1 || got[0].Name != "@kb/app" {
		t.Fatalf("DirtyPackages after input hash read error = %v, want [@kb/app] so Executor can report it", got)
	}
}

func TestDirtyPackagesAllTasksMustBeClean(t *testing.T) {
	root := t.TempDir()
	pkgDir := filepath.Join(root, "packages", "app")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "package.json"), []byte(`{"name":"@kb/app"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cacheRoot := filepath.Join(root, ".kb", "devkit")
	states := cache.NewStateStore(cacheRoot)
	ws := &workspace.Workspace{Root: root, Packages: []workspace.Package{{Name: "@kb/app", Dir: pkgDir}}}
	cfg := &config.DevkitConfig{Tasks: map[string]config.TaskConfig{"build": {{Command: "true"}}, "test": {{Command: "true"}}}}
	buildHash, err := hashTaskInputs(ws.Packages[0], *ResolveTaskDef(cfg, "build", ""), nil, cache.RuntimeFingerprint())
	if err != nil {
		t.Fatal(err)
	}
	testHash, err := hashTaskInputs(ws.Packages[0], *ResolveTaskDef(cfg, "test", ""), nil, cache.RuntimeFingerprint())
	if err != nil {
		t.Fatal(err)
	}
	// build is clean but test is dirty
	if err := states.SetClean("@kb/app", "build", buildHash); err != nil {
		t.Fatalf("SetClean build: %v", err)
	}
	if err := states.SetDirty("@kb/app", "test", "last_run_failed"); err != nil {
		t.Fatalf("SetDirty test: %v", err)
	}
	got := DirtyPackages(ws, cfg, []string{"build", "test"}, cacheRoot)
	if len(got) != 1 || got[0].Name != "@kb/app" {
		t.Fatalf("DirtyPackages = %v, want [@kb/app] (test task is DIRTY)", got)
	}

	// Now fix test state — both clean → excluded
	if err := states.SetClean("@kb/app", "test", testHash); err != nil {
		t.Fatalf("SetClean test: %v", err)
	}
	got2 := DirtyPackages(ws, cfg, []string{"build", "test"}, cacheRoot)
	if len(got2) != 0 {
		t.Fatalf("DirtyPackages = %v, want [] (all tasks CLEAN)", got2)
	}
}

func TestDirtyPackagesEmptyTasks(t *testing.T) {
	cacheRoot := filepath.Join(t.TempDir(), ".kb", "devkit")
	ws := makeWS("@kb/app")

	got := DirtyPackages(ws, nil, []string{}, cacheRoot)
	if len(got) != 0 {
		t.Fatalf("DirtyPackages with empty tasks = %v, want []", got)
	}
}
