package command

import (
	"os"

	"github.com/kb-labs/clikit/diag"
)

// Exit is the only process boundary for Go tools. Callers must provide a
// status selected by the shared diagnostic contract.
func Exit(status diag.ExitStatus) {
	os.Exit(status.Code())
}
