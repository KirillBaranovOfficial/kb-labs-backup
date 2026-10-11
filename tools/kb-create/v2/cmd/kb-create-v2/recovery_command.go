package v2cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/charmbracelet/x/term"
	"github.com/kb-labs/clikit/diag"
	"github.com/kb-labs/create/v2/artifacts"
	"github.com/kb-labs/create/v2/contracts"
	"github.com/kb-labs/create/v2/gateway"
	"github.com/kb-labs/create/v2/host"
	"github.com/kb-labs/create/v2/lifecycle"
	"github.com/kb-labs/create/v2/logs"
	"github.com/kb-labs/create/v2/receipt"
	"github.com/kb-labs/create/v2/runtime"
	"github.com/kb-labs/create/v2/shim"
	"github.com/kb-labs/create/v2/swap"
	"github.com/kb-labs/create/v2/versionroot"
	"github.com/spf13/cobra"
)

type recoveryCommandResult struct {
	OK               bool           `json:"ok"`
	Operation        string         `json:"operation"`
	Snapshot         map[string]any `json:"snapshot"`
	HostRestarted    bool           `json:"hostRestarted"`
	LogPath          string         `json:"logPath"`
	PreservedEntries []string       `json:"preservedEntries,omitempty"`
}

type uninstallCommandInput struct {
	PlatformRoot, Registry, Executable string
	Yes, JSON                          bool
	Command                            *cobra.Command
}
type rollbackCommandInput struct {
	PlatformRoot, Registry, Executable, Snapshot string
	Yes, JSON                                    bool
	Command                                      *cobra.Command
}
type recoveryRuntimeInput struct{ Registry, Executable string }

func executeUninstall(ctx context.Context, input uninstallCommandInput) (recoveryCommandResult, *diag.Diag) {
	root := input.PlatformRoot
	if root == "" {
		return recoveryCommandResult{}, inputFailure(contracts.NewDiag(contracts.CodeInputRequired, "--platform-root is required", "pass the V2 platform root that owns the active receipt", nil))
	}
	hint := fmt.Sprintf("rerun `kb-create uninstall --platform-root %s", strconv.Quote(root))
	if input.Registry != "" {
		hint += " --registry " + input.Registry
	}
	if input.JSON {
		hint += " --json"
	}
	hint += " --yes`"
	if failure := confirmDestructive(input.Yes, input.JSON, "This will remove platform root "+root+", its installed services, active receipt, and saved snapshots. Project files are kept. Continue? [y/N] ",
		hint, input.Command); failure != nil {
		return recoveryCommandResult{}, failure
	}
	layout, layoutErr := versionroot.New(root)
	if layoutErr == nil {
		layoutErr = layout.ValidateStructure()
	}
	if layoutErr != nil {
		if layout != (versionroot.Layout{}) && layout.HasOwnershipMarker() {
			preserved, preserveErr := layout.PreservedEntries()
			if preserveErr != nil {
				return recoveryCommandResult{}, unsupportedPlatformLayout(root, preserveErr)
			}
			if removeErr := layout.RemoveOwned(); removeErr != nil {
				return recoveryCommandResult{}, unsupportedPlatformLayout(root, removeErr)
			}
			return recoveryCommandResult{OK: true, Operation: "uninstall", HostRestarted: false, PreservedEntries: preserved}, nil
		}
		return recoveryCommandResult{}, unsupportedPlatformLayout(root, layoutErr)
	}
	platformRoot := layout.PlatformRoot()
	active, err := receipt.Read(platformRoot)
	if err != nil {
		failure := contracts.NewDiag(contracts.CodeReceiptUnavailable, "active V2 receipt could not be read", "run apply first or restore a named V2 snapshot", err)
		failure.ProcessExitCode = diag.ExitInput
		return recoveryCommandResult{}, failure
	}
	if active.InstallationID == "" {
		return recoveryCommandResult{}, unsupportedPlatformLayout(root, fmt.Errorf("active receipt has no installation identity"))
	}
	if layoutErr := layout.Validate(); layoutErr != nil {
		return recoveryCommandResult{}, unsupportedPlatformLayout(root, layoutErr)
	}
	launcherState, err := host.StateHome()
	if err != nil {
		return recoveryCommandResult{}, asDiag(err)
	}
	state, err := host.StateForRoot(root)
	if err != nil {
		return recoveryCommandResult{}, asDiag(err)
	}
	release, err := state.AcquireOperation("uninstall")
	if err != nil {
		return recoveryCommandResult{}, asDiag(err)
	}
	defer release()
	preservedEntries, err := layout.PreservedEntries()
	if err != nil {
		return recoveryCommandResult{}, unsupportedPlatformLayout(root, err)
	}
	result, failure := runRecoveryMutation(ctx, recoveryRuntimeInput{Registry: input.Registry, Executable: input.Executable}, root, state, "uninstall", "", true, func(ctx context.Context, deps runtime.Dependencies) (contracts.Snapshot, error) {
		return runtime.Uninstall(root, deps)
	})
	if failure != nil {
		return recoveryCommandResult{}, failure
	}
	if input.Executable != "" {
		if err := shim.Remove(input.Executable, platformRoot); err != nil {
			return recoveryCommandResult{}, contracts.NewDiag("KB_INSTALL_APPLY_FAILED", "installation was removed but the kb command shim could not be removed", "remove the stale kb link from the launcher directory after checking its target", err)
		}
	}
	if err := (gateway.Registry{Path: filepath.Join(launcherState.Dir, "gateway-roots.json")}).Remove(platformRoot); err != nil {
		return recoveryCommandResult{}, contracts.NewDiag(contracts.CodeApplyFailed, "the installation was removed but its Gateway address could not be released", "check launcher state permissions, then rerun `kb-create apply` for remaining roots", err)
	}
	return recoveryCommandResult{OK: true, Operation: "uninstall", Snapshot: snapshotSummary(result.Snapshot), HostRestarted: false, LogPath: result.LogPath, PreservedEntries: preservedEntries}, nil
}

