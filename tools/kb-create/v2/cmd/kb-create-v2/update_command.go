package v2cli

import (
	"context"
	"encoding/json"
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
	"github.com/kb-labs/create/v2/lifecycle"
	"github.com/kb-labs/create/v2/logs"
	"github.com/kb-labs/create/v2/receipt"
	"github.com/kb-labs/create/v2/resolve"
	"github.com/kb-labs/create/v2/runtime"
	"github.com/kb-labs/create/v2/secrets"
	"github.com/kb-labs/create/v2/swap"
	"github.com/kb-labs/create/v2/telemetry"
	"github.com/kb-labs/create/v2/transport"
)

type updateCommandResult struct {
	OK            bool                     `json:"ok"`
	Operation     string                   `json:"operation"`
	Receipt       contracts.InstallReceipt `json:"receipt"`
	Snapshot      map[string]any           `json:"snapshot"`
	HostRestarted bool                     `json:"hostRestarted"`
	LogPath       string                   `json:"logPath"`
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
		failure.ProcessExitCode = diag.ExitInput
		return updateCommandResult{}, failure
	}
	resolved, failure := resolvePlan(planInput{Operation: "update", Index: input.Index, RequestFile: input.RequestFile, Direct: input.Direct,
		ScenarioID: input.ScenarioID, Answers: input.Answers, Resume: input.Resume, Executable: input.Executable})
	if failure != nil {
		return updateCommandResult{}, inputFailure(updateCompatibilityDiag(failure))
	}
	plan := resolved.Plan
	active, err := receipt.Read(plan.Request.PlatformRoot)
	if err != nil {
		failure := contracts.NewDiag(contracts.CodeReceiptUnavailable, "active V2 receipt could not be read", "run apply first or restore a named V2 snapshot", err)
		failure.ProcessExitCode = diag.ExitInput
		return updateCommandResult{}, failure
	}
	if err := resolve.ValidateUpdate(active, plan, resolved.Catalog); err != nil {
		return updateCommandResult{}, inputFailure(updateCompatibilityDiag(asDiag(err)))
	}
	request, changed, err := requestWithGeneratedSecrets(plan.Request, plan.Artifacts)
	if err != nil {
		return updateCommandResult{}, inputFailure(contracts.NewDiag(contracts.CodeInputRequired, "the install request could not include generated secrets.", "rerun `kb-create install` with the selected platform manifest", err))
	}
	if changed {
		encoded, marshalErr := json.Marshal(request)
		if marshalErr != nil {
			return updateCommandResult{}, inputFailure(contracts.NewDiag(contracts.CodeInputRequired, "the update request could not be encoded", "rerun `kb-create update` with the same request", marshalErr))
		}
		response := transport.PlanWith(encoded, resolved.Catalog, resolve.Options{LauncherVersion: buildVersion})
		if !response.OK {
			return updateCommandResult{}, inputFailure(updateCompatibilityDiag(response.Error))
		}
		plan = *response.Plan
	}
	if running := refuseForeignHost(resolved.State, plan.Request.PlatformRoot); running != nil {
		return updateCommandResult{}, running
	}
	result, failure := runUpdateCommand(ctx, input, plan, resolved.State, commandDeps.Runtime)
	if failure != nil {
		return updateCommandResult{}, failure
	}
	return result, nil
}

