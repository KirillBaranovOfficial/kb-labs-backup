package command

import (
	"errors"
	"os"

	"github.com/kb-labs/clikit/diag"
	"github.com/kb-labs/clikit/result"
)

// Exit is the only process boundary for Go tools. Callers must provide a
// status selected by the shared diagnostic contract.
func Exit(status diag.ExitStatus) {
	if !status.IsSelected() {
		diagnostic := diag.FromError(errors.New("command returned without selecting an exit status"))
		diagnostic.Message = "A command returned without selecting an exit status."
		diagnostic.Reason = "The command did not produce an explicit process outcome."
		status = result.RenderDiag(os.Stderr, os.Stderr, diagnostic, result.ModeHuman)
	}
	os.Exit(status.Code())
}
