package contracts

import "github.com/kb-labs/clikit/diag"

// Diagnostic converts shared request validation into the platform CLI failure
// contract, including its input exit code and field metadata.
func (e *RequestValidationError) Diagnostic() *diag.Diag {
	failure := NewDiag(e.Code, e.Message, e.Hint, e.Cause)
	failure.Meta = map[string]any{"field": e.Field}
	failure.ProcessExitCode = diag.ExitInput
	return failure
}

// ErrorStage is the moment of the user journey in which an error happened.
// The set is open; the values below are the ones the launcher emits.
type ErrorStage string

const (
	StagePreflight  ErrorStage = "preflight"
	StageResolve    ErrorStage = "resolve"
	StageApply      ErrorStage = "apply"
	StageVerify     ErrorStage = "verify"
	StageRecover    ErrorStage = "recover"
	StageStart      ErrorStage = "start"
	StageRun        ErrorStage = "run"
	StageLogin      ErrorStage = "login"
	StageAddProject ErrorStage = "add-project"
	StageExtend     ErrorStage = "extend"
	StageUpdate     ErrorStage = "update"
	StageRollback   ErrorStage = "rollback"
)

// Error codes emitted by kb-create's operation layer.
const (
	CodeToolchainUnsupported      = "KB_INSTALL_TOOLCHAIN_UNSUPPORTED"
	CodeNodeVersion               = "KB_INSTALL_NODE_VERSION"
	CodeMemoryInsufficient        = "KB_INSTALL_MEMORY_INSUFFICIENT"
	CodeResourceCheckFailed       = "KB_INSTALL_RESOURCE_CHECK_FAILED"
	CodePackageManagerKilled      = "KB_INSTALL_PACKAGE_MANAGER_KILLED"
	CodePortInUse                 = "KB_INSTALL_PORT_IN_USE"
	CodeIncompatibleComponents    = "KB_INSTALL_INCOMPATIBLE_COMPONENTS"
	CodeProviderUnresolved        = "KB_INSTALL_PROVIDER_UNRESOLVED"
	CodeProviderAmbiguous         = "KB_INSTALL_PROVIDER_AMBIGUOUS"
	CodeInputRequired             = "KB_INSTALL_INPUT_REQUIRED"
	CodeConfigRequired            = "KB_INSTALL_CONFIG_REQUIRED"
	CodeScenarioInvalid           = "KB_INSTALL_SCENARIO_INVALID"
	CodeWizardInputInvalid        = "KB_INSTALL_WIZARD_INPUT_INVALID"
	CodeSecretInputInvalid        = "KB_INSTALL_SECRET_INPUT_INVALID"
	CodeReleaseIndexInvalid       = "KB_INSTALL_RELEASE_INDEX_INVALID"
	CodeReleaseIndexSealFailed    = "KB_RELEASE_INDEX_SEAL_FAILED"
	CodeReleaseIndexUnreadable    = "KB_INSTALL_RELEASE_INDEX_UNREADABLE"
	CodeOfflineArtifactMissing    = "KB_INSTALL_OFFLINE_ARTIFACT_MISSING"
	CodeIndexSchemaUnsupported    = "KB_INSTALL_INDEX_SCHEMA_UNSUPPORTED"
	CodeLauncherTooOld            = "KB_INSTALL_LAUNCHER_TOO_OLD"
	CodeArtifactMismatch          = "KB_INSTALL_ARTIFACT_MANIFEST_MISMATCH"
	CodeServiceGraphMismatch      = "KB_INSTALL_SERVICE_GRAPH_MISMATCH"
	CodeApplyFailed               = "KB_INSTALL_APPLY_FAILED"
	CodeOperationFailed           = "KB_INSTALL_OPERATION_FAILED"
	CodeRecoveryFailed            = "KB_INSTALL_RECOVERY_FAILED"
	CodeReceiptUnavailable        = "KB_INSTALL_RECEIPT_UNAVAILABLE"
	CodeDoctorInputInvalid        = "KB_INSTALL_DOCTOR_INPUT_INVALID"
	CodeLogUnavailable            = "KB_INSTALL_LOG_UNAVAILABLE"
	CodeDiagnosticUnavailable     = "KB_INSTALL_DIAGNOSTIC_UNAVAILABLE"
	CodeRollbackFailed            = "KB_INSTALL_ROLLBACK_FAILED"
	CodeOperationInProgress       = "KB_INSTALL_OPERATION_IN_PROGRESS"
	CodeRecoveryHostRunning    = "KB_INSTALL_RECOVERY_HOST_RUNNING"
	CodeRecoveryUnavailable    = "KB_INSTALL_RECOVERY_UNAVAILABLE"
	CodeUpdateApplyFailed      = "KB_UPDATE_APPLY_FAILED"
	CodeUpdateIncompatible     = "KB_UPDATE_INCOMPATIBLE"
	CodeUpdateDowngradeRefused = "KB_UPDATE_DOWNGRADE_REFUSED"
	CodeUpdateHostNotHealthy   = "KB_UPDATE_HOST_NOT_HEALTHY"
	CodeUpdateNoSnapshot       = "KB_UPDATE_NO_SNAPSHOT"
	CodeHostSpecInvalid        = "KB_HOST_SPEC_INVALID"
	CodeHostStateDirUnwritable = "KB_HOST_STATE_DIR_UNWRITABLE"
)

const (
	CodeProjectSelectionChanged   = "KB_INSTALL_PROJECT_SELECTION_CHANGED"
	CodeProjectRegistrationFailed = "KB_INSTALL_PROJECT_REGISTRATION_FAILED"
)

// NewDiag constructs the shared clikit diagnostic for a kb-create error code.
// The wrapped cause remains available through Unwrap. Its sanitized text is
// exposed in both machine and human diagnostics at this shared boundary.
func NewDiag(code, message, hint string, cause error) *diag.Diag {
	opts := make([]diag.Opt, 0, 2)
	if hint != "" {
		opts = append(opts, diag.WithHint(hint))
	}
	if cause != nil {
		opts = append(opts, diag.WithCauseAndReason(cause.Error()))
		return diag.Wrap(cause, code, message, opts...)
	}
	return diag.New(code, message, opts...)
}
