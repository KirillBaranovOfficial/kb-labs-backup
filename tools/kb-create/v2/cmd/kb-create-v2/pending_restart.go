package v2cli

import (
	"fmt"
	"strings"

	"github.com/kb-labs/create/v2/contracts"
	"github.com/kb-labs/create/v2/host"
)

func pendingRestart(active contracts.InstallReceipt, running host.Status, root string) *contracts.PendingRestart {
	if running.State != host.StateRunning || running.ReceiptID == "" || active.ID == running.ReceiptID {
		return nil
	}
	version := active.Plan.ServiceGraph.PlatformVersion
	services := make([]contracts.PendingRestartService, 0, len(running.ServiceIDs))
	for _, id := range running.ServiceIDs {
		services = append(services, contracts.PendingRestartService{ID: id, RunningVersion: running.PlatformVersion})
	}
	return &contracts.PendingRestart{Services: services, InstalledVersion: version,
		Command: contracts.ShellCommand("kb-create", "restart", "--platform-root", root)}
}

func renderPendingRestart(pending *contracts.PendingRestart) string {
	if pending == nil {
		return ""
	}
	services := make([]string, 0, len(pending.Services))
	for _, service := range pending.Services {
		services = append(services, fmt.Sprintf("%s (running %s)", service.ID, service.RunningVersion))
	}
	return fmt.Sprintf("Pending restart: %s; installed %s. Run %s", strings.Join(services, ", "), pending.InstalledVersion, pending.Command)
}

func pendingRestartValue(value any) *contracts.PendingRestart {
	pending, _ := value.(*contracts.PendingRestart)
	return pending
}
