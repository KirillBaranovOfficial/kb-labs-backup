package diag

import "os/exec"

// Numeric category codes stay private; explicit process results are validated
// through ProcessExitStatus.
const (
	exitOK    = 0
	exitError = 1
	exitInput = 2
)

// ExitStatus is a process status selected by the diagnostic contract. Its
// value can only be constructed in this package from a catalogue class or an
// explicit process outcome validated against the portable OS exit range.
type ExitStatus struct {
	code     int
	selected bool
}

// Code returns the OS status for the single launcher boundary.
func (s ExitStatus) Code() int {
	if !s.selected {
		return exitError
	}
	return s.code
}

// IsSelected reports whether diag explicitly constructed this process outcome.
func (s ExitStatus) IsSelected() bool { return s.selected }

func SuccessStatus() ExitStatus          { return ExitStatus{code: exitOK, selected: true} }
func operationFailureStatus() ExitStatus { return ExitStatus{code: exitError, selected: true} }
func inputRefusalStatus() ExitStatus     { return ExitStatus{code: exitInput, selected: true} }

// PlanChangesStatus and PlanDriftStatus are the non-error outcomes declared
// by the kb-deploy plan command's documented result contract.
func PlanChangesStatus() ExitStatus          { return ExitStatus{code: 2, selected: true} }
func PlanDriftStatus() ExitStatus            { return ExitStatus{code: 4, selected: true} }
func DeployChangesAppliedStatus() ExitStatus { return ExitStatus{code: 1, selected: true} }

// ProcessExitStatus preserves an explicit operating-system process outcome,
// including command fixtures whose exit values are part of their contract.
// Values outside the portable process exit range become an operation failure.
func ProcessExitStatus(code int) ExitStatus {
	if code < 0 || code > 255 {
		return operationFailureStatus()
	}
	return ExitStatus{code: code, selected: true}
}

// ChildProcessStatus preserves the explicit child-process pass-through
// contract and normalizes values outside the portable process exit range.
func ChildProcessStatus(child *exec.ExitError) ExitStatus {
	if child == nil || child.ExitCode() < 0 {
		return operationFailureStatus()
	}
	return ProcessExitStatus(child.ExitCode())
}

// ExitCode maps the shared CLI error categories to the shared process table.
func ExitCode(d *Diag) ExitStatus {
	if d == nil {
		return SuccessStatus()
	}
	if entry, ok := LookupCatalog(d.Code); ok {
		if entry.ProcessExitClass == ExitClassInputRefusal {
			return inputRefusalStatus()
		}
	}
	return operationFailureStatus()
}
