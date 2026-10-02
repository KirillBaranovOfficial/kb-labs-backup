package diag

import "sync"

// Hint registry for known platform error codes. Process exits use the shared
// fixed table below and do not depend on per-command registrations.

var (
	mu    sync.RWMutex
	hints = map[string]string{}
)

// Shared process exit codes for Go tool CLIs.
const (
	ExitOK    = 0
	ExitError = 1 // generic failure
	ExitInput = 2 // invalid usage or input
)

// RegisterHints adds code→hint defaults. Safe to call from multiple inits.
func RegisterHints(m map[string]string) {
	mu.Lock()
	defer mu.Unlock()
	for k, v := range m {
		hints[k] = v
	}
}

// HintFor returns the registered default hint for a code, or "".
func HintFor(code string) string {
	mu.RLock()
	defer mu.RUnlock()
	return hints[code]
}

// ExitCode maps the shared CLI error categories to the shared process table.
func ExitCode(d *Diag) int {
	if d == nil {
		return ExitOK
	}
	if d.Code == "KB_RUNTIME_INPUT_INVALID" {
		return ExitInput
	}
	return ExitError
}
