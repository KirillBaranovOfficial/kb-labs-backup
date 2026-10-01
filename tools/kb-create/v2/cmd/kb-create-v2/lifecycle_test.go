//go:build !windows

package v2cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kb-labs/create/v2/contracts"
	"github.com/kb-labs/create/v2/doctor"
	"github.com/kb-labs/create/v2/host"
	"github.com/kb-labs/create/v2/installed"
	"github.com/kb-labs/create/v2/lifecycle"
	"github.com/kb-labs/create/v2/receipt"
	"github.com/kb-labs/create/v2/swap"
)

func TestReadConfirmation(t *testing.T) {
	for _, test := range []struct {
		answer string
		want   bool
	}{{"y\n", true}, {"yes\n", true}, {"n\n", false}, {"\n", false}} {
		got, err := readConfirmation(strings.NewReader(test.answer))
		if err != nil || got != test.want {
			t.Fatalf("answer %q: got %v, %v", test.answer, got, err)
		}
	}
}

func TestConfirmRecoveryTTYYesAndNo(t *testing.T) {
	for _, test := range []struct {
		answer string
		want   bool
	}{{"y\n", true}, {"n\n", false}} {
		var output bytes.Buffer
		got, err := confirmRecovery(invocation{}, "uninstall", "/platform", strings.NewReader(test.answer), &output, true)
		if err != nil || got != test.want {
			t.Fatalf("answer %q: got %v, %v", test.answer, got, err)
		}
		for _, expected := range []string{"/platform", "services", "receipt", "snapshots", "Project files are kept"} {
			if !strings.Contains(output.String(), expected) {
				t.Fatalf("prompt missing %q: %s", expected, output.String())
			}
		}
	}
}

func TestRecoveryRequiresYesWithoutTTY(t *testing.T) {
	root := t.TempDir()
	receiptPath := receipt.Path(root)
	if err := os.MkdirAll(filepath.Dir(receiptPath), 0o750); err != nil {
		t.Fatal(err)
	}
	wantReceipt := []byte("active receipt must survive refused uninstall\n")
	if err := os.WriteFile(receiptPath, wantReceipt, 0o600); err != nil {
		t.Fatal(err)
	}

	registry := "https://registry.example.test"
	payload, code, raw := invoke(t, invocation{Operation: "uninstall", PlatformRoot: root, Registry: registry, JSON: true})
	wantHint := "rerun `kb-create uninstall --platform-root " + strconv.Quote(root) + " --registry " + registry + " --json --yes`"
	errorPayload, _ := payload["error"].(map[string]any)
	if code != 2 || errorCode(payload) != "KB_INSTALL_CONFIRMATION_REQUIRED" || errorPayload["hint"] != wantHint {
		t.Fatalf("unexpected confirmation envelope: %d %s", code, raw)
	}
	if gotReceipt, err := os.ReadFile(receiptPath); err != nil || string(gotReceipt) != string(wantReceipt) {
		t.Fatalf("receipt changed after refused uninstall: got %q, err %v", gotReceipt, err)
	}
}

func TestRecoveryWithYesPassesConfirmationGate(t *testing.T) {
	payload, code, raw := invoke(t, invocation{Operation: "uninstall", PlatformRoot: t.TempDir(), Yes: true})
	if code != 2 || errorCode(payload) != contracts.CodeReceiptUnavailable {
		t.Fatalf("--yes did not proceed to receipt validation: %d %s", code, raw)
	}
}

// These tests run the real launcher binary as the supervisor and a real child
// process as the host; only the artifact swap (pnpm) is replaced by rewriting
// the receipt, which is what an update changes as far as the host is concerned.

func freeLoopbackPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	return listener.Addr().(*net.TCPAddr).Port
}

// versionedReceipt is the receipt of one platform version whose host is the
// fake host on the given port.
func versionedReceipt(t *testing.T, version string, port int, hostArgs ...string) contracts.InstallReceipt {
	t.Helper()
	_, fakehost := binaries(t)
	spec := &contracts.HostSpec{
		Command: fakehost, Args: append([]string{"-port", fmt.Sprint(port)}, hostArgs...),
		HealthURL: fmt.Sprintf("http://127.0.0.1:%d/health", port), ReadyTimeoutSeconds: 10, StopGraceSeconds: 5,
	}
	plan := contracts.ResolvedInstallPlan{Schema: contracts.ResolvedPlanSchema, ServiceGraph: contracts.ServiceGraph{PlatformVersion: version}, Host: spec}
	return contracts.InstallReceipt{ID: "receipt-" + version, Plan: plan}
}

