package checks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/kb-labs/devkit/internal/config"
	"github.com/kb-labs/devkit/internal/workspace"
)

func TestExternalCommandCheckAndFix(t *testing.T) {
	root := t.TempDir()
	pkgDir := filepath.Join(root, "packages", "demo")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatalf("mkdir pkg: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "package.json"), []byte(`{"name":"@acme/demo","version":"1.0.0"}`), 0o644); err != nil {
		t.Fatalf("write package.json: %v", err)
	}

	script := filepath.Join(root, "external-check.sh")
	if err := os.WriteFile(script, []byte(`#!/bin/sh
if [ "$KB_DEVKIT_MODE" = "fix" ]; then
  if [ "$KB_DEVKIT_DRY_RUN" != "true" ]; then
    echo "# demo" > "$KB_DEVKIT_PACKAGE_DIR/README.md"
  fi
  printf '{"actions":["created README.md"]}\n'
  exit 0
fi
if [ ! -f "$KB_DEVKIT_PACKAGE_DIR/README.md" ]; then
  printf '{"issues":[{"check":"external-readme","severity":"error","message":"README missing","file":"%s/README.md","fix":"create README.md","capability":"scaffoldable"}]}\n' "$KB_DEVKIT_PACKAGE_DIR"
else
  printf '{"issues":[]}\n'
fi
`), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}

	cfg := &config.DevkitConfig{
		Presets: map[string]config.Preset{
			"ext-lib": {Language: "typescript"},
		},
		Checks: config.ChecksConfig{
			Packages: map[string]config.CheckPackConfig{
				"external-readme": {
					Enabled: true,
					Config:  map[string]any{"requiredFile": "README.md"},
				},
			},
		},
		Custom: []config.CustomCheck{{
			Name:     "external-readme",
			Run:      script,
			Fix:      script,
			On:       []string{"check"},
			Language: "typescript",
		}},
	}
	ws := &workspace.Workspace{
		Root: root,
		Packages: []workspace.Package{{
			Name:     "@acme/demo",
			Dir:      pkgDir,
			RelPath:  "packages/demo",
			Category: "ext-lib",
			Preset:   "ext-lib",
			Language: "typescript",
		}},
	}

	registry := Build(cfg, root, "check")
	results := RunAll(ws, cfg, registry, []string{"external-readme"})
	result := results["@acme/demo"]
	if len(result.Issues) != 1 || result.Issues[0].Check != "external-readme" {
		t.Fatalf("issues = %#v, want external-readme issue", result.Issues)
	}

	var fixer Fixer
	for _, rule := range registry.All() {
		if rule.Name() == "external-readme" {
			f, ok := rule.(Fixer)
			if !ok {
				t.Fatalf("external rule does not implement Fixer")
			}
			fixer = f
			break
		}
	}
	if fixer == nil {
		t.Fatal("external fixer not found in registry")
	}
	if err := fixer.Apply(result.Package, result.Issues, false); err != nil {
		t.Fatalf("Apply error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(pkgDir, "README.md")); err != nil {
		t.Fatalf("README.md not created: %v", err)
	}
}

func TestWorkspaceExternalCheckRunsOnceWithResolvedPackageCategories(t *testing.T) {
	root := t.TempDir()
	payloadPath := filepath.Join(root, "payload.json")
	callsPath := filepath.Join(root, "calls.txt")
	scriptPath := filepath.Join(root, "check.sh")
	script := "#!/bin/sh\ncat > '" + payloadPath + "'\nprintf x >> '" + callsPath + "'\nprintf '{\"issues\":[]}\\n'\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
	cfg := &config.DevkitConfig{
		Presets: map[string]config.Preset{"typescript": {Language: "typescript"}},
		Custom:  []config.CustomCheck{{Name: "workspace-check", Run: scriptPath, On: []string{"check"}, Scope: "workspace"}},
	}
	ws := &workspace.Workspace{Root: root, Packages: []workspace.Package{
		{Name: "@acme/adapter", Dir: filepath.Join(root, "adapters", "adapter"), Category: "adapter", Preset: "typescript", Language: "typescript", RelPath: "adapters/adapter"},
		{Name: "@acme/library", Dir: filepath.Join(root, "packages", "library"), Category: "ts-lib", Preset: "typescript", Language: "typescript", RelPath: "packages/library"},
	}}
	results := RunAll(ws, cfg, Build(cfg, root, "check"), nil)
	if got := string(mustReadFile(t, callsPath)); got != "x" {
		t.Fatalf("workspace check calls = %q, want one invocation", got)
	}
	var input struct {
		Workspace struct {
			Root     string `json:"root"`
			Packages []struct {
				Name     string `json:"name"`
				Category string `json:"category"`
				RelPath  string `json:"relPath"`
			} `json:"packages"`
		} `json:"workspace"`
	}
	if err := json.Unmarshal(mustReadFile(t, payloadPath), &input); err != nil {
		t.Fatalf("decode workspace stdin: %v", err)
	}
	if input.Workspace.Root != root || len(input.Workspace.Packages) != 2 {
		t.Fatalf("workspace input = %#v, want root and both discovered packages", input.Workspace)
	}
	if input.Workspace.Packages[0].Category != "adapter" || input.Workspace.Packages[1].Category != "ts-lib" {
		t.Fatalf("package categories = %#v, want resolved devkit categories", input.Workspace.Packages)
	}
	if _, ok := results["workspace"]; ok {
		t.Fatalf("clean workspace check created a result with issues: %#v", results["workspace"])
	}
}

func mustReadFile(t *testing.T, file string) []byte {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read %s: %v", file, err)
	}
	return data
}

