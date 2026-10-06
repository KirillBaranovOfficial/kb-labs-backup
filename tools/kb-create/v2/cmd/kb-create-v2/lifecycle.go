package v2cli

import (
	"bufio"
	"context"
	"fmt"
	"github.com/kb-labs/clikit/diag"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/x/term"
	"github.com/kb-labs/create/v2/artifacts"
	"github.com/kb-labs/create/v2/contracts"
	"github.com/kb-labs/create/v2/diagnostics"
	"github.com/kb-labs/create/v2/host"
	"github.com/kb-labs/create/v2/lifecycle"
	"github.com/kb-labs/create/v2/logs"
	"github.com/kb-labs/create/v2/receipt"
	"github.com/kb-labs/create/v2/runtime"
	"github.com/kb-labs/create/v2/shim"
	"github.com/kb-labs/create/v2/supervisor"
	"github.com/kb-labs/create/v2/swap"
)

// installedVersion is the platform version of the active receipt, for messages.
func installedVersion(platformRoot string) string {
	if active, err := receipt.Read(platformRoot); err == nil && active.Plan.ServiceGraph.PlatformVersion != "" {
		return active.Plan.ServiceGraph.PlatformVersion
	}
	return "unknown"
}

// refuseRunningHost stops a fresh apply from starting a trial run next to a
// supervised host: it would fail on the host's port with a misleading message.
// An installed platform is changed with update, which stops and restarts the host.
func refuseRunningHost(state host.State, platformRoot string) *diag.Diag {
	record, alive := activeLauncher(state)
	if !alive {
		return nil
	}
	requested, err := filepath.Abs(platformRoot)
	if err != nil || record.PlatformRoot != filepath.Clean(requested) {
		return supervisor.Refusal(record, platformRoot)
	}
	failure := contracts.NewDiag("KB_HOST_ALREADY_RUNNING", "The KB Labs host is already running.", "Use \"kb-create update\" to change an installed platform, or \"kb-create stop\" before a fresh apply.", nil)
	failure.Meta = map[string]any{"pid": fmt.Sprint(record.PID), "platformRoot": record.PlatformRoot}
	return failure
}

func refuseApplyBesideRunningHost(state host.State, plan contracts.ResolvedInstallPlan, platformRoot string) *diag.Diag {
	if plan.Host == nil {
		return nil
	}
	return refuseRunningHost(state, platformRoot)
}

func refuseForeignHost(state host.State, platformRoot string) *diag.Diag {
	record, alive := activeLauncher(state)
	if !alive {
		return nil
	}
	requested, err := filepath.Abs(platformRoot)
	if err == nil && record.PlatformRoot == filepath.Clean(requested) {
		return nil
	}
	return supervisor.Refusal(record, platformRoot)
}

func activeLauncher(state host.State) (supervisor.Record, bool) {
	if active, ok := host.ActiveHost(state); ok {
		return supervisor.Record{PID: active.PID, PlatformRoot: active.PlatformRoot}, true
	}
	return supervisor.ReadRecord(state)
}

// snapshotSummary is what the machine output says about a snapshot: never its
// captured files.
func snapshotSummary(snapshot contracts.Snapshot) map[string]any {
	return map[string]any{"id": snapshot.ID, "createdAt": snapshot.CreatedAt, "receiptId": snapshot.ReceiptID}
}

// runUpdate replaces the installed platform with the resolved plan around the
// host: the launcher stops the host, swaps the version, restarts the host from
// the new receipt and, when the new host does not come up, restores the
// previous version and restarts that. There are no manual steps.
func (i invocation) runUpdate(ctx context.Context, output *os.File, plan contracts.ResolvedInstallPlan, deps runtime.Dependencies, prober runtime.HostProbe, state host.State, correlationID, logPath string, outcome, errorCode *string) int {
	root := plan.Request.PlatformRoot
	if _, err := receipt.Read(root); err != nil {
		write(output, failure(contracts.CodeReceiptUnavailable, "active V2 receipt could not be read", "run apply first or restore a named V2 snapshot", err))
		return 2
	}
	var updated contracts.InstallReceipt
	var snapshot contracts.Snapshot
	command := hostCommand{PlatformRoot: root, Executable: i.Executable, Version: buildVersion}
	result, err := swap.Run(ctx, lifecycleHost{command: command, state: state}, swap.Operation{
		Name:            "update",
		PreviousVersion: installedVersion(root),
		Change: func(ctx context.Context, wasRunning bool) (string, error) {
			changeDeps := deps
			if !wasRunning {
				// A running host is proven by its restart right after the swap;
				// a stopped one gets a trial run before the receipt is committed.
				changeDeps.Host = prober
			}
			var updateErr error
			updated, snapshot, updateErr = runtime.Update(ctx, plan, changeDeps)
			return snapshot.ID, updateErr
		},
		Restore: func(_ context.Context, id string) error {
			_, restoreErr := lifecycle.Rollback(root, id, restoreReceiptArtifacts(deps.Artifacts))
			return restoreErr
		},
	})
	if err != nil {
		writeDiagnosticFailure(output, root, correlationID, logPath, asDiag(err), &plan)
		return 1
	}
	write(output, map[string]any{"ok": true, "operation": "update", "receipt": updated, "snapshot": snapshotSummary(snapshot), "hostRestarted": result.WasRunning, "logPath": logPath})
	*outcome, *errorCode = "success", ""
	return 0
}

