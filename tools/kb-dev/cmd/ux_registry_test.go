package cmd

import (
	"testing"

	"github.com/spf13/cobra"
)

// case: S-203.6
func TestRegisteredCommandsAreDiscoverableForUXCoverage(t *testing.T) {
	commands := registeredCommands(rootCmd)
	if len(commands) == 0 {
		t.Fatal("kb-dev command registry is empty")
	}
	for _, command := range commands {
		if command.Run == nil && command.RunE == nil && len(command.Commands()) == 0 {
			t.Errorf("registered leaf command %q has no executable handler", command.CommandPath())
		}
	}
}

func registeredCommands(root *cobra.Command) []*cobra.Command {
	commands := make([]*cobra.Command, 0)
	var visit func(*cobra.Command)
	visit = func(parent *cobra.Command) {
		for _, child := range parent.Commands() {
			commands = append(commands, child)
			visit(child)
		}
	}
	visit(root)
	return commands
}
