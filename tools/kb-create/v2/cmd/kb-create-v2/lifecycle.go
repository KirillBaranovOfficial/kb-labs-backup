package v2cli

import (
	"fmt"
	"path/filepath"

	"github.com/kb-labs/clikit/diag"
	"github.com/kb-labs/create/v2/contracts"
	"github.com/kb-labs/create/v2/diagnostics"
	"github.com/kb-labs/create/v2/host"
	"github.com/kb-labs/create/v2/receipt"
	"github.com/kb-labs/create/v2/supervisor"
	"github.com/kb-labs/create/v2/versionroot"
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
func refuseRunningHost(platformRoot string) *diag.Diag {
	layout, err := versionroot.New(platformRoot)
	if err != nil {
		return contracts.NewDiag(contracts.CodeInputRequired, "The installation path is invalid.", "Pass the install root used by this installation, then rerun the command.", err)
	}
	platformRoot = layout.InstallRoot()
	rootState, err := host.StateForRoot(platformRoot)
	if err != nil {
		return contracts.NewDiag(contracts.CodeInputRequired, "The platform path is invalid.", "Pass the platform root used by this installation, then rerun the command.", err)
	}
	record, alive := activeLauncher(rootState)
	if !alive {
		return nil
	}
	if record.PlatformRoot != filepath.Clean(platformRoot) {
		return supervisor.Refusal(record, platformRoot)
	}
	failure := contracts.NewDiag("KB_HOST_ALREADY_RUNNING", "The KB Labs host is already running.", "Use \"kb-create update\" to change an installed platform, or \"kb-create stop\" before a fresh apply.", nil)
	failure.Meta = map[string]any{"pid": fmt.Sprint(record.PID), "platformRoot": record.PlatformRoot}
	return failure
}

func refuseApplyBesideRunningHost(plan contracts.ResolvedInstallPlan, platformRoot string) *diag.Diag {
	if plan.Host == nil {
		return nil
	}
	return refuseRunningHost(platformRoot)
}

func refuseForeignHost(platformRoot string) *diag.Diag {
	layout, err := versionroot.New(platformRoot)
	if err != nil {
		return contracts.NewDiag(contracts.CodeInputRequired, "The installation path is invalid.", "Pass the install root used by this installation, then rerun the command.", err)
	}
	platformRoot = layout.InstallRoot()
	rootState, err := host.StateForRoot(platformRoot)
	if err != nil {
		return contracts.NewDiag(contracts.CodeInputRequired, "The platform path is invalid.", "Pass the platform root used by this installation, then rerun the command.", err)
	}
	record, alive := activeLauncher(rootState)
	if !alive {
		return nil
	}
	if record.PlatformRoot == filepath.Clean(platformRoot) {
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

func operationDiagnostic(root, correlationID, logPath string, failure *diag.Diag, plan *contracts.ResolvedInstallPlan) *diag.Diag {
	failure.CorrelationID = correlationID
	dossier := diagnostics.Dossier{CorrelationID: correlationID, Error: failure, Stage: contracts.StageApply, LogPath: logPath}
	if plan != nil {
		dossier.PlanHash, dossier.ReleaseDigest, dossier.ScenarioStateDigest = plan.PlanHash, plan.ReleaseDigest, plan.ScenarioStateDigest
	}
	layout, layoutErr := versionroot.ForPlatformPath(root)
	if layoutErr != nil {
		return contracts.NewDiag("KB_INSTALL_DIAGNOSTIC_UNAVAILABLE", "The operation failed and the diagnostic dossier could not be written.", "inspect the local operation log", layoutErr)
	}
	path, err := diagnostics.Write(layout.FailureDossiersDir(), dossier, nil)
	if err != nil {
		return contracts.NewDiag("KB_INSTALL_DIAGNOSTIC_UNAVAILABLE", "The operation failed and the diagnostic dossier could not be written.", "inspect the local operation log", err)
	}
	if failure.Meta == nil {
		failure.Meta = map[string]any{}
	}
	failure.Meta["logPath"], failure.Meta["diagnosticPath"] = logPath, path
	return failure
}
