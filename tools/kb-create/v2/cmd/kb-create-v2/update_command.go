package v2cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/kb-labs/clikit/diag"
	"github.com/kb-labs/create/v2/artifacts"
	"github.com/kb-labs/create/v2/contracts"
	"github.com/kb-labs/create/v2/gateway"
	"github.com/kb-labs/create/v2/host"
	"github.com/kb-labs/create/v2/logs"
	"github.com/kb-labs/create/v2/receipt"
	"github.com/kb-labs/create/v2/resolve"
	"github.com/kb-labs/create/v2/runtime"
	"github.com/kb-labs/create/v2/secrets"
	"github.com/kb-labs/create/v2/telemetry"
	"github.com/kb-labs/create/v2/transport"
	"github.com/kb-labs/create/v2/versionroot"
)

type updateCommandResult struct {
	OK                        bool                      `json:"ok"`
	Operation                 string                    `json:"operation"`
	Receipt                   contracts.InstallReceipt  `json:"receipt"`
	Snapshot                  map[string]any            `json:"snapshot"`
	HostRestarted             bool                      `json:"hostRestarted"`
	AlreadyUpToDate           bool                      `json:"alreadyUpToDate,omitempty"`
	UpdateSummary             *contracts.UpdateSummary  `json:"updateSummary,omitempty"`
	PendingRestart            *contracts.PendingRestart `json:"pendingRestart,omitempty"`
	LogPath                   string                    `json:"logPath"`
	ProjectRegistrationNotice string                    `json:"projectRegistrationNotice,omitempty"`
}

type updateCommandInput struct {
	Index, RequestFile, ScenarioID, Answers string
	Direct                                  directRequest
	Resume                                  bool
	Executable, SecretEnv, Registry         string
	TelemetryEndpoint                       string
	TelemetryConsent                        bool
}

func executeUpdate(ctx context.Context, input updateCommandInput, commandDeps commandDependencies) (updateCommandResult, *diag.Diag) {
	if err := commandDeps.NodeCheck(); err != nil {
		failure := contracts.NewDiag(contracts.CodeNodeVersion, "Node.js 24.x is required before installation.",
			"Install Node.js 24, then run `kb-create update` (for nvm: `nvm install 24 && nvm use 24`).", err)
		return updateCommandResult{}, failure
	}
	resolved, failure := resolvePlan(planInput{Operation: "update", Index: input.Index, RequestFile: input.RequestFile, Direct: input.Direct,
		ScenarioID: input.ScenarioID, Answers: input.Answers, Resume: input.Resume, Executable: input.Executable})
	if failure != nil {
		return updateCommandResult{}, updateCompatibilityDiag(failure)
	}
	plan := resolved.Plan
	secretIDs := make([]string, 0)
	for _, mapping := range splitSecretMappings(input.SecretEnv) {
		secretIDs = append(secretIDs, mapping[0])
	}
	if failure := resolve.ValidateSecretInputIDs(plan.Artifacts, secretIDs); failure != nil {
		return updateCommandResult{}, asDiag(failure)
	}
	layout, layoutErr := versionroot.ForPlatformPath(plan.Request.PlatformRoot)
	if layoutErr == nil {
		layoutErr = layout.Validate()
	}
	if layoutErr != nil {
		return updateCommandResult{}, unsupportedPlatformLayout(plan.Request.PlatformRoot, layoutErr)
	}
	platformRoot := layout.PlatformRoot()
	active, err := receipt.Read(platformRoot)
	if err != nil {
		if failure := receiptArtifactIdentityFailure(err, input.Index, plan.Request.PlatformRoot); failure != nil {
			return updateCommandResult{}, failure
		}
		failure := contracts.NewDiag(contracts.CodeReceiptUnavailable, "active V2 receipt could not be read", "run apply first or restore a named V2 snapshot", err)
		return updateCommandResult{}, failure
	}
	activeRoot, err := layout.Active()
	if err != nil {
		return updateCommandResult{}, unsupportedPlatformLayout(plan.Request.PlatformRoot, err)
	}
	if _, err := (secrets.Store{PlatformRoot: activeRoot, ReadOnly: true}).BootstrapCompleted(); err != nil {
		return updateCommandResult{}, asDiag(err)
	}
	secretStore := secrets.Store{PlatformRoot: activeRoot, ReadOnly: true}
	if failure := refuseUpdateSecretChanges(input, layout.InstallRoot(), secretStore); failure != nil {
		return updateCommandResult{}, failure
	}
	disposition, err := resolve.ValidateUpdate(active, plan, resolved.Catalog)
	if err != nil {
		return updateCommandResult{}, updateCompatibilityDiag(asDiag(err))
	}
	if disposition == resolve.UpdateAlreadyUpToDate {
		registrationNotice, noticeErr := completedBootstrapProjectNotice(active, active.Plan.Request.ProjectRoot, false)
		if noticeErr != nil {
			return updateCommandResult{}, contracts.NewDiag(contracts.CodeApplyFailed,
				"the project registration state could not be read", "check platform-root permissions, then rerun `"+contracts.ShellCommand("kb-create", "update", "--platform-root", platformRoot)+"`", noticeErr)
		}
		return updateCommandResult{OK: true, Operation: "update", Receipt: active, HostRestarted: false, AlreadyUpToDate: true,
			ProjectRegistrationNotice: registrationNotice}, nil
	}
	request, changed, err := requestWithGeneratedSecrets(plan.Request, plan.Artifacts)
	if err != nil {
		return updateCommandResult{}, contracts.NewDiag(contracts.CodeInputRequired, "the install request could not include generated secrets.", "rerun `kb-create install` with the selected platform manifest", err)
	}
	if changed {
		encoded, marshalErr := json.Marshal(request)
		if marshalErr != nil {
			return updateCommandResult{}, contracts.NewDiag(contracts.CodeInputRequired, "the update request could not be encoded", "rerun `kb-create update` with the same request", marshalErr)
		}
		response := transport.PlanWith(encoded, resolved.Catalog, resolve.Options{LauncherVersion: buildVersion})
		if !response.OK {
			return updateCommandResult{}, updateCompatibilityDiag(response.Error)
		}
		plan = *response.Plan
	}
	if failure := requireUpdateSecrets(plan, secretStore, input, layout.InstallRoot()); failure != nil {
		return updateCommandResult{}, failure
	}
	updateSummary, err := resolve.SummarizeUpdate(active, plan, resolved.Catalog)
	if err != nil {
		failure := contracts.NewDiag(contracts.CodeOperationFailed, "the installed platform could not be compared with the resolved update", "verify the active receipt and sealed release index, then rerun `kb-create update`", err)
		return updateCommandResult{}, failure
	}
	if running := refuseForeignHost(plan.Request.PlatformRoot); running != nil {
		return updateCommandResult{}, running
	}
	installRoot := plan.Request.PlatformRoot
	lifecycleState, err := host.StateForRoot(installRoot)
	if err != nil {
		return updateCommandResult{}, contracts.NewDiag(contracts.CodeInputRequired, "The installation lifecycle state path could not be resolved.", "Pass the install root used by this installation, then rerun `kb-create update`.", err)
	}
	plan.Request.PlatformRoot = platformRoot
	result, failure := runUpdateCommand(ctx, input, installRoot, plan, lifecycleState, resolved.State, commandDeps.Runtime)
	if failure != nil {
		return updateCommandResult{}, failure
	}
	result.UpdateSummary = &updateSummary
	return result, nil
}

