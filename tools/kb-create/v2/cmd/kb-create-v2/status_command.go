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
)

type statusCommandInput struct{ PlatformRoot, Executable string }

func executeStatus(ctx context.Context, inv statusCommandInput) (map[string]any, *diag.Diag) {
	root := inv.PlatformRoot
	if root == "" {
		root = defaultPlatformRoot()
	}
	active, err := receipt.Read(root)
	if err != nil {
		return map[string]any{"ok": true, "operation": "status", "installed": false, "running": false, "reachable": false, "next": "kb-create install"}, nil
	}
	if active.Plan.ServiceGraph.Profile == "client" {
		return map[string]any{"ok": true, "operation": "status", "installed": true, "running": false, "reachable": false, "profile": "client", "services": []contracts.Service{}}, nil
	}
	state, err := host.StateHome()
	if err != nil {
		return nil, asDiag(err)
	}
	runner, err := host.NewRunner(root, state)
	if err != nil {
		return nil, asDiag(err)
	}
	status, err := runner.Status(ctx)
	if err != nil {
		return nil, asDiag(err)
	}
	payload := map[string]any{"ok": true, "operation": "status", "host": status, "installed": true,
		"running": status.State == host.StateRunning, "reachable": status.Healthy}
	next := "kb-create start"
	if status.State == host.StateRunning && status.Healthy {
		next = "cd ~/any-project && kb commit"
	}
	if inv.Executable != "" && !shim.Correct(inv.Executable, root) {
		next = "kb-create install"
	}
	payload["next"] = next
	if inv.Executable != "" {
		payload["kbShimCorrect"] = shim.Correct(inv.Executable, root)
	}
	payload["installation"] = map[string]any{"receiptId": active.ID, "platformVersion": active.Plan.ServiceGraph.PlatformVersion, "platformChannel": active.Plan.PlatformChannel}
	if pid, ok := supervisor.LivePID(state, root); ok {
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
