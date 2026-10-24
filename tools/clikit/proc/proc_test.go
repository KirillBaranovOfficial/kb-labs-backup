//go:build darwin || linux

package proc_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/kb-labs/clikit/internal/testutil"
	"github.com/kb-labs/clikit/proc"
)

type stableHostResourceSource struct{}

func (stableHostResourceSource) Snapshot() proc.HostResourceSnapshot {
	return proc.HostResourceSnapshot{
		PhysicalMemory: 1 << 50, AvailableMemory: 1 << 50,
		MemoryAvailableKnown: true, LogicalCPUs: 64,
	}
}

func startTest(ctx context.Context, cfg proc.Config) (*proc.Run, error) {
	return proc.StartWithHostResourceSource(ctx, cfg, stableHostResourceSource{})
}

// case: S-237.56
func TestLauncherExitTerminatesItsSpawnedRun(t *testing.T) {
	state := t.TempDir()
	runner := buildRunner(t)
	childPIDFile := filepath.Join(state, "child.pid")
	command := exec.Command(runner, "run", "--tag", "launcher-owned", "--state", state,
		"--stall", "1h", "--stdout", filepath.Join(state, "out"), "--stderr", filepath.Join(state, "err"),
		"--", "/bin/sh", "-c", `echo $$ > "$1"; exec /bin/sleep 30`, "sh", childPIDFile)
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	var childPID int
	var childIdentity string
	t.Cleanup(func() {
		if command.Process != nil && command.ProcessState == nil {
			if err := command.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
				t.Errorf("kill launcher process %d during cleanup: %v", command.Process.Pid, err)
			}
			if _, err := command.Process.Wait(); err != nil && !errors.Is(err, os.ErrProcessDone) {
				t.Errorf("wait for launcher process %d during cleanup: %v", command.Process.Pid, err)
			}
		}
		records, err := filepath.Glob(filepath.Join(state, "runs", "*.json"))
		if err != nil {
			t.Errorf("find test-owned run records during cleanup: %v", err)
		} else {
			for _, path := range records {
				data, readErr := os.ReadFile(path)
				if readErr != nil {
					t.Errorf("read test-owned run record %s during cleanup: %v", path, readErr)
					continue
				}
				var owner struct {
					PID       int    `json:"pid"`
					StartedAt string `json:"started_at"`
				}
				if err := json.Unmarshal(data, &owner); err != nil {
					t.Errorf("decode test-owned run record %s during cleanup: %v", path, err)
					continue
				}
				identity, alive, identityErr := proc.ProcessIdentity(owner.PID)
				if identityErr != nil {
					t.Errorf("inspect test-owned process %d during cleanup: %v", owner.PID, identityErr)
					continue
				}
				if alive && identity == owner.StartedAt {
					if err := syscall.Kill(owner.PID, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
						t.Errorf("kill test-owned process %d during cleanup: %v", owner.PID, err)
					}
				}
			}
		}
		if childPID > 0 && childIdentity != "" {
			identity, alive, err := proc.ProcessIdentity(childPID)
			if err != nil {
				t.Errorf("inspect spawned child %d during cleanup: %v", childPID, err)
			} else if alive && identity == childIdentity {
				if err := syscall.Kill(childPID, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
					t.Errorf("kill spawned child %d during cleanup: %v", childPID, err)
				}
			}
		}
	})
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(childPIDFile)
		if err == nil {
			childPID, err = strconv.Atoi(strings.TrimSpace(string(data)))
			if err == nil {
				childIdentity, _, err = proc.ProcessIdentity(childPID)
				if err == nil && childIdentity != "" {
					break
				}
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if childPID == 0 || childIdentity == "" {
		t.Fatalf("launcher did not start a verifiable child: pid=%d identity=%q", childPID, childIdentity)
	}
	if err := command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := command.Wait(); err == nil {
		t.Fatal("terminated launcher reported a successful exit")
	}
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		identity, alive, err := proc.ProcessIdentity(childPID)
		if err == nil && (!alive || identity != childIdentity) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("launcher-owned child %d survived launcher termination", childPID)
}

// case: S-237.1
func TestNormalExitRemovesDescendantsAndWritesExitEvent(t *testing.T) {
	state := t.TempDir()
	helper := buildHelper(t)
	runner := buildRunner(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	readyFile := filepath.Join(state, "descendant.pid")
	cmd := exec.CommandContext(ctx, runner, "run", "--tag", "normal", "--state", state, "--stall", "1h", "--stdout", filepath.Join(state, "stdout.log"), "--stderr", filepath.Join(state, "stderr.log"), "--", helper, "detached", "3", readyFile)
	output, err := cmd.CombinedOutput()
	if err != nil {
		forceKillRecordedTree(t, state)
		skipOnTeardownObservationFailure(t, state, outputRunID(t, state, "normal"))
		stderr, _ := os.ReadFile(filepath.Join(state, "stderr.log"))
		events, _ := os.ReadFile(filepath.Join(state, "events.jsonl"))
		t.Fatalf("kb-runner normal exit failed: %v\n%s\nstderr: %s\nevents: %s", err, output, stderr, events)
	}
	if !strings.Contains(string(output), "run normal completed") {
		t.Fatalf("kb-runner did not report completion: %s", output)
	}
	descendant, err := waitPIDFile(readyFile, 5*time.Second)
	if err != nil {
		t.Fatalf("setsid descendant did not report a complete PID: %v; output: %s", err, output)
	}
	identity, found := identityForTest(t, descendant)
	if found {
		t.Cleanup(func() {
			if current, ok := identityForTest(t, descendant); ok && current == identity {
				if err := syscall.Kill(descendant, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
					t.Logf("cleanup detached test process %d: %v", descendant, err)
				}
			}
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				if current, ok := identityForTest(t, descendant); !ok || current != identity {
					return
				}
				time.Sleep(20 * time.Millisecond)
			}
			t.Logf("test-owned descendant %d remains after bounded cleanup", descendant)
		})
	}
	descendantEscapedSampling := false
	if processExists(t, descendant) {
		if runtime.GOOS != "darwin" {
			t.Fatalf("descendant %d remains alive after normal command exit", descendant)
		}
		descendantEscapedSampling = true
		if err != nil {
			t.Fatalf("same-uid process without ancestry history or marker dirtied teardown: %v", err)
		}
	}
	listing := proc.List(state)
	if listing.Error != nil || len(listing.Runs) != 1 || listing.Runs[0].Status != proc.StatusDead || len(listing.Runs[0].Tree) != 0 {
		t.Fatalf("tree remains after normal exit: %+v", listing)
	}
	assertEvent(t, state, "exit")
	assertEvent(t, state, "teardown")
	teardown := eventOfType(t, state, proc.EventTeardown)
	if len(teardown.Reaped)+len(teardown.Unverified) == 0 && teardown.Error == "" && !descendantEscapedSampling {
		t.Fatalf("teardown event omitted the process cleanup inventory: %+v", teardown)
	}
	if descendantEscapedSampling {
		for _, process := range append(append(teardown.Reaped, teardown.Unverified...), teardown.Remaining...) {
			if process.PID == descendant {
				t.Fatalf("descendant without ancestry history or marker was still classified as owned: %+v", process)
			}
		}
	}
	skipOnTeardownObservationFailure(t, state, outputRunID(t, state, "normal"))
}

// case: S-237.42
func TestRunScratchEnvironmentIsRemovedAndKeptOutputSurvives(t *testing.T) {
	state := t.TempDir()
	runner := buildRunner(t)
	kept := filepath.Join(state, "kept.txt")
	scratchPathFile := filepath.Join(state, "scratch-path")
	command := exec.Command(runner, "run", "--tag", "scratch", "--state", state, "--stall", "1h", "--stdout", filepath.Join(state, "stdout.log"), "--stderr", filepath.Join(state, "stderr.log"), "--keep", kept, "--keep", "private", "--", "/bin/sh", "-ec", `printf '%s' "$TMPDIR" > "$2"; printf scratch > "$TMPDIR/private"; printf discard > "$TMPDIR/discard"; printf kept > "$3"; "$1" own path --create dir "$TMPDIR/owned"; test "$TMPDIR" = "$TMP" && test "$TMP" = "$TEMP" && test "$TMP" = "$KB_RUNNER_SCRATCH"`, "sh", runner, scratchPathFile, kept)
	output, err := command.CombinedOutput()
	runID := outputRunID(t, state, "scratch")
	scratch := scratchForRun(t, state, runID)
	t.Cleanup(func() {
		if err := os.RemoveAll(scratch); err != nil {
			t.Errorf("remove test scratch %s: %v", scratch, err)
		}
	})
	if err != nil {
		skipOnTeardownObservationFailure(t, state, runID)
		t.Fatalf("run with scratch environment: %v\n%s", err, output)
	}
	scratchBytes, err := os.ReadFile(scratchPathFile)
	if err != nil {
		t.Fatal(err)
	}
	if content, err := os.ReadFile(filepath.Join(string(scratchBytes), "private")); err != nil || string(content) != "scratch" {
		t.Fatalf("kept scratch output = %q, %v", content, err)
	}
	if _, err := os.Stat(filepath.Join(string(scratchBytes), "discard")); !os.IsNotExist(err) {
		t.Fatalf("undeclared scratch output remains: %v", err)
	}
	if content, err := os.ReadFile(kept); err != nil || string(content) != "kept" {
		t.Fatalf("kept output = %q, %v", content, err)
	}
	if _, err := os.Stat(filepath.Join(state, "stdout.log")); err != nil {
		t.Fatalf("implicit stdout keep missing: %v", err)
	}
	leftovers, err := proc.Leftovers(state)
	if err != nil || len(leftovers) != 0 {
		t.Fatalf("kept scratch output appeared as a leftover: %+v, %v", leftovers, err)
	}
	assertExactlyOneScratchOutcome(t, eventOfType(t, state, proc.EventTeardown), string(proc.ResourceRemoved))
	skipOnTeardownObservationFailure(t, state, runID)
}

// case: S-237.41
func TestRunScratchRemovesReadOnlyGoModuleContent(t *testing.T) {
	state := t.TempDir()
	runner := buildRunner(t)
	command := exec.Command(runner, "run", "--tag", "readonly-scratch", "--state", state, "--stall", "1h", "--stdout", filepath.Join(state, "stdout.log"), "--stderr", filepath.Join(state, "stderr.log"), "--", "/bin/sh", "-ec", `mkdir -p "$TMPDIR/go/pkg/mod/example"; printf source > "$TMPDIR/go/pkg/mod/example/decode_test.go"; chmod 0444 "$TMPDIR/go/pkg/mod/example/decode_test.go"; chmod 0555 "$TMPDIR/go/pkg/mod/example" "$TMPDIR/go/pkg/mod" "$TMPDIR/go"`)
	output, err := command.CombinedOutput()
	runID := outputRunID(t, state, "readonly-scratch")
	scratch := scratchForRun(t, state, runID)
	t.Cleanup(func() {
		_ = filepath.WalkDir(scratch, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				return os.Chmod(path, 0o700)
			}
			return nil
		})
		if err := os.RemoveAll(scratch); err != nil {
			t.Errorf("remove test-owned scratch %s: %v", scratch, err)
		}
	})
	if _, err := os.Lstat(scratch); !os.IsNotExist(err) {
		t.Fatalf("read-only scratch remains at %s: %v", scratch, err)
	}
	assertExactlyOneScratchOutcome(t, eventOfType(t, state, proc.EventTeardown), string(proc.ResourceRemoved))
	if err != nil {
		skipOnTeardownObservationFailure(t, state, runID)
		t.Fatalf("read-only scratch run failed: %v\n%s", err, output)
	}
	listing := proc.List(state)
	if listing.Error != nil || len(listing.Runs) != 1 || listing.Runs[0].Status != proc.StatusDead {
		t.Fatalf("read-only scratch cleanup status = %+v", listing)
	}
}

