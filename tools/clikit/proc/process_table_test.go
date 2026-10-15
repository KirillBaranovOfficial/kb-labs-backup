//go:build darwin || linux

package proc

import (
	"errors"
	"strings"
	"syscall"
	"testing"
	"time"
)

type failingProcessSource struct{ err error }

func (s failingProcessSource) Snapshot(string) ([]Process, error) { return nil, s.err }
func (failingProcessSource) Identity(int) (string, bool, error)   { return "", false, nil }
func (failingProcessSource) Signal(int, syscall.Signal) error     { return nil }

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

// case: S-237.1
func TestDeniedEnvironmentMarkerMakesTeardownUnverified(t *testing.T) {
	source := &deniedEnvironmentSource{}
	result := cleanupTreeWithSource(777, "denied-run", nil, source)
	if result.err == nil || !strings.Contains(result.err.Error(), "unverified") {
		t.Fatalf("tree cleanup error = %v, want unverified observation", result.err)
	}
	if strings.Contains(result.err.Error(), "deadline") {
		t.Fatalf("cleanup reported a deadline before exhausting the bound: %v", result.err)
	}
	if len(result.Unverified) != 1 || result.Unverified[0].PID != 778 {
		t.Fatalf("unverified tree = %+v, want PID 778", result.Unverified)
	}
	if len(result.Remaining) != 1 || result.Remaining[0].PID != 778 {
		t.Fatalf("remaining = %+v, want unverified PID 778", result.Remaining)
	}
	if len(source.signaled) != 0 {
		t.Fatalf("unverified process was signaled: %v", source.signaled)
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
