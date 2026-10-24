package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestYAMLOrderedCategoriesAndTaskVariants(t *testing.T) {
	raw := []byte(`
schemaVersion: 2
workspace:
  packageManager: pnpm
  categories:
    tools:
      match: ["infra/*"]
      preset: go-binary
    libs:
      match: ["packages/*"]
      preset: node-lib
tasks:
  build:
    command: pnpm build
    inputs: ["src/**"]
  test:
    - categories: ["libs"]
      command: pnpm test
    - categories: ["tools"]
      command: go test ./...
`)

	var cfg yamlConfig
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("yaml.Unmarshal error: %v", err)
	}

	if len(cfg.Workspace.Categories) != 2 || cfg.Workspace.Categories[0].Name != "tools" || cfg.Workspace.Categories[1].Name != "libs" {
		t.Fatalf("ordered categories = %#v", cfg.Workspace.Categories)
	}

	if len(cfg.Tasks["build"]) != 1 || cfg.Tasks["build"][0].Command != "pnpm build" {
		t.Fatalf("single build task variant = %#v", cfg.Tasks["build"])
	}
	if len(cfg.Tasks["test"]) != 2 || cfg.Tasks["test"][1].Command != "go test ./..." {
		t.Fatalf("list task variants = %#v", cfg.Tasks["test"])
	}
}