func (i installation) lifecycleHost() lifecycleHost {
	return lifecycleHost{command: hostCommand{PlatformRoot: i.root, Executable: i.launcher, Version: "test"}, state: host.State{Dir: i.state}}
}

// swapTo replaces the installation with next around the host, the way
// runUpdate does, and returns swap's outcome.
func (i installation) swapTo(t *testing.T, next contracts.InstallReceipt) (swap.Outcome, error) {
	t.Helper()
	return swap.Run(context.Background(), i.lifecycleHost(), swap.Operation{
		Name: "update", PreviousVersion: "1.0.0",
		Change: func(context.Context, bool) (string, error) {
			snapshot, err := lifecycle.Mutate(i.root, time.Now().UTC(), func() error { return receipt.Write(i.root, next) }, nil, nil)
			return snapshot.ID, err
		},
		Restore: func(_ context.Context, id string) error {
			_, err := lifecycle.Rollback(i.root, id, nil)
			return err
		},
	})
}

func (i installation) hostState(t *testing.T) (state string, healthURL string) {
	t.Helper()
	status, _, raw := i.run("status", "--json")
	hostStatus, _ := status["host"].(map[string]any)
	if hostStatus == nil {
		t.Fatalf("status: %s", raw)
	}
	url, _ := hostStatus["healthUrl"].(string)
	return hostStatus["state"].(string), url
}

func launcherCode(err error) string {
	var typed *contracts.LauncherError
	if errors.As(err, &typed) {
		return typed.Code
	}
	return ""
}

func TestUpdateStopsSwapsAndRestartsTheHostWithoutManualSteps(t *testing.T) {
	inst := newInstallation(t)
	started, code, raw := inst.run("start", "--json")
	if code != 0 {
		t.Fatalf("start: %s", raw)
	}
	before := started["host"].(map[string]any)["pid"].(float64)
	newPort := freeLoopbackPort(t)

	outcome, err := inst.swapTo(t, versionedReceipt(t, "2.0.0", newPort))
	if err != nil || !outcome.WasRunning || outcome.RecoveryPoint == "" {
		t.Fatalf("swap: %#v, %v", outcome, err)
	}
	state, healthURL := inst.hostState(t)
	if state != "running" || healthURL != fmt.Sprintf("http://127.0.0.1:%d/health", newPort) {
		t.Fatalf("the host must run the new version: %s %s", state, healthURL)
	}
	if conn, dialErr := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", inst.port), 200*time.Millisecond); dialErr == nil {
		_ = conn.Close()
		t.Fatal("the previous host must be gone")
	}
	status, _, _ := inst.run("status", "--json")
	if pid := status["host"].(map[string]any)["pid"].(float64); pid == before {
		t.Fatal("the host process must have been replaced")
	}
	if installation, _ := status["installation"].(map[string]any); installation["platformVersion"] != "2.0.0" {
		t.Fatalf("status must report the installed version: %v", status["installation"])
	}
}

func TestUpdateThatBreaksTheHostRestoresAndRestartsThePreviousVersion(t *testing.T) {
	inst := newInstallation(t)
	if _, code, raw := inst.run("start", "--json"); code != 0 {
		t.Fatalf("start: %s", raw)
	}
	broken := versionedReceipt(t, "2.0.0", freeLoopbackPort(t), "-exit", "5")

	outcome, err := inst.swapTo(t, broken)
	if launcherCode(err) != contracts.CodeUpdateHostNotHealthy {
		t.Fatalf("err = %v", err)
	}
	var typed *contracts.LauncherError
	errors.As(err, &typed)
	if !strings.Contains(typed.Message, "previous version 1.0.0 was restored") || outcome.RecoveryPoint == "" {
		t.Fatalf("error = %#v, outcome = %#v", typed, outcome)
	}
	if problems := typed.Validate(); len(problems) != 0 {
		t.Fatalf("the error must be a valid catalog envelope: %v", problems)
	}
	// The previous version is what runs again, and what the receipt says.
	state, healthURL := inst.hostState(t)
	if state != "running" || healthURL != fmt.Sprintf("http://127.0.0.1:%d/health", inst.port) {
		t.Fatalf("the previous host must be running again: %s %s", state, healthURL)
	}
	active, readErr := receipt.Read(inst.root)
	if readErr != nil || active.ID != "r1" {
		t.Fatalf("the previous receipt must be active again: %#v, %v", active, readErr)
	}
}