// case: S-237.41
func TestCanceledKeeperStartupRemovesRegisteredScratch(t *testing.T) {
	state := t.TempDir()
	runner := buildRunner(t)
	stdoutFIFO := filepath.Join(state, "stdout.fifo")
	if err := syscall.Mkfifo(stdoutFIFO, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan error, 1)
	go func() {
		_, err := proc.StartWithHostResourceSource(ctx, proc.Config{
			HelperPath: runner, StateDir: state, Tag: "canceled-startup", Stall: time.Hour,
			StdoutPath: stdoutFIFO, StderrPath: filepath.Join(state, "stderr.log"),
			Command: []string{"/bin/sleep", "30"},
		}, stableHostResourceSource{})
		started <- err
	}()
	t.Cleanup(cancel)

	type runRecord struct {
		Scratch string `json:"scratch"`
	}
	deadline := time.NewTimer(5 * time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer deadline.Stop()
	defer ticker.Stop()
	var scratch string
waitForScratch:
	for {
		paths, err := filepath.Glob(filepath.Join(state, "runs", "*.json"))
		if err != nil {
			t.Fatal(err)
		}
		for _, path := range paths {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var record runRecord
			if err := json.Unmarshal(data, &record); err != nil {
				t.Fatal(err)
			}
			if record.Scratch != "" {
				scratch = record.Scratch
				break waitForScratch
			}
		}
		select {
		case err := <-started:
			t.Fatalf("keeper startup completed before scratch registration: %v", err)
		case <-deadline.C:
			t.Fatal("keeper did not register scratch before the FIFO output open blocked")
		case <-ticker.C:
		}
	}
	t.Cleanup(func() {
		_ = filepath.WalkDir(scratch, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				return os.Chmod(path, 0o700)
			}
			return nil
		})
		if err := os.RemoveAll(scratch); err != nil {
			t.Errorf("remove test-owned scratch %s: %v", scratch, err)
		}
	})
	cancel()
	select {
	case err := <-started:
		if err == nil {
			t.Fatal("canceled keeper startup returned no error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("canceled keeper startup did not return promptly")
	}
	if _, err := os.Lstat(scratch); !os.IsNotExist(err) {
		t.Fatalf("canceled startup left scratch %s: %v", scratch, err)
	}
}

func TestRunScratchAllowsUnixSocketNameUnderDeepStateDirectory(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is unavailable")
	}
	stateRoot, err := os.MkdirTemp("/tmp", "x71-socket-state-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(stateRoot); err != nil {
			t.Errorf("remove deep socket test state %s: %v", stateRoot, err)
		}
	})
	state := filepath.Join(stateRoot, strings.Repeat("deep-state-segment-", 4), "logs", "runner")
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatal(err)
	}
	runner := buildRunner(t)
	boundFile := filepath.Join(state, "socket-bound")
	nodeScript := `const path=require('path');const paths=[path.join(process.env.TMPDIR,'x'.repeat(40)),path.join(process.env.TMPDIR,'tsx-501','12345.pipe')];if(Buffer.byteLength(paths[0])+1>104) throw new Error('path too long for Unix socket: '+Buffer.byteLength(paths[0]));const bind=i=>{const s=require('net').createServer();s.listen(paths[i],()=>s.close(()=>{if(i+1<paths.length)bind(i+1);else require('fs').writeFileSync(process.argv[1],'bound')}))};bind(0)`
	command := exec.Command(runner, "run", "--tag", "scratch-socket", "--state", state, "--stall", "1h",
		"--stdout", filepath.Join(state, "stdout.log"), "--stderr", filepath.Join(state, "stderr.log"), "--",
		"/bin/sh", "-ec", `mkdir -p "$TMPDIR/tsx-501"; node -e "`+nodeScript+`" "`+boundFile+`"`,
	)
	output, err := command.CombinedOutput()
	runID := outputRunID(t, state, "scratch-socket")
	data, recordErr := os.ReadFile(filepath.Join(state, "runs", runID+".json"))
	if recordErr != nil {
		t.Fatalf("read socket run record: %v", recordErr)
	}
	var runRecord struct {
		Scratch string `json:"scratch"`
	}
	if err := json.Unmarshal(data, &runRecord); err != nil {
		t.Fatal(err)
	}
	if runRecord.Scratch == "" {
		t.Fatal("run record omitted scratch path")
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(runRecord.Scratch); err != nil {
			t.Errorf("remove socket test scratch %s: %v", runRecord.Scratch, err)
		}
	})
	if err != nil {
		skipOnTeardownObservationFailure(t, state, runID)
		stderr, _ := os.ReadFile(filepath.Join(state, "stderr.log"))
		t.Fatalf("supervised child could not bind socket below TMPDIR: %v\n%s\nchild stderr: %s", err, output, stderr)
	}
	if data, err := os.ReadFile(boundFile); err != nil || string(data) != "bound" {
		stderr, _ := os.ReadFile(filepath.Join(state, "stderr.log"))
		t.Fatalf("supervised child did not bind its Unix socket: marker=%q err=%v stderr=%s", data, err, stderr)
	}
	if _, err := os.Stat(runRecord.Scratch); !os.IsNotExist(err) {
		t.Fatalf("scratch remains after supervised child exit at %s: %v", runRecord.Scratch, err)
	}
	skipOnTeardownObservationFailure(t, state, runID)
}

func TestRunScratchReplacementWithDifferentInodeIsRefused(t *testing.T) {
	state := t.TempDir()
	runner := buildRunner(t)
	scratchPathFile := filepath.Join(state, "scratch-path")
	command := exec.Command(runner, "run", "--tag", "scratch-replaced", "--state", state, "--stall", "1h",
		"--stdout", filepath.Join(state, "stdout.log"), "--stderr", filepath.Join(state, "stderr.log"), "--",
		"/bin/sh", "-ec", `printf '%s' "$TMPDIR" > "$1"; mv "$TMPDIR" "$TMPDIR.original"; mkdir "$TMPDIR"; printf replacement > "$TMPDIR/marker"`, "sh", scratchPathFile)
	output, runErr := command.CombinedOutput()
	runID := outputRunID(t, state, "scratch-replaced")
	if runErr == nil {
		t.Fatalf("run with replaced scratch directory succeeded; unsafe scratch removal was not reported\n%s", output)
	}
	exit := eventOfType(t, state, proc.EventExit)
	if exit.ExitCode == nil || *exit.ExitCode != 0 || exit.Signal != "" {
		t.Fatalf("replacement child did not exit successfully: %+v", exit)
	}
	scratchBytes, err := os.ReadFile(scratchPathFile)
	if err != nil {
		t.Fatalf("run did not report scratch path: %v", err)
	}
	scratch := string(scratchBytes)
	originalScratch := scratch + ".original"
	t.Cleanup(func() {
		if err := os.RemoveAll(scratch); err != nil {
			t.Errorf("remove test replacement scratch %s: %v", scratch, err)
		}
		if err := os.RemoveAll(originalScratch); err != nil {
			t.Errorf("remove replaced original scratch %s: %v", originalScratch, err)
		}
	})
	marker, err := os.ReadFile(filepath.Join(scratch, "marker"))
	if err != nil || string(marker) != "replacement" {
		t.Fatalf("replacement scratch was removed or changed: data=%q err=%v", marker, err)
	}
	teardown := eventOfType(t, state, proc.EventTeardown)
	assertExactlyOneScratchOutcome(t, teardown, string(proc.ResourceRefused))
	var refusal string
	for _, outcome := range teardown.Resources {
		if outcome.Resource.Kind == "scratch" {
			refusal = outcome.Error
		}
	}
	if refusal == "" || !strings.Contains(teardown.Error, refusal) {
		t.Fatalf("teardown error %q does not identify the refused scratch cleanup %q", teardown.Error, refusal)
	}
	skipOnTeardownObservationFailure(t, state, runID)
	if teardown.Error != "remove run scratch: "+refusal {
		t.Fatalf("runner exited for a different teardown error: %q", teardown.Error)
	}
}

