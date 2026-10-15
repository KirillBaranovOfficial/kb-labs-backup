//go:build darwin || linux

package main

import (
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

	"github.com/kb-labs/clikit/proc"
)

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
