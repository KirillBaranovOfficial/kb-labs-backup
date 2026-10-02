package diag

import "sync"

// Hint registry for known platform error codes. Process exits use the shared
// fixed table below and do not depend on per-command registrations.

var (
	mu        sync.RWMutex
	hints     = map[string]string{}
	exitCodes = map[string]int{}
)

// Shared process exit codes for Go tool CLIs.
const (
	ExitOK        = 0
	ExitError     = 1 // generic failure
	ExitInput     = 2 // invalid usage or input
	ExitConfig    = ExitInput
	ExitForbidden = ExitError
)

// RegisterHints adds code→hint defaults. Safe to call from multiple inits.
func RegisterHints(m map[string]string) {
	mu.Lock()
	defer mu.Unlock()
	for k, v := range m {
		hints[k] = v
	}
}

// RegisterExitCodes associates typed diagnostics with shared exit meanings.
// Values outside the shared 1/2 failure table are discarded.
func RegisterExitCodes(m map[string]int) {
	mu.Lock()
	defer mu.Unlock()
	for code, exitCode := range m {
		if exitCode == ExitInput || exitCode == ExitError {
			exitCodes[code] = exitCode
		}
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
	mu.RLock()
	defer mu.RUnlock()
	if code, ok := d.Meta["exitCode"].(int); ok && (code == ExitInput || code == ExitError) {
		return code
	}
	if code, ok := exitCodes[d.Code]; ok {
		return code
	}
	return ExitError
}
