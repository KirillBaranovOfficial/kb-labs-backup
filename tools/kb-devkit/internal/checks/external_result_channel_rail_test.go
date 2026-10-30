//go:build external_check_result_channel

package checks

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/kb-labs/devkit/internal/config"
	"github.com/kb-labs/devkit/internal/workspace"
)

func TestRegisteredExternalChecksWriteValidResultEnvelopes(t *testing.T) {
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not locate result-channel rail source")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "../../../.."))
	cfg, err := config.LoadFile(filepath.Join(root, "devkit.yaml"))
	if err != nil {
		t.Fatalf("load registered external checks: %v", err)
	}
	registry := Build(cfg, root, "check")
	ws, err := workspace.New(root, cfg)
	if err != nil {
		t.Fatalf("discover check contexts: %v", err)
	}
	count := 0
	for _, rule := range registry.rules {
		external, ok := rule.(*externalRule)
		if !ok {
			continue
		}
		if external.scope == "workspace" {
			assertRequiredBuildOutputs(t, ws.Packages, external)
			packages := make([]externalWorkspacePackage, 0, len(ws.Packages))
			for _, pkg := range ws.Packages {
				packages = append(packages, externalWorkspacePackage{
					Name: pkg.Name, Dir: pkg.Dir, RelPath: pkg.RelPath, Category: pkg.Category,
					Preset: pkg.Preset, Language: pkg.Language, BuildTarget: pkg.BuildTarget,
				})
			}
			count++
			input := externalInput{WorkspaceRoot: root, Check: external.name, Phase: "check", Workspace: &externalWorkspace{Root: ws.Root, Packages: packages}}
			output, runErr := external.runCommandWithInput(external.run, input)
			assertExternalResultEnvelope(t, external.name, output, runErr)
			continue
		}
		contexts := make([]workspace.Package, 0, 1)
		for _, pkg := range ws.Packages {
			if len(external.anchorPackages) > 0 && !containsPackageName(external.anchorPackages, pkg.Name) {
				continue
			}
			if !external.appliesToCategory(pkg.Category) || !matchesLanguage(config.CustomCheck{Language: external.language}, pkg.Language) {
				continue
			}
			contexts = append(contexts, pkg)
		}
		if len(external.anchorPackages) > 0 && len(contexts) != len(external.anchorPackages) {
			t.Fatalf("external check %s declares anchor packages absent from the discovered workspace: declared %v, found %d", external.name, external.anchorPackages, len(contexts))
		}
		if len(contexts) == 0 {
			t.Fatalf("external check %s has no real package context in the discovered workspace", external.name)
		}
		if len(contexts) > 1 && len(external.anchorPackages) == 0 {
			contexts = contexts[:1]
		}
		for _, pkg := range contexts {
			assertRequiredBuildOutputs(t, ws.Packages, external)
			count++
			preset := cfg.Presets[pkg.Preset]
			output, runErr := external.runCommand(external.run, pkg, preset, nil, false)
			assertExternalResultEnvelope(t, external.name, output, runErr)
		}
	}
	if count == 0 {
		t.Fatal("devkit.yaml registered no external checks for the check phase")
	}
	t.Logf("validated result envelopes from %d registered external-check contexts", count)
}

func assertRequiredBuildOutputs(t *testing.T, packages []workspace.Package, external *externalRule) {
	t.Helper()
	for _, requiredPackage := range external.requiresBuild {
		required, ok := findPackage(packages, requiredPackage)
		if !ok {
			t.Fatalf("external check %s requires build output from undiscovered package %s", external.name, requiredPackage)
		}
		if _, err := os.Stat(filepath.Join(required.Dir, "dist", "index.js")); err != nil {
			t.Fatalf("external check %s requires %s/dist/index.js; run the build-dependent result-channel rail after workspace-build: %v", external.name, requiredPackage, err)
		}
	}
}

func assertExternalResultEnvelope(t *testing.T, check string, output externalCheckOutput, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("external check %s failed while writing its result envelope: %v", check, err)
	}
	if output.Issues == nil {
		t.Fatalf("external check %s did not produce a valid result-file envelope", check)
	}
	t.Logf("%s: valid result envelope (%d issue(s))", check, len(output.Issues))
}

func containsPackageName(names []string, target string) bool {
	for _, name := range names {
		if name == target {
			return true
		}
	}
	return false
}

func findPackage(packages []workspace.Package, name string) (workspace.Package, bool) {
	for _, pkg := range packages {
		if pkg.Name == name {
			return pkg, true
		}
	}
	return workspace.Package{}, false
}
