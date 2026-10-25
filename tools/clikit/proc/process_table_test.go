//go:build darwin || linux

package proc

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// case: S-237.70
func TestGovernorStopsAndContinuesOwnedChildTree(t *testing.T) {
	if outputPath := os.Getenv("KB_CLIPROC_GOVERNOR_TICKER"); outputPath != "" {
		file, err := os.OpenFile(outputPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			t.Fatalf("open child ticker output: %v", err)
		}
		defer file.Close()
		for {
			if _, err := file.Write([]byte("x")); err != nil {
				t.Fatalf("write child ticker output: %v", err)
			}
			if err := file.Sync(); err != nil {
				t.Fatalf("sync child ticker output: %v", err)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}

	outputPath := filepath.Join(t.TempDir(), "ticks")
	child := exec.Command(os.Args[0], "-test.run=^TestGovernorStopsAndContinuesOwnedChildTree$")
	child.Env = append(os.Environ(), "KB_CLIPROC_GOVERNOR_TICKER="+outputPath)
	child.Stdout = io.Discard
	child.Stderr = io.Discard
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = child.Process.Signal(syscall.SIGCONT); _ = child.Process.Kill(); _ = child.Wait() })
	waitForOutput := func(previous int64) int64 {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			info, statErr := os.Stat(outputPath)
			if statErr == nil && info.Size() > previous {
				return info.Size()
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("ticker child did not produce output after size %d", previous)
		return previous
	}
	waitForOutput(0)
	identity, alive, err := ProcessIdentity(child.Process.Pid)
	if err != nil || !alive {
		t.Fatalf("inspect child identity: alive=%t err=%v", alive, err)
	}
	keeperIdentity, alive, err := ProcessIdentity(os.Getpid())
	if err != nil || !alive {
		t.Fatalf("inspect test process identity: alive=%t err=%v", alive, err)
	}
	rec := record{RunID: "governor-test", Tag: "governor-test", PID: os.Getpid(), StartedAt: keeperIdentity,
		Observed: []Process{{PID: child.Process.Pid, PPID: os.Getpid(), Identity: identity}}}
	source := ownedProcessTreeSource{processes: rec.Observed}
	if err := signalRunTreeWithSource(rec, syscall.SIGSTOP, source); err != nil {
		t.Fatalf("pause process tree: %v", err)
	}
	waitForStopped := func(want bool) {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			stopped, present, inspectErr := testProcessStopped(child.Process.Pid)
			if inspectErr == nil && present && stopped == want {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		stopped, present, inspectErr := testProcessStopped(child.Process.Pid)
		t.Fatalf("child stopped state = %t present=%t err=%v, want stopped=%t", stopped, present, inspectErr, want)
	}
	waitForStopped(true)
	pausedSize := fileSize(t, outputPath)
	time.Sleep(200 * time.Millisecond)
	if got := fileSize(t, outputPath); got != pausedSize {
		t.Fatalf("stopped child produced output while paused: size %d -> %d", pausedSize, got)
	}
	if err := signalRunTreeWithSource(rec, syscall.SIGCONT, source); err != nil {
		t.Fatalf("resume process tree: %v", err)
	}
	waitForStopped(false)
	deadline := time.Now().Add(2 * time.Second)
	for fileSize(t, outputPath) == pausedSize && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := fileSize(t, outputPath); got <= pausedSize {
		t.Fatalf("resumed child produced no output: size stayed at %d", got)
	}
	if err := child.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		t.Fatalf("stop test child: %v", err)
	}
	if err := child.Wait(); err == nil {
		t.Fatal("sleep child unexpectedly exited successfully")
	}
}

func fileSize(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat ticker output: %v", err)
	}
	return info.Size()
}

type ownedProcessTreeSource struct{ processes []Process }

func (source ownedProcessTreeSource) Snapshot(string) ([]Process, error) {
	return source.processes, nil
}
func (ownedProcessTreeSource) Identity(pid int) (string, bool, error) { return ProcessIdentity(pid) }
func (ownedProcessTreeSource) Signal(pid int, signal syscall.Signal) error {
	return syscall.Kill(pid, signal)
}

