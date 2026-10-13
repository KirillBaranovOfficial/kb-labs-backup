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
	if entry, ok := LookupCatalog(d.Code); ok && entry.ProcessExitCode != ExitOK {
		return entry.ProcessExitCode
	}
	if d.ProcessExitCode != ExitOK {
		return d.ProcessExitCode
	}
	if d.Code == "KB_RUNTIME_INPUT_INVALID" {
		return ExitInput
	}
	return ExitError
}
