package cmd

import (
	"bytes"
	"strings"
	"testing"

	clikitcommand "github.com/kb-labs/clikit/command"
	"github.com/spf13/cobra"
)

// case: S-203.6
func TestLiveCommandTreeHelpAndUnknownCommandContract(t *testing.T) {
	var visit func(*cobra.Command)
	visit = func(command *cobra.Command) {
		path := strings.Fields(strings.TrimPrefix(command.CommandPath(), rootCmd.CommandPath()))
		for _, args := range [][]string{append(append([]string{}, path...), "--help"), append(append([]string{}, path...), "-h"), append([]string{"help"}, path...)} {
			var output bytes.Buffer
			if !clikitcommand.HandleCobraHelp(rootCmd, args, &output) || !strings.Contains(output.String(), "Usage:") {
				t.Errorf("help %q did not print usage: %s", args, output.String())
			}
		}
		for _, child := range command.Commands() {
			visit(child)
		}
	}
	visit(rootCmd)

	var stdout, stderr bytes.Buffer
	status, handled := clikitcommand.HandleUnknownCommand(rootCmd, []string{"x70-command-that-does-not-exist"}, &stdout, &stderr, outputMode(), false)
	if !handled || status.Code() != 2 || !strings.Contains(stderr.String(), "kb-deploy --help") {
		t.Fatalf("unknown command result status=%d handled=%v stdout=%s stderr=%s", status.Code(), handled, stdout.String(), stderr.String())
	}
}