// runRecovery is uninstall and rollback: both replace the installation, so both
// run around the host like an update.
func (i invocation) runRecovery(output *os.File) int {
	operation, root := i.Operation, i.PlatformRoot
	if root == "" {
		write(output, failure("KB_INSTALL_INPUT_REQUIRED", "--platform-root is required", "pass the V2 platform root that owns the active receipt", nil))
		return 2
	}
	if operation == "rollback" {
		state, stateErr := host.StateHome()
		if stateErr != nil {
			write(output, map[string]any{"ok": false, "error": asDiag(stateErr)})
			return 2
		}
		if running := refuseForeignHost(state, root); running != nil {
			write(output, map[string]any{"ok": false, "error": running})
			return 2
		}
	}
	if code := confirmDestructiveOperation(i, operation, root, os.Stdin, os.Stdout, term.IsTerminal(os.Stdin.Fd()), output); code != 0 {
		return code
	}
	active, err := receipt.Read(root)
	if err != nil {
		write(output, failure(contracts.CodeReceiptUnavailable, "active V2 receipt could not be read", "run apply first or restore a named V2 snapshot", err))
		return 2
	}
	target := i.Snapshot
	if operation == "rollback" && target == "" {
		// The receipt records the snapshot taken before the update that produced
		// it: rolling back without a named snapshot undoes the last update.
		target = active.SnapshotID
		if target == "" {
			write(output, failure(contracts.CodeUpdateNoSnapshot, "There is no snapshot to roll back to.", "Reinstall the previous version with \"kb-create apply\", or name a snapshot with --snapshot.", nil))
			return 1
		}
	}
	state, err := host.StateHome()
	if err != nil {
		write(output, map[string]any{"ok": false, "error": asDiag(err)})
		return 2
	}
	release, err := state.AcquireOperation(operation)
	if err != nil {
		write(output, map[string]any{"ok": false, "error": asDiag(err)})
		return 2
	}
	defer release()
	correlationID := fmt.Sprintf("%s-%d", operation, time.Now().UTC().UnixNano())
	transcript, err := logs.New(root, correlationID, nil)
	if err != nil {
		write(output, failure("KB_INSTALL_LOG_UNAVAILABLE", "could not create local operation log", "check that the platform root is writable", err))
		return 2
	}
	defer func() { _ = transcript.Close() }()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	installer := artifacts.Composite{Packages: artifacts.Pnpm{Root: root, Registry: i.Registry, Log: transcript}, Binaries: artifacts.Binaries{Root: root}}
	deps := runtime.Dependencies{Artifacts: installer, CorrelationID: correlationID}
	command := hostCommand{PlatformRoot: root, Executable: i.Executable, Version: buildVersion}
	var snapshot contracts.Snapshot
	op := swap.Operation{
		Name:            operation,
		PreviousVersion: installedVersion(root),
		Restore: func(_ context.Context, id string) error {
			_, restoreErr := lifecycle.Rollback(root, id, restoreReceiptArtifacts(installer))
			return restoreErr
		},
	}
	if operation == "uninstall" {
		op.KeepStopped = true
		op.Change = func(context.Context, bool) (string, error) {
			var uninstallErr error
			snapshot, uninstallErr = runtime.Uninstall(root, deps)
			return snapshot.ID, uninstallErr
		}
	} else {
		op.Change = func(context.Context, bool) (string, error) {
			// Snapshot what runs now, so a restored version that does not start
			// can be undone; a failing restore is undone by Mutate itself.
			var rollbackErr error
			snapshot, rollbackErr = lifecycle.Mutate(root, time.Now().UTC(), func() error {
				_, err := runtime.Rollback(root, target, deps)
				return err
			}, nil, restoreReceiptArtifacts(installer))
			return snapshot.ID, rollbackErr
		}
	}
	result, err := swap.Run(ctx, lifecycleHost{command: command, state: state}, op)
	if err != nil {
		writeDiagnosticFailure(output, root, correlationID, transcript.Path(), asDiag(err), nil)
		return 1
	}
	if operation == "uninstall" && i.Executable != "" {
		if err := shim.Remove(i.Executable, root); err != nil {
			write(output, failure("KB_INSTALL_APPLY_FAILED", "installation was removed but the kb command shim could not be removed", "remove the stale kb link from the launcher directory after checking its target", err))
			return 1
		}
	}
	write(output, map[string]any{"ok": true, "operation": operation, "snapshot": snapshotSummary(snapshot), "hostRestarted": result.WasRunning && operation != "uninstall", "logPath": transcript.Path()})
	return 0
}

