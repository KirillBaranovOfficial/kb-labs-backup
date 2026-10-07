//go:build !windows

package host

import (
	"context"
	"errors"
	"fmt"
	"github.com/kb-labs/clikit/diag"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kb-labs/create/v2/contracts"
	"github.com/kb-labs/create/v2/receipt"
)

var fakeHostBinary string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "kb-create-fakehost-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fakeHostBinary = filepath.Join(dir, "fakehost")
	if output, err := exec.Command("go", "build", "-o", fakeHostBinary, "../internal/fakehost").CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build fakehost: %v\n%s", err, output)
		os.RemoveAll(dir)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// freePort returns a loopback port that was free a moment ago.
func freePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

type fixture struct {
	runner *ProcessRunner
	port   int
}

func newRunner(t *testing.T, ready, grace int, hostArgs ...string) fixture {
	t.Helper()
	port := freePort(t)
	spec, err := NewSpec(t.TempDir(), contracts.HostSpec{
		Command: fakeHostBinary, Args: append([]string{"-port", fmt.Sprint(port)}, hostArgs...),
		HealthURL: fmt.Sprintf("http://127.0.0.1:%d/health", port), ReadyTimeoutSeconds: ready, StopGraceSeconds: grace,
	})
	if err != nil {
		t.Fatal(err)
	}
	runner := &ProcessRunner{Spec: spec, State: State{Dir: filepath.Join(t.TempDir(), "state")}, PollInterval: 20 * time.Millisecond}
	t.Cleanup(func() { _, _ = runner.Stop(context.Background()) })
	return fixture{runner: runner, port: port}
}

func launcherCode(t *testing.T, err error) string {
	t.Helper()
	var typed *diag.Diag
	if !errors.As(err, &typed) {
		t.Fatalf("expected a Diag, got %v", err)
	}
	if typed.Hint == "" {
		t.Fatalf("diagnostic %s has no hint", typed.Code)
	}
	return typed.Code
}

func TestStartStatusStopGraceful(t *testing.T) {
	f := newRunner(t, 10, 5)
	ctx := context.Background()
	info, err := f.runner.Start(ctx)
	if err != nil || info.PID == 0 {
		t.Fatalf("start: %+v %v", info, err)
	}
	status, err := f.runner.Status(ctx)
	if err != nil || status.State != StateRunning || !status.Healthy || status.PID != info.PID {
		t.Fatalf("status: %+v %v", status, err)
	}
	owner := listenerOwner(fmt.Sprint(f.port))
	if owner.PID != fmt.Sprint(info.PID) {
		t.Fatalf("listener owner = %+v, launched pid = %d", owner, info.PID)
	}
	result, err := f.runner.Stop(ctx)
	if err != nil || !result.WasRunning || !result.Graceful || result.Forced {
		t.Fatalf("stop: %+v %v", result, err)
	}
	if processAlive(info.PID) {
		t.Fatal("host still alive after graceful stop")
	}
	if _, err := os.Stat(f.runner.State.HostPID()); !os.IsNotExist(err) {
		t.Fatalf("pid file should be gone: %v", err)
	}
	if status, _ := f.runner.Status(ctx); status.State != StateStopped {
		t.Fatalf("status after stop: %+v", status)
	}
}

func TestPidAndLogFilesArePrivate(t *testing.T) {
	f := newRunner(t, 10, 5)
	if _, err := f.runner.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{f.runner.State.HostPID(), f.runner.State.HostLog()} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("%s: mode %v err %v", path, info, err)
		}
	}
}

func TestStartWhileRunningIsAlreadyRunning(t *testing.T) {
	f := newRunner(t, 10, 5)
	if _, err := f.runner.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, err := f.runner.Start(context.Background())
	if code := launcherCode(t, err); code != "KB_HOST_ALREADY_RUNNING" {
		t.Fatalf("code %s", code)
	}
}