func TestUpdateLeavesAStoppedHostStopped(t *testing.T) {
	inst := newInstallation(t)
	outcome, err := inst.swapTo(t, versionedReceipt(t, "2.0.0", freeLoopbackPort(t)))
	if err != nil || outcome.WasRunning {
		t.Fatalf("swap: %#v, %v", outcome, err)
	}
	if state, _ := inst.hostState(t); state != "stopped" {
		t.Fatalf("an update must not start a host that was stopped, state = %s", state)
	}
}

func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("output is not one JSON document: %v\n%s", err, data)
	}
	return parsed
}

// invoke runs one non-host command and returns its JSON output and status.
func invoke(t *testing.T, command invocation) (map[string]any, int, string) {
	t.Helper()
	file, err := os.CreateTemp(t.TempDir(), "output")
	if err != nil {
		t.Fatal(err)
	}
	code := command.run(file)
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(file.Name())
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	_ = json.Unmarshal(data, &parsed)
	return parsed, code, string(data)
}

func TestRollbackWithoutASnapshotIsRefusedWithTheCatalogError(t *testing.T) {
	inst := newInstallation(t)
	payload, code, raw := invoke(t, invocation{Operation: "rollback", PlatformRoot: inst.root, Yes: true})
	if code != 1 || errorCode(payload) != contracts.CodeUpdateNoSnapshot {
		t.Fatalf("rollback: %d %s", code, raw)
	}
	failure := payload["error"].(map[string]any)
	if failure["message"] != "There is no snapshot to roll back to." || failure["hint"] == "" {
		t.Fatalf("error = %v", failure)
	}
}

func TestLifecycleCommandsNeedAnInstallation(t *testing.T) {
	t.Setenv(host.KBHomeEnv, t.TempDir())
	for _, operation := range []string{"rollback", "uninstall"} {
		payload, code, raw := invoke(t, invocation{Operation: operation, PlatformRoot: t.TempDir(), Yes: true})
		if code != 2 || errorCode(payload) != contracts.CodeReceiptUnavailable {
			t.Fatalf("%s: %d %s", operation, code, raw)
		}
	}
}

func TestStatusReportsTheHostAndTheInstalledVersion(t *testing.T) {
	inst := newInstallation(t)
	payload, code, raw := invoke(t, invocation{Operation: "status", PlatformRoot: inst.root, JSON: true})
	if code != 0 || payload["host"].(map[string]any)["state"] != "stopped" {
		t.Fatalf("status: %d %s", code, raw)
	}
}

