package checks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kb-labs/devkit/internal/config"
	"github.com/kb-labs/devkit/internal/workspace"
)

// case: S-277.3
func TestReleaseBoundaryRejectsShippedWorkspaceDependencyOnInternalTool(t *testing.T) {
	root := t.TempDir()
	gatingDir := filepath.Join(root, "apps", "host")
	internalDir := filepath.Join(root, "tools", "runner")
	for _, dir := range []string{gatingDir, internalDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(gatingDir, "package.json"), []byte(`{"name":"@kb-labs/host","dependencies":{"@kb-labs/runner":"workspace:*"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(internalDir, "package.json"), []byte(`{"name":"@kb-labs/runner"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	ws := &workspace.Workspace{
		Root: root,
		Packages: []workspace.Package{
			{Name: "@kb-labs/host", Dir: gatingDir, RelPath: "apps/host", PnpmMember: true},
			{Name: "@kb-labs/runner", Dir: internalDir, RelPath: "tools/runner", ReleaseLane: "internal-tool", PnpmMember: true},
		},
	}
	results := RunAll(ws, &config.DevkitConfig{}, Default(), nil, nil)
	workspaceResult, exists := results["workspace"]
	if !exists || len(workspaceResult.Issues) != 1 {
		t.Fatalf("workspace check result = %#v, want one gating-to-internal dependency finding", workspaceResult)
	}
	for _, expected := range []string{"@kb-labs/host", "@kb-labs/runner", "workspace dependency", "gating package"} {
		if !strings.Contains(workspaceResult.Issues[0].Message, expected) {
			t.Errorf("issue %q does not identify %q", workspaceResult.Issues[0].Message, expected)
		}
	}
}