func TestProcessMarkerCacheInspectsOnlyUnexplainedIdentityOnce(t *testing.T) {
	processes := []Process{
		{PID: 100, PPID: 1, Identity: "root-start", uid: 501},
		{PID: 101, PPID: 100, Identity: "child-start", uid: 501},
		{PID: 102, PPID: 1, Identity: "sibling-start", uid: 501},
		{PID: 103, PPID: 1, Identity: "other-user-start", uid: 0},
	}
	candidates := processInspectionCandidates(processes, 100, 501)
	if candidates[100] || candidates[101] || !candidates[102] || candidates[103] {
		t.Fatalf("marker candidates = %v, want only unexplained same-uid PID 102", candidates)
	}
	cache := newProcessMarkerCache()
	var inspections atomic.Int32
	inspect := func(pid int) (string, bool, error) {
		inspections.Add(1)
		return "run-102", false, nil
	}
	for i := 0; i < 10; i++ {
		processes = applyCachedProcessMarkers(processes, candidates, "run-102", cache, inspect)
	}
	if got := inspections.Load(); got != 1 {
		t.Fatalf("same process identity inspected %d times, want once", got)
	}
	processes[2].Identity = "sibling-exec-start"
	candidates = processInspectionCandidates(processes, 100, 501)
	applyCachedProcessMarkers(processes, candidates, "run-102", cache, inspect)
	if got := inspections.Load(); got != 2 {
		t.Fatalf("new start identity inspection count = %d, want 2", got)
	}
}

type failingProcessSource struct{ err error }

func (s failingProcessSource) Snapshot(string) ([]Process, error) { return nil, s.err }
func (failingProcessSource) Identity(int) (string, bool, error)   { return "", false, nil }
func (failingProcessSource) Signal(int, syscall.Signal) error     { return nil }

type emptyProcessSource struct{}

func (emptyProcessSource) Snapshot(string) ([]Process, error) { return nil, nil }
func (emptyProcessSource) Identity(int) (string, bool, error) { return "", false, nil }
func (emptyProcessSource) Signal(int, syscall.Signal) error   { return nil }

func TestLostRunWithoutRecordedTreeIsNotClean(t *testing.T) {
	cleanup := cleanupLostRun(record{PID: 777, RunID: "lost-run"}, emptyProcessSource{})
	if cleanup.err == nil || !strings.Contains(cleanup.err.Error(), "no persisted owned process identity") {
		t.Fatalf("lost run with an empty observation was reported clean: %+v", cleanup)
	}
}

func TestTreeCleanupReportsProcessTableObservationError(t *testing.T) {
	observationErr := errors.New("process table denied")
	result := cleanupTreeWithSource(777, "run", nil, failingProcessSource{err: observationErr})
	if !errors.Is(result.err, observationErr) {
		t.Fatalf("tree cleanup error = %v, want wrapped observation error", result.err)
	}
	if len(result.Reaped) != 0 || len(result.Unverified) != 0 {
		t.Fatalf("failed observation was reported as a clean/empty tree: %+v", result)
	}
}

func TestProcessTreeExcludesUnverifiedSameUIDWithoutOwnershipEvidence(t *testing.T) {
	started := time.Now()
	tree := processTreeFromSnapshot([]Process{
		{PID: 777, PPID: 1, Command: "keeper", StartedAt: started, Identity: "keeper", uid: 501},
		{PID: 778, PPID: 1, Command: "launchd-sibling", StartedAt: started.Add(time.Second), Identity: "sibling", uid: 501, Unverified: true},
	}, 777)
	if len(tree) != 0 {
		t.Fatalf("same-uid process without ancestry, history, or marker was added to tree: %+v", tree)
	}
}

type failedHistoryIdentitySource struct {
	processes []Process
	err       error
}

func (s failedHistoryIdentitySource) Snapshot(string) ([]Process, error) { return s.processes, nil }
func (s failedHistoryIdentitySource) Identity(int) (string, bool, error) {
	return "", false, s.err
}
func (failedHistoryIdentitySource) Signal(int, syscall.Signal) error { return nil }