func TestRunnerDoesNotObserveOrStopAHostForAnotherRoot(t *testing.T) {
	f := newRunner(t, 10, 5)
	rootA, rootB := filepath.Join(t.TempDir(), "platform-a"), filepath.Join(t.TempDir(), "platform-b")
	f.runner.PlatformRoot = rootA
	info, err := f.runner.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	other := &ProcessRunner{Spec: f.runner.Spec, State: f.runner.State, PlatformRoot: rootB}
	status, err := other.Status(context.Background())
	if err != nil || status.State != StateStopped || status.PID != 0 {
		t.Fatalf("other-root status = %#v, %v; want stopped", status, err)
	}
	stopped, err := other.Stop(context.Background())
	if err != nil || stopped.WasRunning {
		t.Fatalf("other-root stop = %#v, %v; want no-op", stopped, err)
	}
	if !processAlive(info.PID) {
		t.Fatal("other-root stop signaled the original host")
	}
	_, err = other.Start(context.Background())
	var typed *diag.Diag
	if !errors.As(err, &typed) || typed.Code != "KB_HOST_ALREADY_RUNNING" ||
		typed.Meta["platformRoot"] != rootA || typed.Meta["pid"] != strconv.Itoa(info.PID) ||
		!strings.Contains(typed.Hint, "kb-create stop --platform-root "+rootA) {
		t.Fatalf("other-root start error = %#v; want typed root refusal", err)
	}
	status, err = f.runner.Status(context.Background())
	if err != nil || status.State != StateRunning || status.PID != info.PID {
		t.Fatalf("original root status after other-root operations = %#v, %v", status, err)
	}
	if _, err := f.runner.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestPortInUse(t *testing.T) {
	f := newRunner(t, 10, 5)
	f.runner.Operation = "apply"
	blocker, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", f.port))
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Close()
	plan := contracts.ResolvedInstallPlan{Host: &contracts.HostSpec{HealthURL: fmt.Sprintf("http://127.0.0.1:%d/health", f.port)}}
	if err := CheckPorts(plan, "apply"); err == nil {
		t.Fatal("expected early port conflict")
	} else {
		var typed *diag.Diag
		if !errors.As(err, &typed) || typed.Code != "KB_INSTALL_PORT_IN_USE" || typed.Meta["port"] != fmt.Sprint(f.port) || typed.Meta["pid"] == "" || typed.Meta["command"] == "" {
			t.Fatalf("port diagnostic lacks owner details: %#v", err)
		}
		if !strings.Contains(typed.Hint, "kb-create apply") ||
			!strings.Contains(typed.Hint, typed.Meta["pid"].(string)) || !strings.Contains(typed.Hint, typed.Meta["command"].(string)) {
			t.Fatalf("port hint omits the owning process or attempted command: %#v", err)
		}
	}
	_, err = f.runner.Start(context.Background())
	if code := launcherCode(t, err); code != "KB_INSTALL_PORT_IN_USE" {
		t.Fatalf("code %s", code)
	}
	if _, statErr := os.Stat(f.runner.State.HostPID()); !os.IsNotExist(statErr) {
		t.Fatal("no process should have been spawned")
	}
}

func TestStalePidFileIsRecovered(t *testing.T) {
	f := newRunner(t, 10, 5)
	dead := exec.Command(fakeHostBinary, "-exit", "0")
	if err := dead.Run(); err != nil {
		t.Fatal(err)
	}
	if err := f.runner.State.Ensure(); err != nil {
		t.Fatal(err)
	}
	if err := writeJSONFile(f.runner.State.HostPID(), pidRecord{PID: dead.Process.Pid, StartedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	status, err := f.runner.Status(context.Background())
	if err != nil || status.State != StateStopped || !status.StalePID {
		t.Fatalf("status with stale pid: %+v %v", status, err)
	}
	// Recreate the stale file: Start must clean it and succeed.
	_ = writeJSONFile(f.runner.State.HostPID(), pidRecord{PID: dead.Process.Pid, StartedAt: time.Now()})
	if _, err := f.runner.Start(context.Background()); err != nil {
		t.Fatalf("start over stale pid: %v", err)
	}
}

func TestCorruptPidFileIsStale(t *testing.T) {
	f := newRunner(t, 10, 5)
	_ = f.runner.State.Ensure()
	_ = os.WriteFile(f.runner.State.HostPID(), []byte("not json"), 0o600)
	if _, err := f.runner.Start(context.Background()); err != nil {
		t.Fatalf("start over corrupt pid: %v", err)
	}
}

func TestForcedStopWhenTermIgnored(t *testing.T) {
	f := newRunner(t, 10, 1, "-ignore-term")
	info, err := f.runner.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	result, err := f.runner.Stop(context.Background())
	if err != nil || !result.Forced || result.Graceful {
		t.Fatalf("stop: %+v %v", result, err)
	}
	if time.Since(started) < time.Second {
		t.Fatalf("kill must wait for the grace period, took %s", time.Since(started))
	}
	if processAlive(info.PID) {
		t.Fatal("host survived the kill")
	}
}

func TestStopWhenNotRunning(t *testing.T) {
	f := newRunner(t, 10, 5)
	result, err := f.runner.Stop(context.Background())
	if err != nil || result.WasRunning {
		t.Fatalf("stop idle host: %+v %v", result, err)
	}
}

func TestStartFailureWhenHostExits(t *testing.T) {
	f := newRunner(t, 10, 5, "-exit", "7")
	f.runner.Operation = "apply"
	f.runner.PlatformRoot = "/tmp/platform"
	_, err := f.runner.Start(context.Background())
	if code := launcherCode(t, err); code != "KB_HOST_START_FAILED" {
		t.Fatalf("code %s", code)
	}
	var typed *diag.Diag
	if !errors.As(err, &typed) || typed.Meta["exitCode"] != "7" ||
		!strings.Contains(fmt.Sprint(typed.Meta["stderrTail"]), "fakehost: exiting early") {
		t.Fatalf("process exit diagnostic = %#v; want exit code and stderr tail", err)
	}
	if typed.Message != "The host process failed while starting." || typed.Reason == "" || typed.Cause == "" ||
		typed.Meta["nextCommand"] != "kb-create apply" {
		t.Fatalf("apply startup diagnostic = %#v; want catalog message, concrete reason, and apply retry", typed)
	}
	if _, statErr := os.Stat(f.runner.State.HostPID()); !os.IsNotExist(statErr) {
		t.Fatal("pid file must be removed after a failed start")
	}
	data, _ := os.ReadFile(f.runner.State.HostLog())
	if len(data) == 0 {
		t.Fatal("host output should be captured in the log file")
	}
	launcherLog, err := os.ReadFile(f.runner.State.LauncherLog())
	if err != nil || !strings.Contains(string(launcherLog), "host.start.failed") || !strings.Contains(string(launcherLog), "exit code 7") {
		t.Fatalf("launcher failure log = %q, %v; want the process exit reason", launcherLog, err)
	}
}

func TestReadyTimeoutKillsHost(t *testing.T) {
	f := newRunner(t, 1, 5, "-unhealthy")
	_, err := f.runner.Start(context.Background())
	if code := launcherCode(t, err); code != "KB_HOST_START_FAILED" {
		t.Fatalf("code %s", code)
	}
	var typed *diag.Diag
	if !errors.As(err, &typed) || typed.Meta["readinessURL"] != f.runner.Spec.HealthURL ||
		typed.Meta["readinessStatus"] != "500" || typed.Meta["waited"] == nil ||
		!strings.Contains(fmt.Sprint(typed.Meta["stderrTail"]), "fakehost: ready") {
		t.Fatalf("readiness diagnostic = %#v; want URL, last status, elapsed wait, and stderr tail", err)
	}
	if _, dialErr := net.DialTimeout("tcp", f.runner.Spec.HealthAddress(), 200*time.Millisecond); dialErr == nil {
		t.Fatal("unhealthy host must be killed after the ready timeout")
	}
}

func TestTypedStartupEnvelopeIsTheHostFailureCause(t *testing.T) {
	f := newRunner(t, 10, 5, "-startup-error")
	f.runner.SecretEnv = func() (map[string]string, error) {
		return map[string]string{"API_TOKEN": "fake-secret-token"}, nil
	}
	_, err := f.runner.Start(context.Background())
	var typed *diag.Diag
	if !errors.As(err, &typed) || typed.Code != "KB_HOST_START_FAILED" ||
		typed.Meta["hostStartupCode"] != "KB_CONFIG_INVALID" ||
		!strings.Contains(typed.Cause, "KB_CONFIG_INVALID") ||
		!strings.Contains(fmt.Sprint(typed.Meta["stderrTail"]), `"code":"KB_CONFIG_INVALID"`) ||
		strings.Contains(fmt.Sprint(typed.Meta["stderrTail"]), "fake-secret-token") ||
		!strings.Contains(fmt.Sprint(typed.Meta["stderrTail"]), "[REDACTED]") ||
		typed.Message != "The host process failed while starting." {
		t.Fatalf("typed startup diagnostic = %#v; want catalogued host code in cause and stderr", err)
	}
}

func TestStderrTailIsLimitedToFortyLines(t *testing.T) {
	buffer := &tailBuffer{limit: 64 * 1024}
	for i := 0; i < 80; i++ {
		_, _ = fmt.Fprintf(buffer, "line-%02d\n", i)
	}
	tail := strings.Split(buffer.String(), "\n")
	if len(tail) != 40 || tail[0] != "line-40" || tail[len(tail)-1] != "line-79" {
		t.Fatalf("stderr tail = %d lines, first %q last %q; want last 40 lines", len(tail), tail[0], tail[len(tail)-1])
	}
}

func TestStatusReportsUnhealthy(t *testing.T) {
	f := newRunner(t, 10, 5)
	if _, err := f.runner.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Point the health probe at a dead path on the same server by stopping
	// the answer: use a spec whose URL returns 404.
	f.runner.Spec.HealthURL = fmt.Sprintf("http://127.0.0.1:%d/missing", f.port)
	status, _ := f.runner.Status(context.Background())
	if status.State != StateUnhealthy || status.Healthy {
		t.Fatalf("status: %+v", status)
	}
}

func TestStateDirUnwritable(t *testing.T) {
	f := newRunner(t, 10, 5)
	blocker := filepath.Join(t.TempDir(), "file")
	_ = os.WriteFile(blocker, nil, 0o600)
	f.runner.State = State{Dir: filepath.Join(blocker, "state")}
	_, err := f.runner.Start(context.Background())
	if code := launcherCode(t, err); code != "KB_HOST_STATE_DIR_UNWRITABLE" {
		t.Fatalf("code %s", code)
	}
}

func TestRunForegroundStopsHostOnCancel(t *testing.T) {
	f := newRunner(t, 10, 5)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- RunForeground(ctx, f.runner) }()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if status, _ := f.runner.Status(context.Background()); status.State == StateRunning {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("host never became healthy")
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if status, _ := f.runner.Status(context.Background()); status.State != StateStopped {
		t.Fatalf("host should be stopped: %+v", status)
	}
}

func TestSpecValidationAndResolution(t *testing.T) {
	root := "/platform"
	if _, err := NewSpec(root, contracts.HostSpec{Command: "host", HealthURL: "http://example.com:1/health"}); err == nil {
		t.Fatal("non-loopback health URL must be rejected")
	}
	if _, err := NewSpec(root, contracts.HostSpec{Command: "host", HealthURL: "http://127.0.0.1/health"}); err == nil {
		t.Fatal("health URL without a port must be rejected")
	}
	if _, err := NewSpec(root, contracts.HostSpec{HealthURL: "http://127.0.0.1:1/health"}); err == nil {
		t.Fatal("missing command must be rejected")
	}
	spec, err := NewSpec(root, contracts.HostSpec{Command: "bin/host", WorkingDir: "run", HealthURL: "http://127.0.0.1:9/health"})
	if err != nil || spec.Command != "/platform/bin/host" || spec.WorkingDir != "/platform/run" || spec.ReadyTimeout != defaultReadyTimeout || spec.StopGrace != defaultStopGrace {
		t.Fatalf("spec: %+v %v", spec, err)
	}
}

func writeReceipt(t *testing.T, root string, host *contracts.HostSpec) {
	t.Helper()
	plan := contracts.ResolvedInstallPlan{Schema: contracts.ResolvedPlanSchema, Host: host}
	if err := receipt.Write(root, contracts.InstallReceipt{ID: "r1", Plan: plan}); err != nil {
		t.Fatal(err)
	}
}

func TestNewRunnerRunsTheHostTheReceiptDeclares(t *testing.T) {
	root, state := t.TempDir(), State{Dir: filepath.Join(t.TempDir(), "launcher")}
	writeReceipt(t, root, &contracts.HostSpec{Command: "bin/host", HealthURL: "http://127.0.0.1:9/health"})
	runner, err := NewRunner(root, state)
	if err != nil {
		t.Fatal(err)
	}
	if runner.Spec.Command != filepath.Join(root, "bin/host") || runner.State != state || runner.SecretEnv == nil {
		t.Fatalf("runner: %#v", runner)
	}
}

func TestNewRunnerRejectsAnInstallationWithoutAValidHost(t *testing.T) {
	state := State{Dir: t.TempDir()}
	cases := map[string]*contracts.HostSpec{
		"no host (a receipt from before the host-only launcher)": nil,
		"non-loopback health URL":                                {Command: "host", HealthURL: "http://10.0.0.1:1/health"},
	}
	for name, spec := range cases {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			writeReceipt(t, root, spec)
			_, err := NewRunner(root, state)
			if code := launcherCode(t, err); code != "KB_HOST_SPEC_INVALID" {
				t.Fatalf("code %s", code)
			}
		})
	}
	if _, err := NewRunner(t.TempDir(), state); launcherCode(t, err) != "KB_INSTALL_RECEIPT_UNAVAILABLE" {
		t.Fatalf("a missing receipt must be reported as such: %v", err)
	}
}

func TestRootConflictNamesPidWhenRootIsUnknown(t *testing.T) {
	failure := RootConflict("", 4242)
	if failure.Code != "KB_HOST_ALREADY_RUNNING" {
		t.Fatalf("code = %q", failure.Code)
	}
	if !strings.Contains(failure.Hint, "Stop process 4242") || strings.Contains(failure.Hint, "--platform-root") {
		t.Fatalf("hint must name the pid, not an unmatchable stop command: %q", failure.Hint)
	}
	known := RootConflict("/opt/kb-a", 7)
	if !strings.Contains(known.Hint, "kb-create stop --platform-root /opt/kb-a") {
		t.Fatalf("hint = %q", known.Hint)
	}
}