// runUpdateCommand is the current update seam: a resolved plan is staged by
// runtime.Update, verified there, then committed by swap.Run and reported.
// The next update design can replace these four phases without changing flag
// parsing or the result contract.
func runUpdateCommand(ctx context.Context, input updateCommandInput, plan contracts.ResolvedInstallPlan, state host.State, runtimeOverrides *runtime.Dependencies) (updateCommandResult, *diag.Diag) {
	root := plan.Request.PlatformRoot
	if _, err := receipt.Read(root); err != nil {
		failure := contracts.NewDiag(contracts.CodeReceiptUnavailable, "active V2 receipt could not be read", "run apply first or restore a named V2 snapshot", err)
		failure.ProcessExitCode = diag.ExitInput
		return updateCommandResult{}, failure
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
	transcript, err := logs.New(root, correlationID, nil)
	if err != nil {
		return updateCommandResult{}, contracts.NewDiag("KB_INSTALL_LOG_UNAVAILABLE", "could not create local operation log", "check that the platform root is writable", err)
	}
	defer transcript.Close()
	store := secrets.Store{PlatformRoot: root}
	if err := populateSecrets(store, input.SecretEnv); err != nil {
		return updateCommandResult{}, contracts.NewDiag("KB_INSTALL_SECRET_INPUT_INVALID", "secret input could not be stored", "use --secret-env requirement=ENV_VAR and set the environment variable", err)
	}
	if err := secrets.EnsureGenerated(&plan, store); err != nil {
		return updateCommandResult{}, contracts.NewDiag(contracts.CodeApplyFailed, "the session signing secret could not be generated or stored.", "check that the platform root is writable, then rerun `kb-create update`", err)
	}
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
	installer := artifacts.Composite{Packages: artifacts.Pnpm{Root: root, Registry: input.Registry, Offline: offline, Log: transcript}, Binaries: artifacts.Binaries{Root: root, Offline: offline}}
	prober := host.Prober{PlatformRoot: root, State: state, Operation: "update"}
	deps := runtime.Dependencies{Artifacts: installer, CorrelationID: correlationID, Secrets: &store}
	if runtimeOverrides != nil {
		if runtimeOverrides.Artifacts != nil {
			deps.Artifacts = runtimeOverrides.Artifacts
		}
		if runtimeOverrides.Host != nil {
			deps.Host = runtimeOverrides.Host
		}
		if runtimeOverrides.Clock != nil {
			deps.Clock = runtimeOverrides.Clock
		}
		if runtimeOverrides.Secrets != nil {
			deps.Secrets = runtimeOverrides.Secrets
		}
		deps.Progress = runtimeOverrides.Progress
	}
	command := hostCommand{PlatformRoot: root, Executable: input.Executable, Version: buildVersion}
	var updated contracts.InstallReceipt
	var snapshot contracts.Snapshot
	var wasRunning bool
	// Stage and verify the replacement through runtime.Update. swap.Run owns the
	// current switch/host lifecycle boundary; reporting stays in this handler.
	_, err = swapUpdate(ctx, command, state, plan, deps, prober, &updated, &snapshot, &wasRunning)
	if err != nil {
		return updateCommandResult{}, applyDossier(root, correlationID, transcript.Path(), plan, err)
	}
	if updated.Plan.Host != nil {
		origin, originErr := gateway.OriginFromHealthURL(updated.Verification.HostHealthURL)
		if originErr != nil {
			return updateCommandResult{}, contracts.NewDiag(contracts.CodeHostSpecInvalid, "the updated Gateway address could not be recorded", fmt.Sprintf("rerun `kb-create update --platform-root %q` after checking the receipt", root), originErr)
		}
		if err := (gateway.Registry{Path: filepath.Join(state.Dir, "gateway-roots.json")}).Register(root, origin); err != nil {
			return updateCommandResult{}, contracts.NewDiag(contracts.CodeApplyFailed, "the update succeeded but its Gateway address could not be indexed", "check launcher state permissions, then rerun `kb-create update`", err)
		}
	}
	outcome, errorCode = "success", ""
	return updateCommandResult{OK: true, Operation: "update", Receipt: updated, Snapshot: snapshotSummary(snapshot), HostRestarted: wasRunning, LogPath: transcript.Path()}, nil
}

func swapUpdate(ctx context.Context, command hostCommand, state host.State, plan contracts.ResolvedInstallPlan, deps runtime.Dependencies, prober runtime.HostProbe, updated *contracts.InstallReceipt, snapshot *contracts.Snapshot, wasRunning *bool) (any, error) {
	outcome, err := swap.Run(ctx, lifecycleHost{command: command, state: state}, swap.Operation{
		Name: "update", PreviousVersion: installedVersion(plan.Request.PlatformRoot),
		Change: func(ctx context.Context, running bool) (string, error) {
			changeDeps := deps
			if !running {
				changeDeps.Host = prober
			}
			var updateErr error
			*updated, *snapshot, updateErr = runtime.Update(ctx, plan, changeDeps)
			return snapshot.ID, updateErr
		},
		Restore: func(_ context.Context, id string) error {
			_, restoreErr := lifecycle.Rollback(plan.Request.PlatformRoot, id, restoreReceiptArtifacts(deps.Artifacts))
			return restoreErr
		},
	})
	if err != nil {
		return nil, err
	}
	*wasRunning = outcome.WasRunning
	return outcome, nil
}