// case: S-179.27
func TestChecksTimeoutConfiguration(t *testing.T) {
	cfg, err := parseYAMLBytes([]byte("checks:\n  timeout_ms: 250\n"))
	if err != nil {
		t.Fatalf("parse configured timeout: %v", err)
	}
	if cfg.Checks.TimeoutMS != 250 {
		t.Fatalf("timeout_ms = %d, want configured 250", cfg.Checks.TimeoutMS)
	}

	root := t.TempDir()
	configPath := filepath.Join(root, "devkit.yaml")
	if err := os.WriteFile(configPath, []byte("schemaVersion: 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err = LoadFile(configPath)
	if err != nil {
		t.Fatalf("load default timeout: %v", err)
	}
	if cfg.Checks.TimeoutMS != DefaultCheckTimeoutMS {
		t.Fatalf("default timeout_ms = %d, want %d", cfg.Checks.TimeoutMS, DefaultCheckTimeoutMS)
	}

	if _, err := parseYAMLBytes([]byte("checks:\n  timeout_ms: 0\n")); err == nil || !strings.Contains(err.Error(), "checks.timeout_ms must be a positive integer") {
		t.Fatalf("zero timeout error = %v, want positive-integer validation", err)
	}
}

// case: S-179.27
func TestChecksTimeoutInheritedFromExtendedPack(t *testing.T) {
	root := t.TempDir()
	packPath := filepath.Join(root, "pack.yaml")
	configPath := filepath.Join(root, "devkit.yaml")
	if err := os.WriteFile(packPath, []byte("checks:\n  timeout_ms: 250\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte("extends:\n  - ./pack.yaml\nschemaVersion: 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadFile(configPath)
	if err != nil {
		t.Fatalf("load config extending timeout pack: %v", err)
	}
	if cfg.Checks.TimeoutMS != 250 {
		t.Fatalf("timeout_ms = %d, want inherited 250", cfg.Checks.TimeoutMS)
	}
}

func TestYAMLUnmarshalErrorsAndMapping(t *testing.T) {
	var cats yamlOrderedCategories
	if err := yaml.Unmarshal([]byte(`[]`), &cats); err == nil {
		t.Fatal("yamlOrderedCategories error = nil, want error")
	}

	raw := yamlConfig{
		Version: 2,
		Workspace: yamlWorkspace{
			PackageManager: "pnpm",
			MaxDepth:       4,
			Categories: yamlOrderedCategories{
				{Name: "libs", Category: CategoryConfig{Match: []string{"packages/*"}, Preset: "node-lib"}},
			},
		},
		Run:      yamlRun{Concurrency: 3},
		Affected: yamlAffected{Strategy: "command", Command: "./changed.sh"},
		Sync: yamlSync{
			Sources: map[string]yamlSyncSource{
				"templates": {Type: "local", Path: "./templates"},
			},
			Targets: []yamlSyncTarget{
				{Source: "templates", From: "base", To: ".config"},
			},
			Exclude: []string{"**/*.log"},
		},
		Tasks: map[string]yamlTask{
			"build": {
				{Command: "pnpm build", Inputs: []string{"src/**"}, Outputs: []string{"dist/**"}, Deps: []string{"^build"}},
			},
		},
		Presets: map[string]yamlPreset{
			"custom": {
				Extends:  "node-lib",
				Language: "typescript",
				PackageJSON: yamlPackageJSON{
					RequiredScripts: []string{"build"},
				},
				TSConfig: yamlTSConfig{
					MustExtendPattern: "@kb-labs/devkit/tsconfig/",
				},
				TSup: yamlTSup{
					MustUsePreset: true,
				},
				ESLint: yamlESLint{
					MustUsePreset: true,
				},
				Structure: yamlStructure{
					RequiredFiles: []string{"README.md"},
				},
				Deps: yamlDeps{
					CheckLinks: true,
				},
				Go: yamlGo{
					MinVersion: "1.22",
				},
			},
		},
		Custom: []yamlCustomCheck{
			{Name: "lint", Run: "pnpm lint", On: []string{"check"}, Language: "typescript", Categories: []string{"adapter"}, Scope: "workspace"},
		},
	}

	cfg := mapYAML(raw)
	if cfg.Version != 2 || cfg.Run.Concurrency != 3 || cfg.Affected.Command != "./changed.sh" {
		t.Fatalf("mapped config basics = %+v", cfg)
	}
	if len(cfg.Workspace.Categories) != 1 || cfg.Workspace.Categories[0].Name != "libs" {
		t.Fatalf("mapped categories = %#v", cfg.Workspace.Categories)
	}
	if len(cfg.Sync.Targets) != 1 || cfg.Sync.Targets[0].To != ".config" {
		t.Fatalf("mapped sync = %+v", cfg.Sync)
	}
	if len(cfg.Tasks["build"]) != 1 || cfg.Tasks["build"][0].Command != "pnpm build" {
		t.Fatalf("mapped tasks = %#v", cfg.Tasks["build"])
	}
	if len(cfg.Custom) != 1 || cfg.Custom[0].Name != "lint" {
		t.Fatalf("mapped custom checks = %#v", cfg.Custom)
	}
	if len(cfg.Custom[0].Categories) != 1 || cfg.Custom[0].Categories[0] != "adapter" {
		t.Fatalf("mapped custom check categories = %#v", cfg.Custom[0].Categories)
	}
	if cfg.Custom[0].Scope != "workspace" {
		t.Fatalf("mapped custom check scope = %q, want workspace", cfg.Custom[0].Scope)
	}

	preset := mapPreset(raw.Presets["custom"])
	if !preset.TSup.MustUsePreset || !preset.ESLint.MustUsePreset || preset.Go.MinVersion != "1.22" {
		t.Fatalf("mapPreset = %+v", preset)
	}
}

func TestCustomCheckRejectsUnknownScope(t *testing.T) {
	_, err := parseYAMLBytes([]byte("custom_checks:\n  - name: sample\n    run: node check.mjs\n    scope: repository\n"))
	if err == nil || !strings.Contains(err.Error(), "expected package or workspace") {
		t.Fatalf("parse unknown custom check scope error = %v, want explicit scope validation", err)
	}
}

func TestWorkspaceCustomCheckRejectsPackageFilters(t *testing.T) {
	_, err := parseYAMLBytes([]byte("custom_checks:\n  - name: sample\n    run: node check.mjs\n    scope: workspace\n    categories: [adapter]\n"))
	if err == nil || !strings.Contains(err.Error(), "cannot set package language or categories") {
		t.Fatalf("parse workspace package filter error = %v, want explicit scope validation", err)
	}
}

func TestPresetConfigAndYamlTaskObjectFallback(t *testing.T) {
	cfg := &DevkitConfig{
		Presets: map[string]Preset{
			"custom": {Extends: "node-lib"},
		},
	}
	cat := CategoryConfig{Preset: "custom"}
	preset, err := cat.PresetConfig(cfg)
	if err != nil {
		t.Fatalf("PresetConfig error: %v", err)
	}
	if preset.Language != "typescript" {
		t.Fatalf("PresetConfig preset = %+v", preset)
	}

	var task yamlTask
	if err := yaml.Unmarshal([]byte("command: pnpm build\ninputs: [src/**]\n"), &task); err != nil {
		t.Fatalf("yamlTask single object unmarshal error: %v", err)
	}
	if len(task) != 1 || task[0].Command != "pnpm build" {
		t.Fatalf("yamlTask single object = %#v", task)
	}
}
