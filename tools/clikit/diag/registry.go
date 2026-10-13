package diag

// Shared process exit codes for Go tool CLIs.
const (
	ExitOK    = 0
	ExitError = 1 // generic failure
	ExitInput = 2 // invalid usage or input
)

// ExitCode maps the shared CLI error categories to the shared process table.
func ExitCode(d *Diag) int {
	if d == nil {
		return ExitOK
	}
	if entry, ok := LookupCatalog(d.Code); ok {
		if entry.ProcessExitClass == ExitClassInputRefusal {
			return ExitInput
		}
	}
	return ExitError
}
