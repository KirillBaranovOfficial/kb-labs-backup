package diag

// Numeric statuses are kept private so command packages cannot construct a
// process result from an integer.
const (
	exitOK    = 0
	exitError = 1
	exitInput = 2
)

// ExitStatus is a process status selected by the diagnostic contract. Its
// value can only be constructed in this package from a catalogue class or a
// declared non-error command outcome.
type ExitStatus struct{ code int }

// Code returns the OS status for the single launcher boundary.
func (s ExitStatus) Code() int { return s.code }

func SuccessStatus() ExitStatus          { return ExitStatus{code: exitOK} }
func operationFailureStatus() ExitStatus { return ExitStatus{code: exitError} }
func inputRefusalStatus() ExitStatus     { return ExitStatus{code: exitInput} }

// PlanChangesStatus and PlanDriftStatus are the non-error outcomes declared
// by the kb-deploy plan command's documented result contract.
func PlanChangesStatus() ExitStatus          { return ExitStatus{code: 2} }
func PlanDriftStatus() ExitStatus            { return ExitStatus{code: 4} }
func DeployChangesAppliedStatus() ExitStatus { return ExitStatus{code: 1} }

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
