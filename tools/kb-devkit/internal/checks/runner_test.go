package checks

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/kb-labs/devkit/internal/config"
	"github.com/kb-labs/devkit/internal/workspace"
)

// case: S-179.27
func TestRunAllReportsPerCheckProgress(t *testing.T) {
	root := t.TempDir()
	pkgDir := filepath.Join(root, "packages", "demo")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writePackageJSON(t, pkgDir, `{"name":"@acme/demo"}`)
	cfg := &config.DevkitConfig{Presets: map[string]config.Preset{"demo": {Language: "typescript"}}}
	ws := &workspace.Workspace{Root: root, Packages: []workspace.Package{{Name: "@acme/demo", Dir: pkgDir, RelPath: "packages/demo", Category: "demo", Preset: "demo", Language: "typescript"}}}
	var mu sync.Mutex
	var events []ProgressEvent
	RunAll(ws, cfg, Default(), nil, func(event ProgressEvent) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, event)
	})
	mu.Lock()
	defer mu.Unlock()
	if len(events) == 0 {
		t.Fatal("progress events = none, want each executed rule to report start and completion")
	}
	started, completed := false, false
	for _, event := range events {
		if event.Package != "@acme/demo" && event.Package != "workspace" || event.Check == "" {
			t.Fatalf("progress event = %#v, want package and check identity", event)
		}
		started = started || event.Running
		completed = completed || !event.Running
	}
	if !started || !completed {
		t.Fatalf("progress events = %#v, want running and completed states", events)
	}
}

type fakeRule struct {
	name   string
	issues []Issue
}

func (r fakeRule) Name() string { return r.name }

func (r fakeRule) Check(pkg workspace.Package, preset config.Preset) []Issue {
	out := make([]Issue, len(r.issues))
	copy(out, r.issues)
	for i := range out {
		if out[i].Message == "" {
			out[i].Message = pkg.Name
		}
	}
	return out
}

func TestRunAllAppliesOnlyFilterAndMergesCircularResults(t *testing.T) {
	root := t.TempDir()
	pkgA := workspace.Package{
		Name:     "@kb/a",
		Dir:      filepath.Join(root, "packages", "a"),
		Preset:   "node-lib",
		Language: "typescript",
	}
	pkgB := workspace.Package{
		Name:     "@kb/b",
		Dir:      filepath.Join(root, "packages", "b"),
		Preset:   "node-lib",
		Language: "typescript",
	}

	writePackageJSON(t, pkgA.Dir, `{"name":"@kb/a","dependencies":{"@kb/b":"workspace:*"}}`)
	writePackageJSON(t, pkgB.Dir, `{"name":"@kb/b","dependencies":{"@kb/a":"workspace:*"}}`)

	ws := &workspace.Workspace{Packages: []workspace.Package{pkgA, pkgB}}
	cfg := &config.DevkitConfig{
		Workspace: config.WorkspaceConfig{
			Categories: []config.NamedCategory{
				{Name: "libs", Category: config.CategoryConfig{Preset: "node-lib"}},
			},
		},
	}
	registry := &Registry{
		rules: []Rule{
			fakeRule{name: "selected", issues: []Issue{{Check: "selected", Severity: SeverityInfo}}},
			fakeRule{name: "ignored", issues: []Issue{{Check: "ignored", Severity: SeverityWarning}}},
		},
	}

	results := RunAll(ws, cfg, registry, []string{"selected", "circular"}, nil)

	for _, pkg := range []workspace.Package{pkgA, pkgB} {
		result, ok := results[pkg.Name]
		if !ok {
			t.Fatalf("missing result for %s", pkg.Name)
		}
		if result.Skipped {
			t.Fatalf("package %s unexpectedly skipped", pkg.Name)
		}
		if len(result.Issues) != 2 {
			t.Fatalf("issues for %s = %#v, want selected + circular", pkg.Name, result.Issues)
		}
		if result.Issues[0].Check != "selected" || result.Issues[1].Check != "circular" {
			t.Fatalf("unexpected issues for %s: %#v", pkg.Name, result.Issues)
		}
	}
}

func TestRunAllRunsCircularCheckByItself(t *testing.T) {
	root := t.TempDir()
	pkgA := workspace.Package{Name: "@kb/a", Dir: filepath.Join(root, "a"), Preset: "node-lib"}
	pkgB := workspace.Package{Name: "@kb/b", Dir: filepath.Join(root, "b"), Preset: "node-lib"}
	writePackageJSON(t, pkgA.Dir, `{"name":"@kb/a","dependencies":{"@kb/b":"workspace:*"}}`)
	writePackageJSON(t, pkgB.Dir, `{"name":"@kb/b","devDependencies":{"@kb/a":"workspace:*"}}`)
	cfg := &config.DevkitConfig{
		Presets: map[string]config.Preset{"node-lib": {Deps: config.DepsRules{CheckCircular: true}}},
		Workspace: config.WorkspaceConfig{Categories: []config.NamedCategory{{
			Name: "libs", Category: config.CategoryConfig{Preset: "node-lib"},
		}}},
	}
	results := RunAll(&workspace.Workspace{Packages: []workspace.Package{pkgA, pkgB}}, cfg, &Registry{}, []string{"circular"}, nil)
	for _, pkg := range []workspace.Package{pkgA, pkgB} {
		result, ok := results[pkg.Name]
		if !ok {
			t.Fatalf("missing circular result for %s", pkg.Name)
		}
		if result.ChecksRun != 1 || len(result.Issues) != 1 || result.Issues[0].Check != "circular" {
			t.Fatalf("result for %s = %#v, want one circular finding", pkg.Name, result)
		}
	}
}

func TestRunAllSkipsPackageWhenPresetResolutionFails(t *testing.T) {
	ws := &workspace.Workspace{
		Packages: []workspace.Package{
			{Name: "@kb/missing", Dir: t.TempDir(), Preset: "does-not-exist"},
		},
	}
	cfg := &config.DevkitConfig{
		Workspace: config.WorkspaceConfig{
			Categories: []config.NamedCategory{
				{Name: "libs", Category: config.CategoryConfig{Preset: "node-lib"}},
			},
		},
	}

	results := RunAll(ws, cfg, &Registry{}, nil, nil)
	result := results["@kb/missing"]
	if !result.Skipped {
		t.Fatalf("Skipped = false, want true: %#v", result)
	}
}

func writePackageJSON(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(content), 0o644); err != nil {
		t.Fatalf("write package.json: %v", err)
	}
}
