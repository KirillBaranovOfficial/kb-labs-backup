package cmd

import (
	"fmt"

	"github.com/kb-labs/clikit/diag"
)

func commandFailure(command string) error {
	return diag.New("KB_RUNTIME_COMMAND_FAILED", "The command could not complete.",
		diag.WithMeta(map[string]any{"command": command}),
		diag.WithHint(fmt.Sprintf("Review the command's reported issues, then run %s --help for accepted inputs.", command)))
}
