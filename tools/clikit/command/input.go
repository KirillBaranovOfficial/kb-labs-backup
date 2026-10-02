// Package command contains shared Cobra boundary behavior for KB Labs Go CLIs.
package command

import (
	"fmt"

	"github.com/kb-labs/clikit/diag"
	"github.com/kb-labs/clikit/result"
	"github.com/spf13/cobra"
)

const boundaryAnnotation = "clikit.sharedResultBoundary"

// InstallInputBoundary converts flag parsing and argument validation failures
// from every registered command into the shared invalid-input diagnostic.
func InstallInputBoundary(root *cobra.Command) {
	root.SetFlagErrorFunc(func(cmd *cobra.Command, _ error) error {
		return invalidInput(cmd)
	})
	var visit func(*cobra.Command)
	visit = func(parent *cobra.Command) {
		markBoundary(parent)
		for _, child := range parent.Commands() {
			if !BoundaryInstalled(child) {
				installCommandBoundary(child)
			}
			visit(child)
		}
	}
	visit(root)
}

func installCommandBoundary(command *cobra.Command) {
	if command.Args != nil {
		validateArgs := command.Args
		command.Args = func(cmd *cobra.Command, args []string) error {
			if err := validateArgs(cmd, args); err != nil {
				return invalidInput(cmd)
			}
			return nil
		}
	}
	runE := command.RunE
	run := command.Run
	if runE != nil {
		command.RunE = func(cmd *cobra.Command, args []string) error {
			err := runE(cmd, args)
			if err == nil || result.IsStreamComplete(err) {
				return err
			}
			if _, ok := result.ReturnedData(err); ok {
				return err
			}
			return diag.FromError(err)
		}
		command.Run = nil
	} else if run != nil {
		command.RunE = func(cmd *cobra.Command, args []string) error {
			run(cmd, args)
			return nil
		}
		command.Run = nil
	}
	markBoundary(command)
}

// BoundaryInstalled reports whether the command was registered when the
// shared dispatcher installed its result boundary.
func BoundaryInstalled(command *cobra.Command) bool {
	return command.Annotations != nil && command.Annotations[boundaryAnnotation] == "true"
}

func markBoundary(command *cobra.Command) {
	if command.Annotations == nil {
		command.Annotations = make(map[string]string)
	}
	command.Annotations[boundaryAnnotation] = "true"
}

func invalidInput(cmd *cobra.Command) error {
	return diag.New("KB_RUNTIME_INPUT_INVALID", fmt.Sprintf("The arguments for %s are invalid.", cmd.CommandPath()))
}
