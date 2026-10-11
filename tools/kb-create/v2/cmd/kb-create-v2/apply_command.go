package v2cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"reflect"
	"syscall"
	"time"

	clikitcommand "github.com/kb-labs/clikit/command"
	"github.com/kb-labs/clikit/diag"
	"github.com/kb-labs/clikit/stream"
	"github.com/kb-labs/create/v2/artifacts"
	"github.com/kb-labs/create/v2/contracts"
	"github.com/kb-labs/create/v2/diagnostics"
	"github.com/kb-labs/create/v2/gateway"
	"github.com/kb-labs/create/v2/host"
	"github.com/kb-labs/create/v2/logs"
	"github.com/kb-labs/create/v2/preflight"
	"github.com/kb-labs/create/v2/receipt"
	"github.com/kb-labs/create/v2/resolve"
	"github.com/kb-labs/create/v2/runtime"
	"github.com/kb-labs/create/v2/secrets"
	"github.com/kb-labs/create/v2/shim"
	"github.com/kb-labs/create/v2/telemetry"
	"github.com/kb-labs/create/v2/transport"
	"github.com/kb-labs/create/v2/versionroot"
)

type applyCommandInput struct {
	Plan                   planInput
	Resolved               *resolvedPlan
	Registry               string
	SecretEnv              string
	Executable             string
	Progress               runtime.ProgressSink
	JSON                   bool
	TelemetryEndpoint      string
	TelemetryConsent       bool
	AdminPasswordGenerated bool
}

func executeApply(ctx context.Context, applyInput applyCommandInput, deps commandDependencies) (applyCommandResult, *diag.Diag) {
	return applyPlan(ctx, applyInput, deps)
}

func executeApplyStream(ctx context.Context, applyInput applyCommandInput, deps commandDependencies) (streamCompletion, *diag.Diag) {
	writer := stream.New(clikitcommand.StreamWriter(deps.Command))
	deps.Runtime.Progress = streamProgress{writer: writer}
	result, failure := executeApply(ctx, applyInput, deps)
	if failure != nil {
		if failure.ProcessExitCode == diag.ExitOK {
			failure.ProcessExitCode = diag.ExitError
		}
		if err := writer.Fail(failure); err != nil {
			return streamCompletion{}, diag.Wrap(err, "KB_RUNTIME_UNEXPECTED", "Install progress could not be written.")
		}
	} else if err := writer.Result(result); err != nil {
		return streamCompletion{}, diag.Wrap(err, "KB_RUNTIME_UNEXPECTED", "Apply result could not be written.")
	}
	exitCode := diag.ExitOK
	if failure != nil {
		exitCode = failure.ProcessExitCode
	}
	if err := writer.Finish(exitCode); err != nil {
		return streamCompletion{}, diag.Wrap(err, "KB_RUNTIME_UNEXPECTED", "Install progress could not be completed.")
	}
	return streamCompletion{ExitCode: writer.ExitCode()}, nil
}