func TestBuildSkipsDisabledOrMismatchedExternalChecks(t *testing.T) {
	cfg := &config.DevkitConfig{
		Checks: config.ChecksConfig{
			Packages: map[string]config.CheckPackConfig{
				"disabled": {Enabled: false},
			},
		},
		Custom: []config.CustomCheck{
			{Name: "disabled", Run: "true", On: []string{"check"}, Language: "typescript"},
			{Name: "go-only", Run: "true", On: []string{"check"}, Language: "go"},
		},
	}

	registry := Build(cfg, t.TempDir(), "check")
	rules := registry.RulesFor(config.Preset{Language: "typescript"})
	for _, rule := range rules {
		if rule.Name() == "disabled" || rule.Name() == "go-only" {
			t.Fatalf("unexpected external rule %q in typescript registry", rule.Name())
		}
	}
}

func TestExternalCheckHonorsCategoryFilter(t *testing.T) {
	root := t.TempDir()
	script := filepath.Join(root, "check.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf '{\"issues\":[{\"check\":\"adapter-manifest\",\"severity\":\"error\",\"message\":\"reported\"}]}\\n'\n"), 0o755); err != nil {
		t.Fatalf("write check script: %v", err)
	}
	cfg := &config.DevkitConfig{
		Presets: map[string]config.Preset{"typescript": {Language: "typescript"}},
		Custom:  []config.CustomCheck{{Name: "adapter-manifest", Run: script, On: []string{"check"}, Language: "typescript", Categories: []string{"adapter"}}},
	}
	ws := &workspace.Workspace{Root: root, Packages: []workspace.Package{
		{Name: "@acme/adapter", Dir: root, Category: "adapter", Preset: "typescript", Language: "typescript"},
		{Name: "@acme/library", Dir: root, Category: "ts-lib", Preset: "typescript", Language: "typescript"},
	}}
	results := RunAll(ws, cfg, Build(cfg, root, "check"), []string{"adapter-manifest"})
	if len(results["@acme/adapter"].Issues) != 1 {
		t.Fatalf("adapter issues = %#v, want one", results["@acme/adapter"].Issues)
	}
	if len(results["@acme/library"].Issues) != 0 {
		t.Fatalf("library issues = %#v, want none", results["@acme/library"].Issues)
	}
}

func TestExternalCheckUsesCallerToolPath(t *testing.T) {
	root := t.TempDir()
	binDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	node := filepath.Join(binDir, "node")
	if err := os.WriteFile(node, []byte("#!/bin/sh\nprintf '{\"issues\":[{\"check\":\"tool-path\",\"severity\":\"error\",\"message\":\"caller node selected\"}]}\\n'\n"), 0o755); err != nil {
		t.Fatalf("write fake node: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+"/bin:/usr/bin")
	rule := &externalRule{name: "tool-path", run: "node", phase: "check", workspaceRoot: root}
	issues := rule.Check(workspace.Package{Name: "@acme/demo", Dir: root}, config.Preset{Language: "typescript"})
	if len(issues) != 1 || issues[0].Message != "caller node selected" {
		t.Fatalf("issues = %#v, want the fake node issue", issues)
	}
}
