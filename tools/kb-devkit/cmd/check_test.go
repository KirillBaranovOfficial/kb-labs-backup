package cmd

import (
	"errors"
	"testing"

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
	if len(checkExecutionFailures(map[string]checks.PackageResult{
		"a": {Issues: []checks.Issue{{Check: "browser-node-builtins", CheckFailure: true, Cause: "exit 1"}}},
		"b": {Issues: []checks.Issue{{Check: "adapter-manifest", CheckFailure: true, Cause: "exit 2"}}},
	})) != 2 {
		t.Fatal("checkExecutionFailures dropped a crashed check")
	}
}