func applyPlan(ctx context.Context, input applyCommandInput, deps commandDependencies) (applyCommandResult, *diag.Diag) {
	if err := deps.NodeCheck(); err != nil {
		failure := contracts.NewDiag(contracts.CodeNodeVersion,
			"Node.js 24.x is required before installation.",
			"Install Node.js 24, then run `kb-create install` (for nvm: `nvm install 24 && nvm use 24`).", err)
		failure.ProcessExitCode = diag.ExitInput
		return applyCommandResult{}, failure
	}
	started := time.Now()
	outcome, errorCode := "failure", "KB_INSTALL_OPERATION_FAILED"
	var planned contracts.ResolvedInstallPlan
	defer func() {
		telemetry.Send(input.TelemetryEndpoint, input.TelemetryConsent, telemetry.New("apply", outcome, errorCode,
			string(planned.Request.Platform.Channel), string(planned.Request.Source), len(planned.Artifacts), time.Since(started)))
	}()
	resolved := input.Resolved
	if resolved == nil {
		result, failure := resolvePlan(input.Plan)
		if failure != nil {
			return applyCommandResult{}, inputFailure(failure)
		}
		resolved = &result
	}
	planned = resolved.Plan
	launcherState := resolved.State
	root := planned.Request.PlatformRoot
	installRoot := root
	layout, layoutErr := versionroot.ForPlatformPath(root)
	if layoutErr != nil {
		return applyCommandResult{}, contracts.NewDiag(contracts.CodeOperationFailed, "the platform root could not be resolved", "check the install path, then rerun `kb-create install`", layoutErr)
	}
	initialInstall := false
	operationRoot := root
	platformRoot := root
	initialStage := ""
	platformRoot = layout.PlatformRoot()
	if _, receiptErr := receipt.Read(platformRoot); receiptErr != nil {
		if !os.IsNotExist(receiptErr) {
			return applyCommandResult{}, contracts.NewDiag(contracts.CodeReceiptUnavailable, "the active receipt could not be read", "run `"+contracts.ShellCommand("kb-create", "doctor", "--platform-root", root)+"` before reinstalling", receiptErr)
		}
		if err := layout.ValidateInitialTarget(); err != nil {
			return applyCommandResult{}, unsupportedPlatformLayout(root, err)
		}
		if _, activeErr := receipt.Read(layout.PlatformRoot()); activeErr == nil {
			if err := layout.Validate(); err != nil {
				return applyCommandResult{}, unsupportedPlatformLayout(root, err)
			}
			platformRoot = layout.PlatformRoot()
			operationRoot = platformRoot
			planned.Request.PlatformRoot = platformRoot
		} else if !os.IsNotExist(activeErr) {
			return applyCommandResult{}, contracts.NewDiag(contracts.CodeReceiptUnavailable, "the recovered active receipt could not be read", "run `"+contracts.ShellCommand("kb-create", "doctor", "--platform-root", root)+"` before reinstalling", activeErr)
		} else {
			stage, stageErr := layout.StageInitial()
			if stageErr != nil {
				return applyCommandResult{}, unsupportedPlatformLayout(root, stageErr)
			}
			initialStage = stage
			initialInstall = true
			operationRoot = initialStage
			planned.Request.PlatformRoot = initialStage
		}
	} else if err := layout.Validate(); err != nil {
		return applyCommandResult{}, unsupportedPlatformLayout(root, err)
	} else {
		platformRoot = layout.PlatformRoot()
		activeRoot, activeErr := layout.Active()
		if activeErr != nil {
			return applyCommandResult{}, unsupportedPlatformLayout(root, activeErr)
		}
		operationRoot = activeRoot
		planned.Request.PlatformRoot = activeRoot
	}
	initialActivated := false
	if initialStage != "" {
		defer func() {
			if !initialActivated {
				_ = os.RemoveAll(initialStage)
			}
		}()
	}
	state, err := host.StateForRoot(installRoot)
	if err != nil {
		return applyCommandResult{}, contracts.NewDiag(contracts.CodeInputRequired, "The installation lifecycle state path could not be resolved.", "Pass the install root used by this installation, then rerun `kb-create install`.", err)
	}
	if !initialInstall {
		if _, failure := existingProjectSelection(platformRoot, planned.Request.ProjectRoot); failure != nil {
			return applyCommandResult{}, failure
		}
	}
	release, err := state.AcquireOperation("apply")
	if err != nil {
		return applyCommandResult{}, asDiag(err)
	}
	defer release()
	request, changed, err := requestWithGeneratedSecrets(planned.Request, planned.Artifacts)
	if err != nil {
		return applyCommandResult{}, contracts.NewDiag(contracts.CodeInputRequired,
			"The install request could not include generated secrets.", "rerun `kb-create install` with the selected platform manifest", err)
	}
	if changed {
		encoded, marshalErr := json.Marshal(request)
		if marshalErr != nil {
			return applyCommandResult{}, contracts.NewDiag(contracts.CodeInputRequired, "the install request could not be encoded", "rerun `kb-create apply` with the same request", marshalErr)
		}
		response := transport.PlanWith(encoded, resolved.Catalog, resolve.Options{LauncherVersion: buildVersion})
		if !response.OK {
			return applyCommandResult{}, response.Error
		}
		planned = *response.Plan
	}
	if running := refuseApplyBesideRunningHost(planned, installRoot); running != nil {
		return applyCommandResult{}, running
	}
	if err := preflight.CheckInstallResources(operationRoot); err != nil {
		var typed *diag.Diag
		if errors.As(err, &typed) {
			return applyCommandResult{}, typed
		}
		return applyCommandResult{}, contracts.NewDiag(contracts.CodeResourceCheckFailed,
			"The launcher could not check available memory and disk space.",
			"Check that the platform root is on a mounted writable filesystem, then run `kb-create install` again.", err)
	}
	if planned.Host != nil {
		if err := host.CheckPorts(planned, "apply"); err != nil {
			return applyCommandResult{}, asDiag(err)
		}
	}
	correlationID := fmt.Sprintf("apply-%d", time.Now().UTC().UnixNano())
	transcript, err := logs.New(layout.OperationLogsDir(), correlationID, nil)
	if err != nil {
		return applyCommandResult{}, contracts.NewDiag("KB_INSTALL_LOG_UNAVAILABLE", "could not create local operation log", "check that the platform root is writable", err)
	}
	defer transcript.Close()
	store := secrets.Store{PlatformRoot: operationRoot}
	if err := populateSecrets(store, input.SecretEnv); err != nil {
		return applyCommandResult{}, contracts.NewDiag("KB_INSTALL_SECRET_INPUT_INVALID", "secret input could not be stored", "use --secret-env requirement=ENV_VAR and set the environment variable", err)
	}
	adminEmail, securedAdmin := configuredFirstAdmin(planned.Request)
	bootstrapCompleted := false
	adminPasswordPresent := false
	if securedAdmin {
		var existsErr error
		bootstrapCompleted, existsErr = store.Exists(secrets.BootstrapCompleted)
		if existsErr == nil {
			adminPasswordPresent, existsErr = store.Exists("gateway.bootstrap.password")
		}
		if existsErr != nil {
			return applyCommandResult{}, contracts.NewDiag(contracts.CodeApplyFailed, "the first administrator password could not be read from the secret store", "check that the platform root is writable, then rerun `kb-create install`", existsErr)
		}
		if bootstrapCompleted && !adminPasswordPresent {
			return applyCommandResult{}, contracts.NewDiag(contracts.CodeApplyFailed,
				"the secured Gateway was bootstrapped, but its stored first administrator password is missing",
				"Recover the account with `"+contracts.ShellCommand("kb-create", "doctor", "--recover", "--platform-root", root)+"`.", nil)
		}
	}
	if err := secrets.EnsureGenerated(&planned, store); err != nil {
		return applyCommandResult{}, contracts.NewDiag(contracts.CodeApplyFailed,
			"the session signing secret could not be generated or stored.",
			"check that the platform root is writable, then rerun `kb-create install`", err)
	}
	if securedAdmin && !bootstrapCompleted && !adminPasswordPresent && input.AdminPasswordGenerated == false {
		if err := store.Put(secrets.BootstrapGenerated, "true"); err != nil {
			return applyCommandResult{}, contracts.NewDiag(contracts.CodeApplyFailed, "the generated bootstrap credential state could not be stored", "check that the platform root is writable, then rerun `kb-create install`", err)
		}
	}
	if securedAdmin && input.AdminPasswordGenerated {
		if err := store.Put(secrets.BootstrapGenerated, "true"); err != nil {
			return applyCommandResult{}, contracts.NewDiag(contracts.CodeApplyFailed, "the generated bootstrap credential state could not be stored", "check that the platform root is writable, then rerun `kb-create install`", err)
		}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	applyCtx, cancel := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer cancel()
	offline := planned.Request.Source == contracts.SourceOffline
	artifactExecutor := artifacts.Composite{Packages: artifacts.Pnpm{Root: operationRoot, Registry: input.Registry, Offline: offline, Log: transcript, Runner: deps.ArtifactRunner}, Binaries: artifacts.Binaries{Root: operationRoot, Offline: offline}}
	prober := host.Prober{PlatformRoot: installRoot, Operation: "apply"}
	runtimeDeps := runtime.Dependencies{Artifacts: artifactExecutor, CorrelationID: correlationID, Secrets: &store, Progress: input.Progress}
	if deps.Runtime != nil {
		if deps.Runtime.Artifacts != nil {
			runtimeDeps.Artifacts = deps.Runtime.Artifacts
		}
		if deps.Runtime.Host != nil {
			runtimeDeps.Host = deps.Runtime.Host
		}
		if deps.Runtime.Clock != nil {
			runtimeDeps.Clock = deps.Runtime.Clock
		}
		if deps.Runtime.Secrets != nil {
			runtimeDeps.Secrets = deps.Runtime.Secrets
		}
		if deps.Runtime.Progress != nil {
			runtimeDeps.Progress = deps.Runtime.Progress
		}
	}
	if runtimeDeps.Host == nil && planned.Host != nil {
		if !initialInstall {
			runtimeDeps.Host = prober
		}
	}
	postInstallStarter := runtimeDeps.Host
	if initialInstall {
		runtimeDeps.Host = nil
	}
	if securedAdmin && !bootstrapCompleted && runtimeDeps.Host != nil {
		runtimeDeps.Host = bootstrapOutcomeHost{starter: runtimeDeps.Host, store: store}
	}
	applied, applyErr := runtime.Apply(applyCtx, planned, runtimeDeps)
	if applyErr != nil {
		return applyCommandResult{}, applyDossier(root, correlationID, transcript.Path(), planned, applyErr)
	}
	var firstAdmin *firstAdminCredential
	if initialInstall {
		if _, err := layout.ActivateInitial(initialStage, applied.Plan.ServiceGraph.PlatformVersion, applied.Plan.PlanHash); err != nil {
			return applyCommandResult{}, contracts.NewDiag(contracts.CodeApplyFailed, "the verified first installation could not be atomically activated", "the stable platform path remains unchanged; inspect permissions and rerun `kb-create install`", err)
		}
		initialActivated = true
		platformRoot = layout.PlatformRoot()
		activeRoot, activeErr := layout.Active()
		if activeErr != nil {
			return applyCommandResult{}, unsupportedPlatformLayout(root, activeErr)
		}
		store = secrets.Store{PlatformRoot: activeRoot}
		applied.Plan.Request.PlatformRoot = platformRoot
		if err := receipt.Write(platformRoot, applied); err != nil {
			return applyCommandResult{}, contracts.NewDiag(contracts.CodeApplyFailed, "the activated installation receipt could not be finalized", "run `"+contracts.ShellCommand("kb-create", "doctor", "--platform-root", root)+"` and resolve the reported layout issue", err)
		}
		if planned.Host != nil {
			starter := postInstallStarter
			if starter == nil {
				starter = installedHostStarter{root: installRoot}
			}
			if securedAdmin && !bootstrapCompleted {
				starter = bootstrapOutcomeHost{starter: starter, store: store}
			}
			if runtimeDeps.Progress != nil {
				if err := runtimeDeps.Progress.Emit(runtime.ProgressEvent{Phase: "Start host", Start: true}); err != nil {
					return applyCommandResult{}, progressWriteFailure(err, "apply")
				}
			}
			_, startErr := starter.Start(applyCtx, *planned.Host)
			if runtimeDeps.Progress != nil {
				startErr = errors.Join(startErr, runtimeDeps.Progress.Emit(runtime.ProgressEvent{Phase: "Start host"}))
			}
			if startErr != nil {
				return applyCommandResult{}, contracts.NewDiag(contracts.CodeApplyFailed, "the installed host did not become healthy", "run `"+contracts.ShellCommand("kb-create", "doctor", "--platform-root", root)+"` to inspect the installation, then rerun `"+contracts.ShellCommand("kb-create", "start", "--platform-root", root)+"`", startErr)
			}
		}
	} else {
		applied.Plan.Request.PlatformRoot = platformRoot
	}
	if securedAdmin {
		bootstrapState, present, readErr := store.Get(secrets.BootstrapCompleted)
		generated, generatedErr := store.Exists(secrets.BootstrapGenerated)
		disclosed, disclosedErr := store.Exists(secrets.BootstrapDisclosed)
		if readErr != nil || generatedErr != nil || disclosedErr != nil {
			return applyCommandResult{}, contracts.NewDiag(contracts.CodeApplyFailed, "the first administrator bootstrap state could not be read", "check that the platform root is writable, then rerun `kb-create install`", errors.Join(readErr, generatedErr, disclosedErr))
		}
		if present && bootstrapState == "provisioned" && generated && !disclosed {
			password, passwordPresent, passwordErr := store.Get("gateway.bootstrap.password")
			if passwordErr != nil || !passwordPresent {
				return applyCommandResult{}, contracts.NewDiag(contracts.CodeApplyFailed, "the generated first administrator password could not be read", "check that the platform root is writable, then rerun `kb-create install`", passwordErr)
			}
			if err := store.Put(secrets.BootstrapDisclosed, "true"); err != nil {
				return applyCommandResult{}, contracts.NewDiag(contracts.CodeApplyFailed, "the first administrator credential disclosure could not be recorded", "check that the platform root is writable, then rerun `kb-create install`", err)
			}
			firstAdmin = &firstAdminCredential{Email: adminEmail, Password: password, ShownOnce: true}
		}
	}
	if planned.Host != nil {
		roots := make([]gateway.Root, 0, 2)
		if owner, ok := shim.Owner(input.Executable); ok && filepath.Clean(owner) != filepath.Clean(platformRoot) {
			if activeOwner, ownerErr := receipt.Read(owner); ownerErr == nil {
				if ownerURL := recordedGatewayURL(activeOwner); ownerURL != "" {
					roots = append(roots, gateway.Root{PlatformRoot: owner, URL: ownerURL})
				}
			}
		}
		origin, originErr := gateway.OriginFromHealthURL(applied.Verification.HostHealthURL)
		if originErr != nil {
			return applyCommandResult{}, contracts.NewDiag(contracts.CodeHostSpecInvalid, "the applied Gateway address could not be recorded", "rerun `"+contracts.ShellCommand("kb-create", "update", "--platform-root", root)+"` after checking the receipt", originErr)
		}
		roots = append(roots, gateway.Root{PlatformRoot: platformRoot, URL: origin})
		if err := (gateway.Registry{Path: filepath.Join(launcherState.Dir, "gateway-roots.json")}).RegisterMany(roots); err != nil {
			return applyCommandResult{}, contracts.NewDiag(contracts.CodeApplyFailed, "the installation succeeded but its Gateway address could not be indexed", "check launcher state permissions, then rerun `kb-create apply`", err)
		}
	}
	var shimResult shim.EnsureResult
	if input.Executable != "" {
		if runtimeDeps.Progress != nil {
			if err := runtimeDeps.Progress.Emit(runtime.ProgressEvent{Phase: "Create kb shim", Start: true}); err != nil {
				return applyCommandResult{}, progressWriteFailure(err, "apply")
			}
		}
		var shimErr error
		shimResult, shimErr = shim.Ensure(input.Executable, platformRoot)
		if shimErr != nil {
			if runtimeDeps.Progress != nil {
				shimErr = errors.Join(shimErr, runtimeDeps.Progress.Emit(runtime.ProgressEvent{Phase: "Create kb shim"}))
			}
			return applyCommandResult{}, shimFailure("KB_INSTALL_APPLY_FAILED", root, shimErr)
		}
		if runtimeDeps.Progress != nil {
			if err := runtimeDeps.Progress.Emit(runtime.ProgressEvent{Phase: "Create kb shim"}); err != nil {
				return applyCommandResult{}, progressWriteFailure(err, "status")
			}
		}
	}
	outcome, errorCode = "success", ""
	logPath := transcript.Path()
	if initialActivated {
		if activeRoot, activeErr := layout.Active(); activeErr == nil {
			if relative, relErr := filepath.Rel(operationRoot, transcript.Path()); relErr == nil {
				logPath = filepath.Join(activeRoot, relative)
			}
		}
	}
	return applyCommandResult{OK: true, Operation: "apply", Receipt: applied, LogPath: logPath,
		Profile: applied.Plan.ServiceGraph.Profile, Services: applied.Plan.ServiceGraph.Services,
		Shim: optionalShim(input.Executable, shimResult), FirstAdmin: firstAdmin}, nil
}

func unsupportedPlatformLayout(root string, cause error) *diag.Diag {
	failure := contracts.NewDiag(contracts.CodeInstallLayoutUnsupported,
		"This platform root does not use the supported versioned installation layout.",
		"Choose a fresh writable path, then run `"+contracts.ShellCommand("kb-create", "install", "--platform-root", "NEW_ROOT")+"`.", cause)
	failure.Meta = map[string]any{"platformRoot": root, "nextCommand": contracts.ShellCommand("kb-create", "install", "--platform-root", "NEW_ROOT")}
	failure.ProcessExitCode = diag.ExitInput
	return failure
}

func optionalShim(executable string, result shim.EnsureResult) *shim.EnsureResult {
	if executable == "" {
		return nil
	}
	return &result
}

type installedHostStarter struct {
	root string
}

func (starter installedHostStarter) Start(ctx context.Context, spec contracts.HostSpec) (func() error, error) {
	layout, err := versionroot.New(starter.root)
	if err != nil {
		return nil, err
	}
	installed, err := receipt.Read(layout.PlatformRoot())
	if err != nil {
		return nil, err
	}
	if installed.Plan.Host == nil || !reflect.DeepEqual(*installed.Plan.Host, spec) {
		return nil, fmt.Errorf("host starter does not match the active receipt")
	}
	runner, err := host.NewRunner(starter.root)
	if err != nil {
		return nil, err
	}
	if _, err := runner.Start(ctx); err != nil {
		return nil, err
	}
	return func() error {
		_, err := runner.Stop(context.Background())
		return err
	}, nil
}

func applyDossier(root, correlationID, logPath string, plan contracts.ResolvedInstallPlan, cause error) *diag.Diag {
	var failure *diag.Diag
	if !errors.As(cause, &failure) {
		failure = contracts.NewDiag(contracts.CodeApplyFailed, "V2 operation did not reach a verified installation", "Inspect the local log and diagnostic dossier, then fix the reported prerequisite or run doctor --fix.", cause)
	}
	failure.CorrelationID = correlationID
	dossier := diagnostics.Dossier{CorrelationID: correlationID, Error: failure, Stage: contracts.StageApply, LogPath: logPath,
		PlanHash: plan.PlanHash, ReleaseDigest: plan.ReleaseDigest, ScenarioStateDigest: plan.ScenarioStateDigest}
	layout, layoutErr := versionroot.ForPlatformPath(root)
	if layoutErr != nil {
		return contracts.NewDiag("KB_INSTALL_DIAGNOSTIC_UNAVAILABLE", "The operation failed and the diagnostic dossier could not be written.", "inspect the local operation log", layoutErr)
	}
	path, err := diagnostics.Write(layout.FailureDossiersDir(), dossier, nil)
	if err != nil {
		return contracts.NewDiag("KB_INSTALL_DIAGNOSTIC_UNAVAILABLE", "The operation failed and the diagnostic dossier could not be written.", "inspect the local operation log", err)
	}
	failure.Meta = ensureMeta(failure.Meta)
	failure.Meta["logPath"], failure.Meta["diagnosticPath"] = logPath, path
	return failure
}

func ensureMeta(meta map[string]any) map[string]any {
	if meta == nil {
		return map[string]any{}
	}
	return meta
}

func shimFailure(code, root string, cause error) *diag.Diag {
	var failureCause *shim.Failure
	if errors.As(cause, &failureCause) {
		reason := failureCause.Reason
		if failureCause.Cause != nil {
			reason = failureCause.Cause.Error()
		}
		diagnostic := contracts.NewDiag(code, fmt.Sprintf("Platform files and receipt remain applied at %s, but the kb command shim could not be created.", root),
			"Fix the filesystem reason for the shim path, then run `"+contracts.ShellCommand("kb-create", "doctor", "--fix", "--platform-root", root)+"`.", cause)
		diagnostic.Reason = fmt.Sprintf("The filesystem refused shim path %s: %s", failureCause.Path, reason)
		diagnostic.Meta = map[string]any{"path": failureCause.Path, "reason": reason}
		return diagnostic
	}
	return contracts.NewDiag(code, fmt.Sprintf("Platform files and receipt remain applied at %s, but the kb command shim could not be created: %s", root, cause),
		"Fix the filesystem reason for the shim path, then run `"+contracts.ShellCommand("kb-create", "doctor", "--fix", "--platform-root", root)+"`.", cause)
}
