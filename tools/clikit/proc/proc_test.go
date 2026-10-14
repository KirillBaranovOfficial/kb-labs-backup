//go:build darwin || linux

package proc_test

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

	"github.com/kb-labs/clikit/proc"
)

// case: S-237.1
func TestNormalExitRemovesDescendantsAndWritesExitEvent(t *testing.T) {
	state := t.TempDir()
	helper := buildHelper(t)
	runner := buildRunner(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	readyFile := filepath.Join(state, "descendant.pid")
	cmd := exec.CommandContext(ctx, runner, "run", "--tag", "normal", "--state", state, "--stall", "1h", "--stdout", filepath.Join(state, "stdout.log"), "--stderr", filepath.Join(state, "stderr.log"), "--", helper, "detached", "30", readyFile)
	output, err := cmd.CombinedOutput()
	if err != nil {
		forceKillRecordedTree(t, state)
		stderr, _ := os.ReadFile(filepath.Join(state, "stderr.log"))
		events, _ := os.ReadFile(filepath.Join(state, "events.jsonl"))
		t.Fatalf("kb-runner normal exit failed: %v\n%s\nstderr: %s\nevents: %s", err, output, stderr, events)
	}
	if !strings.Contains(string(output), "run normal completed") {
		t.Fatalf("kb-runner did not report completion: %s", output)
	}
	pidBytes, err := os.ReadFile(readyFile)
	if err != nil {
		t.Fatalf("setsid descendant did not report its PID: %v; output: %s", err, output)
	}
	descendant, err := strconv.Atoi(strings.TrimSpace(string(pidBytes)))
	if err != nil {
		t.Fatalf("invalid descendant PID %q: %v", pidBytes, err)
	}
	identity, found := proc.ProcessIdentity(descendant)
	if found {
		t.Cleanup(func() {
			if current, ok := proc.ProcessIdentity(descendant); ok && current == identity {
				if err := syscall.Kill(descendant, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
					t.Logf("cleanup detached test process %d: %v", descendant, err)
				}
			}
		})
	}
	if processExists(descendant) {
		t.Fatalf("descendant %d remains alive after normal command exit", descendant)
	}
	listing := proc.List(state)
	if listing.Error != nil || len(listing.Runs) != 1 || listing.Runs[0].Status != proc.StatusDead || len(listing.Runs[0].Tree) != 0 {
		t.Fatalf("tree remains after normal exit: %+v", listing)
	}
	assertEvent(t, state, "exit")
	assertEvent(t, state, "reap")
}

// case: S-237.1
func TestKilledSupervisorReapsSetsidDescendant(t *testing.T) {
	state := t.TempDir()
	helper := buildHelper(t)
	runner := buildRunner(t)
	cmd := exec.Command(runner, "run", "--state", state, "--tag", "sigkill", "--stall", "1h", "--stdout", filepath.Join(state, "out"), "--stderr", filepath.Join(state, "err"), "--", "/bin/sh", "-c", helper+" double-fork 300 & /bin/sleep 300 & wait")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	var descendant int
	var owned []proc.Process
	t.Cleanup(func() {
		if listing := proc.List(state); listing.Error == nil {
			for _, run := range listing.Runs {
				owned = append(owned, run.Tree...)
			}
		}
		if !waited {
			if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
				t.Logf("cleanup test supervisor: %v", err)
			}
		}
		for _, process := range owned {
			if identity, ok := proc.ProcessIdentity(process.PID); ok && identity == process.Identity {
				if err := syscall.Kill(process.PID, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
					t.Logf("cleanup test process %d: %v", process.PID, err)
				}
			}
		}
		forceKillRecordedTree(t, state)
		if !waited {
			if err := cmd.Wait(); err != nil {
				t.Logf("wait for killed test supervisor: %v", err)
			}
		}
	})
	scanner := bufio.NewScanner(stdout)
	line := make(chan string, 1)
	go func() {
		if scanner.Scan() {
			line <- scanner.Text()
			return
		}
		line <- ""
	}()
	select {
	case ready := <-line:
		if !strings.HasPrefix(ready, "descendant-ready ") {
			t.Fatalf("setsid descendant did not become ready: %q; stderr: %s", ready, stderr.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("setsid descendant readiness timed out")
	}
	listing := proc.List(state)
	if listing.Error != nil || len(listing.Runs) != 1 || listing.Runs[0].Status != proc.StatusLive {
		t.Fatalf("run did not become live before SIGKILL: %+v", listing)
	}
	for _, process := range listing.Runs[0].Tree {
		if strings.Contains(process.Command, filepath.Base(helper)) {
			descendant = process.PID
			break
		}
	}
	if descendant == 0 {
		t.Fatalf("setsid descendant absent from live tree: %+v", listing.Runs[0].Tree)
	}
	owned = append(owned, listing.Runs[0].Tree...)
	var found bool
	_, found = proc.ProcessIdentity(descendant)
	if !found {
		t.Fatalf("setsid descendant %d disappeared before supervisor kill", descendant)
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if !waitUntil(5*time.Second, func() bool {
		listing := proc.List(state)
		if listing.Error != nil || len(listing.Runs) != 1 || listing.Runs[0].Status != proc.StatusDead || countEvents(t, state, "reap") != 1 {
			return false
		}
		for _, process := range owned {
			if identity, ok := proc.ProcessIdentity(process.PID); ok && identity == process.Identity {
				return false
			}
		}
		return !processExists(descendant)
	}) {
		events, _ := os.ReadFile(filepath.Join(state, "events.jsonl"))
		t.Fatalf("supervised tree did not end after supervisor SIGKILL; events: %s; stderr: %s", events, stderr.String())
	}
	if err := cmd.Wait(); err != nil {
		t.Logf("wait for SIGKILLed test supervisor: %v", err)
	}
	waited = true
}

// case: S-237.2
func TestSilentLeafEmitsOneStallEvent(t *testing.T) {
	state := t.TempDir()
	runner := buildRunner(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, runner, "run", "--state", state, "--tag", "stall", "--stall", "250ms", "--stdout", filepath.Join(state, "stdout.log"), "--stderr", filepath.Join(state, "stderr.log"), "--", "/bin/sleep", "3")
	output, err := cmd.CombinedOutput()
	if err != nil {
		forceKillRecordedTree(t, state)
		t.Fatalf("kb-runner silent run failed: %v\n%s", err, output)
	}
	if got := countEvents(t, state, "stall"); got != 1 {
		t.Fatalf("stall event count = %d, want 1", got)
	}
	if !strings.Contains(eventCommand(t, state, "stall"), "sleep") {
		t.Fatalf("stall event does not identify the leaf command: %q", eventCommand(t, state, "stall"))
	}
	if leaves := eventLeaves(t, state, "stall"); len(leaves) != 1 || leaves[0].ElapsedMS <= 0 {
		t.Fatalf("stall event leaves = %+v, want one leaf with elapsed time", leaves)
	}
}

// case: S-237.3
func TestListShowsLiveTreeAndDetectsDeadKeeper(t *testing.T) {
	state := t.TempDir()
	helper := buildHelper(t)
	runner := buildRunner(t)
	run, err := proc.Start(context.Background(), proc.Config{
		HelperPath: helper, StateDir: state, Tag: "list", Stall: time.Hour,
		StdoutPath: filepath.Join(state, "stdout.log"), StderrPath: filepath.Join(state, "stderr.log"),
		Command: []string{"/bin/sleep", "30"},
	})
	if err != nil {
		t.Fatal(err)
	}
	cleanupRun(t, state, run)
	output, err := exec.Command(runner, "ps", "--state", state).CombinedOutput()
	if err != nil {
		t.Fatalf("kb-runner ps failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "LIVE") || !strings.Contains(string(output), "  └─") || !strings.Contains(string(output), "sleep") {
		t.Fatalf("separate kb-runner ps did not show the live tree: %s", output)
	}
	if err := run.Kill(); err != nil {
		t.Fatal(err)
	}
	if !waitUntil(3*time.Second, func() bool {
		listing, err := exec.Command(runner, "ps", "--state", state).CombinedOutput()
		return err == nil && strings.Contains(string(listing), "DEAD")
	}) {
		listing, _ := exec.Command(runner, "ps", "--state", state).CombinedOutput()
		t.Fatalf("dead keeper was reported as live: %s", listing)
	}
	if err := run.Wait(); err != nil {
		t.Logf("wait for stopped test run: %v", err)
	}
	assertSignalExitEvent(t, state)
}

// case: S-237.4
func TestEventsAreStructuredJSONL(t *testing.T) {
	state := t.TempDir()
	helper := buildHelper(t)
	runner := buildRunner(t)
	run, err := proc.Start(context.Background(), proc.Config{
		HelperPath: helper, StateDir: state, Tag: "events", Stall: time.Hour,
		StdoutPath: filepath.Join(state, "stdout.log"), StderrPath: filepath.Join(state, "stderr.log"),
		Command: []string{"/usr/bin/true"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := run.Wait(); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(filepath.Join(state, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := f.Close(); err != nil {
			t.Logf("close test event stream: %v", err)
		}
	}()
	counts := make(map[string]int)
	exitHasCode := false
	s := bufio.NewScanner(f)
	for s.Scan() {
		var event proc.Event
		if err := json.Unmarshal(s.Bytes(), &event); err != nil {
			t.Fatalf("invalid JSONL event: %v", err)
		}
		if event.Schema != proc.EventSchema || event.RunID == "" || event.At.IsZero() || event.Type == "" {
			t.Fatalf("incomplete structured event: %+v", event)
		}
		if event.Type == "exit" && event.ExitCode != nil && *event.ExitCode == 0 {
			exitHasCode = true
		}
		counts[event.Type]++
	}
	if err := s.Err(); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"start", "exit", "reap"} {
		if counts[kind] != 1 {
			t.Fatalf("%s event count = %d, want one", kind, counts[kind])
		}
	}
	if !exitHasCode {
		t.Fatal("normal exit event has no exit code")
	}

	followState := t.TempDir()
	follow := exec.Command(runner, "events", "--state", followState, "--follow")
	followStdout, err := follow.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var followStderr bytes.Buffer
	follow.Stderr = &followStderr
	if err := follow.Start(); err != nil {
		t.Fatal(err)
	}
	followWaited := false
	t.Cleanup(func() {
		if !followWaited {
			if err := follow.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
				t.Logf("stop events --follow test process: %v", err)
			}
			if err := follow.Wait(); err != nil {
				t.Logf("wait for events --follow test process: %v", err)
			}
		}
	})
	followRun, err := proc.Start(context.Background(), proc.Config{
		HelperPath: helper, StateDir: followState, Tag: "follow", Stall: time.Hour,
		StdoutPath: filepath.Join(followState, "stdout.log"), StderrPath: filepath.Join(followState, "stderr.log"),
		Command: []string{"/usr/bin/true"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := followRun.Wait(); err != nil {
		t.Fatal(err)
	}
	lines := make(chan []string, 1)
	go func() {
		scanner := bufio.NewScanner(followStdout)
		var records []string
		for len(records) < 3 && scanner.Scan() {
			records = append(records, scanner.Text())
		}
		lines <- records
	}()
	select {
	case records := <-lines:
		if len(records) != 3 {
			t.Fatalf("events --follow returned %d lifecycle records: %v; stderr: %s", len(records), records, followStderr.String())
		}
		for _, record := range records {
			var event proc.Event
			if err := json.Unmarshal([]byte(record), &event); err != nil || event.Schema != proc.EventSchema {
				t.Fatalf("events --follow emitted an invalid event %q: %v", record, err)
			}
		}
	case <-time.After(5 * time.Second):
		t.Fatal("events --follow did not stream appended lifecycle events")
	}
	if err := follow.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if err := follow.Wait(); err != nil {
		t.Fatalf("stop events --follow: %v; stderr: %s", err, followStderr.String())
	}
	followWaited = true
}

func buildHelper(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "proc-helper")
	cmd := exec.Command("go", "build", "-o", path, "./testhelper")
	cmd.Dir = packageDir(t)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build process helper: %v\n%s", err, out)
	}
	return path
}

func buildRunner(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "kb-runner")
	cmd := exec.Command("go", "build", "-o", path, ".")
	cmd.Dir = filepath.Join(packageDir(t), "..", "..", "kb-runner")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build kb-runner: %v\n%s", err, out)
	}
	return path
}

func packageDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test package")
	}
	return filepath.Dir(file)
}

func assertEvent(t *testing.T, state, kind string) {
	t.Helper()
	if countEvents(t, state, kind) < 1 {
		t.Fatalf("missing %q event", kind)
	}
}

func assertSignalExitEvent(t *testing.T, state string) {
	t.Helper()
	f, err := os.Open(filepath.Join(state, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := f.Close(); err != nil {
			t.Logf("close test event stream: %v", err)
		}
	}()
	s := bufio.NewScanner(f)
	for s.Scan() {
		var event proc.Event
		if json.Unmarshal(s.Bytes(), &event) == nil && event.Type == "exit" && event.Signal != "" {
			return
		}
	}
	t.Fatal("missing signal exit event")
}

func cleanupRun(t *testing.T, state string, run *proc.Run) {
	t.Helper()
	t.Cleanup(func() {
		if err := run.Kill(); err != nil {
			t.Logf("stop test run: %v", err)
		}
		if err := run.Wait(); err != nil {
			t.Logf("wait for stopped test run: %v", err)
		}
		forceKillRecordedTree(t, state)
	})
}

func forceKillRecordedTree(t *testing.T, state string) {
	t.Helper()
	listing := proc.List(state)
	if listing.Error != nil {
		return
	}
	for _, run := range listing.Runs {
		for _, child := range run.Tree {
			if identity, ok := proc.ProcessIdentity(child.PID); ok && identity == child.Identity {
				if err := syscall.Kill(child.PID, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
					t.Logf("cleanup test process %d: %v", child.PID, err)
				}
			}
		}
		if identity, ok := proc.ProcessIdentity(run.PID); ok && identity == run.StartedAt {
			if err := syscall.Kill(run.PID, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
				t.Logf("cleanup test keeper %d: %v", run.PID, err)
			}
		}
	}
}

func countEvents(t *testing.T, state, kind string) int {
	t.Helper()
	f, err := os.Open(filepath.Join(state, "events.jsonl"))
	if err != nil {
		return 0
	}
	defer func() {
		if err := f.Close(); err != nil {
			t.Logf("close test event stream: %v", err)
		}
	}()
	n := 0
	s := bufio.NewScanner(f)
	for s.Scan() {
		var event proc.Event
		if json.Unmarshal(s.Bytes(), &event) == nil && event.Type == kind {
			n++
		}
	}
	return n
}

func eventCommand(t *testing.T, state, kind string) string {
	t.Helper()
	f, err := os.Open(filepath.Join(state, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := f.Close(); err != nil {
			t.Logf("close test event stream: %v", err)
		}
	}()
	s := bufio.NewScanner(f)
	for s.Scan() {
		var event proc.Event
		if json.Unmarshal(s.Bytes(), &event) == nil && event.Type == kind {
			if kind == "stall" && len(event.Leaves) > 0 {
				return event.Leaves[0].Command
			}
			return event.Command
		}
	}
	return ""
}

func eventLeaves(t *testing.T, state, kind string) []proc.Process {
	t.Helper()
	f, err := os.Open(filepath.Join(state, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := f.Close(); err != nil {
			t.Logf("close test event stream: %v", err)
		}
	}()
	s := bufio.NewScanner(f)
	for s.Scan() {
		var event proc.Event
		if json.Unmarshal(s.Bytes(), &event) == nil && event.Type == kind {
			return event.Leaves
		}
	}
	return nil
}

func waitUntil(d time.Duration, condition func() bool) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if condition() {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return condition()
}

func processExists(pid int) bool { return proc.ProcessExists(pid) }
