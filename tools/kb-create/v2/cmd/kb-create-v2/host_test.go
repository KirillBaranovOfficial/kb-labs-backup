//go:build !windows

package v2cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/kb-labs/clikit/diag"
	"github.com/kb-labs/create/v2/contracts"
	"github.com/kb-labs/create/v2/host"
	"github.com/kb-labs/create/v2/receipt"
	"github.com/kb-labs/create/v2/versionroot"
)

func TestStartStatusErrorRemainsAnError(t *testing.T) {
	statusErr := diag.New(contracts.CodeReceiptUnavailable, "status could not read the active receipt")
	got := startStatusDiagnostic(statusErr)
	if got.Code != statusErr.Code || got.Message != statusErr.Message {
		t.Fatalf("status diagnostic = %#v, want the status failure", got)
	}
}

var (
	buildOnce sync.Once
	buildDir  string
	buildErr  error
)

// binaries builds the real launcher and the fake host once per test binary.
func binaries(t *testing.T) (launcher, fakehost string) {
	t.Helper()
	buildOnce.Do(func() {
		buildDir, buildErr = os.MkdirTemp("", "kb-create-cli-")
		if buildErr != nil {
			return
		}
		for name, pkg := range map[string]string{"kb-create": "../../..", "fakehost": "../../internal/fakehost"} {
			if output, err := exec.Command("go", "build", "-o", filepath.Join(buildDir, name), pkg).CombinedOutput(); err != nil {
				buildErr = fmt.Errorf("build %s: %v\n%s", name, err, output)
				return
			}
		}
	})
	if buildErr != nil {
		t.Fatal(buildErr)
	}
	return filepath.Join(buildDir, "kb-create"), filepath.Join(buildDir, "fakehost")
}

func TestMain(m *testing.M) {
	code := m.Run()
	if buildDir != "" {
		for _, binary := range []string{filepath.Join(buildDir, "kb-create"), filepath.Join(buildDir, "fakehost")} {
			output, err := exec.Command("lsof", "-nP", "-t", binary).CombinedOutput()
			if err == nil && len(bytes.TrimSpace(output)) > 0 {
				fmt.Fprintf(os.Stderr, "test process remains alive for %s:\n%s", binary, output)
				code = 1
			} else if err != nil {
				if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 1 || len(bytes.TrimSpace(output)) != 0 {
					fmt.Fprintf(os.Stderr, "could not audit test process path %s: %v\n%s", binary, err, output)
					code = 1
				} else {
					fmt.Fprintf(os.Stdout, "process audit %s: none\n", binary)
				}
			} else {
				fmt.Fprintf(os.Stdout, "process audit %s: none\n", binary)
			}
		}
	}
	if buildDir != "" {
		os.RemoveAll(buildDir)
	}
	os.Exit(code)
}

type installation struct {
	launcher, root, kbHome, state string
	port                          int
	t                             *testing.T
}

