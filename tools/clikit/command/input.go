// Package command contains shared Cobra boundary behavior for KB Labs Go CLIs.
package command

import (
	"fmt"

	"github.com/kb-labs/clikit/diag"
	"github.com/spf13/cobra"
)

// InstallInputBoundary converts flag parsing and argument validation failures
// from every registered command into the shared invalid-input diagnostic.
func InstallInputBoundary(root *cobra.Command) {
	root.SetFlagErrorFunc(func(cmd *cobra.Command, _ error) error {
		return invalidInput(cmd)
	})
	var visit func(*cobra.Command)
	visit = func(parent *cobra.Command) {
		for _, child := range parent.Commands() {
			if child.Args != nil {
				validateArgs := child.Args
				child.Args = func(cmd *cobra.Command, args []string) error {
					if err := validateArgs(cmd, args); err != nil {
						return invalidInput(cmd)
					}
					return nil
				}
			}
			visit(child)
		}
	}
	visit(root)
}

func invalidInput(cmd *cobra.Command) error {
	return diag.New("KB_RUNTIME_INPUT_INVALID", "The command arguments are invalid.",
		diag.WithHint(fmt.Sprintf("Correct the arguments and run `%s --help` for usage.", cmd.CommandPath())))
}