func restoreReceiptArtifacts(installer interface {
	Restore([]contracts.Artifact) error
}) lifecycle.PackageRestore {
	return installer.Restore
}

func recoveryConfirmationHint(i invocation, operation, root string) string {
	command := fmt.Sprintf("kb-create %s --platform-root %s", operation, strconv.Quote(root))
	if operation == "recover" {
		command = fmt.Sprintf("kb-create doctor --platform-root %s --recover", strconv.Quote(root))
	}
	if operation == "rollback" && i.Snapshot != "" {
		command += " --snapshot " + strconv.Quote(i.Snapshot)
	}
	if operation == "uninstall" && i.Registry != "" {
		command += " --registry " + i.Registry
	}
	jsonMode := i.JSON
	if operation == "recover" {
		jsonMode = i.Recover.JSON
	}
	if jsonMode {
		command += " --json"
	}
	return "rerun `" + command + " --yes`"
}

func confirmDestructiveOperation(i invocation, operation, root string, input io.Reader, prompt io.Writer, isTTY bool, output *os.File) int {
	yes := i.Yes
	if operation == "recover" {
		yes = i.Recover.Yes
	}
	if yes {
		return 0
	}
	jsonMode := i.JSON
	if operation == "recover" {
		jsonMode = i.Recover.JSON
	}
	if jsonMode || !isTTY {
		hint := recoveryConfirmationHint(i, operation, root)
		write(output, failure("KB_INSTALL_CONFIRMATION_REQUIRED", "This destructive operation requires explicit confirmation.", hint, fmt.Errorf("confirmation unavailable without a terminal")))
		return 2
	}
	confirmed, err := confirmRecovery(operation, root, input, prompt)
	if err != nil {
		hint := recoveryConfirmationHint(i, operation, root)
		write(output, failure("KB_INSTALL_CONFIRMATION_REQUIRED", "This destructive operation requires explicit confirmation.", hint, err))
		return 2
	}
	if !confirmed {
		write(output, failure("KB_INSTALL_OPERATION_CANCELLED", "The operation was cancelled.", "rerun the command and confirm when prompted", nil))
		return 1
	}
	return 0
}

func confirmRecovery(operation, root string, input io.Reader, output io.Writer) (bool, error) {
	if operation == "uninstall" {
		fmt.Fprintf(output, "This will remove platform root %s, its installed services, active receipt, and saved snapshots. Project files are kept. Continue? [y/N] ", root)
	} else if operation == "recover" {
		fmt.Fprintf(output, "This will run the platform's administrator recovery tool for %s. Continue? [y/N] ", root)
	} else {
		fmt.Fprintf(output, "This will replace platform root %s and its active receipt with a saved snapshot. Continue? [y/N] ", root)
	}
	return readConfirmation(input)
}

func readConfirmation(input io.Reader) (bool, error) {
	line, err := bufio.NewReader(input).ReadString('\n')
	if err != nil && len(line) == 0 {
		return false, err
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	if answer == "y" || answer == "yes" {
		return true, nil
	}
	return false, nil
}

// writeDiagnosticFailure emits a typed failure with its diagnostic dossier.
func writeDiagnosticFailure(output *os.File, platformRoot, correlationID, logPath string, resolutionDiag *diag.Diag, plan *contracts.ResolvedInstallPlan) {
	resolutionDiag.CorrelationID = correlationID
	dossier := diagnostics.Dossier{CorrelationID: correlationID, Error: resolutionDiag, Stage: contracts.StageApply, LogPath: logPath}
	if plan != nil {
		dossier.PlanHash, dossier.ReleaseDigest, dossier.ScenarioStateDigest = plan.PlanHash, plan.ReleaseDigest, plan.ScenarioStateDigest
	}
	path, dossierErr := diagnostics.Write(platformRoot, dossier, nil)
	if dossierErr != nil {
		write(output, failure("KB_INSTALL_DIAGNOSTIC_UNAVAILABLE", "The operation failed and the diagnostic dossier could not be written.", "inspect the local operation log", dossierErr))
		return
	}
	write(output, map[string]any{"ok": false, "error": resolutionDiag, "logPath": logPath, "diagnosticPath": path})
}