func executeRollback(ctx context.Context, input rollbackCommandInput) (recoveryCommandResult, *diag.Diag) {
	root := input.PlatformRoot
	if root == "" {
		return recoveryCommandResult{}, inputFailure(contracts.NewDiag(contracts.CodeInputRequired, "--platform-root is required", "pass the V2 platform root that owns the active receipt", nil))
	}
	launcherState, err := host.StateHome()
	if err != nil {
		return recoveryCommandResult{}, asDiag(err)
	}
	layout, layoutErr := versionroot.New(root)
	if layoutErr == nil {
		layoutErr = layout.Validate()
	}
	if layoutErr != nil {
		return recoveryCommandResult{}, unsupportedPlatformLayout(root, layoutErr)
	}
	state, err := host.StateForRoot(root)
	if err != nil {
		return recoveryCommandResult{}, asDiag(err)
	}
	platformRoot := layout.PlatformRoot()
	if running := refuseForeignHost(root); running != nil {
		return recoveryCommandResult{}, running
	}
	commandLine := fmt.Sprintf("rerun `kb-create rollback --platform-root %s", strconv.Quote(root))
	if input.Snapshot != "" {
		commandLine += " --snapshot " + strconv.Quote(input.Snapshot)
	}
	if input.JSON {
		commandLine += " --json"
	}
	commandLine += " --yes`"
	if failure := confirmDestructive(input.Yes, input.JSON, "This will replace platform root "+root+" and its active receipt with a saved snapshot. Continue? [y/N] ", commandLine, input.Command); failure != nil {
		return recoveryCommandResult{}, failure
	}
	active, err := receipt.Read(platformRoot)
	if err != nil {
		failure := contracts.NewDiag(contracts.CodeReceiptUnavailable, "active V2 receipt could not be read", "run apply first or restore a named V2 snapshot", err)
		failure.ProcessExitCode = diag.ExitInput
		return recoveryCommandResult{}, failure
	}
	target := input.Snapshot
	if target == "" {
		target = active.SnapshotID
		if target == "" {
			return recoveryCommandResult{}, contracts.NewDiag(contracts.CodeUpdateNoSnapshot, "There is no snapshot to roll back to.", "Reinstall the previous version with \"kb-create apply\", or name a snapshot with --snapshot.", nil)
		}
	}
	release, err := state.AcquireOperation("rollback")
	if err != nil {
		return recoveryCommandResult{}, asDiag(err)
	}
	defer release()
	result, failure := runRecoveryMutation(ctx, recoveryRuntimeInput{Registry: input.Registry, Executable: input.Executable}, root, state, "rollback", target, false, func(_ context.Context, deps runtime.Dependencies) (contracts.Snapshot, error) {
		installer := deps.Artifacts
		return lifecycle.Mutate(platformRoot, platformRoot, layout.SnapshotsDir(), time.Now().UTC(), func() error {
			_, rollbackErr := runtime.Rollback(platformRoot, target, deps)
			return rollbackErr
		}, nil, restoreReceiptArtifacts(installer))
	})
	if failure != nil {
		return recoveryCommandResult{}, failure
	}
	if restored, readErr := receipt.Read(platformRoot); readErr == nil && restored.Plan.Host != nil {
		origin, originErr := gateway.OriginFromHealthURL(restored.Verification.HostHealthURL)
		if originErr != nil {
			return recoveryCommandResult{}, contracts.NewDiag(contracts.CodeHostSpecInvalid, "the restored Gateway address could not be recorded", fmt.Sprintf("rerun `kb-create rollback --platform-root %q --snapshot %q --yes`", root, target), originErr)
		}
		if err := (gateway.Registry{Path: filepath.Join(launcherState.Dir, "gateway-roots.json")}).Register(platformRoot, origin); err != nil {
			return recoveryCommandResult{}, contracts.NewDiag(contracts.CodeApplyFailed, "the rollback succeeded but its Gateway address could not be indexed", "check launcher state permissions, then rerun `kb-create rollback`", err)
		}
	}
	return recoveryCommandResult{OK: true, Operation: "rollback", Snapshot: snapshotSummary(result.Snapshot), HostRestarted: result.WasRunning, LogPath: result.LogPath}, nil
}

