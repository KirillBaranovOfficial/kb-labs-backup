//go:build !windows

package v2cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/kb-labs/clikit/diag"
	"github.com/kb-labs/create/v2/contracts"
	"github.com/kb-labs/create/v2/host"
	"github.com/kb-labs/create/v2/receipt"
	"github.com/kb-labs/create/v2/runtime"
)

// recordingInstaller stands in for pnpm: what an update changes for the host is
// the receipt and the generated config, which runtime.Update writes for real.
type recordingInstaller struct {
	installed []contracts.Artifact
	restores  int
}

func (r *recordingInstaller) Install(items []contracts.Artifact) error {
	r.installed = append(r.installed, items...)
	return nil
}
func (r *recordingInstaller) Restore([]contracts.Artifact) error { r.restores++; return nil }

// updatePlan is the resolved plan of platform 2.0.0 whose host is the fake host.
func updatePlan(t *testing.T, root string, port int, hostArgs ...string) contracts.ResolvedInstallPlan {
	t.Helper()
	plan := versionedReceipt(t, "2.0.0", port, hostArgs...).Plan
	plan.PlanHash = "plan-2.0.0-000000"
	plan.Request = contracts.InstallRequest{PlatformRoot: root}
	plan.Artifacts = []contracts.Artifact{{ID: "platform", Kind: "platform", Package: "@kb/platform", Version: "2.0.0"}}
	return plan
}

// update runs the update command's orchestration and returns its JSON and status.
func (i installation) update(t *testing.T, plan contracts.ResolvedInstallPlan, installer *recordingInstaller) (map[string]any, int, string) {
	t.Helper()
	state := host.State{Dir: i.state}
	deps := runtime.Dependencies{Artifacts: installer, CorrelationID: "update-test"}
	result, failure := runUpdateCommand(context.Background(), updateCommandInput{Executable: i.launcher}, plan, state, &deps)
	if failure != nil {
		payload := diagValue(t, failure)
		return payload, diag.ExitCode(failure), fmt.Sprint(payload)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(encoded, &payload); err != nil {
		t.Fatal(err)
	}
	return payload, 0, fmt.Sprint(payload)
}

func TestUpdateCommandRestartsARunningHostOnTheNewVersion(t *testing.T) {
	inst := newInstallation(t)
	if _, code, raw := inst.run("start", "--json"); code != 0 {
		t.Fatalf("start: %s", raw)
	}
	newPort := freeLoopbackPort(t)
	installer := &recordingInstaller{}

	payload, code, raw := inst.update(t, updatePlan(t, inst.root, newPort), installer)
	if code != 0 || payload["ok"] != true || payload["hostRestarted"] != true {
		t.Fatalf("update: %d %s", code, raw)
	}
	if len(installer.installed) != 1 || installer.restores != 0 {
		t.Fatalf("installer: %#v", installer)
	}
	committed := payload["receipt"].(map[string]any)
	if committed["snapshotId"] == "" || committed["snapshotId"] != payload["snapshot"].(map[string]any)["id"] {
		t.Fatalf("the committed receipt must name the recovery snapshot: %s", raw)
	}
	if _, healthURL := inst.hostState(t); healthURL != fmt.Sprintf("http://127.0.0.1:%d/health", newPort) {
		t.Fatalf("the host must run the new version: %s", healthURL)
	}
}

func TestUpdateCommandRollsBackWhenTheNewHostCrashes(t *testing.T) {
	inst := newInstallation(t)
	if _, code, raw := inst.run("start", "--json"); code != 0 {
		t.Fatalf("start: %s", raw)
	}
	installer := &recordingInstaller{}

	payload, code, raw := inst.update(t, updatePlan(t, inst.root, freeLoopbackPort(t), "-exit", "5"), installer)
	if code != 1 || payload["ok"] != false || errorCode(payload) != contracts.CodeUpdateHostNotHealthy {
		t.Fatalf("update: %d %s", code, raw)
	}
	failure := payload["error"].(map[string]any)
	if failure["correlationId"] != "update-test" || failure["message"] == "" || failure["hint"] == "" {
		t.Fatalf("error = %v", failure)
	}
	errorDetails := failure["details"].(map[string]any)
	if path, _ := errorDetails["diagnosticPath"].(string); path == "" {
		t.Fatalf("a failed update must leave a diagnostic dossier: %s", raw)
	} else if _, err := os.Stat(path); err != nil {
		t.Fatalf("dossier: %v", err)
	}
	if installer.restores != 1 {
		t.Fatalf("the package state must be restored once: %d", installer.restores)
	}
	state, healthURL := inst.hostState(t)
	if state != "running" || healthURL != fmt.Sprintf("http://127.0.0.1:%d/health", inst.port) {
		t.Fatalf("the previous version must run again: %s %s", state, healthURL)
	}
	if active, err := receipt.Read(inst.root); err != nil || active.ID != "r1" {
		t.Fatalf("active receipt = %#v, %v", active, err)
	}
}

func TestUpdateCommandTrialRunsTheNewHostWhenTheHostWasStopped(t *testing.T) {
	inst := newInstallation(t)
	installer := &recordingInstaller{}

	payload, code, raw := inst.update(t, updatePlan(t, inst.root, freeLoopbackPort(t), "-exit", "5"), installer)
	if code != 1 || errorCode(payload) != contracts.CodeUpdateApplyFailed {
		t.Fatalf("update: %d %s", code, raw)
	}
	if installer.restores != 1 {
		t.Fatalf("the package state must be restored once: %d", installer.restores)
	}
	if state, _ := inst.hostState(t); state != "stopped" {
		t.Fatalf("a stopped host must stay stopped, state = %s", state)
	}
	if active, err := receipt.Read(inst.root); err != nil || active.ID != "r1" {
		t.Fatalf("active receipt = %#v, %v", active, err)
	}

	payload, code, raw = inst.update(t, updatePlan(t, inst.root, freeLoopbackPort(t)), installer)
	if code != 0 || payload["hostRestarted"] != false {
		t.Fatalf("a healthy new host must commit without starting: %d %s", code, raw)
	}
	if state, _ := inst.hostState(t); state != "stopped" {
		t.Fatalf("state = %s", state)
	}
}

func TestUpdateCommandNeedsAnInstallation(t *testing.T) {
	inst := newInstallation(t)
	if err := os.Remove(receipt.Path(inst.root)); err != nil {
		t.Fatal(err)
	}
	payload, code, raw := inst.update(t, updatePlan(t, inst.root, freeLoopbackPort(t)), &recordingInstaller{})
	if code != 2 || errorCode(payload) != contracts.CodeReceiptUnavailable {
		t.Fatalf("update: %d %s", code, raw)
	}
	if _, err := os.Stat(filepath.Join(inst.root, ".kb", "kb.config.jsonc")); !os.IsNotExist(err) {
		t.Fatal("nothing may be written when there is nothing to update")
	}
}
