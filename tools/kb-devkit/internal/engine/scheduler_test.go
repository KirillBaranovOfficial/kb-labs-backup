package engine

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/kb-labs/devkit/internal/config"
	"github.com/kb-labs/devkit/internal/workspace"
)

// writePkgJSON creates a minimal package.json in dir.
func writePkgJSON(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// case: S-179.19, S-179.37
func TestWorkspaceBuildGraphOrdersEveryDeclaredWorkspaceDependency(t *testing.T) {
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not locate scheduler test source")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "../../../.."))
	cfg, err := config.LoadFile(filepath.Join(root, "devkit.yaml"))
	if err != nil {
		t.Fatalf("load workspace devkit.yaml: %v", err)
	}
	ws, err := workspace.New(root, cfg)
	if err != nil {
		t.Fatalf("discover workspace packages: %v", err)
	}
	pkgs := expandBuildDependencyClosure(ws.Packages, ws.Packages)
	nodes, err := buildDAG(pkgs, []string{"build"}, cfg, ws)
	if err != nil {
		t.Fatalf("build workspace task DAG: %v", err)
	}
	if len(pkgs) != len(ws.Packages) {
		t.Fatalf("build dependency closure has %d packages; workspace has %d", len(pkgs), len(ws.Packages))
	}

	byName := make(map[string]workspace.Package, len(ws.Packages))
	for _, pkg := range ws.Packages {
		byName[pkg.Name] = pkg
		def := ResolveTaskDef(cfg, "build", pkg.Category)
		if def == nil {
			if _, scheduled := nodes[nodeKey{pkg.Name, "build"}]; scheduled {
				t.Errorf("package %s has a build node without a matching task definition", pkg.Name)
			}
			continue
		}
		if _, scheduled := nodes[nodeKey{pkg.Name, "build"}]; !scheduled {
			t.Errorf("package %s has a build task definition but no build node", pkg.Name)
		}
	}

	for key, node := range nodes {
		if key.task != "build" {
			continue
		}
		def := ResolveTaskDef(cfg, "build", node.pkg.Category)
		if def == nil || !containsTask(def.Deps, "^build") {
			continue
		}
		for _, depName := range readWorkspaceDeps(node.pkg.Dir, byName) {
			if depName == node.pkg.Name {
				continue
			}
			depKey := nodeKey{pkg: depName, task: "build"}
			if _, scheduled := nodes[depKey]; !scheduled {
				t.Errorf("%s build depends on %s, but the dependency has no build task node", key.pkg, depName)
				continue
			}
			found := false
			for _, actual := range node.deps {
				if actual == depKey {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("%s build node is missing declared workspace dependency %s", key.pkg, depName)
			}
		}
	}
}

func TestBuildPkgDepMapFiltersSelfReference(t *testing.T) {
	root := t.TempDir()

	devkitDir := filepath.Join(root, "devkit")
	writePkgJSON(t, devkitDir, `{
		"name": "@kb/devkit",
		"devDependencies": {"@kb/devkit": "workspace:*"}
	}`)

	pkgs := []workspace.Package{
		{Name: "@kb/devkit", Dir: devkitDir},
	}
	pkgByName := map[string]workspace.Package{
		"@kb/devkit": pkgs[0],
	}

	depMap := buildPkgDepMap(pkgs, pkgByName)

	if deps, ok := depMap["@kb/devkit"]; ok && len(deps) > 0 {
		t.Fatalf("self-reference not filtered: got deps %v for @kb/devkit", deps)
	}
}

func TestBuildPkgDepMapKeepsLegitDeps(t *testing.T) {
	root := t.TempDir()

	typesDir := filepath.Join(root, "types")
	writePkgJSON(t, typesDir, `{"name": "@kb/types"}`)

	appDir := filepath.Join(root, "app")
	writePkgJSON(t, appDir, `{
		"name": "@kb/app",
		"dependencies": {"@kb/types": "workspace:*"},
		"devDependencies": {"@kb/app": "workspace:*"}
	}`)

	pkgs := []workspace.Package{
		{Name: "@kb/types", Dir: typesDir},
		{Name: "@kb/app", Dir: appDir},
	}
	pkgByName := map[string]workspace.Package{
		"@kb/types": pkgs[0],
		"@kb/app":   pkgs[1],
	}

	depMap := buildPkgDepMap(pkgs, pkgByName)

	// @kb/app should depend on @kb/types but NOT on itself
	deps := depMap["@kb/app"]
	if len(deps) != 1 || deps[0] != "@kb/types" {
		t.Fatalf("@kb/app deps = %v, want [@kb/types]", deps)
	}
}

// case: S-179.37
func TestBuildDAGRejectsWorkspaceBuildDependencyWithoutTaskNode(t *testing.T) {
	root := t.TempDir()
	coreDir := filepath.Join(root, "core")
	writePkgJSON(t, coreDir, `{"name":"@fixture/core"}`)
	runtimeDir := filepath.Join(root, "runtime")
	writePkgJSON(t, runtimeDir, `{"name":"@fixture/runtime","dependencies":{"@fixture/core":"workspace:*"}}`)
	appDir := filepath.Join(root, "app")
	writePkgJSON(t, appDir, `{"name":"@fixture/app","dependencies":{"@fixture/runtime":"workspace:*"}}`)

	pkgs := []workspace.Package{
		{Name: "@fixture/core", Dir: coreDir, Category: "libraries"},
		{Name: "@fixture/runtime", Dir: runtimeDir, Category: "runtime"},
		{Name: "@fixture/app", Dir: appDir, Category: "applications"},
	}
	cfg := &config.DevkitConfig{
		Tasks: map[string]config.TaskConfig{
			"build": {
				{Categories: []string{"libraries", "applications"}, Command: "true", Deps: []string{"^build"}},
			},
		},
	}

	_, err := buildDAG(pkgs, []string{"build"}, cfg, &workspace.Workspace{Root: root, Packages: pkgs})
	var graphErr *TaskGraphError
	if !errors.As(err, &graphErr) {
		t.Fatal("build DAG accepted @fixture/app -> @fixture/runtime -> @fixture/core without a runtime build task")
	}
	if graphErr.Package != "@fixture/app" || graphErr.Dependency != "@fixture/runtime" ||
		graphErr.Task != "build" || graphErr.DependencyTask != "build" {
		t.Fatalf("task graph error = %+v, want the omitted runtime build node", graphErr)
	}
}

func TestExpandBuildDependencyClosureAddsUpstreamPackages(t *testing.T) {
	root := t.TempDir()

	coreDir := filepath.Join(root, "core")
	writePkgJSON(t, coreDir, `{"name":"@kb/core"}`)
	appDir := filepath.Join(root, "app")
	writePkgJSON(t, appDir, `{"name":"@kb/app","dependencies":{"@kb/core":"workspace:*"}}`)

	all := []workspace.Package{
		{Name: "@kb/core", Dir: coreDir},
		{Name: "@kb/app", Dir: appDir},
	}
	closure := expandBuildDependencyClosure(all[1:], all)

	if len(closure) != 2 || closure[0].Name != "@kb/core" || closure[1].Name != "@kb/app" {
		t.Fatalf("closure = %v, want [@kb/core @kb/app]", closure)
	}
}

func TestBuildDAGSelfDepDoesNotCreateCycle(t *testing.T) {
	root := t.TempDir()

	// Simulate @kb-labs/devkit depending on itself — the exact real-world bug.
	devkitDir := filepath.Join(root, "devkit")
	writePkgJSON(t, devkitDir, `{
		"name": "@kb/devkit",
		"devDependencies": {"@kb/devkit": "workspace:*"}
	}`)

	coreDir := filepath.Join(root, "core")
	writePkgJSON(t, coreDir, `{
		"name": "@kb/core",
		"devDependencies": {"@kb/devkit": "workspace:*"}
	}`)

	pkgs := []workspace.Package{
		{Name: "@kb/devkit", Dir: devkitDir, Category: "ts-lib"},
		{Name: "@kb/core", Dir: coreDir, Category: "ts-lib"},
	}

	cfg := &config.DevkitConfig{
		Tasks: map[string]config.TaskConfig{
			"build": {
				{Categories: []string{"ts-lib"}, Command: "tsup", Deps: []string{"^build"}},
			},
		},
	}
	ws := &workspace.Workspace{Root: root, Packages: pkgs}

	nodes, err := buildDAG(pkgs, []string{"build"}, cfg, ws)
	if err != nil {
		t.Fatal(err)
	}

	// Both packages must be in the DAG
	if len(nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(nodes))
	}

	// @kb/devkit must have zero deps (self-ref filtered)
	devkitNode := nodes[nodeKey{"@kb/devkit", "build"}]
	if len(devkitNode.deps) != 0 {
		t.Fatalf("@kb/devkit should have 0 deps, got %v", devkitNode.deps)
	}

	// @kb/core depends on @kb/devkit only
	coreNode := nodes[nodeKey{"@kb/core", "build"}]
	if len(coreNode.deps) != 1 || coreNode.deps[0].pkg != "@kb/devkit" {
		t.Fatalf("@kb/core deps = %v, want [{@kb/devkit build}]", coreNode.deps)
	}

	// Verify no cycles: compute in-degrees, confirm both packages become runnable
	inDegree := map[nodeKey]int{}
	for k := range nodes {
		inDegree[k] = 0
	}
	for k, n := range nodes {
		_ = k
		for _, dep := range n.deps {
			inDegree[nodeKey{dep.pkg, dep.task}]++
		}
	}

	// At least one node must have in-degree 0 (DAG root)
	hasRoot := false
	for _, deg := range inDegree {
		if deg == 0 {
			hasRoot = true
			break
		}
	}
	if !hasRoot {
		t.Fatal("no DAG root found — cycle detected (in-degree > 0 for all nodes)")
	}
}