func updateSecretDiagnostic(root, secret, index, requestFile string) *diag.Diag {
	stopCommand := contracts.ShellCommand("kb-create", "stop", "--platform-root", root)
	applyArgs := []string{"kb-create", "apply", "--platform-root", root}
	if index != "" {
		applyArgs = append(applyArgs, "--index", index)
	}
	if requestFile != "" {
		applyArgs = append(applyArgs, "--input", requestFile)
	}
	applyArgs = append(applyArgs, "--secret-env", secret+"=ENV_VAR")
	command := stopCommand + " && " + contracts.ShellCommand(applyArgs...)
	return contracts.NewDiag(contracts.CodeUpdateSecretRefused,
		"Update cannot create or change installation secret "+secret+".",
		"Set the environment variable configured for this secret to the intended value, then run `"+command+"`.", nil)
}

func refuseUpdateSecretChanges(input updateCommandInput, root string, store secrets.Store) *diag.Diag {
	for _, mapping := range splitSecretMappings(input.SecretEnv) {
		name, environment := mapping[0], mapping[1]
		if environment == "" {
			return contracts.NewDiag(contracts.CodeSecretInputInvalid, "secret mapping is invalid", "use --secret-env requirement=ENV_VAR", nil)
		}
		value, present := os.LookupEnv(environment)
		if !present || value == "" {
			return contracts.NewDiag(contracts.CodeSecretInputInvalid, "the mapped secret environment value is missing", "set the environment variable configured for this secret, then rerun `kb-create update`", nil)
		}
		stored, exists, err := store.Get(name)
		if err != nil {
			if errors.Is(err, secrets.ErrUnsupportedSecretStoreFormat) {
				return asDiag(err)
			}
			return contracts.NewDiag(contracts.CodeUpdateSecretRefused, "the stored installation secret could not be compared", "run `kb-create doctor` before changing installation secrets", err)
		}
		if !exists || stored != value {
			return updateSecretDiagnostic(root, name, input.Index, input.RequestFile)
		}
	}
	return nil
}

