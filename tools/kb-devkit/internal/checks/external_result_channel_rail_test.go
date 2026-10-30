//go:build external_check_result_channel

package checks

import (
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
	pkg := workspace.Package{
		Name:     "@fixture/non-anchor-result-channel",
		Dir:      root,
		Category: "plugin-entry",
		Preset:   "node-lib",
		Language: "typescript",
		RelPath:  ".",
	}
	preset := config.Preset{Language: pkg.Language}
	count := 0
	for _, rule := range registry.rules {
		external, ok := rule.(*externalRule)
		if !ok {
			continue
		}
		count++
		output, runErr := external.runCommand(external.run, pkg, preset, nil, false)
		if runErr != nil {
			t.Fatalf("external check %s failed while writing its result envelope: %v", external.name, runErr)
		}
		if output.Issues == nil {
			t.Fatalf("external check %s did not produce a valid result-file envelope", external.name)
		}
		t.Logf("%s: valid result envelope (%d issue(s))", external.name, len(output.Issues))
	}
	if count == 0 {
		t.Fatal("devkit.yaml registered no external checks for the check phase")
	}
	t.Logf("validated result envelopes from %d registered external checks", count)
}
