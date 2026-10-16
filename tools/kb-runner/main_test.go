//go:build darwin || linux

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/kb-labs/clikit/diag"
	"github.com/kb-labs/clikit/proc"
)

func TestWaitRendererCoversEveryRunStatus(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state with spaces")
	zero := 0
	statuses := []struct {
		status   proc.RunStatus
		terminal bool
	}{
		{proc.StatusRunning, false},
		{proc.StatusQueued, false},
		{proc.StatusBlocked, false},
		{proc.StatusStalled, false},
		{proc.StatusStarting, false},
		{proc.StatusDead, true},
		{proc.StatusLost, true},
		{proc.StatusDirty, true},
	}
	seen := make(map[proc.RunStatus]bool, len(statuses))
	var nonterminalOutcome string
	for _, entry := range statuses {
		status := entry.status
		if seen[status] {
			t.Fatalf("status %q was enumerated more than once", status)
		}
		seen[status] = true
		if got := status.Terminal(); got != entry.terminal {
			t.Errorf("%s.Terminal() = %t, want %t", status, got, entry.terminal)
		}
		run := proc.RunInfo{Status: status, Tag: "runner", DurationMS: 12, Error: "scratch path /tmp/run/scratch cleanup failed"}
		if status == proc.StatusDead {
			run.ExitCode = &zero
		}
		var output bytes.Buffer
		code, err := renderWaitOutcome(run, state, &output, "kb-runner")
		handled := true
		switch status {
		case proc.StatusRunning, proc.StatusQueued, proc.StatusBlocked, proc.StatusStalled, proc.StatusStarting:
			catalogEntry, ok := diag.LookupCatalog("KB_RUNTIME_WAIT_NOT_FINISHED")
			if !ok || code == 0 || err == nil {
				t.Errorf("nonterminal %s rendered code %d error %v catalog entry exists=%t", status, code, err, ok)
			} else {
				var diagnostic *diag.Diag
				if !errors.As(err, &diagnostic) || diagnostic.Code != catalogEntry.Code {
					t.Errorf("nonterminal %s outcome is not typed as %s: %v", status, catalogEntry.Code, err)
				}
				if nonterminalOutcome == "" {
					nonterminalOutcome = err.Error()
				} else if err.Error() != nonterminalOutcome {
					t.Errorf("nonterminal %s produced a different outcome: %q, want %q", status, err, nonterminalOutcome)
				}
				for _, want := range []string{
					catalogEntry.Code, catalogEntry.Message, catalogEntry.Hint,
					shellQuote("kb-runner") + " ps --state " + shellQuote(state),
				} {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("nonterminal %s outcome %q omitted %q", status, err, want)
					}
				}
			}
		case proc.StatusDead:
			if code != 0 || err != nil || !strings.Contains(output.String(), "run runner exited after 12ms") {
				t.Errorf("dead outcome = code %d error %v output %q", code, err, output.String())
			}
		case proc.StatusLost:
			if code == 0 || err == nil {
				t.Errorf("lost outcome = code %d error %v", code, err)
			}
		case proc.StatusDirty:
			entry, ok := diag.LookupCatalog("KB_RUNTIME_RUN_DIRTY")
			if !ok || code == 0 || err == nil {
				t.Fatalf("dirty outcome = code %d error %v, catalog entry exists=%t", code, err, ok)
			}
			for _, want := range []string{
				entry.Code, entry.Message, run.Error, entry.Hint,
				shellQuote("kb-runner") + " ps --state " + shellQuote(state),
				shellQuote("kb-runner") + " ps --leftovers --state " + shellQuote(state),
				shellQuote("kb-runner") + " gc --state " + shellQuote(state),
			} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("dirty outcome %q omitted %q", err, want)
				}
			}
		default:
			handled = false
		}
		if !handled {
			t.Errorf("test has no assertion for run status %q", status)
		}
	}
}

// case: S-237.48
func TestWaitReturnsDirtyDiagnosticAfterLostResourceCleanup(t *testing.T) {
	testWaitReturnsDirtyDiagnosticAfterLostCleanup(t, true)
}