func TestProcessTreeRetainsHistoryAsUnverifiedWhenIdentityReadFails(t *testing.T) {
	identityErr := errors.New("identity denied")
	observed := []Process{{PID: 778, PPID: 777, Command: "detached-child", Identity: "child-start"}}
	tree, err := processTreeWithHistorySource(failedHistoryIdentitySource{
		processes: []Process{{PID: 777, PPID: 1, Command: "keeper", Identity: "keeper-start"}},
		err:       identityErr,
	}, 777, "run", observed)
	if !errors.Is(err, identityErr) {
		t.Fatalf("history identity error = %v, want %v", err, identityErr)
	}
	if len(tree) != 1 || tree[0].PID != 778 || !tree[0].Unverified {
		t.Fatalf("history entry after identity failure = %+v, want PID 778 unverified", tree)
	}
}

type deniedCandidateSource struct {
	processes []Process
	deniedPID int
	uid       uint32
}

func (s *deniedCandidateSource) Snapshot(runID string) ([]Process, error) {
	candidates := processInspectionCandidates(s.processes, 777, s.uid)
	return applyCachedProcessMarkers(s.processes, candidates, runID, newProcessMarkerCache(), func(pid int) (string, bool, error) {
		if pid == s.deniedPID {
			return "", false, syscall.EACCES
		}
		return "", false, nil
	}), nil
}

func (s *deniedCandidateSource) Identity(pid int) (string, bool, error) {
	for _, process := range s.processes {
		if process.PID == pid {
			return process.Identity, true, nil
		}
	}
	return "", false, nil
}

func (*deniedCandidateSource) Signal(int, syscall.Signal) error { return nil }

func newDeniedCandidateSource(deniedPID int) *deniedCandidateSource {
	started := time.Now().Add(-time.Second)
	return &deniedCandidateSource{uid: 501, deniedPID: deniedPID, processes: []Process{
		{PID: 777, PPID: 1, Command: "keeper", StartedAt: started, Identity: "keeper", uid: 501},
		{PID: 778, PPID: 1, Command: "foreign-unrelated", StartedAt: started.Add(time.Millisecond), Identity: "foreign", uid: 0},
		{PID: 779, PPID: 1, Command: "same-user-unrelated", StartedAt: started.Add(time.Millisecond), Identity: "same-user", uid: 501},
		{PID: 780, PPID: 777, Command: "foreign-descendant", StartedAt: started.Add(time.Millisecond), Identity: "descendant", uid: 0},
	}}
}

// case: S-237.13
func TestForeignUIDDenialOutsideAncestryDoesNotDirtyTeardown(t *testing.T) {
	source := newDeniedCandidateSource(778)
	source.processes[3].PPID = 1
	result := cleanupTreeWithSource(777, "denied-run", nil, source)
	if result.err != nil || len(result.Unverified) != 0 || len(result.Remaining) != 0 {
		t.Fatalf("foreign unrelated process made teardown unclean: %+v", result)
	}
}

// case: S-237.13
func TestSameUIDDenialWithoutHistoryDoesNotDirtyTeardown(t *testing.T) {
	source := newDeniedCandidateSource(779)
	source.processes[3].PPID = 1
	result := cleanupTreeWithSource(777, "denied-run", nil, source)
	if result.err != nil || len(result.Unverified) != 0 || len(result.Remaining) != 0 {
		t.Fatalf("same-uid process without history made teardown unclean: %+v", result)
	}
}

// case: S-237.13
func TestAncestryDenialKeepsCurrentDescendantOwned(t *testing.T) {
	source := newDeniedCandidateSource(780)
	snapshot, err := source.Snapshot("denied-run")
	if err != nil {
		t.Fatal(err)
	}
	tree := processTreeFromSnapshot(snapshot, 777)
	for _, process := range tree {
		if process.PID == 780 {
			if process.Unverified {
				t.Fatalf("current ancestry descendant marked unverified after marker denial: %+v", process)
			}
			return
		}
	}
	t.Fatalf("current ancestry descendant omitted after marker denial: %+v", tree)
}

