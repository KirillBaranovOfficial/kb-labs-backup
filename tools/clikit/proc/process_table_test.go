//go:build darwin || linux

package proc

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

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
	return applyProcessMarkers(s.processes, candidates, runID, func(pid int, _ string) (bool, bool, error) {
		if pid == s.deniedPID {
			return false, false, syscall.EACCES
		}
		return false, false, nil
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
		{RunID: "first", Tag: "agent:stream:first", LatestTag: "stream", StateDir: state, Status: StatusDead, EnqueuedAt: base, ExitCode: &firstCode},
		{RunID: "latest", Tag: "resume:stream:latest", LatestTag: "stream", StateDir: state, Status: StatusDead, EnqueuedAt: base.Add(time.Second), ExitCode: &latestCode},
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
	marker, unverified, gone := processMarkerReadResult(nil, syscall.EACCES, runEnv+"=denied-run")
	if gone {
		return nil, nil
	}
	return []Process{{
		PID: 778, PPID: 1, Command: "detached-child", StartedAt: time.Now(),
		Identity: "child-start", runMarker: marker, Unverified: unverified,
	}}, nil
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