// case: S-237.48
func TestWaitReturnsDirtyDiagnosticAfterLostScratchCleanup(t *testing.T) {
	testWaitReturnsDirtyDiagnosticAfterLostCleanup(t, false)
}

func testWaitReturnsDirtyDiagnosticAfterLostCleanup(t *testing.T, resource bool) {
	t.Helper()
	state := t.TempDir()
	runner := buildRunnerBinary(t)
	tag := "lost-scratch"
	path := filepath.Join(state, "runs", "owned-path")
	childPIDFile := filepath.Join(state, "child.pid")
	script := `printf '%s' "$$" > "$1"; exec /bin/sleep 30`
	if resource {
		tag = "lost-resource"
		script = `"$1" own path --create file "$2"; printf '%s' "$$" > "$3"; exec /bin/sleep 30`
	}
	commandArgs := []string{"run", "--tag", tag, "--state", state, "--stall", "1h", "--stdout", filepath.Join(state, "stdout"), "--stderr", filepath.Join(state, "stderr"), "--", "/bin/sh", "-c", script}
	if resource {
		commandArgs = append(commandArgs, "sh", runner, path, childPIDFile)
	} else {
		commandArgs = append(commandArgs, "sh", childPIDFile)
	}
	runCommand := exec.Command(runner, commandArgs...)
	var run proc.RunInfo
	var scratch string
	var childPID int
	var childIdentity string
	if err := runCommand.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		killOwnedProcess(childPID, childIdentity)
		for _, child := range run.Tree {
			killOwnedProcess(child.PID, child.Identity)
		}
		killOwnedProcess(run.PID, run.StartedAt)
		_ = runCommand.Process.Kill()
		_ = runCommand.Wait()
	})

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		listing := proc.List(state)
		if listing.Error != nil {
			t.Fatalf("list active test run: %v", listing.Error)
		}
		for _, candidate := range listing.Runs {
			if candidate.Tag == tag && candidate.Status == proc.StatusRunning {
				run = candidate
				if resource {
					if _, err := os.Stat(path); err == nil {
						break
					}
				} else {
					matches, err := filepath.Glob(filepath.Join(state, "runs", candidate.RunID, "scratch"))
					if err != nil {
						t.Fatal(err)
					}
					if len(matches) == 1 {
						scratch = matches[0]
						break
					}
				}
			}
		}
		if run.PID != 0 && fileExists(childPIDFile) && (resource && fileExists(path) || !resource && scratch != "") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if run.PID == 0 {
		t.Fatalf("run %q did not become visible", tag)
	}
	pidData, err := os.ReadFile(childPIDFile)
	if err != nil {
		t.Fatal(err)
	}
	childPID, err = strconv.Atoi(string(pidData))
	if err != nil {
		t.Fatal(err)
	}
	childIdentity, alive, err := proc.ProcessIdentity(childPID)
	if err != nil || !alive || childIdentity == "" {
		t.Fatalf("inspect test-owned child %d: identity=%q alive=%t error=%v", childPID, childIdentity, alive, err)
	}
	if resource {
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("replacement"), 0o600); err != nil {
			t.Fatal(err)
		}
	} else {
		if err := os.RemoveAll(scratch); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(t.TempDir(), scratch); err != nil {
			t.Fatal(err)
		}
	}
	killOwnedProcess(run.PID, run.StartedAt)
	terminalDeadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(terminalDeadline) {
		listing := proc.List(state)
		for _, candidate := range listing.Runs {
			if candidate.Tag == tag && candidate.Status.Terminal() {
				run = candidate
				break
			}
		}
		if run.Status.Terminal() {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !run.Status.Terminal() {
		t.Fatalf("keeper loss did not become terminal; status=%q", run.Status)
	}
	waitCtx, waitCancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	procRun, procWaitErr := proc.WaitTag(waitCtx, state, tag)
	waitCancel()
	if procWaitErr != nil || procRun.Status != proc.StatusDirty || !strings.Contains(procRun.Error, failedCleanupPath(resource, path, scratch)) {
		t.Fatalf("proc wait result = %+v, %v; want dirty cleanup outcome", procRun, procWaitErr)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	wait := exec.CommandContext(ctx, runner, "wait", "--tag", tag, "--state", state)
	waitOutput, waitErr := wait.CombinedOutput()
	var exitErr *exec.ExitError
	if !errors.As(waitErr, &exitErr) || exitErr.ExitCode() != 1 {
		listing := proc.List(state)
		t.Fatalf("dirty wait result = %v, output=%s; listing=%+v; want prompt exit 1", waitErr, waitOutput, listing)
	}
	failedPath := path
	if !resource {
		failedPath = scratch
	}
	for _, want := range []string{
		"KB_RUNTIME_RUN_DIRTY", failedPath,
		shellQuote(runner) + " ps --state " + shellQuote(state),
		shellQuote(runner) + " ps --leftovers --state " + shellQuote(state),
	} {
		if !strings.Contains(string(waitOutput), want) {
			t.Errorf("dirty wait output %q omitted %q", waitOutput, want)
		}
	}
}

func failedCleanupPath(resource bool, path, scratch string) string {
	if resource {
		return path
	}
	return scratch
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func killOwnedProcess(pid int, expectedIdentity string) {
	if pid <= 0 || expectedIdentity == "" {
		return
	}
	identity, alive, err := proc.ProcessIdentity(pid)
	if err == nil && alive && identity == expectedIdentity {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
}

func buildRunnerBinary(t *testing.T) string {
	t.Helper()
	runner := filepath.Join(t.TempDir(), "kb-runner")
	build := exec.Command("go", "build", "-o", runner, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build kb-runner: %v\n%s", err, output)
	}
	return runner
}

// case: S-237.27
func TestQuotaShowSetUsesStateDirectoryValues(t *testing.T) {
	state := t.TempDir()
	var output bytes.Buffer
	if code, err := execute([]string{"quota", "show", "--state", state}, &output, &output); code != 0 || err != nil {
		t.Fatalf("quota show default: code=%d err=%v output=%s", code, err, output.String())
	}
	if !strings.Contains(output.String(), "memory=") || !strings.Contains(output.String(), "(default)") {
		t.Fatalf("default quota output = %s", output.String())
	}
	output.Reset()
	if code, err := execute([]string{"quota", "set", "--state", state, "--memory", "12G", "--cpus", "4", "--min-free", "2G"}, &output, &output); code != 0 || err != nil {
		t.Fatalf("quota set: code=%d err=%v output=%s", code, err, output.String())
	}
	for _, want := range []string{"memory=12.0G (set)", "cpus=4 (set)", "min-free=2.0G (set)"} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("quota output missing %q: %s", want, output.String())
		}
	}
}

// case: S-237.34
func TestElasticCPUGrantReachesChildEnvironmentAndPS(t *testing.T) {
	state := t.TempDir()
	if err := proc.SetQuota(state, "8G", "2", ""); err != nil {
		t.Fatal(err)
	}
	runner := filepath.Join(state, "kb-runner")
	build := exec.Command("go", "build", "-o", runner, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build kb-runner: %v\n%s", err, output)
	}
	stdout := filepath.Join(state, "child.out")
	command := exec.Command(runner, "run", "--tag", "elastic", "--state", state, "--stall", "1h", "--stdout", stdout, "--stderr", filepath.Join(state, "child.err"), "--cost-key", "elastic", "--mem", "1M", "--cpus", "1..2", "--", "/bin/sh", "-c", "printf '%s %s' \"$KB_RUNNER_CPUS\" \"$KB_RUNNER_MEM_BYTES\"")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("run elastic command: %v\n%s", err, output)
	}
	data, err := os.ReadFile(stdout)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "2 1048576" {
		t.Fatalf("resource env = %q", data)
	}
	var ps bytes.Buffer
	if code, err := execute([]string{"ps", "--state", state}, &ps, &ps); code != 0 || err != nil {
		t.Fatalf("ps: code=%d err=%v output=%s", code, err, ps.String())
	}
	if !strings.Contains(ps.String(), "grant=2") || !strings.Contains(ps.String(), "mem=1.0M") {
		t.Fatalf("ps omitted resource grant: %s", ps.String())
	}
}

// case: S-237.12
func TestPSIncludesEachRunTreeAndElapsed(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("process supervision supports Linux and macOS")
	}
	state := t.TempDir()
	childPIDFile := filepath.Join(state, "child.pid")
	runner := filepath.Join(state, "kb-runner")
	build := exec.Command("go", "build", "-o", runner, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build kb-runner: %v\n%s", err, output)
	}
	run, err := proc.Start(context.Background(), proc.Config{
		HelperPath: runner, StateDir: state, Tag: "ps-check", Stall: time.Hour,
		StdoutPath: filepath.Join(state, "out"), StderrPath: filepath.Join(state, "err"),
		Command: []string{"/bin/sh", "-c", `echo $$ > "$1"; exec /bin/sleep 30`, "sh", childPIDFile},
	})
	if err != nil {
		t.Fatal(err)
	}
	childPID := 0
	childIdentity := ""
	finished := false
	stopOwnedChild := func() {
		if childPID == 0 || childIdentity == "" {
			return
		}
		identity, alive, err := proc.ProcessIdentity(childPID)
		if err != nil {
			t.Logf("cannot verify test-owned ps child %d during cleanup: %v", childPID, err)
			return
		}
		if !alive || identity != childIdentity {
			return
		}
		if err := syscall.Kill(childPID, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
			t.Logf("stop test-owned ps child %d: %v", childPID, err)
			return
		}
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			identity, alive, err = proc.ProcessIdentity(childPID)
			if err != nil {
				t.Logf("cannot verify test-owned ps child %d after cleanup: %v", childPID, err)
				return
			}
			if !alive || identity != childIdentity {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Logf("test-owned ps child %d remains after bounded cleanup", childPID)
	}
	t.Cleanup(func() {
		stopOwnedChild()
		if err := run.Kill(); err != nil {
			t.Logf("stop test-owned ps run %s: %v", run.RunID, err)
		}
		if err := run.Wait(); err != nil {
			t.Logf("wait for test-owned ps run %s: %v", run.RunID, err)
		}
		if !finished && !waitForTeardownEvent(state, 5*time.Second) {
			t.Log("runner teardown event was not recorded before temporary state cleanup")
		}
	})
	deadline := time.Now().Add(8 * time.Second)
	var listing []byte
	for time.Now().Before(deadline) {
		listing, _ = exec.Command(runner, "ps", "--state", state).CombinedOutput()
		if childPID == 0 {
			if data, err := os.ReadFile(childPIDFile); err == nil {
				if pid, parseErr := strconv.Atoi(strings.TrimSpace(string(data))); parseErr == nil {
					identity, alive, identityErr := proc.ProcessIdentity(pid)
					if identityErr == nil && alive {
						childPID, childIdentity = pid, identity
					}
				}
			}
		}
		// The command child is observed before it execs sleep; wait until the
		// tree row shows the exec'd command, not the transient shell.
		if strings.Contains(string(listing), "ps-check\t") && strings.Contains(string(listing), "RUNNING") &&
			strings.Contains(string(listing), "sleep") && childPID != 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	var runRow, childRow string
	for _, line := range strings.Split(string(listing), "\n") {
		if strings.HasPrefix(line, "ps-check\t") {
			runRow = line
		} else if strings.HasPrefix(line, "  └─ ") {
			childRow = line
		}
	}
	fields := strings.Split(runRow, "\t")
	if len(fields) < 7 || !strings.HasPrefix(fields[4], "elapsed=") || !strings.HasPrefix(fields[5], "tree=") {
		t.Fatalf("ps run row omitted its elapsed or tree fields: %s", listing)
	}
	treeCount, err := strconv.Atoi(strings.TrimPrefix(fields[5], "tree="))
	if err != nil || treeCount == 0 {
		t.Fatalf("ps run row omitted a nonempty tree inventory: %s", listing)
	}
	childCount := 0
	childHasSleep := false
	for _, line := range strings.Split(string(listing), "\n") {
		if strings.HasPrefix(line, "  └─ ") {
			childCount++
			childHasSleep = childHasSleep || strings.Contains(line, "sleep")
			if !strings.Contains(line, " elapsed=") {
				t.Fatalf("ps child row omitted elapsed: %s", line)
			}
		}
	}
	if childCount != treeCount || !childHasSleep || childRow == "" {
		t.Fatalf("ps child row omitted command or elapsed field: %s", listing)
	}
	stopOwnedChild()
	if err := run.Kill(); err != nil {
		t.Logf("stop test-owned ps run during fixture cleanup: %v", err)
	}
	if err := run.Wait(); err != nil {
		t.Logf("wait for test-owned ps run during fixture cleanup: %v", err)
	}
	finished = true
}

func TestGateStatusReportsFreeFromRegistry(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state")
	var output bytes.Buffer
	code, err := execute([]string{"gate", "status", "batch", "--state", state}, &output, &output)
	if err != nil || code != 0 || output.String() != "free\n" {
		t.Fatalf("gate status = code %d, err %v, output %q; want free", code, err, output.String())
	}
}

// case: S-237.20
func TestProcIdentityPrintsCurrentOSIdentity(t *testing.T) {
	want, alive, err := proc.ProcessIdentity(os.Getpid())
	if err != nil || !alive || want == "" {
		t.Fatalf("ProcessIdentity(current PID) = %q, alive=%t, err=%v", want, alive, err)
	}

	var stdout, stderr bytes.Buffer
	code, err := execute([]string{"proc", "identity", strconv.Itoa(os.Getpid())}, &stdout, &stderr)
	if err != nil || code != 0 {
		t.Fatalf("proc identity command = code %d, err %v, stderr %q", code, err, stderr.String())
	}
	if got := stdout.String(); got != want+"\n" {
		t.Fatalf("proc identity output = %q, want %q", got, want+"\n")
	}
}

func TestProcIdentityReturnsDistinctCodeForGonePID(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code, err := execute([]string{"proc", "identity", "2147483647"}, &stdout, &stderr)
	if code != 3 || err == nil || !strings.Contains(err.Error(), "PID 2147483647 is not live") || stdout.Len() != 0 {
		t.Fatalf("gone PID identity = code %d, err %v, stdout %q; want code 3 and a visible not-live reason", code, err, stdout.String())
	}
}

// case: S-237.21
func TestProcInventoryReturnsExactOSArgumentElements(t *testing.T) {
	if os.Getenv("KB_RUNNER_INVENTORY_FIXTURE") == "1" {
		time.Sleep(2 * time.Minute)
		return
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	odd := []string{"unbalanced'quote", "first-line\nsecond-line", "one argument with spaces"}
	args := []string{"-test.run=^TestProcInventoryReturnsExactOSArgumentElements$", "--"}
	args = append(args, odd...)
	child := exec.Command(executable, args...)
	child.Env = append(os.Environ(), "KB_RUNNER_INVENTORY_FIXTURE=1")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	childIdentity, alive, err := proc.ProcessIdentity(child.Process.Pid)
	if err != nil || !alive || childIdentity == "" {
		_ = child.Process.Kill()
		_ = child.Wait()
		t.Fatalf("identify argv fixture child: identity=%q alive=%t err=%v", childIdentity, alive, err)
	}
	t.Cleanup(func() {
		identity, childAlive, identityErr := proc.ProcessIdentity(child.Process.Pid)
		if identityErr != nil {
			t.Errorf("inspect owned argv fixture during cleanup: %v", identityErr)
		} else if childAlive && identity == childIdentity {
			if killErr := child.Process.Kill(); killErr != nil {
				t.Errorf("stop owned argv fixture: %v", killErr)
			}
		}
		if waitErr := child.Wait(); waitErr != nil {
			var exitErr *exec.ExitError
			if !errors.As(waitErr, &exitErr) {
				t.Errorf("wait owned argv fixture: %v", waitErr)
			}
		}
	})

	var stdout, stderr bytes.Buffer
	code, runErr := execute([]string{"proc", "inventory", "--json"}, &stdout, &stderr)
	if code != 0 || runErr != nil {
		t.Fatalf("proc inventory = code %d, err %v, stderr %q", code, runErr, stderr.String())
	}
	var entries []struct {
		PID              int      `json:"pid"`
		Identity         string   `json:"identity"`
		Argv             []string `json:"argv"`
		Unverified       bool     `json:"unverified"`
		SettleMS         int64    `json:"settle_ms"`
		Samples          int      `json:"samples"`
		ObservationError string   `json:"observation_error"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &entries); err != nil {
		t.Fatalf("decode process inventory: %v; output=%q", err, stdout.String())
	}
	for _, entry := range entries {
		if entry.PID == child.Process.Pid {
			want := append([]string{executable}, args...)
			if entry.Unverified || entry.ObservationError != "" || entry.Identity != childIdentity ||
				!equalStrings(entry.Argv, want) || entry.SettleMS != 100 || entry.Samples != 1 {
				t.Fatalf("child inventory = %+v, want exact argv %q and identity %q", entry, want, childIdentity)
			}
			return
		}
	}
	t.Fatalf("inventory omitted live child PID %d; rows=%s", child.Process.Pid, stdout.String())
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

// case: S-237.14
func TestWaitLatestTagUsesNewestRunRecord(t *testing.T) {
	dir := t.TempDir()
	runner := filepath.Join(dir, "kb-runner")
	build := exec.Command("go", "build", "-o", runner, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build kb-runner: %v\n%s", err, output)
	}
	state := filepath.Join(dir, "state")
	run := func(tag string, exitCode string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, runner, "run", "--tag", tag, "--latest-tag", "stream", "--state", state,
			"--stall", "1h", "--stdout", filepath.Join(dir, tag+".out"), "--stderr", filepath.Join(dir, tag+".err"),
			"--", "/bin/sh", "-c", "exit \"$1\"", "sh", exitCode)
		output, err := cmd.CombinedOutput()
		if exitCode == "0" && err != nil {
			t.Fatalf("run %s: %v\n%s", tag, err, output)
		}
		if exitCode != "0" {
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) || exitErr.ExitCode() != 7 {
				t.Fatalf("run %s result = %v\n%s, want exit 7", tag, err, output)
			}
		}
	}
	run("stream-first", "0")
	run("stream-second", "7")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	wait := exec.CommandContext(ctx, runner, "wait", "--latest-tag", "stream", "--state", state)
	output, err := wait.CombinedOutput()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 7 {
		t.Fatalf("wait latest result = %v\n%s, want exit 7", err, output)
	}
	if !strings.Contains(string(output), "run stream-second exited after ") {
		t.Fatalf("wait selected the wrong run record: %s", output)
	}
	if _, err := os.Stat(filepath.Join(state, "events.jsonl")); err != nil {
		t.Fatalf("runner event stream missing: %v", err)
	}
}

func waitForTeardownEvent(state string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		file, err := os.Open(filepath.Join(state, "events.jsonl"))
		if err == nil {
			var event proc.Event
			scanner := bufio.NewScanner(file)
			for scanner.Scan() {
				if json.Unmarshal(scanner.Bytes(), &event) == nil && event.Type == proc.EventTeardown {
					_ = file.Close()
					return true
				}
			}
			_ = file.Close()
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

// case: S-237.44
func TestOwnUnsupportedXattrFailureGivesFilesystemAndNextCommands(t *testing.T) {
	err := ownFailure(
		[]string{"path", "--create", "file", "/tmp/path with space"},
		"/tmp/state",
		proc.Resource{Kind: proc.ResourcePath, ID: "/tmp/path with space"},
		&proc.PathXattrUnsupportedError{Filesystem: "apfs", Path: "/tmp/path with space"},
	).Error()
	for _, expected := range []string{
		`filesystem "apfs"`,
		`df -P '/tmp/path with space'`,
		`'kb-runner' 'own' 'path' '--create' 'file' '/tmp/path with space'`,
	} {
		if !strings.Contains(err, expected) {
			t.Fatalf("own xattr error omitted %q: %s", expected, err)
		}
	}
}

// case: S-237.50
func TestOwnOutsideRunIsActionablyRefused(t *testing.T) {
	t.Setenv("KBLABS_CLIPROC_RUN", "")
	t.Setenv("KBLABS_CLIPROC_STATE", "")
	var output bytes.Buffer
	code, err := execute([]string{"own", "path", "--create", "dir", "/tmp/unowned"}, &output, &output)
	if code == 0 || err == nil || !strings.Contains(err.Error(), "no active run marker") || !strings.Contains(err.Error(), "inside kb-runner run") {
		t.Fatalf("own outside run result = code %d, err %v, output %q", code, err, output.String())
	}
}