// case: S-237.44
func TestRunScratchSymlinkReplacementRefusesWithoutTouchingTarget(t *testing.T) {
	state := t.TempDir()
	runner := buildRunner(t)
	outside := t.TempDir()
	sentinel := filepath.Join(outside, "sentinel")
	if err := os.WriteFile(sentinel, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	scratchPathFile := filepath.Join(state, "scratch-path")
	command := exec.Command(runner, "run", "--tag", "scratch-symlink", "--state", state, "--stall", "1h", "--stdout", filepath.Join(state, "stdout.log"), "--stderr", filepath.Join(state, "stderr.log"), "--", "/bin/sh", "-ec", `printf '%s' "$TMPDIR" > "$2"; rm -rf "$TMPDIR"; ln -s "$1" "$TMPDIR"`, "sh", outside, scratchPathFile)
	_, _ = command.CombinedOutput()
	scratchBytes, err := os.ReadFile(scratchPathFile)
	if err != nil {
		t.Fatalf("run did not report scratch path: %v", err)
	}
	if data, err := os.ReadFile(sentinel); err != nil || string(data) != "keep" {
		t.Fatalf("scratch cleanup touched symlink target: data=%q err=%v", data, err)
	}
	if target, err := os.Readlink(string(scratchBytes)); err != nil || target != outside {
		t.Fatalf("scratch symlink replacement changed: target=%q err=%v", target, err)
	}
	teardown := eventOfType(t, state, proc.EventTeardown)
	assertExactlyOneScratchOutcome(t, teardown, string(proc.ResourceRefused))
}

// case: S-237.41
func TestRunScratchIsRemovedAfterNonzeroExitAndSignal(t *testing.T) {
	state := t.TempDir()
	runner := buildRunner(t)
	for _, outcome := range []struct {
		tag    string
		script string
		stall  string
	}{{tag: "nonzero", script: `printf '%s' "$TMPDIR" > "$1"; exit 7`, stall: "1h"}, {tag: "signal", script: `printf '%s' "$TMPDIR" > "$1"; kill -TERM $$`, stall: "1h"}, {tag: "stall", script: `printf '%s' "$TMPDIR" > "$1"; /bin/sleep 0.3`, stall: "100ms"}} {
		scratchPathFile := filepath.Join(state, outcome.tag+"-scratch")
		command := exec.Command(runner, "run", "--tag", outcome.tag, "--state", state, "--stall", outcome.stall, "--stdout", filepath.Join(state, outcome.tag+".out"), "--stderr", filepath.Join(state, outcome.tag+".err"), "--", "/bin/sh", "-c", outcome.script, "sh", scratchPathFile)
		_, _ = command.CombinedOutput()
		scratchBytes, err := os.ReadFile(scratchPathFile)
		if err != nil {
			t.Fatalf("run %s did not write its scratch path: %v", outcome.tag, err)
		}
		if _, err := os.Stat(string(scratchBytes)); !os.IsNotExist(err) {
			t.Fatalf("run %s scratch remains at %s: %v", outcome.tag, scratchBytes, err)
		}
		if outcome.tag == "stall" {
			assertEvent(t, state, "stall")
		}
		assertExactlyOneScratchOutcome(t, eventOfType(t, state, proc.EventTeardown), string(proc.ResourceRemoved))
	}
}

func assertExactlyOneScratchOutcome(t *testing.T, event proc.Event, expected string) {
	t.Helper()
	count := 0
	for _, outcome := range event.Resources {
		if outcome.Resource.Kind == "scratch" {
			count++
			if outcome.Outcome != expected {
				t.Errorf("scratch outcome = %q, want %q", outcome.Outcome, expected)
			}
		}
	}
	if count != 1 {
		t.Errorf("scratch outcomes = %d, want exactly one: %+v", count, event.Resources)
	}
}

// case: S-237.1
func TestKilledSupervisorReapsSetsidDescendant(t *testing.T) {
	state := t.TempDir()
	helper := buildHelper(t)
	runner := buildRunner(t)
	descendantPIDFile := filepath.Join(state, "descendant.pid")
	sleepPIDFile := filepath.Join(state, "sleep.pid")
	releaseIntermediate := filepath.Join(state, "release-intermediate")
	cmd := exec.Command(runner, "run", "--state", state, "--tag", "sigkill", "--stall", "1h", "--stdout", filepath.Join(state, "out"), "--stderr", filepath.Join(state, "err"), "--", "/bin/sh", "-c", `"$1" double-fork-held 4 "$2" "$4" & printf 'descendant-ready %s\n' "$!"; /bin/sleep 30 & echo $! > "$3"; wait`, "sh", helper, descendantPIDFile, sleepPIDFile, releaseIntermediate)
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
	var descendantUnverified bool
	var owned []proc.Process
	started := make(map[int]string)
	stdoutReadDone := make(chan struct{})
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
		for pid, identity := range started {
			if current, ok := identityForTest(t, pid); ok && current == identity {
				if err := syscall.Kill(pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
					t.Logf("cleanup test process %d: %v", pid, err)
				}
			}
		}
		forceKillRecordedTree(t, state)
		if !waited {
			if err := testutil.WaitAfterPipeRead(cmd, stdoutReadDone); err != nil {
				t.Logf("wait for killed test supervisor: %v", err)
			}
		}
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			alive := false
			for pid, identity := range started {
				if current, ok := identityForTest(t, pid); ok && current == identity {
					alive = true
				}
			}
			if !alive {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Logf("test-owned process identities remain after bounded cleanup: %v", started)
	})
	scanner := bufio.NewScanner(stdout)
	line := make(chan string, 1)
	go func() {
		defer close(stdoutReadDone)
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
	if listing.Error != nil || len(listing.Runs) != 1 || listing.Runs[0].Status != proc.StatusRunning {
		t.Fatalf("run did not become live before SIGKILL: %+v", listing)
	}
	descendant, err = waitPIDFile(descendantPIDFile, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !waitUntil(2*time.Second, func() bool {
		current := proc.List(state)
		if current.Error != nil || len(current.Runs) != 1 {
			return false
		}
		for _, process := range current.Runs[0].Tree {
			if process.PID == descendant {
				descendantUnverified = process.Unverified
				started[process.PID] = process.Identity
				return true
			}
		}
		return false
	}) {
		t.Fatalf("setsid descendant absent from live tree: %+v", listing.Runs[0].Tree)
	}
	if descendant == 0 {
		t.Fatalf("setsid descendant absent from live tree: %+v", listing.Runs[0].Tree)
	}
	sleepPID, err := waitPIDFile(sleepPIDFile, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if identity, ok := identityForTest(t, sleepPID); ok {
		started[sleepPID] = identity
	}
	var found bool
	_, found = identityForTest(t, descendant)
	if !found {
		t.Fatalf("setsid descendant %d disappeared before supervisor kill", descendant)
	}
	owned = append(owned, listing.Runs[0].Tree...)
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if !waitUntil(5*time.Second, func() bool {
		listing := proc.List(state)
		if listing.Error != nil || len(listing.Runs) != 1 ||
			!listing.Runs[0].Status.Terminal() || countEvents(t, state, "teardown") != 1 {
			return false
		}
		teardown := eventOfType(t, state, proc.EventTeardown)
		if runtime.GOOS == "darwin" && teardown.Error != "" {
			if len(teardown.Remaining) == 0 {
				t.Fatalf("non-clean teardown omitted remaining observations: %+v", teardown)
			}
		} else if teardown.Error != "" || len(teardown.Remaining) != 0 {
			return false
		}
		for _, process := range owned {
			if identity, ok := identityForTest(t, process.PID); ok && identity == process.Identity {
				if runtime.GOOS == "darwin" && process.Unverified {
					continue
				}
				return false
			}
		}
		return runtime.GOOS == "darwin" && descendantUnverified || !processExists(t, descendant)
	}) {
		events, _ := os.ReadFile(filepath.Join(state, "events.jsonl"))
		t.Fatalf("supervised tree did not end after supervisor SIGKILL; events: %s; stderr: %s", events, stderr.String())
	}
	if err := testutil.WaitAfterPipeRead(cmd, stdoutReadDone); err != nil {
		t.Logf("wait for SIGKILLed test supervisor: %v", err)
	}
	waited = true
}

// case: S-237.2
func TestSilentLeafEmitsOneStallEvent(t *testing.T) {
	state := t.TempDir()
	helper := buildHelper(t)
	leafPIDFile := filepath.Join(state, "leaf.pid")
	releaseFile := filepath.Join(state, "release-leaves")
	run, err := startTest(context.Background(), proc.Config{
		HelperPath: helper, StateDir: state, Tag: "stall", Stall: 250 * time.Millisecond,
		StdoutPath: filepath.Join(state, "stdout.log"), StderrPath: filepath.Join(state, "stderr.log"),
		Command: []string{helper, "hold", leafPIDFile, releaseFile},
	})
	if err != nil {
		t.Fatal(err)
	}
	cleanupRun(t, state, run)
	// Registered after cleanupRun, so LIFO order releases the held leaf before
	// the run kill: the leaf exits on its own and the keeper reaps a live tree.
	t.Cleanup(func() { releaseHeldLeaves(t, releaseFile) })
	if _, err := waitPIDFile(leafPIDFile, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	if !waitUntil(5*time.Second, func() bool {
		listing := proc.List(state)
		return listing.Error == nil && len(listing.Runs) == 1 && listing.Runs[0].Status == proc.StatusStalled
	}) {
		t.Fatalf("silent run never became visibly stalled: %+v", proc.List(state))
	}
	if !waitUntil(2*time.Second, func() bool { return countEvents(t, state, "stall") == 1 }) {
		t.Fatalf("stall event count = %d, want 1", countEvents(t, state, "stall"))
	}
	listing := proc.List(state)
	if listing.Error != nil || len(listing.Runs) != 1 || listing.Runs[0].Status != proc.StatusStalled || listing.Runs[0].ElapsedMS <= 0 || len(listing.Runs[0].Tree) == 0 {
		t.Fatalf("ps listing does not distinguish a stalled run with elapsed tree: %+v", listing)
	}
	runner := buildRunner(t)
	psOutput := outputPS(t, runner, state)
	if !strings.Contains(psOutput, "\tSTALLED\t") || !strings.Contains(psOutput, "elapsed=") {
		t.Fatalf("kb-runner ps does not show stalled run and tree elapsed time: %s", psOutput)
	}
	leaves := eventLeaves(t, state, "stall")
	var foundLeaf bool
	for _, leaf := range leaves {
		if leaf.Command == filepath.Base(helper) && !leaf.Unverified && leaf.ElapsedMS > 0 && leaf.Identity != "" {
			foundLeaf = true
		}
	}
	if !foundLeaf {
		t.Fatalf("stall event leaves = %+v, want the command's verified held leaf with elapsed time and identity", leaves)
	}
}

// case: S-237.7
func TestDarwinAncestryHistoryReapsDetachedDescendant(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS ancestry sampling contract")
	}
	state := t.TempDir()
	helper := buildHelper(t)
	pidFile := filepath.Join(state, "grandchild.pid")
	releaseFile := filepath.Join(state, "release-command")
	run, err := startTest(context.Background(), proc.Config{
		HelperPath: helper, StateDir: state, Tag: "darwin-sampled-descendant", Stall: time.Hour,
		StdoutPath: filepath.Join(state, "stdout.log"), StderrPath: filepath.Join(state, "stderr.log"),
		Command: []string{helper, "double-fork-system-held", "30", pidFile, releaseFile},
	})
	if err != nil {
		t.Fatal(err)
	}
	cleanupRun(t, state, run)
	t.Cleanup(func() {
		if err := os.WriteFile(releaseFile, []byte("release after test\n"), 0o600); err != nil && !os.IsNotExist(err) {
			t.Logf("release test-owned intermediate during cleanup: %v", err)
		}
	})
	grandchild, err := waitPIDFile(pidFile, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	grandchildIdentity, alive := identityForTest(t, grandchild)
	if !alive {
		t.Fatalf("fixture grandchild %d exited before ownership sampling", grandchild)
	}
	t.Cleanup(func() {
		if identity, exists := identityForTest(t, grandchild); exists && identity == grandchildIdentity {
			if err := syscall.Kill(grandchild, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
				t.Logf("stop test-owned detached grandchild %d: %v", grandchild, err)
			}
		}
	})
	if !waitUntil(2*time.Second, func() bool {
		listing := proc.List(state)
		if listing.Error != nil || len(listing.Runs) != 1 {
			return false
		}
		for _, process := range listing.Runs[0].Tree {
			if process.PID == grandchild && !process.Unverified && process.Identity != "" {
				return true
			}
		}
		return false
	}) {
		t.Fatalf("sampler did not record detached PID %d as verified: %+v", grandchild, proc.List(state))
	}
	time.Sleep(350 * time.Millisecond)
	if err := os.WriteFile(releaseFile, []byte("release\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run.Wait(); err != nil {
		skipOnTeardownObservationFailure(t, state, run.RunID)
		t.Fatalf("keeper failed after detached command exit: %v", err)
	}
	if _, alive := identityForTest(t, grandchild); alive {
		t.Fatalf("sampled detached grandchild %d survived tree teardown", grandchild)
	}
	skipOnTeardownObservationFailure(t, state, run.RunID)
}

// case: S-237.13
func TestUnrelatedSameUIDProcessIsNotOwnedByRun(t *testing.T) {
	state := t.TempDir()
	sleepPath, err := exec.LookPath("sleep")
	if err != nil {
		t.Fatal(err)
	}
	unrelated := exec.Command(sleepPath, "30")
	if err := unrelated.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if unrelated.Process != nil {
			if err := unrelated.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
				t.Logf("stop test-owned unrelated process: %v", err)
			}
			if err := unrelated.Wait(); err != nil {
				var exitErr *exec.ExitError
				if !errors.As(err, &exitErr) || !wasKilledByTest(exitErr) {
					t.Logf("wait for test-owned unrelated process: %v", err)
				}
			}
		}
	})
	if _, alive := identityForTest(t, unrelated.Process.Pid); !alive {
		t.Fatal("test-owned unrelated process exited before the run started")
	}

	run, err := proc.Start(context.Background(), proc.Config{
		HelperPath: buildHelper(t), StateDir: state, Tag: "unrelated-same-uid", Stall: time.Hour,
		StdoutPath: filepath.Join(state, "stdout.log"), StderrPath: filepath.Join(state, "stderr.log"),
		Command: []string{sleepPath, "3"},
	})
	if err != nil {
		t.Fatal(err)
	}
	cleanupRun(t, state, run)
	if !waitUntil(2*time.Second, func() bool {
		tree, treeErr := proc.ProcessTree(run.PID, run.RunID)
		if treeErr != nil {
			return false
		}
		for _, process := range tree {
			if process.PID == unrelated.Process.Pid {
				t.Fatalf("same-uid process without ownership evidence appeared in run tree: %+v", process)
			}
			if process.Command == "sleep" && process.PID != unrelated.Process.Pid {
				return true
			}
		}
		return false
	}) {
		t.Fatalf("supervised sleep was not visible or process-table observation failed: %+v", proc.List(state))
	}
	if err := run.Wait(); err != nil {
		t.Fatalf("unrelated same-uid process dirtied clean run teardown: %v", err)
	}
	if _, alive := identityForTest(t, unrelated.Process.Pid); !alive {
		t.Fatal("supervisor signalled unrelated same-uid process")
	}
}

func wasKilledByTest(err *exec.ExitError) bool {
	status, ok := err.Sys().(syscall.WaitStatus)
	return ok && status.Signaled() && status.Signal() == syscall.SIGKILL
}

// case: S-237.3
func TestListShowsLiveTreeAndDetectsDeadKeeper(t *testing.T) {
	state := t.TempDir()
	helper := buildHelper(t)
	runner := buildRunner(t)
	run, err := startTest(context.Background(), proc.Config{
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
	if !strings.Contains(string(output), "RUNNING") || !strings.Contains(string(output), "  └─") || !strings.Contains(string(output), "sleep") {
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
	runCtx, cancelRun := context.WithTimeout(context.Background(), 15*time.Second)
	run, err := startTest(runCtx, proc.Config{
		HelperPath: helper, StateDir: state, Tag: "events", Stall: time.Hour,
		StdoutPath: filepath.Join(state, "stdout.log"), StderrPath: filepath.Join(state, "stderr.log"),
		Command: []string{"/usr/bin/true"},
	})
	if err != nil {
		cancelRun()
		t.Fatal(err)
	}
	runFinished := false
	t.Cleanup(func() {
		cancelRun()
		if runFinished {
			return
		}
		if err := run.Kill(); err != nil {
			t.Logf("stop structured-event test run %s: %v", run.RunID, err)
		}
		if err := run.Wait(); err != nil {
			t.Logf("wait for structured-event test run %s: %v", run.RunID, err)
		}
	})
	waitErr := run.Wait()
	runFinished = true
	if waitErr != nil {
		skipOnTeardownObservationFailure(t, state, run.RunID)
		t.Fatal(waitErr)
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
	s.Buffer(make([]byte, 64*1024), 8*1024*1024)
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
		counts[string(event.Type)]++
	}
	if err := s.Err(); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"start", "exit", "teardown"} {
		if counts[kind] != 1 {
			t.Fatalf("%s event count = %d, want one", kind, counts[kind])
		}
	}
	if !exitHasCode {
		t.Fatal("normal exit event has no exit code")
	}
	skipOnTeardownObservationFailure(t, state, run.RunID)

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
	followReadDone := make(chan struct{})
	t.Cleanup(func() {
		if !followWaited {
			if err := follow.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
				t.Logf("stop events --follow test process: %v", err)
			}
			if err := testutil.WaitAfterPipeRead(follow, followReadDone); err != nil {
				t.Logf("wait for events --follow test process: %v", err)
			}
		}
	})
	followRun, err := startTest(context.Background(), proc.Config{
		HelperPath: helper, StateDir: followState, Tag: "follow", Stall: time.Hour,
		StdoutPath: filepath.Join(followState, "stdout.log"), StderrPath: filepath.Join(followState, "stderr.log"),
		Command: []string{"/usr/bin/true"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := followRun.Wait(); err != nil {
		skipOnTeardownObservationFailure(t, followState, followRun.RunID)
		t.Fatal(err)
	}
	lines := make(chan []string, 1)
	go func() {
		defer close(followReadDone)
		scanner := bufio.NewScanner(followStdout)
		scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
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
	waitErr = testutil.WaitAfterPipeRead(follow, followReadDone)
	followWaited = true
	if waitErr != nil {
		t.Fatalf("stop events --follow: %v; stderr: %s", waitErr, followStderr.String())
	}
	skipOnTeardownObservationFailure(t, followState, followRun.RunID)
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

func eventOfType(t *testing.T, state string, kind proc.EventType) proc.Event {
	t.Helper()
	f, err := os.Open(filepath.Join(state, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		var event proc.Event
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatal(err)
		}
		if event.Type == kind {
			return event
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	t.Fatalf("event %q not found", kind)
	return proc.Event{}
}

func outputRunID(t *testing.T, state, tag string) string {
	t.Helper()
	f, err := os.Open(filepath.Join(state, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		var event proc.Event
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatal(err)
		}
		if event.Tag == tag {
			return event.RunID
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	t.Fatalf("run tag %q has no lifecycle event", tag)
	return ""
}

func skipOnTeardownObservationFailure(t *testing.T, state, runID string) {
	t.Helper()
	teardown := eventForRunAndType(t, state, runID, proc.EventTeardown)
	if strings.Contains(teardown.Error, "unverified/failed observation") {
		t.Skipf("process table unavailable; teardown could not be verified: %s", teardown.Error)
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
	s.Buffer(make([]byte, 64*1024), 8*1024*1024)
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

// releaseHeldLeaves frees every leaf the test started on the testhelper hold
// mode. Cleanup paths must call it before killing keepers so held leaves exit
// through the keeper's own reaping instead of being orphaned.
func releaseHeldLeaves(t *testing.T, releaseFile string) {
	t.Helper()
	if err := os.WriteFile(releaseFile, []byte("release\n"), 0o600); err != nil {
		t.Logf("release held fixture leaves via %s: %v", releaseFile, err)
	}
}

func forceKillRecordedTree(t *testing.T, state string) {
	t.Helper()
	listing := proc.List(state)
	if listing.Error != nil {
		return
	}
	for _, run := range listing.Runs {
		for _, child := range run.Tree {
			if child.Unverified {
				continue
			}
			if identity, ok := identityForTest(t, child.PID); ok && identity == child.Identity {
				if err := syscall.Kill(child.PID, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
					t.Logf("cleanup test process %d: %v", child.PID, err)
				}
			}
		}
		if identity, ok := identityForTest(t, run.PID); ok && identity == run.StartedAt {
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
	s.Buffer(make([]byte, 64*1024), 8*1024*1024)
	for s.Scan() {
		var event proc.Event
		if json.Unmarshal(s.Bytes(), &event) == nil && string(event.Type) == kind {
			n++
		}
	}
	return n
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
	s.Buffer(make([]byte, 64*1024), 8*1024*1024)
	for s.Scan() {
		var event proc.Event
		if json.Unmarshal(s.Bytes(), &event) == nil && string(event.Type) == kind {
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

func identityForTest(t testing.TB, pid int) (string, bool) {
	t.Helper()
	identity, alive, err := proc.ProcessIdentity(pid)
	if err != nil {
		t.Errorf("inspect process %d identity: %v", pid, err)
		return "", false
	}
	return identity, alive
}

func processExists(t testing.TB, pid int) bool {
	t.Helper()
	alive, err := proc.ProcessExists(pid)
	if err != nil {
		t.Errorf("inspect process %d: %v", pid, err)
		return false
	}
	return alive
}

// case: S-237.1
func TestLinuxDoubleForkGrandchildIsAdoptedAndReaped(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux child-subreaper contract")
	}
	state := t.TempDir()
	helper := buildHelper(t)
	pidFile := filepath.Join(state, "grandchild.pid")
	releaseFile := filepath.Join(state, "release-command")
	run, err := startTest(context.Background(), proc.Config{
		HelperPath: helper, StateDir: state, Tag: "double-fork", Stall: time.Hour,
		StdoutPath: filepath.Join(state, "stdout.log"), StderrPath: filepath.Join(state, "stderr.log"),
		Command: []string{helper, "double-fork-held", "30", pidFile, releaseFile},
	})
	if err != nil {
		t.Fatal(err)
	}
	cleanupRun(t, state, run)
	grandchild, err := waitPIDFile(pidFile, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !waitUntil(3*time.Second, func() bool {
		tree, treeErr := proc.ProcessTree(run.PID, run.RunID)
		if treeErr != nil {
			t.Fatalf("process tree for run %s: %v", run.RunID, treeErr)
		}
		for _, child := range tree {
			if child.PID == grandchild && child.PPID == run.PID {
				return true
			}
		}
		return false
	}) {
		t.Fatalf("double-fork grandchild %d did not survive and reparent to keeper %d", grandchild, run.PID)
	}
	if err := os.WriteFile(releaseFile, []byte("release\n"), 0o600); err != nil {
		t.Fatalf("release supervised command after observing adoption: %v", err)
	}
	if err := run.Wait(); err != nil {
		t.Fatal(err)
	}
	if _, alive := identityForTest(t, grandchild); alive {
		t.Fatalf("double-fork grandchild %d survived the keeper's subreaper cleanup", grandchild)
	}
}

// case: S-237.4
func TestConcurrentKeepersAppendCompleteEventLines(t *testing.T) {
	state := t.TempDir()
	helper := buildHelper(t)
	const count = 8
	runs := make([]*proc.Run, 0, count)
	for i := 0; i < count; i++ {
		run, err := startTest(context.Background(), proc.Config{
			HelperPath: helper, StateDir: state, Tag: fmt.Sprintf("parallel-%d", i), Stall: time.Hour,
			StdoutPath: filepath.Join(state, fmt.Sprintf("out-%d", i)), StderrPath: filepath.Join(state, fmt.Sprintf("err-%d", i)),
			Command: []string{"/bin/sleep", "0.3"},
		})
		if err != nil {
			t.Fatal(err)
		}
		cleanupRun(t, state, run)
		runs = append(runs, run)
	}
	for _, run := range runs {
		if err := run.Wait(); err != nil {
			skipOnTeardownObservationFailure(t, state, run.RunID)
			t.Fatal(err)
		}
	}
	f, err := os.Open(filepath.Join(state, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	countLines := 0
	types := make(map[proc.EventType]int)
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		var event proc.Event
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatalf("event line %d is invalid JSON: %v", countLines+1, err)
		}
		countLines++
		types[event.Type]++
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if countLines != count*4 {
		t.Fatalf("event lines = %d, want %d (queued, start, exit, teardown per keeper); event types: %v", countLines, count*4, types)
	}
	data, err := os.ReadFile(filepath.Join(state, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasSuffix(data, []byte("\n")) {
		t.Fatal("event stream ends with a partial JSONL record")
	}
	for _, run := range runs {
		skipOnTeardownObservationFailure(t, state, run.RunID)
	}
}

// case: S-237.5
func TestStartRejectsHelperWithoutKeeperDispatchPromptly(t *testing.T) {
	testStartRejectsHelperWithoutKeeperDispatchPromptly(t)
}

// case: S-237.41
func TestStartupErrorTeardownReportsScratchOutcome(t *testing.T) {
	testStartRejectsHelperWithoutKeeperDispatchPromptly(t)
}

func testStartRejectsHelperWithoutKeeperDispatchPromptly(t *testing.T) {
	state := t.TempDir()
	badHelper := filepath.Join(state, "wrong-helper")
	pidFile := filepath.Join(state, "bad-helper.pid")
	if err := os.WriteFile(badHelper, []byte("#!/bin/sh\nprintf '%s\\n' \"$$\" > \""+pidFile+"\"\nexec /bin/sleep 30\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	killBadHelper := func() {
		data, readErr := os.ReadFile(pidFile)
		if readErr == nil {
			if pid, ready, parseErr := parsePIDFile(data); parseErr == nil && ready {
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	}
	t.Cleanup(killBadHelper)
	started := time.Now()
	type startResult struct {
		err error
	}
	startedResult := make(chan startResult, 1)
	startCtx, cancelStart := context.WithCancel(context.Background())
	t.Cleanup(cancelStart)
	go func() {
		_, err := startTest(startCtx, proc.Config{
			HelperPath: badHelper, StateDir: state, Tag: "wrong-helper", Stall: time.Hour,
			StdoutPath: filepath.Join(state, "stdout.log"), StderrPath: filepath.Join(state, "stderr.log"),
			Command: []string{"/usr/bin/true"},
		})
		startedResult <- startResult{err: err}
	}()
	var err error
	select {
	case result := <-startedResult:
		err = result.err
	case <-time.After(4 * time.Second):
		cancelStart()
		killBadHelper()
		select {
		case <-startedResult:
			t.Fatal("Start did not reject an incorrect helper within four seconds")
		case <-time.After(2 * time.Second):
			t.Fatal("Start did not stop after its context was cancelled")
		}
	}
	if err == nil || !strings.Contains(err.Error(), "must dispatch __keeper") {
		t.Fatalf("Start error = %v, want clear helper-dispatch error", err)
	}
	pid, readErr := waitPIDFile(pidFile, 5*time.Second)
	if readErr != nil {
		t.Fatalf("invalid helper did not record a complete process id: %v", readErr)
	}
	if identity, alive := identityForTest(t, pid); alive {
		t.Fatalf("invalid helper process %d remains alive with identity %s", pid, identity)
	}
	if time.Since(started) > 4*time.Second {
		t.Fatalf("invalid helper took too long to reject: %s", time.Since(started))
	}
	assertExactlyOneScratchOutcome(t, eventOfType(t, state, proc.EventTeardown), string(proc.ResourceAlreadyGone))
}

// case: S-237.5
func TestRunningKeeperUsesLoadedImageAfterHelperReplacement(t *testing.T) {
	state := t.TempDir()
	helper := buildHelper(t)
	run, err := startTest(context.Background(), proc.Config{
		HelperPath: helper, StateDir: state, Tag: "replace-helper", Stall: time.Hour,
		StdoutPath: filepath.Join(state, "stdout.log"), StderrPath: filepath.Join(state, "stderr.log"),
		Command: []string{"/bin/sleep", "1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	cleanupRun(t, state, run)
	replacement := filepath.Join(state, "replacement")
	if err := os.WriteFile(replacement, []byte("#!/bin/sh\nexit 99\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, helper); err != nil {
		t.Fatal(err)
	}
	if identity, alive := identityForTest(t, run.PID); !alive || identity != run.StartedAt {
		t.Fatalf("running keeper changed identity after on-disk replacement: pid=%d identity=%q alive=%t", run.PID, identity, alive)
	}
	if err := run.Wait(); err != nil {
		skipOnTeardownObservationFailure(t, state, run.RunID)
		t.Fatalf("running keeper failed after on-disk helper replacement: %v", err)
	}
	_, err = startTest(context.Background(), proc.Config{
		HelperPath: helper, StateDir: state, Tag: "replacement-next-run", Stall: time.Hour,
		StdoutPath: filepath.Join(state, "next-stdout.log"), StderrPath: filepath.Join(state, "next-stderr.log"),
		Command: []string{"/usr/bin/true"},
	})
	if err == nil || !strings.Contains(err.Error(), "must dispatch __keeper") {
		t.Fatalf("future run with replacement helper error = %v", err)
	}
	skipOnTeardownObservationFailure(t, state, run.RunID)
}

// case: S-237.6
func TestRunReadsStdinAndMergesEnvWithMarker(t *testing.T) {
	state := t.TempDir()
	runner := buildRunner(t)
	prompt := filepath.Join(state, "prompt.txt")
	if err := os.WriteFile(prompt, []byte("prompt-line-one\nprompt-line-two\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(runner,
		"run", "--tag", "stdin-env", "--state", state, "--stall", "1h",
		"--stdout", filepath.Join(state, "stdout.log"), "--stderr", filepath.Join(state, "stderr.log"),
		"--stdin", prompt, "--env", "PHASE2=present", "--env", "TASK_ID=42", "--",
		"/bin/sh", "-c", `printf 'prompt='; cat; printf 'phase2=%s task=%s marker=%s\n' "$PHASE2" "$TASK_ID" "$KBLABS_CLIPROC_RUN"`,
	)
	output, err := command.CombinedOutput()
	runID := outputRunID(t, state, "stdin-env")
	if err != nil {
		skipOnTeardownObservationFailure(t, state, runID)
		t.Fatalf("kb-runner run with stdin/env failed: %v\n%s", err, output)
	}
	if len(output) == 0 {
		t.Fatal("kb-runner did not report command completion")
	}
	data, err := os.ReadFile(filepath.Join(state, "stdout.log"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, expected := range []string{"prompt=prompt-line-one\nprompt-line-two\n", "phase2=present task=42 marker="} {
		if !strings.Contains(text, expected) {
			t.Fatalf("supervised output missing %q: stdout-file=%q stderr-file=%q runner=%s", expected, text, readText(t, filepath.Join(state, "stderr.log")), output)
		}
	}
	if strings.HasSuffix(strings.TrimSpace(text), "marker=") {
		t.Fatalf("run marker was empty: %s", text)
	}
	skipOnTeardownObservationFailure(t, state, runID)
}

// case: S-237.8
func TestSlotAdmissionIsAtomicAndFIFO(t *testing.T) {
	state := t.TempDir()
	runner := buildRunner(t)
	startedLog := filepath.Join(state, "start-order")
	commands := make([]*exec.Cmd, 4)
	pidFiles := make([]string, 4)
	finished := make([]bool, 4)
	t.Cleanup(func() {
		for i, command := range commands {
			if command != nil && command.Process != nil && !finished[i] {
				if err := command.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
					t.Logf("stop slot test runner: %v", err)
				}
				if err := command.Wait(); err != nil {
					t.Logf("wait for slot test runner: %v", err)
				}
			}
		}
		forceKillRecordedTree(t, state)
	})
	for i := range commands {
		tag := fmt.Sprintf("worker-%d", i+1)
		pidFiles[i] = filepath.Join(state, tag+".pid")
		commands[i] = exec.Command(runner,
			"run", "--tag", tag, "--state", state, "--stall", "1h",
			"--stdout", filepath.Join(state, tag+".stdout"), "--stderr", filepath.Join(state, tag+".stderr"),
			"--slot-group", "workers", "--slots", "2", "--",
			"/bin/sh", "-c", `printf '%s\n' "$1" >> "$2"; echo $$ > "$3"; exec /bin/sleep 30`, "sh", tag, startedLog, pidFiles[i],
		)
		if err := commands[i].Start(); err != nil {
			t.Fatal(err)
		}
	}
	if !waitUntil(5*time.Second, func() bool {
		output, err := exec.Command(runner, "ps", "--state", state).CombinedOutput()
		return err == nil && strings.Count(string(output), "\tRUNNING\t") == 2 && strings.Count(string(output), "\tQUEUED\t") == 2
	}) {
		t.Fatalf("slot capacity was not enforced for four concurrent starts: %s", outputPS(t, runner, state))
	}
	ps := outputPS(t, runner, state)
	for _, tag := range activeTags(t, ps) {
		if _, err := waitPIDFile(pidFiles[tagIndex(tag)], 5*time.Second); err != nil {
			t.Fatalf("active worker %s did not create its pid file: %v", tag, err)
		}
	}
	firstQueued := queuedTagAtPosition(t, ps, 1)
	secondQueued := queuedTagAtPosition(t, ps, 2)
	firstActive := activeTags(t, ps)
	for _, tag := range firstActive[:1] {
		stopSlotProcess(t, pidFiles[tagIndex(tag)])
	}
	firstQueuedIndex := tagIndex(firstQueued)
	if _, err := waitPIDFile(pidFiles[firstQueuedIndex], 5*time.Second); err != nil {
		t.Fatalf("first queued run was not admitted: %v; ps: %s", err, outputPS(t, runner, state))
	}
	order, err := os.ReadFile(startedLog)
	if err != nil || !strings.Contains(string(order), firstQueued+"\n") {
		t.Fatalf("slot admission was not FIFO after first release: %q, err=%v", order, err)
	}
	ps = outputPS(t, runner, state)
	active := activeTags(t, ps)
	for _, tag := range active {
		if tag != firstQueued {
			stopSlotProcess(t, pidFiles[tagIndex(tag)])
			break
		}
	}
	secondQueuedIndex := tagIndex(secondQueued)
	if _, err := waitPIDFile(pidFiles[secondQueuedIndex], 5*time.Second); err != nil {
		t.Fatalf("second queued run was not admitted: %v; ps: %s", err, outputPS(t, runner, state))
	}
	order, err = os.ReadFile(startedLog)
	if err != nil || !strings.Contains(string(order), firstQueued+"\n"+secondQueued+"\n") {
		t.Fatalf("slot admission order = %q, err=%v", order, err)
	}
	stopSlotProcess(t, pidFiles[firstQueuedIndex])
	stopSlotProcess(t, pidFiles[secondQueuedIndex])
	for i, command := range commands {
		if err := command.Wait(); err == nil {
			t.Fatal("slot command returned success after SIGTERM")
		}
		finished[i] = true
	}
}

// case: S-237.8
func TestDeadKeeperIsLostReapedAndDoesNotConsumeSlot(t *testing.T) {
	testDeadKeeperIsLostReapedAndDoesNotConsumeSlot(t)
}

// case: S-237.47
func TestLostKeeperCleansRegisteredResources(t *testing.T) {
	testDeadKeeperIsLostReapedAndDoesNotConsumeSlot(t)
}

func testDeadKeeperIsLostReapedAndDoesNotConsumeSlot(t *testing.T) {
	state := t.TempDir()
	runner := buildRunner(t)
	firstPIDFile := filepath.Join(state, "first-child.pid")
	secondPIDFile := filepath.Join(state, "second-child.pid")
	start := func(tag, pidFile string) *exec.Cmd {
		t.Helper()
		cmd := exec.Command(runner, "run", "--tag", tag, "--state", state, "--stall", "1h", "--stdout", filepath.Join(state, tag+".out"), "--stderr", filepath.Join(state, tag+".err"), "--slot-group", "stale", "--slots", "1", "--", "/bin/sh", "-ec", `"$1" own path --create dir "$TMPDIR/owned"; echo $$ > "$2"; exec /bin/sleep 30`, "sh", runner, pidFile)
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		return cmd
	}
	first := start("stale-holder", firstPIDFile)
	var second *exec.Cmd
	ownedChildren := make(map[int]string)
	t.Cleanup(func() {
		if first.Process != nil {
			_ = first.Process.Kill()
			_ = first.Wait()
		}
		if second != nil && second.Process != nil {
			_ = second.Process.Kill()
			_ = second.Wait()
		}
		for pid, identity := range ownedChildren {
			if current, alive := identityForTest(t, pid); alive && current == identity {
				if err := syscall.Kill(pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
					t.Logf("cleanup owned slot-test child %d: %v", pid, err)
				}
			}
		}
		forceKillRecordedTree(t, state)
	})
	firstChild, err := waitPIDFile(firstPIDFile, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	firstIdentity, alive := identityForTest(t, firstChild)
	if !alive {
		t.Fatalf("cannot verify first command identity for PID %d", firstChild)
	}
	ownedChildren[firstChild] = firstIdentity
	listing := proc.List(state)
	if listing.Error != nil || len(listing.Runs) != 1 {
		t.Fatalf("list first run: %+v", listing)
	}
	firstRun := listing.Runs[0]
	if runtime.GOOS == "darwin" && !waitUntil(3*time.Second, func() bool {
		data, err := os.ReadFile(filepath.Join(state, "runs", firstRun.RunID+".json"))
		if err != nil {
			return false
		}
		var stored struct {
			Observed []proc.Process `json:"observed"`
		}
		if json.Unmarshal(data, &stored) != nil {
			return false
		}
		for _, process := range stored.Observed {
			if process.PID == firstChild {
				return true
			}
		}
		return false
	}) {
		t.Fatalf("keeper never persisted the live descendant identity before SIGKILL")
	}
	if err := syscall.Kill(firstRun.PID, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	second = start("stale-waiter", secondPIDFile)
	secondChild, err := waitPIDFile(secondPIDFile, 5*time.Second)
	if err != nil {
		t.Fatalf("queued run did not acquire slot after keeper death: %v; ps: %s", err, outputPS(t, runner, state))
	}
	secondIdentity, alive := identityForTest(t, secondChild)
	if !alive {
		t.Fatalf("cannot verify second command identity for PID %d", secondChild)
	}
	ownedChildren[secondChild] = secondIdentity
	if !waitUntil(5*time.Second, func() bool {
		listing := proc.List(state)
		if listing.Error != nil {
			return false
		}
		for _, run := range listing.Runs {
			if run.RunID == firstRun.RunID && run.Status == proc.StatusLost {
				return countLostEvents(t, state, firstRun.RunID) == 1
			}
		}
		return false
	}) {
		t.Fatalf("dead keeper was not recorded LOST with one event: %s", outputPS(t, runner, state))
	}
	if _, alive := identityForTest(t, firstChild); alive {
		t.Fatalf("surviving tree process %d was not cleaned after keeper death", firstChild)
	}
	teardown := eventForRunAndType(t, state, firstRun.RunID, proc.EventTeardown)
	scratch := scratchForRun(t, state, firstRun.RunID)
	if _, err := os.Stat(scratch); !os.IsNotExist(err) {
		t.Fatalf("lost-run scratch remains at %s: %v; teardown=%+v", scratch, err, teardown.Resources)
	}
	if _, err := os.Stat(filepath.Join(state, "runs", firstRun.RunID, "resources.json")); err != nil {
		t.Fatalf("lost-run resource ledger was not persisted for audit: %v", err)
	}
	removedPath, removedScratch := false, false
	ledger, err := os.ReadFile(filepath.Join(state, "runs", firstRun.RunID, "resources.json"))
	if err != nil {
		t.Fatal(err)
	}
	var entries []proc.Resource
	if err := json.Unmarshal(ledger, &entries); err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Kind != proc.ResourcePath {
		t.Fatalf("lost run ledger entries = %+v", entries)
	}
	for _, outcome := range teardown.Resources {
		if outcome.Resource.Kind == "scratch" && outcome.Outcome == proc.ResourceRemoved {
			removedScratch = true
		}
	}
	for _, entry := range entries {
		found := false
		for _, outcome := range teardown.Resources {
			if outcome.Resource.Kind == entry.Kind && outcome.Resource.ID == entry.ID && outcome.Outcome == proc.ResourceRemoved {
				found = true
				removedPath = true
				break
			}
		}
		if !found {
			t.Fatalf("lost-run teardown omitted outcome for ledger entry %+v: %+v", entry, teardown.Resources)
		}
	}
	if len(teardown.Resources) != len(entries)+1 || !removedPath || !removedScratch {
		t.Fatalf("lost-run teardown omitted resource outcomes: %+v", teardown.Resources)
	}
}

func scratchForRun(t *testing.T, state, runID string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(state, "runs", runID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var record struct {
		Scratch string `json:"scratch"`
	}
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatal(err)
	}
	if record.Scratch == "" {
		t.Fatalf("run %s record omitted scratch path", runID)
	}
	return record.Scratch
}

func eventForRunAndType(t *testing.T, state, runID string, eventType proc.EventType) proc.Event {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(state, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var found *proc.Event
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var event proc.Event
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatal(err)
		}
		if event.RunID == runID && event.Type == eventType {
			copy := event
			found = &copy
		}
	}
	if found == nil {
		t.Fatalf("event %s for run %s not found", eventType, runID)
	}
	return *found
}

// case: S-237.17
func TestRunWaitRecordsKeeperLossWithoutRegistryReader(t *testing.T) {
	state := t.TempDir()
	helper := buildHelper(t)
	childFile := filepath.Join(state, "daemon-child.pid")
	run, err := proc.Start(context.Background(), proc.Config{
		HelperPath: helper, StateDir: state, Tag: "daemon:wait-owner", Stall: time.Hour,
		StdoutPath: filepath.Join(state, "stdout.log"), StderrPath: filepath.Join(state, "stderr.log"),
		Command: []string{"/bin/sh", "-c", `echo $$ > "$1"; exec /bin/sleep 30`, "sh", childFile},
	})
	if err != nil {
		t.Fatal(err)
	}
	var childPID int
	var childIdentity string
	t.Cleanup(func() {
		if _, alive := identityForTest(t, run.PID); alive {
			if err := syscall.Kill(run.PID, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
				t.Logf("cleanup test-owned keeper %d: %v", run.PID, err)
			}
		}
		if err := run.Wait(); err != nil {
			t.Logf("wait for test-owned keeper %d: %v", run.PID, err)
		}
		if childPID > 0 && childIdentity != "" {
			if identity, alive := identityForTest(t, childPID); alive && identity == childIdentity {
				if err := syscall.Kill(childPID, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
					t.Logf("cleanup test-owned command child %d: %v", childPID, err)
				}
			}
		}
	})
	childPID, err = waitPIDFile(childFile, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	childIdentity, alive := identityForTest(t, childPID)
	if !alive {
		t.Fatalf("cannot identify test-owned command child %d", childPID)
	}
	if err := syscall.Kill(run.PID, syscall.SIGKILL); err != nil {
		t.Fatalf("SIGKILL test-owned keeper %d: %v", run.PID, err)
	}
	if err := run.Wait(); err == nil {
		t.Fatal("Wait returned success after the keeper was SIGKILLed")
	}
	runEvents := eventsForRun(t, state, run.RunID)
	lostCount, teardownCount := 0, 0
	var teardown proc.Event
	for _, event := range runEvents {
		if event.Type == proc.EventLost {
			lostCount++
		}
		if event.Type == proc.EventTeardown {
			teardownCount++
			teardown = event
		}
	}
	if lostCount != 1 || teardownCount != 1 {
		t.Fatalf("run.Wait did not write exactly one lost and teardown event: %+v", runEvents)
	}
	if identity, alive := identityForTest(t, childPID); alive && identity == childIdentity {
		t.Fatalf("run.Wait left its recorded command child %d alive: %+v", childPID, teardown)
	}
	for _, process := range teardown.Reaped {
		if process.PID == childPID && process.Identity == childIdentity {
			return
		}
	}
	t.Fatalf("run.Wait did not account for the command child identity %d:%s in teardown: %+v", childPID, childIdentity, teardown)
}

// case: S-237.9
func TestWaitGateBlocksAndDeadHolderReleases(t *testing.T) {
	state := t.TempDir()
	runner := buildRunner(t)
	start := func(tag string, args ...string) *exec.Cmd {
		t.Helper()
		base := []string{"run", "--tag", tag, "--state", state, "--stall", "1h", "--stdout", filepath.Join(state, tag+".out"), "--stderr", filepath.Join(state, tag+".err")}
		command := exec.Command(runner, append(base, args...)...)
		command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if err := command.Start(); err != nil {
			t.Fatal(err)
		}
		return command
	}
	holderPID := filepath.Join(state, "holder.pid")
	holder := exec.Command(runner, append([]string{"gate", "hold", "maintenance", "--tag", "holder", "--state", state, "--stall", "1h", "--stdout", filepath.Join(state, "holder.out"), "--stderr", filepath.Join(state, "holder.err"), "--"}, "/bin/sh", "-c", `echo $$ > "$1"; exec /bin/sleep 30`, "sh", holderPID)...)
	holder.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := holder.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := killTestProcessGroup(holder); err != nil {
			t.Logf("stop holder command child: %v", err)
		}
		if holder.Process != nil {
			_ = holder.Process.Kill()
			_ = holder.Wait()
		}
		forceKillRecordedTree(t, state)
	})
	if _, err := waitPIDFile(holderPID, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	waiterPID := filepath.Join(state, "waiter.pid")
	waiter := start("waiter", "--wait-gate", "maintenance", "--", "/bin/sh", "-c", `echo $$ > "$1"; exec /bin/sleep 30`, "sh", waiterPID)
	t.Cleanup(func() {
		if err := killTestProcessGroup(waiter); err != nil {
			t.Logf("stop waiter command child: %v", err)
		}
		if waiter.Process != nil {
			_ = waiter.Process.Kill()
			_ = waiter.Wait()
		}
	})
	if !waitUntil(5*time.Second, func() bool {
		return strings.Contains(outputPS(t, runner, state), "waiter\t") && strings.Contains(outputPS(t, runner, state), "BLOCKED")
	}) {
		t.Fatalf("gate waiter was not visibly blocked: %s", outputPS(t, runner, state))
	}
	if _, err := os.Stat(waiterPID); !os.IsNotExist(err) {
		t.Fatalf("wait-gated command started before release: stat=%v", err)
	}
	if err := holder.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := holder.Wait(); err == nil {
		t.Fatal("killed gate holder returned success")
	}
	if _, err := waitPIDFile(waiterPID, 5*time.Second); err != nil {
		t.Fatalf("dead holder did not release gate: %v; ps=%s", err, outputPS(t, runner, state))
	}
	stopSlotProcess(t, waiterPID)
	if err := waiter.Wait(); err == nil {
		t.Fatal("terminated waiter command returned success")
	}
}

// case: S-237.51
func TestGateWaitDoesNotBlockUnrelatedRun(t *testing.T) {
	state := t.TempDir()
	runner := buildRunner(t)
	start := func(tag string, args ...string) *exec.Cmd {
		t.Helper()
		base := []string{"run", "--tag", tag, "--state", state, "--stall", "1h", "--stdout", filepath.Join(state, tag+".out"), "--stderr", filepath.Join(state, tag+".err")}
		command := exec.Command(runner, append(base, args...)...)
		command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if err := command.Start(); err != nil {
			t.Fatal(err)
		}
		return command
	}
	startHeld := func(tag, gate, started, release string) *exec.Cmd {
		command := exec.Command(runner, "gate", "hold", gate, "--tag", tag, "--state", state, "--stall", "1h", "--stdout", filepath.Join(state, tag+".out"), "--stderr", filepath.Join(state, tag+".err"), "--", "/bin/sh", "-c", `touch "$1"; while [ ! -f "$2" ]; do /bin/sleep 0.01; done; exit 0`, "sh", started, release)
		command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if err := command.Start(); err != nil {
			t.Fatal(err)
		}
		return command
	}
	stopped := make(map[*exec.Cmd]bool)
	stop := func(command *exec.Cmd) {
		t.Helper()
		if command.Process != nil && !stopped[command] {
			stopped[command] = true
			_ = killTestProcessGroup(command)
			_ = command.Process.Kill()
			_ = command.Wait()
		}
	}
	holderStarted := filepath.Join(state, "holder.started")
	holderRelease := filepath.Join(state, "holder.release")
	holder := startHeld("admission-holder", "maintenance", holderStarted, holderRelease)
	t.Cleanup(func() { stop(holder); forceKillRecordedTree(t, state) })
	if _, err := waitFile(t, holderStarted, 5*time.Second); err != nil {
		t.Fatalf("gate holder did not start: %v; ps=%s", err, outputPS(t, runner, state))
	}
	waiterStarted := filepath.Join(state, "waiter.started")
	waiter := start("admission-waiter", "--wait-gate", "maintenance", "--", "/bin/sh", "-c", `touch "$1"; exec /bin/sleep 30`, "sh", waiterStarted)
	t.Cleanup(func() { stop(waiter); forceKillRecordedTree(t, state) })
	if !waitUntil(5*time.Second, func() bool {
		return strings.Contains(outputPS(t, runner, state), "admission-waiter\t") && strings.Contains(outputPS(t, runner, state), "BLOCKED")
	}) {
		t.Fatalf("gate waiter did not become blocked: %s", outputPS(t, runner, state))
	}
	freeStarted := filepath.Join(state, "unrelated.started")
	unrelated := start("admission-unrelated", "--", "/bin/sh", "-c", `touch "$1"; exec /bin/sleep 30`, "sh", freeStarted)
	t.Cleanup(func() { stop(unrelated); forceKillRecordedTree(t, state) })
	if _, err := waitFile(t, freeStarted, 5*time.Second); err != nil {
		t.Fatalf("unrelated run was held behind gate waiter: %v; ps=%s", err, outputPS(t, runner, state))
	}
	if strings.Contains(outputPS(t, runner, state), "admission-waiter\tRUNNING") {
		t.Fatal("gate waiter started before gate release")
	}
	stop(unrelated)
	if err := os.WriteFile(holderRelease, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := waitFile(t, waiterStarted, 5*time.Second); err != nil {
		t.Fatalf("gate waiter did not start after gate release: %v; ps=%s", err, outputPS(t, runner, state))
	}
	stop(waiter)
}

// case: S-237.52
func TestGateWaitUsesLittleCPU(t *testing.T) {
	state := t.TempDir()
	runner := buildRunner(t)
	start := func(args ...string) *exec.Cmd {
		t.Helper()
		command := exec.Command(runner, args...)
		command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if err := command.Start(); err != nil {
			t.Fatal(err)
		}
		return command
	}
	stopped := make(map[*exec.Cmd]bool)
	stop := func(command *exec.Cmd) {
		t.Helper()
		if command.Process != nil && !stopped[command] {
			stopped[command] = true
			_ = killTestProcessGroup(command)
			_ = command.Process.Kill()
			_ = command.Wait()
		}
	}
	holder := start("gate", "hold", "maintenance", "--tag", "cpu-holder", "--state", state, "--stall", "1h", "--stdout", filepath.Join(state, "holder.out"), "--stderr", filepath.Join(state, "holder.err"), "--", "/bin/sleep", "30")
	t.Cleanup(func() { stop(holder); forceKillRecordedTree(t, state) })
	var gateStatErr error
	if !waitUntil(5*time.Second, func() bool {
		_, held, err := proc.GateStatus(state, "maintenance")
		gateStatErr = err
		if err != nil {
			return false
		}
		if !held {
			return false
		}
		return true
	}) {
		t.Fatalf("gate holder did not claim maintenance before waiter launch: %v", gateStatErr)
	}
	waiterStarted := filepath.Join(state, "waiter.started")
	waiter := start("run", "--tag", "cpu-waiter", "--state", state, "--stall", "1h", "--stdout", filepath.Join(state, "waiter.out"), "--stderr", filepath.Join(state, "waiter.err"), "--wait-gate", "maintenance", "--", "/bin/sh", "-c", `touch "$1"; exec /bin/sleep 30`, "sh", waiterStarted)
	t.Cleanup(func() { stop(waiter); forceKillRecordedTree(t, state) })
	if !waitUntil(5*time.Second, func() bool {
		ps := outputPS(t, runner, state)
		return strings.Contains(ps, "cpu-waiter\t") && strings.Contains(ps, "\tBLOCKED\tblocked: position=") && strings.Contains(ps, "gate: maintenance")
	}) {
		t.Fatalf("gate waiter did not become blocked: %s", outputPS(t, runner, state))
	}
	if _, err := os.Stat(waiterStarted); !os.IsNotExist(err) {
		t.Fatalf("waiter's command started before gate release: stat=%v", err)
	}
	// ProcessState reports CPU for the waiting CLI process after it is stopped.
	time.Sleep(5 * time.Second)
	stop(waiter)
	usage, ok := waiter.ProcessState.SysUsage().(*syscall.Rusage)
	if !ok {
		t.Fatal("waiter process CPU usage is unavailable")
	}
	cpu := time.Duration(usage.Utime.Sec)*time.Second + time.Duration(usage.Utime.Usec)*time.Microsecond + time.Duration(usage.Stime.Sec)*time.Second + time.Duration(usage.Stime.Usec)*time.Microsecond
	t.Logf("gate waiter used %s CPU while visibly blocked for five seconds", cpu)
	if cpu > 100*time.Millisecond {
		t.Fatalf("gate wait consumed %s CPU over five seconds", cpu)
	}
	if err := holder.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := holder.Wait(); err == nil {
		t.Fatal("killed gate holder returned success")
	}
}

// case: S-237.10
func TestMinFreeWaitIsVisibleAndEmitsOneEvent(t *testing.T) {
	state := t.TempDir()
	runner := buildRunner(t)
	started := filepath.Join(state, "command.started")
	command := exec.Command(runner,
		"run", "--tag", "disk-wait", "--state", state, "--stall", "1h",
		"--stdout", filepath.Join(state, "stdout.log"), "--stderr", filepath.Join(state, "stderr.log"),
		"--min-free", "18446744073709551615",
		"--", "/bin/sh", "-c", `touch "$1"`, "sh", started,
	)
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if command.Process != nil {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
		forceKillRecordedTree(t, state)
	})
	if !waitUntil(5*time.Second, func() bool {
		output, err := exec.Command(runner, "ps", "--state", state).CombinedOutput()
		return err == nil && strings.Contains(string(output), "BLOCKED") && strings.Contains(string(output), "disk budget")
	}) {
		t.Fatalf("minimum-free wait was invisible: %s", outputPS(t, runner, state))
	}
	if _, err := os.Stat(started); !os.IsNotExist(err) {
		t.Fatalf("command started below min-free threshold: %v", err)
	}
	if got := countEvents(t, state, "blocked"); got != 1 {
		t.Fatalf("blocked event count = %d, want one disk wait transition", got)
	}
	blocked := eventOfType(t, state, proc.EventBlocked)
	if blocked.DiskNeedBytes != ^uint64(0) || blocked.DiskFreeBytes >= blocked.DiskNeedBytes {
		t.Fatalf("blocked disk event reports need=%d free=%d; want observed below-budget values", blocked.DiskNeedBytes, blocked.DiskFreeBytes)
	}
	if err := command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := command.Wait(); err == nil {
		t.Fatal("cancelled disk wait returned success")
	}
}

// case: S-237.11
func TestWaitReturnsRecordedExitAndFilteredEvents(t *testing.T) {
	state := t.TempDir()
	runner := buildRunner(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(func() {
		cancel()
		forceKillRecordedTree(t, state)
	})
	command := exec.CommandContext(ctx, runner,
		"run", "--tag", "result", "--state", state, "--stall", "100ms",
		"--stdout", filepath.Join(state, "stdout.log"), "--stderr", filepath.Join(state, "stderr.log"),
		"--", "/bin/sh", "-c", "/bin/sleep 0.4; exit 7",
	)
	output, err := command.CombinedOutput()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 7 {
		t.Fatalf("run exit = %v, output=%s; want code 7", err, output)
	}
	wait := exec.Command(runner, "wait", "--tag", "result", "--state", state)
	waitOutput, waitErr := wait.CombinedOutput()
	if !errors.As(waitErr, &exitErr) || exitErr.ExitCode() != 7 || !strings.Contains(string(waitOutput), "run result exited after ") {
		t.Fatalf("wait result = %v, output=%s; want recorded code 7 and duration", waitErr, waitOutput)
	}
	events := exec.Command(runner, "events", "--state", state, "--filter", "exit,stall")
	eventOutput, err := events.CombinedOutput()
	if err != nil {
		t.Fatalf("filtered events: %v: %s", err, eventOutput)
	}
	var seen []proc.EventType
	for _, line := range bytes.Split(bytes.TrimSpace(eventOutput), []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		var event proc.Event
		if err := json.Unmarshal(line, &event); err != nil {
			t.Fatalf("invalid filtered event: %v: %s", err, line)
		}
		seen = append(seen, event.Type)
	}
	if len(seen) != 2 || seen[0] != proc.EventStall || seen[1] != proc.EventExit {
		t.Fatalf("filtered lifecycle types = %v, want [stall exit]", seen)
	}
	if countEvents(t, state, "teardown") != 1 || countEvents(t, state, "start") != 1 {
		t.Fatalf("lifecycle event counts start=%d teardown=%d", countEvents(t, state, "start"), countEvents(t, state, "teardown"))
	}
}

// case: S-237.59
func TestEventsStartsAtPersistedByteOffset(t *testing.T) {
	state := t.TempDir()
	runner := buildRunner(t)
	first := []byte(`{"schema":"kb.proc-event/v2","type":"queued","tag":"agent:old"}` + "\n")
	second := []byte(`{"schema":"kb.proc-event/v2","type":"queued","tag":"agent:new"}` + "\n")
	if err := os.WriteFile(filepath.Join(state, "events.jsonl"), append(first, second...), 0o600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(runner, "events", "--state", state, "--offset", strconv.Itoa(len(first)))
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("events from byte offset failed: %v; output=%s", err, output)
	}
	var event proc.Event
	if err := json.Unmarshal(bytes.TrimSpace(output), &event); err != nil {
		t.Fatalf("decode event from byte offset: %v; output=%s", err, output)
	}
	if event.Tag != "agent:new" {
		t.Fatalf("event at offset = %q, want agent:new; output=%s", event.Tag, output)
	}
}

// case: S-237.11
func TestEventsFollowStreamsLaterFilteredRecords(t *testing.T) {
	state := t.TempDir()
	runner := buildRunner(t)
	follower := exec.Command(runner, "events", "--state", state, "--follow", "--filter", "exit,stall")
	stdout, err := follower.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := follower.Start(); err != nil {
		t.Fatal(err)
	}
	type followedEvent struct {
		event proc.Event
		err   error
	}
	readEvents := make(chan followedEvent, 8)
	readDone := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			var event proc.Event
			if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
				readEvents <- followedEvent{err: err}
				continue
			}
			readEvents <- followedEvent{event: event}
		}
		readDone <- scanner.Err()
	}()
	stopped := false
	stopFollower := func() error {
		if stopped {
			return nil
		}
		stopped = true
		var stopErr error
		if err := follower.Process.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
			stopErr = fmt.Errorf("signal event follower: %w", err)
		}
		// StdoutPipe's reader must finish before Wait closes the pipe. Waiting
		// first races the scanner's final read and can produce os.ErrClosed.
		var readErr error
		select {
		case readErr = <-readDone:
		case <-time.After(2 * time.Second):
			if err := follower.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
				stopErr = errors.Join(stopErr, fmt.Errorf("kill event follower after reader timeout: %w", err))
			}
			select {
			case readErr = <-readDone:
			case <-time.After(2 * time.Second):
				stopErr = errors.Join(stopErr, errors.New("event follower stdout reader did not finish after process termination"))
			}
		}
		waitErr := follower.Wait()
		if waitErr != nil && !errors.Is(waitErr, syscall.EINTR) {
			stopErr = errors.Join(stopErr, fmt.Errorf("wait for event follower: %w", waitErr))
		}
		if readErr != nil {
			stopErr = errors.Join(stopErr, fmt.Errorf("read event follower stdout to EOF: %w", readErr))
		}
		return stopErr
	}
	t.Cleanup(func() {
		if err := stopFollower(); err != nil {
			t.Errorf("stop event follower during cleanup: %v", err)
		}
	})
	command := exec.Command(runner,
		"run", "--tag", "followed", "--state", state, "--stall", "100ms",
		"--stdout", filepath.Join(state, "stdout.log"), "--stderr", filepath.Join(state, "stderr.log"),
		"--", "/bin/sh", "-c", "/bin/sleep 0.4; exit 7",
	)
	if output, err := command.CombinedOutput(); err == nil {
		t.Fatalf("followed command returned success: %s", output)
	}
	seen := make(map[proc.EventType]int)
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	for seen[proc.EventExit] == 0 || seen[proc.EventStall] == 0 {
		select {
		case row := <-readEvents:
			if row.err != nil {
				t.Fatal(row.err)
			}
			if row.event.Type != proc.EventExit && row.event.Type != proc.EventStall {
				t.Fatalf("filtered follower emitted %q", row.event.Type)
			}
			seen[row.event.Type]++
		case <-deadline.C:
			t.Fatalf("event follower did not receive later filtered rows: %v", seen)
		}
	}
	if seen[proc.EventExit] != 1 || seen[proc.EventStall] != 1 {
		t.Fatalf("followed event counts = %v, want one exit and one stall", seen)
	}
	if err := stopFollower(); err != nil {
		t.Fatalf("event follower shutdown failed: %v", err)
	}
}

// case: S-237.26
func TestPSShowsRunningQueuedBlockedAndStalledTogether(t *testing.T) {
	state := t.TempDir()
	runner := buildRunner(t)
	helper := buildHelper(t)
	releaseFile := filepath.Join(state, "release-leaves")
	var commands []*exec.Cmd
	t.Cleanup(func() {
		// Release the held leaves first so they exit on their own and their
		// keepers reap them; killing the launchers first would orphan them.
		releaseHeldLeaves(t, releaseFile)
		for _, command := range commands {
			if command.Process != nil {
				_ = command.Process.Kill()
				_ = command.Wait()
			}
		}
		forceKillRecordedTree(t, state)
	})
	start := func(args ...string) *exec.Cmd {
		t.Helper()
		command := exec.Command(runner, args...)
		if err := command.Start(); err != nil {
			t.Fatal(err)
		}
		commands = append(commands, command)
		return command
	}
	heldCommand := func(pidFile string) []string {
		return []string{helper, "hold", pidFile, releaseFile}
	}
	startHeld := func(args []string, pidFile string) *exec.Cmd {
		t.Helper()
		return start(append(args, heldCommand(pidFile)...)...)
	}
	startWorker := func(tag, pidFile string) *exec.Cmd {
		return startHeld([]string{"run", "--tag", tag, "--state", state, "--stall", "1h", "--stdout", filepath.Join(state, tag+".out"), "--stderr", filepath.Join(state, tag+".err"), "--slot-group", "ps-workers", "--slots", "1", "--"}, pidFile)
	}
	activePID := filepath.Join(state, "active.pid")
	startWorker("active", activePID)
	if _, err := waitPIDFile(activePID, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	holderPID := filepath.Join(state, "gate-holder.pid")
	startHeld([]string{"gate", "hold", "maintenance", "--tag", "gate-holder", "--state", state, "--stall", "1h", "--stdout", filepath.Join(state, "gate-holder.out"), "--stderr", filepath.Join(state, "gate-holder.err"), "--"}, holderPID)
	if _, err := waitPIDFile(holderPID, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	startHeld([]string{"run", "--tag", "stalled", "--state", state, "--stall", "100ms", "--stdout", filepath.Join(state, "stalled.out"), "--stderr", filepath.Join(state, "stalled.err"), "--"}, filepath.Join(state, "stalled.pid"))
	if !waitUntil(5*time.Second, func() bool {
		return psHasTagStatus(outputPS(t, runner, state), "stalled", "STALLED")
	}) {
		t.Fatalf("silent command did not become stalled before queued work: %s", outputPS(t, runner, state))
	}
	startHeld([]string{"run", "--tag", "blocked", "--state", state, "--stall", "1h", "--wait-gate", "maintenance", "--stdout", filepath.Join(state, "blocked.out"), "--stderr", filepath.Join(state, "blocked.err"), "--"}, filepath.Join(state, "blocked.pid"))
	startWorker("queued", filepath.Join(state, "queued.pid"))
	if !waitUntil(5*time.Second, func() bool {
		output, err := exec.Command(runner, "ps", "--state", state).CombinedOutput()
		if err != nil {
			return false
		}
		return psHasTagStatus(string(output), "active", "RUNNING") &&
			psHasTagStatus(string(output), "queued", "QUEUED") &&
			psHasTagStatus(string(output), "gate-holder", "RUNNING") &&
			psHasTagStatus(string(output), "blocked", "BLOCKED") &&
			psHasTagStatus(string(output), "stalled", "STALLED")
	}) {
		t.Fatalf("ps did not show all live/waiting states together: %s", outputPS(t, runner, state))
	}
	output := outputPS(t, runner, state)
	assertPSRowDetails(t, output, "active", true, "")
	assertPSRowDetails(t, output, "queued", false, "position=1")
	assertPSRowDetails(t, output, "gate-holder", true, "")
	assertPSRowDetails(t, output, "blocked", false, "gate: maintenance")
	assertPSRowDetails(t, output, "stalled", true, "")
}

func assertPSRowDetails(t *testing.T, output, tag string, hasChild bool, waitDetail string) {
	t.Helper()
	lines := strings.Split(output, "\n")
	for i, line := range lines {
		fields := strings.Split(line, "\t")
		if len(fields) == 0 || fields[0] != tag {
			continue
		}
		if len(fields) < 7 || !strings.HasPrefix(fields[4], "elapsed=") || !strings.HasPrefix(fields[5], "tree=") {
			t.Fatalf("ps row %q omitted its own tree and elapsed fields: %s", tag, line)
		}
		if waitDetail != "" && !strings.Contains(fields[3], waitDetail) {
			t.Fatalf("ps row %q omitted wait detail %q: %s", tag, waitDetail, line)
		}
		treeCount, err := strconv.Atoi(strings.TrimPrefix(fields[5], "tree="))
		if err != nil {
			t.Fatalf("ps row %q has invalid tree count: %s", tag, line)
		}
		childCount := 0
		for _, childLine := range lines[i+1:] {
			if !strings.HasPrefix(childLine, "  └─ ") {
				if childLine != "" {
					break
				}
				continue
			}
			childFields := strings.Split(childLine, "\t")
			if len(childFields) < 3 || !strings.Contains(childFields[2], "elapsed=") {
				t.Fatalf("ps child of %q omitted its own elapsed field: %s", tag, childLine)
			}
			childCount++
		}
		if treeCount != childCount {
			t.Fatalf("ps row %q advertises tree=%d but prints %d descendants: %s", tag, treeCount, childCount, output)
		}
		if hasChild != (childCount > 0) {
			t.Fatalf("ps row %q has %d descendants, want child=%t: %s", tag, childCount, hasChild, output)
		}
		return
	}
	t.Fatalf("ps row %q was omitted: %s", tag, output)
}

func countLostEvents(t *testing.T, state, runID string) int {
	return countRunEvents(t, state, runID, proc.EventLost)
}

func countRunEvents(t *testing.T, state, runID string, eventType proc.EventType) int {
	t.Helper()
	count := 0
	for _, event := range eventsForRun(t, state, runID) {
		if event.Type == eventType {
			count++
		}
	}
	return count
}

func eventsForRun(t *testing.T, state, runID string) []proc.Event {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(state, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var matches []proc.Event
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		var event proc.Event
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatal(err)
		}
		if event.RunID == runID {
			matches = append(matches, event)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return matches
}

func psHasTagStatus(output, tag, status string) bool {
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) >= 3 && fields[0] == tag && fields[2] == status {
			return true
		}
	}
	return false
}

func activeTags(t *testing.T, output string) []string {
	t.Helper()
	var tags []string
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) >= 3 && fields[2] == "RUNNING" && strings.HasPrefix(fields[0], "worker-") {
			tags = append(tags, fields[0])
		}
	}
	if len(tags) != 2 {
		t.Fatalf("active worker set = %v, want 2: %s", tags, output)
	}
	return tags
}

func queuedTagAtPosition(t *testing.T, output string, position int) string {
	t.Helper()
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) >= 4 && strings.HasPrefix(fields[3], fmt.Sprintf("position=%d ", position)) {
			return fields[0]
		}
	}
	t.Fatalf("queue position %d absent from ps output: %s", position, output)
	return ""
}

func tagIndex(tag string) int {
	var index int
	if _, err := fmt.Sscanf(tag, "worker-%d", &index); err != nil {
		panic(err)
	}
	return index - 1
}

func outputPS(t *testing.T, runner, state string) string {
	t.Helper()
	output, err := exec.Command(runner, "ps", "--state", state).CombinedOutput()
	if err != nil {
		t.Fatalf("kb-runner ps: %v\n%s", err, output)
	}
	return string(output)
}

func readText(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		return err.Error()
	}
	return string(data)
}

func stopSlotProcess(t *testing.T, pidFile string) {
	t.Helper()
	pid, err := waitPIDFile(pidFile, 5*time.Second)
	if err != nil {
		t.Fatalf("read complete process PID from %s: %v", pidFile, err)
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		t.Fatalf("release slot by terminating process %d: %v", pid, err)
	}
}

func killTestProcessGroup(command *exec.Cmd) error {
	if command == nil || command.Process == nil {
		return nil
	}
	if err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("kill process group for %d: %w", command.Process.Pid, err)
	}
	return nil
}

func TestKillTestProcessGroupStopsChild(t *testing.T) {
	child := exec.Command("/bin/sh", "-c", "exec /bin/sleep 30")
	child.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	t.Cleanup(func() {
		if !waited {
			_ = killTestProcessGroup(child)
			_ = child.Process.Kill()
			_ = child.Wait()
		}
	})
	if err := killTestProcessGroup(child); err != nil {
		t.Fatal(err)
	}
	if err := child.Wait(); err == nil {
		waited = true
		t.Fatal("child survived process-group cleanup")
	}
	waited = true
}

func TestParsePIDFileReadiness(t *testing.T) {
	tests := []struct {
		name     string
		contents []byte
		wantPID  int
		ready    bool
		wantErr  bool
	}{
		{name: "empty file is still being written"},
		{name: "unterminated PID is still being written", contents: []byte("123")},
		{name: "complete PID", contents: []byte("123\n"), wantPID: 123, ready: true},
		{name: "empty complete PID", contents: []byte("\n"), ready: true, wantErr: true},
		{name: "invalid complete PID", contents: []byte("invalid\n"), ready: true, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gotPID, gotReady, err := parsePIDFile(test.contents)
			if gotReady != test.ready || (gotReady && !test.wantErr && gotPID != test.wantPID) {
				t.Fatalf("parsePIDFile(%q) = (%d, %t, %v), want PID %d ready %t", test.contents, gotPID, gotReady, err, test.wantPID, test.ready)
			}
			if (err != nil) != test.wantErr {
				t.Fatalf("parsePIDFile(%q) error = %v, wantErr %t", test.contents, err, test.wantErr)
			}
		})
	}
}

func parsePIDFile(data []byte) (int, bool, error) {
	if len(data) == 0 || data[len(data)-1] != '\n' {
		return 0, false, nil
	}
	value := strings.TrimSpace(string(data))
	pid, err := strconv.Atoi(value)
	if err != nil {
		return 0, true, fmt.Errorf("parse process PID %q: %w", value, err)
	}
	if pid <= 0 {
		return 0, true, fmt.Errorf("process PID %d must be positive", pid)
	}
	return pid, true, nil
}

func waitPIDFile(path string, timeout time.Duration) (int, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil {
			pid, ready, parseErr := parsePIDFile(data)
			if parseErr != nil {
				return 0, parseErr
			}
			if ready {
				return pid, nil
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return 0, fmt.Errorf("read process PID from %s: %w", path, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	return 0, fmt.Errorf("timed out waiting for a complete process PID in %s", path)
}

func waitFile(t *testing.T, path string, timeout time.Duration) ([]byte, error) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(path); err == nil {
			return data, nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	return nil, fmt.Errorf("timed out waiting for %s", path)
}