func requireUpdateSecrets(plan contracts.ResolvedInstallPlan, store secrets.Store, input updateCommandInput, root string) *diag.Diag {
	for _, secret := range plan.Request.SecretInputs {
		exists, err := store.Exists(secret)
		if err != nil || !exists {
			return updateSecretDiagnostic(root, secret, input.Index, input.RequestFile)
		}
	}
	return nil
}

// runUpdateCommand stages and verifies a complete version root before the
// stable platform path is atomically switched to it.
func runUpdateCommand(ctx context.Context, input updateCommandInput, installRoot string, plan contracts.ResolvedInstallPlan, state, launcherState host.State, runtimeOverrides *runtime.Dependencies) (updateCommandResult, *diag.Diag) {
	layout, err := versionroot.ForPlatformPath(installRoot)
	if err != nil {
		return updateCommandResult{}, contracts.NewDiag(contracts.CodeInstallLayoutUnsupported, "The install root cannot use the required atomic layout.", "Run `"+contracts.ShellCommand("kb-create", "install", "--platform-root", "NEW_ROOT")+"` on Linux or macOS.", err)
	}
	root := layout.PlatformRoot()
	previousReceipt, err := receipt.Read(root)
	if err != nil {
		failure := contracts.NewDiag(contracts.CodeReceiptUnavailable, "active V2 receipt could not be read", "run apply first or restore a named V2 snapshot", err)
		return updateCommandResult{}, failure
	}
	runner, err := host.NewRunner(installRoot)
	if err != nil {
		return updateCommandResult{}, asDiag(err)
	}
	checkCtx, cancelCheck := context.WithTimeout(context.Background(), 5*time.Second)
	priorHost, err := runner.Status(checkCtx)
	cancelCheck()
	if err != nil {
		return updateCommandResult{}, asDiag(err)
	}
	started := time.Now()
	outcome, errorCode := "failure", "KB_INSTALL_OPERATION_FAILED"
	defer func() {
		telemetry.Send(input.TelemetryEndpoint, input.TelemetryConsent, telemetry.New("update", outcome, errorCode,
			string(plan.Request.Platform.Channel), string(plan.Request.Source), len(plan.Artifacts), time.Since(started)))
	}()
	correlationID := fmt.Sprintf("update-%d", time.Now().UTC().UnixNano())
	if runtimeOverrides != nil && runtimeOverrides.CorrelationID != "" {
		correlationID = runtimeOverrides.CorrelationID
	}
	stage, err := layout.Stage()
	if err != nil {
		return updateCommandResult{}, contracts.NewDiag(contracts.CodeOperationFailed, "the update could not create an isolated version root", "free disk space or fix platform-root permissions, then rerun `kb-create update`", err)
	}
	activated := false
	defer func() {
		if !activated {
			_ = os.RemoveAll(stage)
		}
	}()
	plan.Request.PlatformRoot = stage
	activeRoot, err := layout.Active()
	if err != nil {
		return updateCommandResult{}, contracts.NewDiag(contracts.CodeInstallLayoutUnsupported, "The active version root could not be resolved for read-only update inputs.", "Run `"+contracts.ShellCommand("kb-create", "doctor", "--platform-root", installRoot)+"` before retrying the update.", err)
	}
	store := secrets.Store{PlatformRoot: activeRoot, ReadOnly: true}
	redactions, err := secretRedactionValues(store, plan, input.SecretEnv)
	if err != nil {
		return updateCommandResult{}, contracts.NewDiag(contracts.CodeApplyFailed, "the installation secret state could not be read for safe logging", "run `kb-create doctor` before retrying the update", err)
	}
	transcript, err := logs.New(layout.OperationLogsDir(), correlationID, redactions)
	if err != nil {
		return updateCommandResult{}, contracts.NewDiag("KB_INSTALL_LOG_UNAVAILABLE", "could not create local operation log", "check that the staged version root is writable", err)
	}
	defer transcript.Close()
	release, err := state.AcquireOperation("update")
	if err != nil {
		return updateCommandResult{}, asDiag(err)
	}
	defer release()
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	offline := plan.Request.Source == contracts.SourceOffline
	cacheRoot, err := layout.SharedRuntimePath(".kb/v2/cache")
	if err != nil {
		return updateCommandResult{}, contracts.NewDiag(contracts.CodeInstallLayoutUnsupported, "the shared artifact cache path could not be resolved", "run `"+contracts.ShellCommand("kb-create", "doctor", "--platform-root", installRoot)+"` before retrying the update", err)
	}
	installer := artifacts.Composite{Packages: artifacts.Pnpm{Root: stage, Registry: input.Registry, Offline: offline, Log: transcript}, Binaries: artifacts.Binaries{Root: stage, CacheRoot: cacheRoot, Offline: offline}}
	deps := runtime.Dependencies{Artifacts: installer, CorrelationID: correlationID, Secrets: &store}
	deps.PreserveGeneratedConfig = true
	deps.VerificationConfigRoot = activeRoot
	if runtimeOverrides != nil {
		if runtimeOverrides.Artifacts != nil {
			deps.Artifacts = runtimeOverrides.Artifacts
		}
		if runtimeOverrides.Clock != nil {
			deps.Clock = runtimeOverrides.Clock
		}
		if runtimeOverrides.Secrets != nil {
			readOnly := *runtimeOverrides.Secrets
			readOnly.ReadOnly = true
			deps.Secrets = &readOnly
		}
		deps.Progress = runtimeOverrides.Progress
		deps.PreserveGeneratedConfig = true
	}
	deps.Host = nil
	// Staging verification is deliberately offline. A second host would bind
	// the same service ports and open the live project state.
	var updated contracts.InstallReceipt
	var snapshot contracts.Snapshot
	if ctx == nil {
		ctx = context.Background()
	}
	updated, snapshot, err = runtime.Update(ctx, plan, deps)
	if err != nil {
		return updateCommandResult{}, applyDossier(stage, correlationID, transcript.Path(), plan, redactions, err)
	}
	updated.Plan.Request.PlatformRoot = root
	if err := receipt.Write(stage, updated); err != nil {
		return updateCommandResult{}, contracts.NewDiag(contracts.CodeApplyFailed, "the verified update receipt could not be finalized", "inspect the staged operation log, then rerun `kb-create update`", err)
	}
	logRelative, err := filepath.Rel(installRoot, transcript.Path())
	if err != nil {
		return updateCommandResult{}, contracts.NewDiag(contracts.CodeApplyFailed, "the update operation log is outside the installation container", "inspect the staged update and rerun `kb-create update`", err)
	}
	if err := layout.WriteUpdateMetadata(stage, versionroot.UpdateMetadata{SnapshotID: snapshot.ID, LogPath: logRelative}); err != nil {
		return updateCommandResult{}, contracts.NewDiag(contracts.CodeApplyFailed, "the update recovery record could not be written", "check staged-root permissions, then rerun `kb-create update`", err)
	}
	runningRoot := ""
	if priorHost.PID > 0 {
		runningRoot = priorHost.TreeRoot
	}
	_, err = layout.Activate(stage, updated.Plan.ServiceGraph.PlatformVersion, updated.Plan.PlanHash, runningRoot)
	if err != nil {
		return updateCommandResult{}, contracts.NewDiag(contracts.CodeApplyFailed, "the verified update could not be atomically activated", "the installed version is unchanged; inspect permissions and rerun `kb-create update`", err)
	}
	activated = true
	root = layout.PlatformRoot()
	if _, err := receipt.Read(root); err != nil {
		return updateCommandResult{}, contracts.NewDiag(contracts.CodeApplyFailed, "the active version root does not contain its verified receipt", "inspect the version-root layout and restore the previous version before restarting", err)
	}
	activeRoot, err = layout.Active()
	transcriptPath := transcript.Path()
	if updated.Plan.Host != nil {
		origin, originErr := gateway.OriginFromHealthURL(updated.Verification.HostHealthURL)
		if originErr != nil {
			return updateCommandResult{}, contracts.NewDiag(contracts.CodeHostSpecInvalid, "the updated Gateway address could not be recorded", "rerun `"+contracts.ShellCommand("kb-create", "update", "--platform-root", installRoot)+"` after checking the receipt", originErr)
		}
		if err := (gateway.Registry{Path: filepath.Join(launcherState.Dir, "gateway-roots.json"), OperationRoot: installRoot}).Register(root, origin); err != nil {
			return updateCommandResult{}, asDiag(err)
		}
	}
	outcome, errorCode = "success", ""
	registrationNotice, noticeErr := completedBootstrapProjectNotice(previousReceipt, previousReceipt.Plan.Request.ProjectRoot, false)
	if noticeErr != nil {
		return updateCommandResult{}, contracts.NewDiag(contracts.CodeApplyFailed,
			"the project registration state could not be read", "check platform-root permissions, then rerun `"+contracts.ShellCommand("kb-create", "update", "--platform-root", installRoot)+"`", noticeErr)
	}
	runner, err = host.NewRunner(installRoot)
	if err != nil {
		return updateCommandResult{}, asDiag(err)
	}
	statusCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	running, err := runner.Status(statusCtx)
	if err != nil {
		return updateCommandResult{}, asDiag(err)
	}
	outcome, errorCode = "success", ""
	return updateCommandResult{OK: true, Operation: "update", Receipt: updated, Snapshot: snapshotSummary(snapshot), HostRestarted: false,
		PendingRestart: pendingRestart(updated, running, installRoot), LogPath: transcriptPath,
		ProjectRegistrationNotice: registrationNotice}, nil
}
