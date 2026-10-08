package v2cli

import (
	"bufio"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/kb-labs/clikit/diag"
	"github.com/kb-labs/create/v2/contracts"
	"github.com/kb-labs/create/v2/diagnostics"
	"github.com/kb-labs/create/v2/host"
	"github.com/kb-labs/create/v2/receipt"
	"github.com/kb-labs/create/v2/supervisor"
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

func operationDiagnostic(root, correlationID, logPath string, failure *diag.Diag, plan *contracts.ResolvedInstallPlan) *diag.Diag {
	failure.CorrelationID = correlationID
	dossier := diagnostics.Dossier{CorrelationID: correlationID, Error: failure, Stage: contracts.StageApply, LogPath: logPath}
	if plan != nil {
		dossier.PlanHash, dossier.ReleaseDigest, dossier.ScenarioStateDigest = plan.PlanHash, plan.ReleaseDigest, plan.ScenarioStateDigest
	}
	path, err := diagnostics.Write(root, dossier, nil)
	if err != nil {
		return contracts.NewDiag("KB_INSTALL_DIAGNOSTIC_UNAVAILABLE", "The operation failed and the diagnostic dossier could not be written.", "inspect the local operation log", err)
	}
	if failure.Meta == nil {
		failure.Meta = map[string]any{}
	}
	failure.Meta["logPath"], failure.Meta["diagnosticPath"] = logPath, path
	return failure
}