func TestDoctorSeparatesTheInstallationFromThePlatform(t *testing.T) {
	inst := newInstallation(t)
	if err := os.MkdirAll(filepath.Join(inst.root, ".kb"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(inst.root, ".kb", "kb.config.jsonc"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Stopped host: the installation is fine, the platform is not running.
	payload, code, raw := invoke(t, invocation{Operation: "doctor", PlatformRoot: inst.root, JSON: true})
	if code != 1 || payload["ok"] != false {
		t.Fatalf("doctor: %d %s", code, raw)
	}
	if payload["installation"].(map[string]any)["ok"] != true || payload["platform"].(map[string]any)["ok"] != false {
		t.Fatalf("the two questions must be answered separately: %s", raw)
	}
	platformChecks := payload["platform"].(map[string]any)["checks"].([]any)
	var adapterCheck map[string]any
	for _, item := range platformChecks {
		if check, ok := item.(map[string]any); ok && check["id"] == "adapters" {
			adapterCheck = check
		}
	}
	if adapterCheck == nil || !strings.Contains(adapterCheck["detail"].(string), "cannot be checked") ||
		adapterCheck["hint"] != fmt.Sprintf("Run `kb-create start --platform-root %q`.", inst.root) {
		t.Fatalf("stopped doctor adapter check = %#v", adapterCheck)
	}
	textOutput, err := os.CreateTemp(t.TempDir(), "doctor-text")
	if err != nil {
		t.Fatal(err)
	}
	if textCode := (invocation{Operation: "doctor", PlatformRoot: inst.root}).run(textOutput); textCode != 1 {
		t.Fatalf("stopped text doctor exit code = %d", textCode)
	}
	if err := textOutput.Close(); err != nil {
		t.Fatal(err)
	}
	textBytes, err := os.ReadFile(textOutput.Name())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(textBytes), "Adapter status cannot be checked") ||
		!strings.Contains(string(textBytes), fmt.Sprintf("kb-create start --platform-root %q", inst.root)) {
		t.Fatalf("stopped text doctor omits the adapter limitation or start command: %s", textBytes)
	}

	// Running host: both are healthy.
	if _, exit, out := inst.run("start", "--json"); exit != 0 {
		t.Fatalf("start: %s", out)
	}
	payload, code, raw = invoke(t, invocation{Operation: "doctor", PlatformRoot: inst.root, JSON: true})
	if code != 0 || payload["ok"] != true || payload["platform"].(map[string]any)["ok"] != true {
		t.Fatalf("doctor on a healthy platform: %d %s", code, raw)
	}

	// A broken installation is reported as installation, whatever the platform does.
	if err := os.Remove(filepath.Join(inst.root, ".kb", "kb.config.jsonc")); err != nil {
		t.Fatal(err)
	}
	payload, code, raw = invoke(t, invocation{Operation: "doctor", PlatformRoot: inst.root, JSON: true})
	if code != 1 || payload["installation"].(map[string]any)["ok"] != false || payload["platform"].(map[string]any)["ok"] != true {
		t.Fatalf("doctor on a broken installation: %d %s", code, raw)
	}
}

func TestDoctorReportsMissingProjectionReadOnlyAndFixRestoresIt(t *testing.T) {
	inst := newInstallation(t)
	if err := os.MkdirAll(filepath.Join(inst.root, ".kb"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(inst.root, ".kb", "kb.config.jsonc"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	active, err := receipt.Read(inst.root)
	if err != nil {
		t.Fatal(err)
	}
	artifact := contracts.Artifact{ID: "@kb/plugin", Package: "@kb/plugin", Version: "1.2.3"}
	active.Plan.Artifacts = []contracts.Artifact{artifact}
	active.Plan.Request.PlatformRoot = inst.root
	if err := receipt.Write(inst.root, active); err != nil {
		t.Fatal(err)
	}
	packageRoot := filepath.Join(inst.root, "node_modules", "@kb", "plugin")
	if err := os.MkdirAll(packageRoot, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packageRoot, "package.json"), []byte(`{"name":"@kb/plugin","version":"1.2.3"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(packageRoot, "kb-create.manifest.json")

	payload, _, raw := invoke(t, invocation{Operation: "doctor", PlatformRoot: inst.root, JSON: true})
	findings, ok := payload["findings"].([]any)
	if !ok || len(findings) != 1 {
		t.Fatalf("doctor did not report the missing manifest projection: %s", raw)
	}
	finding := findings[0].(map[string]any)
	if finding["code"] != "KB_INSTALL_MANIFEST_PROJECTION_INVALID" || finding["component"] != "@kb/plugin" ||
		!strings.Contains(finding["hint"].(string), "kb-create doctor --fix") {
		t.Fatalf("unexpected projection finding: %#v", finding)
	}
	if _, err := os.Stat(manifestPath); !os.IsNotExist(err) {
		t.Fatalf("plain doctor changed the installed platform: %v", err)
	}

	_, fixCode, fixOutput := invoke(t, invocation{Operation: "doctor", PlatformRoot: inst.root, DoctorFix: true, JSON: true})
	if fixCode != 0 {
		t.Fatalf("doctor --fix failed: %s", fixOutput)
	}
	if _, err := os.Stat(manifestPath); err != nil {
		t.Fatalf("doctor --fix did not restore the projection: %v", err)
	}
}

func TestDoctorOptionalCredentialWarningKeepsFreshInstallHealthy(t *testing.T) {
	inst := newInstallation(t)
	t.Setenv("OPENAI_API_KEY", "")
	if err := os.MkdirAll(filepath.Join(inst.root, ".kb"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(inst.root, ".kb", "kb.config.jsonc"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	active, err := receipt.Read(inst.root)
	if err != nil {
		t.Fatal(err)
	}
	artifact := contracts.Artifact{ID: "openai", Package: "@kb-labs/adapters-openai", Version: "1.0.0", Config: []contracts.ConfigRequirement{{
		ID: "openai.apiKey", Path: "/platform/adapterOptions/llm/apiKey", Required: true, Optional: true,
		Secret: true, Env: "OPENAI_API_KEY", Services: []string{"gateway"},
		Action: "set OPENAI_API_KEY to enable LLM features", Hint: "set OPENAI_API_KEY",
	}}}
	active.Plan.Artifacts = []contracts.Artifact{artifact}
	if err := receipt.Write(inst.root, active); err != nil {
		t.Fatal(err)
	}
	packageRoot := filepath.Join(inst.root, "node_modules", filepath.FromSlash(artifact.Package))
	if err := os.MkdirAll(packageRoot, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packageRoot, "package.json"), []byte(`{"name":"@kb-labs/adapters-openai","version":"1.0.0"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := installed.Write(inst.root, artifact); err != nil {
		t.Fatal(err)
	}
	if _, code, output := inst.run("start", "--json"); code != 0 {
		t.Fatalf("start: %s", output)
	}
	payload, code, output := invoke(t, invocation{Operation: "doctor", PlatformRoot: inst.root, JSON: true})
	findings, ok := payload["findings"].([]any)
	if code != 0 || payload["ok"] != true || !ok || len(findings) != 1 {
		t.Fatalf("optional credential warning made the fresh install unhealthy: exit=%d %s", code, output)
	}
	warning := findings[0].(map[string]any)
	if warning["code"] != doctor.CodeAdapterCredentialsMissing || warning["severity"] != doctor.SeverityWarning || warning["action"] != "set OPENAI_API_KEY to enable LLM features" || warning["path"] != "/platform/adapterOptions/llm/apiKey" {
		t.Fatalf("unexpected optional credential warning: %#v", warning)
	}
	if strings.Contains(output, "secret-value") {
		t.Fatalf("doctor exposed secret material: %s", output)
	}
	textFile, err := os.CreateTemp(t.TempDir(), "doctor-warning")
	if err != nil {
		t.Fatal(err)
	}
	if textCode := (invocation{Operation: "doctor", PlatformRoot: inst.root}).run(textFile); textCode != 0 {
		t.Fatalf("text doctor rejected the optional credential warning: exit=%d", textCode)
	}
	if err := textFile.Close(); err != nil {
		t.Fatal(err)
	}
	textBytes, err := os.ReadFile(textFile.Name())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(textBytes), "Warnings:") || !strings.Contains(string(textBytes), "set OPENAI_API_KEY to enable LLM features") {
		t.Fatalf("text doctor omits the optional credential warning: %s", textBytes)
	}

	artifact.Config = append(artifact.Config, contracts.ConfigRequirement{ID: "gateway.mode", Path: "/platform/mode", Required: true, Hint: "configure platform mode"})
	active.Plan.Artifacts = []contracts.Artifact{artifact}
	if err := receipt.Write(inst.root, active); err != nil {
		t.Fatal(err)
	}
	if err := installed.Write(inst.root, artifact); err != nil {
		t.Fatal(err)
	}
	payload, code, output = invoke(t, invocation{Operation: "doctor", PlatformRoot: inst.root, JSON: true})
	findings, ok = payload["findings"].([]any)
	if code == 0 || payload["ok"] != false || !ok || len(findings) != 2 {
		t.Fatalf("required platform configuration did not fail doctor: exit=%d %s", code, output)
	}
	if findings[1].(map[string]any)["severity"] != doctor.SeverityError {
		t.Fatalf("required platform finding is not an error: %#v", findings)
	}
}

// recoverInstallation returns an installation whose platform ships a recovery
// tool that records the arguments it was run with.
func recoverInstallation(t *testing.T, withTool bool) (installation, string) {
	t.Helper()
	inst := newInstallation(t)
	record := filepath.Join(t.TempDir(), "args.txt")
	script := filepath.Join(inst.root, "recover.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s\\n' \"$*\" > \"$RECOVER_RECORD\"\nprintf 'recovered\\n'\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RECOVER_RECORD", record)
	active, err := receipt.Read(inst.root)
	if err != nil {
		t.Fatal(err)
	}
	if withTool {
		active.Plan.Host.Recover = &contracts.ToolSpec{Command: "./recover.sh", Args: []string{"--base"}}
	}
	if err := receipt.Write(inst.root, active); err != nil {
		t.Fatal(err)
	}
	return inst, record
}

func TestDoctorRecoverRunsThePlatformRecoveryToolWithTheHostStopped(t *testing.T) {
	inst, record := recoverInstallation(t, true)
	request := recoverRequest{Enabled: true, Email: "admin@example.com", Tenant: "acme", Generate: true, Yes: true}
	_, code, raw := invoke(t, invocation{Operation: "doctor", PlatformRoot: inst.root, Recover: request})
	if code != 0 || !strings.Contains(raw, "recovered") {
		t.Fatalf("recover: %d %q", code, raw)
	}
	args, err := os.ReadFile(record)
	if err != nil || strings.TrimSpace(string(args)) != "--base --email admin@example.com --tenant acme --generate --yes" {
		t.Fatalf("the tool must receive the operator's flags: %q, %v", args, err)
	}
}

func TestDoctorRecoverRefusesWhileTheHostRuns(t *testing.T) {
	inst, record := recoverInstallation(t, true)
	if _, code, raw := inst.run("start", "--json"); code != 0 {
		t.Fatalf("start: %s", raw)
	}
	payload, code, raw := invoke(t, invocation{Operation: "doctor", PlatformRoot: inst.root, Recover: recoverRequest{Enabled: true, JSON: true}})
	if code != 1 || errorCode(payload) != contracts.CodeRecoveryHostRunning {
		t.Fatalf("recover: %d %s", code, raw)
	}
	if _, err := os.Stat(record); !os.IsNotExist(err) {
		t.Fatal("the recovery tool must not run while the host runs")
	}
}

func TestDoctorRecoverNeedsAPlatformThatShipsARecoveryTool(t *testing.T) {
	inst, _ := recoverInstallation(t, false)
	payload, code, raw := invoke(t, invocation{Operation: "doctor", PlatformRoot: inst.root, Recover: recoverRequest{Enabled: true, JSON: true}})
	if code != 1 || errorCode(payload) != contracts.CodeRecoveryUnavailable {
		t.Fatalf("recover: %d %s", code, raw)
	}
}

func TestDoctorRecoverPropagatesTheToolsExitStatus(t *testing.T) {
	inst, _ := recoverInstallation(t, true)
	script := filepath.Join(inst.root, "recover.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 4\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, code, _ := invoke(t, invocation{Operation: "doctor", PlatformRoot: inst.root, Recover: recoverRequest{Enabled: true}}); code != 4 {
		t.Fatalf("exit status = %d, want the tool's 4", code)
	}
}

func TestFreshApplyIsRefusedWhileASupervisedHostRuns(t *testing.T) {
	inst := newInstallation(t)
	state := host.State{Dir: inst.state}
	if failure := refuseRunningHost(state); failure != nil {
		t.Fatalf("nothing runs yet: %v", failure)
	}
	if _, code, raw := inst.run("start", "--json"); code != 0 {
		t.Fatalf("start: %s", raw)
	}
	failure := refuseRunningHost(state)
	if failure == nil || failure.Code != "KB_HOST_ALREADY_RUNNING" || failure.Details["supervisorPid"] == "" {
		t.Fatalf("failure = %#v", failure)
	}
	if problems := failure.Validate(); len(problems) != 0 {
		t.Fatalf("the refusal must be a valid catalog envelope: %v", problems)
	}
}