type recoveryMutationResult struct {
	Snapshot   contracts.Snapshot
	WasRunning bool
	LogPath    string
}

func restoreReceiptArtifacts(installer interface {
	Restore([]contracts.Artifact) error
}) lifecycle.PackageRestore {
	return installer.Restore
}

func runRecoveryMutation(ctx context.Context, input recoveryRuntimeInput, root string, state host.State, operation string, target string, keepStopped bool,
	change func(context.Context, runtime.Dependencies) (contracts.Snapshot, error)) (recoveryMutationResult, *diag.Diag) {
	layout, err := versionroot.New(root)
	if err != nil {
		return recoveryMutationResult{}, unsupportedPlatformLayout(root, err)
	}
	platformRoot := layout.PlatformRoot()
	activeRoot, err := layout.Active()
	if err != nil {
		return recoveryMutationResult{}, unsupportedPlatformLayout(root, err)
	}
	correlationID := fmt.Sprintf("%s-%d", operation, time.Now().UTC().UnixNano())
	transcript, err := logs.New(layout.OperationLogsDir(), correlationID, nil)
	if err != nil {
		return recoveryMutationResult{}, contracts.NewDiag("KB_INSTALL_LOG_UNAVAILABLE", "could not create local operation log", "check that the platform root is writable", err)
	}
	defer transcript.Close()
	installer := artifacts.Composite{Packages: artifacts.Pnpm{Root: activeRoot, Registry: input.Registry, Log: transcript}, Binaries: artifacts.Binaries{Root: activeRoot}}
	deps := runtime.Dependencies{Artifacts: installer, CorrelationID: correlationID}
	command := hostCommand{PlatformRoot: root, Executable: input.Executable, Version: buildVersion}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer cancel()
	var snapshot contracts.Snapshot
	operationSpec := swap.Operation{Name: operation, PreviousVersion: installedVersion(platformRoot), KeepStopped: keepStopped,
		Change: func(changeCtx context.Context, _ bool) (string, error) {
			var changeErr error
			snapshot, changeErr = change(changeCtx, deps)
			return snapshot.ID, changeErr
		},
		Restore: func(_ context.Context, id string) error {
			_, restoreErr := lifecycle.Rollback(platformRoot, layout.SnapshotsDir(), id, restoreReceiptArtifacts(installer))
			return restoreErr
		},
	}
	result, err := swap.Run(ctx, lifecycleHost{command: command, state: state}, operationSpec)
	if err != nil {
		return recoveryMutationResult{}, operationDiagnostic(root, correlationID, transcript.Path(), asDiag(err), nil)
	}
	return recoveryMutationResult{Snapshot: snapshot, WasRunning: result.WasRunning, LogPath: transcript.Path()}, nil
}

func confirmDestructive(yes, jsonMode bool, prompt, hint string, command *cobra.Command) *diag.Diag {
	if yes {
		return nil
	}
	if jsonMode || !term.IsTerminal(os.Stdin.Fd()) {
		failure := contracts.NewDiag("KB_INSTALL_CONFIRMATION_REQUIRED", "This destructive operation requires explicit confirmation.", hint, fmt.Errorf("confirmation unavailable without a terminal"))
		failure.ProcessExitCode = diag.ExitInput
		return failure
	}
	if _, err := fmt.Fprint(command.OutOrStdout(), prompt); err != nil {
		return progressWriteFailure(err, "uninstall")
	}
	confirmed, err := readConfirmation(os.Stdin)
	if err != nil {
		return contracts.NewDiag("KB_INSTALL_CONFIRMATION_REQUIRED", "This destructive operation requires explicit confirmation.", hint, err)
	}
	if !confirmed {
		return contracts.NewDiag("KB_INSTALL_OPERATION_CANCELLED", "The operation was cancelled.", "rerun the command and confirm when prompted", nil)
	}
	return nil
}
