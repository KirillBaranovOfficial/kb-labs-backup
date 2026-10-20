package command

import (
	"errors"
	"testing"

	"github.com/kb-labs/clikit/diag"
	"github.com/spf13/cobra"
)

// case: S-203.2
func TestInstallInputBoundaryNormalizesRegisteredArgumentFailures(t *testing.T) {
	root := &cobra.Command{Use: "kb-test", SilenceErrors: true, SilenceUsage: true}
	child := &cobra.Command{Use: "run", Args: cobra.ExactArgs(1), Run: func(*cobra.Command, []string) {}}
	root.AddCommand(child)
	InstallInputBoundary(root)
	root.SetArgs([]string{"run"})
	_, err := root.ExecuteC()
	var d *diag.Diag
	if !errors.As(err, &d) {
		t.Fatalf("error = %v, want *Diag", err)
	}
	if d.Code != "KB_RUNTIME_INPUT_INVALID" || diag.ExitCode(d) != diag.ExitCode(diag.New("KB_RUNTIME_INPUT_INVALID", "invalid")) || d.Hint == "" {
		t.Fatalf("diagnostic = %+v, exit=%d", d, diag.ExitCode(d).Code())
	}
}

// case: S-203.2
func TestInstallInputBoundaryNormalizesFlagParseFailures(t *testing.T) {
	root := &cobra.Command{Use: "kb-test", SilenceErrors: true, SilenceUsage: true}
	root.Flags().Bool("known", false, "known flag")
	InstallInputBoundary(root)
	root.SetArgs([]string{"--unknown"})
	_, err := root.ExecuteC()
	var d *diag.Diag
	if !errors.As(err, &d) || d.Code != "KB_RUNTIME_INPUT_INVALID" {
		t.Fatalf("error = %v, want KB_RUNTIME_INPUT_INVALID", err)
	}
}

// case: S-203.24
func TestInstallInputBoundaryNamesTheCommandHelpAction(t *testing.T) {
	root := &cobra.Command{Use: "kb-create", SilenceErrors: true, SilenceUsage: true}
	child := &cobra.Command{Use: "status", Args: cobra.NoArgs, Run: func(*cobra.Command, []string) {}}
	root.AddCommand(child)
	InstallInputBoundary(root)
	root.SetArgs([]string{"status", "unexpected"})
	_, err := root.ExecuteC()
	var failure *diag.Diag
	if !errors.As(err, &failure) || failure.Code != "KB_RUNTIME_INPUT_INVALID" {
		t.Fatalf("error = %v, want KB_RUNTIME_INPUT_INVALID", err)
	}
	if want := "Run `kb-create status --help` to see supported flags and arguments."; failure.Hint != want {
		t.Fatalf("hint = %q, want %q", failure.Hint, want)
	}
}
