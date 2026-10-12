package v2cli

import (
	"context"

	"github.com/kb-labs/clikit/diag"
	"github.com/kb-labs/create/v2/contracts"
	"github.com/kb-labs/create/v2/control"
	"github.com/kb-labs/create/v2/host"
	"github.com/kb-labs/create/v2/receipt"
	"github.com/kb-labs/create/v2/shim"
	"github.com/kb-labs/create/v2/supervisor"
	"github.com/kb-labs/create/v2/versionroot"
)

type statusCommandInput struct{ PlatformRoot, Executable string }

func executeStatus(ctx context.Context, inv statusCommandInput) (map[string]any, *diag.Diag) {
	root := inv.PlatformRoot
	if root == "" {
		root = defaultPlatformRoot()
	}
	layout, err := versionroot.ForPlatformPath(root)
	if err != nil {
		return nil, asDiag(err)
	}
	platformRoot := layout.PlatformRoot()
	active, receiptErr := receipt.Read(platformRoot)
	if receiptErr == nil && active.Plan.ServiceGraph.Profile == "client" {
		return map[string]any{"ok": true, "operation": "status", "installed": true, "running": false, "reachable": false, "profile": "client", "services": []contracts.Service{}}, nil
	}
	runner, err := host.NewRecordedRunner(root)
	if err != nil {
		return nil, asDiag(err)
	}
	state := runner.State
	status, err := runner.Status(ctx)
	if err != nil {
		return nil, asDiag(err)
	}
	if status.State == host.StateStopped {
		if failure := host.RegisteredPortConflict(platformRoot, "status"); failure != nil {
			return nil, failure
		}
	}
	if record, identityStatus, stale := supervisor.StaleRecord(state); stale {
		if identityStatus == host.ProcessIdentityUndetermined && record.InstallationID != "" {
			return nil, host.UndeterminedProcessIdentityFailure(state.SupervisorPID(), "supervisor", record.PID, platformRoot, host.GatewayURLForState(state))
		}
		return nil, host.StaleProcessIdentityFailure(state.SupervisorPID(), "supervisor", record.PID, platformRoot, host.GatewayURLForState(state))
	}
	installed := receiptErr == nil
	payload := map[string]any{"ok": true, "operation": "status", "host": status, "installed": installed,
		"running": status.State == host.StateRunning, "reachable": status.Healthy}
	if installed {
		payload["installedVersion"] = active.Plan.ServiceGraph.PlatformVersion
	}
	if status.PlatformVersion != "" {
		payload["runningVersion"] = status.PlatformVersion
	}
	if installed {
		if pending := pendingRestart(active, status, root); pending != nil {
			payload["pendingRestart"] = pending
		}
	}
	next := "kb-create start"
	if status.State == host.StateRunning && status.Healthy {
		next = "cd ~/any-project && kb commit"
	}
	if inv.Executable != "" && installed && !shim.Correct(inv.Executable, platformRoot) {
		next = "kb-create install"
	}
	payload["next"] = next
	if inv.Executable != "" {
		payload["kbShimCorrect"] = shim.Correct(inv.Executable, platformRoot)
	}
	if installed {
		payload["installation"] = map[string]any{"receiptId": active.ID, "platformVersion": active.Plan.ServiceGraph.PlatformVersion, "platformChannel": active.Plan.PlatformChannel}
	}
	if pid, ok := supervisor.LivePID(state, platformRoot); ok {
		info := map[string]any{"pid": pid}
		if client, connectErr := control.Connect(state); connectErr == nil {
			info["address"], info["version"] = client.File.Address, client.File.Version
		}
		payload["supervisor"] = info
	}
	if status.State == host.StateUnhealthy {
		return nil, contracts.NewDiag("KB_HOST_UNHEALTHY", "The host responds but is unhealthy.", "Run \"kb-create restart\" or \"kb-create doctor\".", nil)
	}
	return payload, nil
}