// case: S-237.14
func TestWaitLatestTagSelectsNewestTerminalRun(t *testing.T) {
	state := t.TempDir()
	if err := os.MkdirAll(filepath.Join(state, "runs"), 0o700); err != nil {
		t.Fatal(err)
	}
	firstCode, latestCode := 0, 7
	base := time.Now().Add(-time.Minute)
	for _, rec := range []record{
		{RunID: "first", Tag: "agent:stream:first", LatestTag: "stream", StateDir: state, Status: StatusCompleted, EnqueuedAt: base, ExitCode: &firstCode},
		{RunID: "latest", Tag: "resume:stream:latest", LatestTag: "stream", StateDir: state, Status: StatusFailed, EnqueuedAt: base.Add(time.Second), ExitCode: &latestCode},
	} {
		if err := writeRecordAtomic(rec); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	info, err := WaitLatestTag(ctx, state, "stream")
	if err != nil {
		t.Fatal(err)
	}
	if info.RunID != "latest" || info.ExitCode == nil || *info.ExitCode != 7 {
		t.Fatalf("latest run = %+v, want terminal run latest with exit 7", info)
	}
}

type deniedEnvironmentSource struct {
	signaled []int
}

func (s *deniedEnvironmentSource) Snapshot(string) ([]Process, error) {
	processes := []Process{{
		PID: 778, PPID: 1, Command: "detached-child", StartedAt: time.Now(),
		Identity: "child-start", uid: uint32(os.Getuid()),
	}}
	return applyCachedProcessMarkers(processes, map[int]bool{778: true}, "denied-run", newProcessMarkerCache(), func(int) (string, bool, error) {
		return "", false, syscall.EACCES
	}), nil
}

func (*deniedEnvironmentSource) Identity(int) (string, bool, error) {
	return "child-start", true, nil
}

func (s *deniedEnvironmentSource) Signal(pid int, _ syscall.Signal) error {
	s.signaled = append(s.signaled, pid)
	return nil
}

// case: S-237.13
func TestDeniedEnvironmentWithoutHistoryDoesNotDirtyTeardown(t *testing.T) {
	source := &deniedEnvironmentSource{}
	result := cleanupTreeWithSource(777, "denied-run", nil, source)
	if result.err != nil || len(result.Unverified) != 0 || len(result.Remaining) != 0 {
		t.Fatalf("unrelated hidden-environment process made teardown unclean: %+v", result)
	}
	if len(source.signaled) != 0 {
		t.Fatalf("unrelated process was signaled: %v", source.signaled)
	}
}

type lateDescendantSource struct {
	snapshots int
	alive     bool
	signals   []syscall.Signal
}

func (s *lateDescendantSource) Snapshot(string) ([]Process, error) {
	s.snapshots++
	if s.snapshots == 1 || !s.alive {
		return nil, nil
	}
	return []Process{{
		PID: 779, PPID: 1, Command: "late-detached-child", StartedAt: time.Now(),
		Identity: "late-child-start", runMarker: true,
	}}, nil
}

func (s *lateDescendantSource) Identity(pid int) (string, bool, error) {
	if pid == 779 && s.alive {
		return "late-child-start", true, nil
	}
	return "", false, nil
}

func (s *lateDescendantSource) Signal(pid int, signal syscall.Signal) error {
	if pid != 779 {
		return errors.New("cleanup signaled an unexpected process")
	}
	s.signals = append(s.signals, signal)
	s.alive = false
	return nil
}

// case: S-237.1
func TestCleanupConvergesOnDescendantAfterInitialSnapshot(t *testing.T) {
	source := &lateDescendantSource{alive: true}
	result := cleanupTreeWithSource(777, "late-run", nil, source)
	if result.err != nil {
		t.Fatalf("tree cleanup error = %v", result.err)
	}
	if len(source.signals) == 0 || source.signals[0] != syscall.SIGTERM {
		t.Fatalf("late descendant signals = %v, want SIGTERM", source.signals)
	}
	if len(result.Reaped) != 1 || result.Reaped[0].PID != 779 {
		t.Fatalf("reaped = %+v, want late descendant PID 779", result.Reaped)
	}
	if len(result.Remaining) != 0 {
		t.Fatalf("remaining = %+v, want empty after convergence", result.Remaining)
	}
	if source.snapshots < 4 {
		t.Fatalf("snapshot count = %d, want initial empty, late descendant, and two empty convergence observations", source.snapshots)
	}
}