func newInstallation(t *testing.T, hostArgs ...string) installation {
	t.Helper()
	launcher, fakehost := binaries(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	root := t.TempDir()
	spec := &contracts.HostSpec{Command: fakehost, Args: append([]string{"-port", fmt.Sprint(port)}, hostArgs...), HealthURL: fmt.Sprintf("http://127.0.0.1:%d/health", port), ReadyTimeoutSeconds: 10, StopGraceSeconds: 5}
	plan := contracts.ResolvedInstallPlan{Schema: contracts.ResolvedPlanSchema, Host: spec,
		ServiceGraph: contracts.ServiceGraph{PlatformVersion: "1.0.0", Services: []contracts.Service{{ID: "gateway", Command: "gateway"}}}}
	writeVersionedReceipt(t, root, contracts.InstallReceipt{ID: "r1", InstallationID: "installation-r1", Plan: plan})
	if err := os.WriteFile(filepath.Join(root, "state", "kb.config.jsonc"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "current", ".kb", "marketplace.lock"), []byte("old package set\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	kbHome := t.TempDir()
	t.Setenv(host.KBHomeEnv, kbHome)
	state, err := host.StateHome()
	if err != nil {
		t.Fatal(err)
	}
	inst := installation{launcher: launcher, root: root, kbHome: kbHome, state: state.Dir, port: port, t: t}
	t.Cleanup(func() {
		_, code, output := inst.run("stop", "--json")
		if code != 0 {
			t.Errorf("test cleanup stop failed: %s", output)
		}
		status, statusCode, statusOutput := inst.run("status", "--json")
		if statusCode != 0 || status["running"] == true {
			t.Errorf("test cleanup left host running: %s", statusOutput)
		}
		if pid, ok := status["host"].(map[string]any)["pid"].(float64); ok && host.ProcessAlive(int(pid)) {
			t.Errorf("test cleanup host process %d is still alive", int(pid))
		}
	})
	return inst
}

func newEmptyInstallation(t *testing.T) installation {
	t.Helper()
	launcher, _ := binaries(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	root := t.TempDir()
	kbHome := t.TempDir()
	t.Setenv(host.KBHomeEnv, kbHome)
	state, err := host.StateHome()
	if err != nil {
		t.Fatal(err)
	}
	inst := installation{launcher: launcher, root: root, kbHome: kbHome, state: state.Dir, port: port, t: t}
	t.Cleanup(func() {
		_, code, output := inst.run("stop", "--json")
		if code != 0 {
			t.Errorf("test cleanup stop failed: %s", output)
		}
		layout, layoutErr := versionroot.New(inst.root)
		if layoutErr == nil && layout.Validate() == nil {
			status, statusCode, statusOutput := inst.run("status", "--json")
			if statusCode != 0 || status["running"] == true {
				t.Errorf("test cleanup left host running: %s", statusOutput)
			}
		}
	})
	return inst
}

func (i installation) run(args ...string) (map[string]any, int, string) {
	command := exec.Command(i.launcher, append(args, "--platform-root", i.root)...)
	command.Env = append(os.Environ(), "KB_HOME="+i.kbHome)
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	err := command.Run()
	code := 0
	if exit, ok := err.(*exec.ExitError); ok {
		code = exit.ExitCode()
	}
	var parsed map[string]any
	_ = json.Unmarshal(stdout.Bytes(), &parsed)
	if data, ok := parsed["data"].(map[string]any); ok {
		parsed = data
	}
	if code == 0 && len(args) > 0 && (args[0] == "start" || args[0] == "restart") {
		pid := 0
		if hostStatus, ok := parsed["host"].(map[string]any); ok {
			if value, ok := hostStatus["pid"].(float64); ok {
				pid = int(value)
			}
		}
		i.t.Cleanup(func() {
			_, stopCode, stopOutput := i.run("stop", "--json")
			if stopCode != 0 {
				i.t.Errorf("test cleanup stop failed: %s", stopOutput)
			}
			status, statusCode, statusOutput := i.run("status", "--json")
			if statusCode != 0 || status["running"] == true {
				i.t.Errorf("test cleanup left host running: %s", statusOutput)
			}
			if pid != 0 && !waitHostExit(pid, 5*time.Second) {
				i.t.Errorf("test cleanup host process %d did not exit", pid)
			}
		})
	}
	return parsed, code, stdout.String() + stderr.String()
}

func (i installation) runCommand(args []string, extraEnv ...string) commandResult {
	return i.runArgv(append([]string{i.launcher}, args...), extraEnv...)
}

func (i installation) runArgv(argv []string, extraEnv ...string) commandResult {
	if len(argv) == 0 {
		i.t.Fatal("cannot run an empty argv")
	}
	program := argv[0]
	if program == "kb-create" {
		program = i.launcher
	}
	command := exec.Command(program, argv[1:]...)
	command.Env = append(os.Environ(), "KB_HOME="+i.kbHome, "PATH="+filepath.Dir(i.launcher)+string(os.PathListSeparator)+os.Getenv("PATH"))
	command.Env = append(command.Env, extraEnv...)
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	err := command.Run()
	code := 0
	if exit, ok := err.(*exec.ExitError); ok {
		code = exit.ExitCode()
	}
	output := stdout.String() + stderr.String()
	var payload map[string]any
	_ = json.Unmarshal(stdout.Bytes(), &payload)
	if data, ok := payload["data"].(map[string]any); ok {
		payload = data
	}
	return commandResult{payload: payload, code: code, output: output}
}

func waitHostExit(pid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for host.ProcessAlive(pid) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	return !host.ProcessAlive(pid)
}

func errorCode(payload map[string]any) string {
	if failure, ok := payload["error"].(map[string]any); ok {
		code, _ := failure["code"].(string)
		return code
	}
	return ""
}

func TestHostLifecycleThroughTheLauncher(t *testing.T) {
	inst := newInstallation(t)

	started, code, raw := inst.run("start", "--json")
	if code != 0 {
		t.Fatalf("start: %d %s", code, raw)
	}
	pid := started["host"].(map[string]any)["pid"].(float64)

	rootState, err := host.StateForRoot(inst.root)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(rootState.Dir, "control.json"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("control.json: %v %v", info, err)
	}

	again, code, raw := inst.run("start", "--json")
	if code != 1 || errorCode(again) != "KB_HOST_ALREADY_RUNNING" {
		t.Fatalf("second start: %d %s", code, raw)
	}

	status, code, raw := inst.run("status", "--json")
	hostInfo := status["host"].(map[string]any)
	if code != 0 || hostInfo["state"] != "running" || hostInfo["pid"].(float64) != pid {
		t.Fatalf("status: %d %s", code, raw)
	}
	doctor, doctorCode, doctorOutput := inst.run("doctor", "--json")
	retention, _ := doctor["retention"].(map[string]any)
	stateForDoctor, err := host.StateForRoot(inst.root)
	if err != nil || doctorCode != 0 || retention["hostState"] != stateForDoctor.Dir || retention["installRoot"] == "" ||
		retention["platformRoot"] == "" || retention["versionsDirectory"] == "" ||
		retention["sharedState"] == "" || retention["installerState"] == "" {
		t.Fatalf("doctor did not report container paths: code=%d retention=%#v output=%s", doctorCode, retention, doctorOutput)
	}

	restarted, code, raw := inst.run("restart", "--json")
	if code != 0 || restarted["host"].(map[string]any)["pid"].(float64) == pid {
		t.Fatalf("restart must replace the host process: %d %s", code, raw)
	}
	statusAfterRestart, statusCode, statusRaw := inst.run("status", "--json")
	if statusCode != 0 || statusAfterRestart["host"].(map[string]any)["state"] != "running" {
		t.Fatalf("status after restart: %d %s", statusCode, statusRaw)
	}

	stopped, code, raw := inst.run("stop", "--json")
	result, _ := stopped["stopped"].(map[string]any)
	if code != 0 || result["wasRunning"] != true || result["graceful"] != true || result["forced"] == true {
		t.Fatalf("stop: %d %s", code, raw)
	}
	if _, err := os.Stat(filepath.Join(rootState.Dir, "control.json")); !os.IsNotExist(err) {
		t.Fatal("control.json must be gone after stop")
	}
	status, code, _ = inst.run("status", "--json")
	if code != 0 || status["host"].(map[string]any)["state"] != "stopped" {
		t.Fatalf("status after stop: %v", status)
	}
	idle, code, _ := inst.run("stop", "--json")
	if code != 0 || idle["stopped"].(map[string]any)["wasRunning"] != false {
		t.Fatalf("stopping an idle host must succeed: %v", idle)
	}
}

// case: S-223.5
func TestStatusAndStopUseTheDurableHostRecordWhenLayoutMetadataIsUnreadable(t *testing.T) {
	inst := newInstallation(t)
	started, code, output := inst.run("start", "--json")
	if code != 0 {
		t.Fatalf("start: %d %s", code, output)
	}
	pid := int(started["host"].(map[string]any)["pid"].(float64))
	state, err := host.StateForRoot(inst.root)
	if err != nil {
		t.Fatal(err)
	}
	metadata := filepath.Join(filepath.Dir(state.Dir), "layout.json")
	if err := os.WriteFile(metadata, []byte("not valid layout metadata\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	status, code, output := inst.run("status", "--json")
	if code != 0 || status["running"] != true || status["host"].(map[string]any)["pid"].(float64) != float64(pid) {
		t.Fatalf("status did not read the durable host record: code=%d payload=%#v output=%s", code, status, output)
	}
	stopped, code, output := inst.run("stop", "--json")
	result, _ := stopped["stopped"].(map[string]any)
	if code != 0 || result["wasRunning"] != true {
		t.Fatalf("stop did not reach the recorded process: code=%d payload=%#v output=%s", code, stopped, output)
	}
	if !waitHostExit(pid, 5*time.Second) {
		t.Fatalf("recorded host process %d is still running after stop", pid)
	}
}

// case: S-025.7, S-025.8, PC-001.10, PC-001.12
func TestIndependentPlatformRootsOwnTheirHostLifecycle(t *testing.T) {
	inst := newInstallation(t)
	if _, code, output := inst.run("start", "--json"); code != 0 {
		t.Fatalf("start A: %d %s", code, output)
	}
	receiptAPath := receipt.Path(platformRootForTest(t, inst.root))
	receiptABefore, err := os.ReadFile(receiptAPath)
	if err != nil {
		t.Fatal(err)
	}
	rootB := t.TempDir()
	active, err := receipt.Read(platformRootForTest(t, inst.root))
	if err != nil {
		t.Fatal(err)
	}
	active.Plan.Request.PlatformRoot = filepath.Join(rootB, "current")
	portB := freeLoopbackPort(t)
	active.Plan.Host.Args = append([]string(nil), active.Plan.Host.Args...)
	active.Plan.Host.Args[1] = fmt.Sprint(portB)
	active.Plan.Host.HealthURL = fmt.Sprintf("http://127.0.0.1:%d/health", portB)
	writeVersionedReceipt(t, rootB, active)
	if err := os.WriteFile(filepath.Join(rootB, "state", "kb.config.jsonc"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	fixValue, fixFailure, code, _ := executeForTest(t, operationTestSetup{Operation: "doctor", PlatformRoot: rootB, Fix: true, JSON: true})
	if code != 0 || fixFailure != nil {
		t.Fatalf("doctor --fix B: %d %#v", code, fixFailure)
	}
	fixResult := fixValue.(doctorFixResult)
	if fixResult.HostRestarted {
		t.Fatalf("doctor --fix B reported a restart: %#v", fixResult)
	}
	statusB := runForRoot(t, inst, rootB, "status")
	if statusB["host"].(map[string]any)["state"] != "stopped" {
		t.Fatalf("status B observed A's host: %#v", statusB)
	}

	startB := runForRoot(t, inst, rootB, "start")
	if startB["ok"] != true || startB["operation"] != "start" {
		t.Fatalf("independent root B did not start beside A: %#v", startB)
	}
	statusBRunning := runForRoot(t, inst, rootB, "status")
	if statusBRunning["host"].(map[string]any)["state"] != "running" {
		t.Fatalf("B did not report its own host as running: %#v", statusBRunning)
	}
	stopB := runForRoot(t, inst, rootB, "stop")
	if stopB["ok"] != true {
		t.Fatalf("B stop failed: %#v", stopB)
	}
	statusA, code, output := inst.run("status", "--json")
	if code != 0 || statusA["host"].(map[string]any)["state"] != "running" {
		t.Fatalf("A was affected by B operations: %d %s", code, output)
	}
	if _, code, output = inst.run("restart", "--json"); code != 0 {
		t.Fatalf("restart A after B operations: %d %s", code, output)
	}
	receiptAAfter, err := os.ReadFile(receiptAPath)
	if err != nil || !bytes.Equal(receiptAAfter, receiptABefore) {
		t.Fatalf("operations on B changed A's receipt: err=%v", err)
	}
}

func runForRoot(t *testing.T, inst installation, root, operation string) map[string]any {
	t.Helper()
	command := exec.Command(inst.launcher, operation, "--platform-root", root, "--json")
	command.Env = append(os.Environ(), "KB_HOME="+inst.kbHome)
	output, err := command.CombinedOutput()
	code := 0
	if exit, ok := err.(*exec.ExitError); ok {
		code = exit.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	var envelope map[string]any
	if err := json.Unmarshal(output, &envelope); err != nil {
		t.Fatalf("%s result is not JSON (exit %d): %v %s", operation, code, err, output)
	}
	if data, ok := envelope["data"].(map[string]any); ok {
		if code == 0 && operation == "start" {
			pid := 0
			if hostStatus, ok := data["host"].(map[string]any); ok {
				if value, ok := hostStatus["pid"].(float64); ok {
					pid = int(value)
				}
			}
			t.Cleanup(func() {
				stopped := runForRoot(t, inst, root, "stop")
				if stopped["ok"] != true || (pid != 0 && !waitHostExit(pid, 5*time.Second)) {
					t.Errorf("cleanup did not stop root %s cleanly: %#v", root, stopped)
				}
			})
		}
		return data
	}
	envelope["code"] = float64(code)
	return envelope
}

// case: S-025.1, S-025.2, S-203.10
func TestStatusAndDoctorReportReceiptChannelAndExactVersion(t *testing.T) {
	inst := newInstallation(t)
	active := contracts.InstallReceipt{ID: "receipt-canary", InstallationID: "installation-r1", Plan: contracts.ResolvedInstallPlan{
		Schema: contracts.ResolvedPlanSchema, PlatformChannel: "canary",
		ServiceGraph: contracts.ServiceGraph{PlatformVersion: "3.2.0-canary.4"},
		Host:         &contracts.HostSpec{Command: inst.launcher, HealthURL: fmt.Sprintf("http://127.0.0.1:%d/health", inst.port)},
	}}
	installationID, err := versionroot.InstallationIDForPath(inst.root)
	if err != nil {
		t.Fatal(err)
	}
	active.InstallationID = installationID
	if err := receipt.Write(platformRootForTest(t, inst.root), active); err != nil {
		t.Fatal(err)
	}

	status, _, statusOutput := inst.run("status", "--json")
	installation, ok := status["installation"].(map[string]any)
	if !ok || installation["platformChannel"] != "canary" || installation["platformVersion"] != "3.2.0-canary.4" {
		t.Fatalf("status installation = %v; output %s", status["installation"], statusOutput)
	}
	_, _, statusText := inst.run("status")
	if !strings.Contains(statusText, "Platform channel: canary") || !strings.Contains(statusText, "Platform version: 3.2.0-canary.4") {
		t.Fatalf("status text omitted release identity: %s", statusText)
	}

	doctor, doctorCode, doctorOutput := inst.run("doctor", "--json")
	doctorInstallation, doctorOK := doctor["installation"].(map[string]any)
	if doctorCode != 0 || !doctorOK || doctor["operation"] != "doctor" || doctor["status"] != "unhealthy" || doctor["platformChannel"] != "canary" || doctor["platformVersion"] != "3.2.0-canary.4" || doctorInstallation["ok"] != false {
		t.Fatalf("doctor omitted successful unhealthy report or release identity: %v; output %s", doctor, doctorOutput)
	}
	_, doctorCode, doctorOutput = inst.run("doctor")
	if doctorCode != 0 || !strings.Contains(doctorOutput, "Platform diagnosis completed.") || !strings.Contains(doctorOutput, "unhealthy") || !strings.Contains(doctorOutput, "Platform check host-process") {
		t.Fatalf("human doctor output omitted report status or installation finding: %d %s", doctorCode, doctorOutput)
	}
}

func TestStartReportsPortInUse(t *testing.T) {
	inst := newInstallation(t)
	blocker, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", inst.port))
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Close()
	payload, code, raw := inst.run("start", "--json")
	if code != 1 || errorCode(payload) != "KB_INSTALL_PORT_IN_USE" {
		t.Fatalf("start: %d %s", code, raw)
	}
}

func TestStartReportsHostCrash(t *testing.T) {
	inst := newInstallation(t, "-exit", "5")
	payload, code, raw := inst.run("start", "--json")
	if code != 1 || errorCode(payload) != "KB_HOST_START_FAILED" {
		t.Fatalf("start: %d %s", code, raw)
	}
}

func TestStartPrintsHumanTextWithoutJSONFlag(t *testing.T) {
	inst := newInstallation(t)
	_, code, raw := inst.run("start")
	if code != 0 || !bytes.Contains([]byte(raw), []byte("host started")) || json.Valid([]byte(raw)) {
		t.Fatalf("start: %d %q", code, raw)
	}
	_, code, raw = inst.run("start")
	if code != 1 || !bytes.Contains([]byte(raw), []byte("KB_HOST_ALREADY_RUNNING")) || !bytes.Contains([]byte(raw), []byte("Hint:")) {
		t.Fatalf("second start: %d %q", code, raw)
	}
}

func TestForegroundStartRunsTheHostDirectly(t *testing.T) {
	inst := newInstallation(t)
	command := exec.Command(inst.launcher, "start", "--foreground", "--json", "--platform-root", inst.root)
	command.Env = append(os.Environ(), "KB_HOME="+inst.kbHome)
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	waited := make(chan error, 1)
	go func() { waited <- command.Wait() }()
	finished := false
	t.Cleanup(func() {
		if finished {
			return
		}
		_ = command.Process.Signal(syscall.SIGTERM)
		select {
		case <-waited:
		case <-time.After(15 * time.Second):
			_ = command.Process.Kill()
		}
	})
	deadline := time.Now().Add(15 * time.Second)
	for {
		if status, _, _ := inst.run("status", "--json"); status["host"] != nil && status["host"].(map[string]any)["state"] == "running" {
			break
		}
		if time.Now().After(deadline) {
			_ = command.Process.Kill()
			t.Fatal("foreground host never became healthy")
		}
		time.Sleep(50 * time.Millisecond)
	}
	rootState, err := host.StateForRoot(inst.root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(rootState.Control()); !os.IsNotExist(err) {
		t.Fatal("foreground mode has no control channel")
	}
	_ = command.Process.Signal(syscall.SIGTERM)
	select {
	case <-waited:
		finished = true
	case <-time.After(15 * time.Second):
		_ = command.Process.Kill()
		t.Fatal("launcher did not exit after SIGTERM")
	}
	if status, _, _ := inst.run("status", "--json"); status["host"].(map[string]any)["state"] != "stopped" {
		t.Fatalf("host must stop with the foreground launcher: %v", status)
	}
}
