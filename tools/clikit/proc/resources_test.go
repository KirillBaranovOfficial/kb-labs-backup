//go:build darwin || linux

package proc

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func countResourceEvents(t *testing.T, state string, kind EventType) int {
	t.Helper()
	var stream bytes.Buffer
	if err := Events(context.Background(), state, false, []EventType{kind}, &stream); err != nil {
		t.Fatal(err)
	}
	scanner := bufio.NewScanner(&stream)
	count := 0
	for scanner.Scan() {
		var event Event
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatal(err)
		}
		if event.Type == kind {
			count++
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return count
}

func writeAdmissionFixtureRecord(t *testing.T, state string, rec record) {
	t.Helper()
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(state, "runs", rec.RunID+".json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

type fakeResourceSource struct{ sample ResourceSnapshot }

func (source fakeResourceSource) Snapshot() ResourceSnapshot { return source.sample }

type mutableResourceSource struct {
	mu     sync.RWMutex
	sample ResourceSnapshot
}

func (source *mutableResourceSource) Snapshot() ResourceSnapshot {
	source.mu.RLock()
	defer source.mu.RUnlock()
	return source.sample
}

func (source *mutableResourceSource) set(sample ResourceSnapshot) {
	source.mu.Lock()
	source.sample = sample
	source.mu.Unlock()
}

func readAdmissionRecord(state, runID string) (record, error) {
	var result record
	err := withStateLock(state, func() error {
		var err error
		result, err = readRecordLocked(state, runID)
		return err
	})
	return result, err
}

func waitForAdmissionRecord(t *testing.T, state, runID string, check func(record) bool) record {
	t.Helper()
	timer := time.NewTimer(3 * time.Second)
	ticker := time.NewTicker(admissionPollInterval)
	defer timer.Stop()
	defer ticker.Stop()
	for {
		rec, err := readAdmissionRecord(state, runID)
		if err == nil && check(rec) {
			return rec
		}
		select {
		case <-ticker.C:
		case <-timer.C:
			t.Fatalf("run %s did not reach expected admission state; last record=%+v err=%v", runID, rec, err)
		}
	}
}

func TestParseResourceSize(t *testing.T) {
	got, err := ParseResourceSize("12G")
	if err != nil {
		t.Fatal(err)
	}
	if got != 12*1024*1024*1024 {
		t.Fatalf("ParseResourceSize(12G) = %d", got)
	}
}

func TestZeroMinFreeQuotaRemainsAnExplicitSetValue(t *testing.T) {
	state := t.TempDir()
	if err := SetQuota(state, "", "", "0"); err != nil {
		t.Fatal(err)
	}
	status, err := ShowQuota(state)
	if err != nil || !status.MinFreeSet || status.MinFree != 0 {
		t.Fatalf("zero min-free quota = %+v, err=%v", status, err)
	}
}

func TestPressureHysteresis(t *testing.T) {
	state := pressureState{}
	now := time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)
	if !state.blocks(true, now) {
		t.Fatal("elevated pressure did not block admission")
	}
	if !state.blocks(false, now.Add(time.Second)) {
		t.Fatal("pressure hold ended before ten seconds")
	}
	if state.blocks(false, now.Add(11*time.Second)) {
		t.Fatal("pressure hold remained after ten normal seconds")
	}
}

func TestResourceAdmissionAllowsOversizedAlone(t *testing.T) {
	if !resourceFits(resourceUsage{}, resourceUsage{MemoryBytes: 16, MinCPUs: 8}, resourceQuota{MemoryBytes: 4, CPUs: 2}) {
		t.Fatal("oversized run should be admitted when no resource-costed run is active")
	}
	if resourceFits(resourceUsage{MemoryBytes: 1, MinCPUs: 1}, resourceUsage{MemoryBytes: 16, MinCPUs: 8}, resourceQuota{MemoryBytes: 4, CPUs: 2}) {
		t.Fatal("oversized run should wait while another resource-costed run is active")
	}
	if resourceFits(resourceUsage{MinCPUs: 1}, resourceUsage{DiskBytes: 1}, resourceQuota{MemoryBytes: 4, CPUs: 0}) {
		t.Fatal("disk-costed run should not overlap an oversized CPU run")
	}
}

// case: S-237.17
func TestAdmissionAloneIgnoresMemoryAvailabilityButWaitsForPressure(t *testing.T) {
	identity, alive, err := ProcessIdentity(os.Getpid())
	if err != nil || !alive {
		t.Fatalf("identify test process for admission: alive=%t err=%v", alive, err)
	}
	normal := ResourceSnapshot{
		PhysicalMemory: 1000, AvailableMemory: 100, MemoryAvailableKnown: true,
		PressureAvailable: true, LoadAvailable: true, LogicalCPUs: 4, Pressure: "normal",
	}
	quotaAndCost := func(state string) Config {
		t.Helper()
		if err := SetQuota(state, "50B", "1", "0"); err != nil {
			t.Fatal(err)
		}
		return Config{StateDir: state, Tag: "alone", Command: []string{"/bin/true"}, MemoryBytes: 200, CPUsMin: 2, CPUsMax: 2}
	}

	t.Run("normal pressure admits despite quota and live-memory shortfall", func(t *testing.T) {
		state := t.TempDir()
		cfg := quotaAndCost(state)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := reserveAdmission(ctx, cfg, "alone-normal", os.Getpid(), identity, fakeResourceSource{sample: normal}); err != nil {
			t.Fatalf("admission with no active costed run: %v", err)
		}
		rec, err := readAdmissionRecord(state, "alone-normal")
		if err != nil || rec.Status != StatusStarting || rec.WaitReason != "" {
			t.Fatalf("alone admission record = %+v, err=%v", rec, err)
		}
		cleanupAdmission(state, rec.RunID, "test complete")
	})

	t.Run("elevated pressure holds until ten normal seconds", func(t *testing.T) {
		state := t.TempDir()
		cfg := quotaAndCost(state)
		elevated := normal
		elevated.Pressure = "memory warn"
		source := &mutableResourceSource{sample: elevated}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		done := make(chan error, 1)
		go func() { done <- reserveAdmission(ctx, cfg, "alone-elevated", os.Getpid(), identity, source) }()
		blocked := waitForAdmissionRecord(t, state, "alone-elevated", func(rec record) bool {
			return rec.Status == StatusBlocked && strings.HasPrefix(rec.WaitReason, "host pressure (")
		})
		if blocked.WaitReason != "host pressure (memory warn)" {
			t.Fatalf("elevated-pressure wait = %q", blocked.WaitReason)
		}
		source.set(normal)
		select {
		case err := <-done:
			t.Fatalf("run admitted before pressure hysteresis elapsed: %v", err)
		case <-time.After(300 * time.Millisecond):
		}
		duringHold, err := readAdmissionRecord(state, "alone-elevated")
		if err != nil || duringHold.Status != StatusBlocked || !strings.HasPrefix(duringHold.WaitReason, "host pressure") {
			t.Fatalf("normal-pressure hold record = %+v, err=%v", duringHold, err)
		}
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("admission after pressure hysteresis: %v", err)
			}
		case <-ctx.Done():
			t.Fatalf("admission did not resume after pressure hysteresis: %v", ctx.Err())
		}
		rec, err := readAdmissionRecord(state, "alone-elevated")
		if err != nil || rec.Status != StatusStarting {
			t.Fatalf("admitted record = %+v, err=%v", rec, err)
		}
		cleanupAdmission(state, rec.RunID, "test complete")
	})
}

// case: S-237.19
func TestNoticeChangesPersistWithoutDuplicatingBlockedEvents(t *testing.T) {
	state := t.TempDir()
	if err := os.MkdirAll(filepath.Join(state, "runs"), 0o700); err != nil {
		t.Fatal(err)
	}
	identity, alive, err := ProcessIdentity(os.Getpid())
	if err != nil || !alive {
		t.Fatalf("identify test process for admission: alive=%t err=%v", alive, err)
	}
	if err := SetQuota(state, "100B", "2", "0"); err != nil {
		t.Fatal(err)
	}
	writeAdmissionFixtureRecord(t, state, record{
		RunID: "active", QueueOrder: 1, PID: os.Getpid(), StartedAt: identity,
		Status: StatusRunning, MemoryBytes: 90, CPUsMin: 1,
	})
	unavailable := ResourceSnapshot{
		PhysicalMemory: 1000, AvailableMemory: 900, MemoryAvailableKnown: true,
		LoadAvailable: true, LogicalCPUs: 4, Unavailable: map[string]string{"memory pressure": "read denied"},
	}
	unavailable.PressureAvailable = false
	source := &mutableResourceSource{sample: unavailable}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	cfg := Config{StateDir: state, Tag: "notice-change", Command: []string{"/bin/true"}, MemoryBytes: 20, CPUsMin: 1, CPUsMax: 1}
	go func() { done <- reserveAdmission(ctx, cfg, "notice-change", os.Getpid(), identity, source) }()
	blocked := waitForAdmissionRecord(t, state, "notice-change", func(rec record) bool {
		return rec.Status == StatusBlocked && rec.WaitReason != "" && rec.ResourceNotice == "memory pressure unavailable: read denied"
	})
	waitReason := blocked.WaitReason
	available := unavailable
	available.Unavailable = nil
	available.PressureAvailable = true
	available.Pressure = "normal"
	source.set(available)
	waitForAdmissionRecord(t, state, "notice-change", func(rec record) bool {
		return rec.Status == StatusBlocked && rec.WaitReason == waitReason && rec.ResourceNotice == ""
	})
	secondUnavailable := unavailable
	secondUnavailable.Unavailable = map[string]string{"memory pressure": "read timed out"}
	source.set(secondUnavailable)
	latest := waitForAdmissionRecord(t, state, "notice-change", func(rec record) bool {
		return rec.Status == StatusBlocked && rec.WaitReason == waitReason && rec.ResourceNotice == "memory pressure unavailable: read timed out"
	})
	if latest.WaitReason != waitReason || latest.ResourceNotice != "memory pressure unavailable: read timed out" {
		t.Fatalf("latest notice was not persisted with the unchanged wait reason: %+v", latest)
	}
	if got := countResourceEvents(t, state, EventBlocked); got != 1 {
		t.Fatalf("notice-only changes emitted %d blocked events, want 1", got)
	}
	cancel()
	if err := <-done; err == nil {
		t.Fatal("cancelled blocked admission returned nil")
	}
}

// case: S-237.16
func TestResourceAdmissionBlocksMemoryAndKeepsCostedFIFO(t *testing.T) {
	state := t.TempDir()
	if err := os.MkdirAll(filepath.Join(state, "runs"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := SetQuota(state, "100B", "4", ""); err != nil {
		t.Fatal(err)
	}
	writeAdmissionFixtureRecord(t, state, record{RunID: "active", QueueOrder: 1, Status: StatusRunning, MemoryBytes: 80, CPUsMin: 1})
	source := fakeResourceSource{sample: ResourceSnapshot{PhysicalMemory: 1000, AvailableMemory: 900, MemoryAvailableKnown: true, LogicalCPUs: 8, Pressure: "normal", PressureAvailable: true, LoadAvailable: true}}
	second := record{RunID: "second", QueueOrder: 2, MemoryBytes: 30, CPUsMin: 1}
	reason, status, _, err := admissionStateLockedWithSource(state, second, source)
	if err != nil || status != StatusBlocked || !strings.HasPrefix(reason, "memory budget (") {
		t.Fatalf("second admission = %q, %s, err=%v", reason, status, err)
	}
	writeAdmissionFixtureRecord(t, state, record{RunID: "second", QueueOrder: 2, Status: StatusBlocked, MemoryBytes: 30, CPUsMin: 1})
	third := record{RunID: "third", QueueOrder: 3}
	reason, status, _, err = admissionStateLockedWithSource(state, third, source)
	if err != nil || status != StatusQueued || !strings.Contains(reason, "earlier run") {
		t.Fatalf("third admission = %q, %s, err=%v", reason, status, err)
	}
}

// case: S-237.24
func TestDiskReservationIsSubtractedBeforeAdmission(t *testing.T) {
	if diskFits(7, 3, 3, 2) {
		t.Fatal("7 - 3 reserved - 3 needed is below the 2 byte floor")
	}
	if !diskFits(8, 3, 3, 2) {
		t.Fatal("8 - 3 reserved - 3 needed meets the 2 byte floor")
	}
}

// case: S-237.25
func TestLearnedEstimateNeverLowersDeclaredCost(t *testing.T) {
	got := effectiveResourceUsage(resourceUsage{MemoryBytes: 4, MinCPUs: 2}, resourceUsage{MemoryBytes: 1, MinCPUs: 1})
	if got.MemoryBytes != 4 || got.MinCPUs != 2 {
		t.Fatalf("effective cost = %+v, want declared floor", got)
	}
}

// case: S-237.20
func TestLearnedEstimateUsesMaximumOfLatestFiveCompletedRuns(t *testing.T) {
	records := []record{
		{CostKey: "build", Status: StatusDead, QueueOrder: 1, PeakRSSBytes: 100, AverageCPU: 4},
		{CostKey: "build", Status: StatusDead, QueueOrder: 2, PeakRSSBytes: 10, AverageCPU: 1},
		{CostKey: "build", Status: StatusDead, QueueOrder: 3, PeakRSSBytes: 20, AverageCPU: 2},
		{CostKey: "other", Status: StatusDead, QueueOrder: 4, PeakRSSBytes: 200, AverageCPU: 8},
		{CostKey: "build", Status: StatusDead, QueueOrder: 5, PeakRSSBytes: 30, AverageCPU: 3},
		{CostKey: "build", Status: StatusDead, QueueOrder: 6, PeakRSSBytes: 40, AverageCPU: 1},
		{CostKey: "build", Status: StatusDead, QueueOrder: 7, PeakRSSBytes: 50, AverageCPU: 2},
	}
	got := learnedUsage(records, "build")
	if got.MemoryBytes != 50 || got.MinCPUs != 2 {
		t.Fatalf("learned usage = %+v, want latest-five maximum and matching CPU", got)
	}
	effective := effectiveResourceUsage(resourceUsage{MemoryBytes: 1, MinCPUs: 1}, got)
	if effective.MemoryBytes != 50 || effective.MinCPUs != 2 {
		t.Fatalf("effective estimate = %+v, want learned estimate above declaration", effective)
	}
}

// case: S-237.26
func TestAvailableMemoryIsIndependentAdmissionCondition(t *testing.T) {
	if !memoryUnavailable(8, 7) {
		t.Fatal("a run that exceeds live available memory must wait")
	}
	if memoryUnavailable(8, 8) {
		t.Fatal("a run that fits live available memory should proceed")
	}
}

// case: S-237.28
func TestOverBudgetThresholdEmitsOnlyOnce(t *testing.T) {
	var state overBudgetState
	if !state.observe(126, 100) || state.observe(130, 100) {
		t.Fatal("over-budget transition should emit once after 125 percent")
	}
}

// case: S-237.26
func TestAdmissionBlocksWhenLiveMemoryIsBelowNeed(t *testing.T) {
	state := t.TempDir()
	if err := os.MkdirAll(filepath.Join(state, "runs"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := SetQuota(state, "200B", "4", "0"); err != nil {
		t.Fatal(err)
	}
	writeAdmissionFixtureRecord(t, state, record{RunID: "active", QueueOrder: 1, Status: StatusRunning, MemoryBytes: 10, CPUsMin: 1})
	source := fakeResourceSource{sample: ResourceSnapshot{PhysicalMemory: 100, AvailableMemory: 50, MemoryAvailableKnown: true, LogicalCPUs: 4, Pressure: "normal", PressureAvailable: true, LoadAvailable: true}}
	candidate := record{RunID: "candidate", QueueOrder: 2, MemoryBytes: 80, CPUsMin: 1}
	reason, status, _, err := admissionStateLockedWithSource(state, candidate, source)
	if err != nil {
		t.Fatal(err)
	}
	if status != StatusBlocked || reason != "available memory budget (have 50B, needs 80B)" {
		t.Fatalf("admission = %q, %s", reason, status)
	}
}

// case: S-237.19
func TestUnreadablePressureIsReportedAndDoesNotBlockBudgetAdmission(t *testing.T) {
	state := t.TempDir()
	if err := os.MkdirAll(filepath.Join(state, "runs"), 0o700); err != nil {
		t.Fatal(err)
	}
	source := fakeResourceSource{sample: ResourceSnapshot{
		PhysicalMemory: 100, AvailableMemory: 80, MemoryAvailableKnown: true,
		LogicalCPUs: 4, Unavailable: map[string]string{"pressure": "permission denied"},
	}}
	if pressureSignalsAvailable(source.Snapshot()) {
		t.Fatal("unreadable pressure signal was classified as available/normal")
	}
	reason, status, _, err := admissionStateLockedWithSource(state, record{RunID: "candidate", QueueOrder: 1}, source)
	if err != nil || reason != "" || status != StatusStarting {
		t.Fatalf("unreadable pressure admission = %q, %s, err=%v", reason, status, err)
	}
	if got := resourceNotice(source.Snapshot()); got != "pressure unavailable: permission denied" {
		t.Fatalf("resource notice = %q", got)
	}
}

// case: S-237.18
func TestAdmissionPressureTransitionWritesEvent(t *testing.T) {
	state := t.TempDir()
	if err := os.MkdirAll(filepath.Join(state, "runs"), 0o700); err != nil {
		t.Fatal(err)
	}
	source := fakeResourceSource{sample: ResourceSnapshot{PhysicalMemory: 100, AvailableMemory: 50, MemoryAvailableKnown: true, LogicalCPUs: 4, Pressure: "memory warn", PressureAvailable: true, LoadAvailable: true}}
	var reason string
	var status RunStatus
	err := withStateLock(state, func() error {
		var err error
		reason, status, _, err = admissionStateLockedWithSource(state, record{RunID: "candidate", QueueOrder: 1}, source)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if status != StatusBlocked || reason != "host pressure (memory warn)" {
		t.Fatalf("admission = %q, %s", reason, status)
	}
	if countResourceEvents(t, state, EventPressure) != 1 {
		t.Fatal("pressure transition event missing")
	}
}

func TestConcurrentAdmissionObserversEmitOnePressureTransition(t *testing.T) {
	state := t.TempDir()
	if err := os.MkdirAll(filepath.Join(state, "runs"), 0o700); err != nil {
		t.Fatal(err)
	}
	source := fakeResourceSource{sample: ResourceSnapshot{
		PhysicalMemory: 100, AvailableMemory: 50, MemoryAvailableKnown: true,
		LogicalCPUs: 4, Pressure: "memory warn", PressureAvailable: true, LoadAvailable: true,
	}}
	const observers = 8
	start := make(chan struct{})
	errs := make(chan error, observers)
	for range observers {
		go func() {
			<-start
			errs <- withStateLock(state, func() error {
				_, _, _, err := admissionStateLockedWithSource(state, record{RunID: "candidate", QueueOrder: 1}, source)
				return err
			})
		}()
	}
	close(start)
	for range observers {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	if got := countResourceEvents(t, state, EventPressure); got != 1 {
		t.Fatalf("concurrent observers emitted %d pressure events, want 1", got)
	}
}

// case: S-237.18
func TestAdmissionResumesAfterNormalPressureHoldAndEmitsTransition(t *testing.T) {
	state := t.TempDir()
	if err := os.MkdirAll(filepath.Join(state, "runs"), 0o700); err != nil {
		t.Fatal(err)
	}
	normalSince := time.Now().Add(-pressureNormalDuration - time.Second)
	data, err := json.Marshal(pressureState{ElevatedAt: normalSince.Add(-time.Second), NormalAt: normalSince, Level: "host pressure (memory warn)"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(state, "pressure-state.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	source := fakeResourceSource{sample: ResourceSnapshot{PhysicalMemory: 100, AvailableMemory: 80, MemoryAvailableKnown: true, LogicalCPUs: 4, Pressure: "normal", PressureAvailable: true, LoadAvailable: true}}
	reason, status, _, err := admissionStateLockedWithSource(state, record{RunID: "candidate", QueueOrder: 1}, source)
	if err != nil || reason != "" || status != StatusStarting {
		t.Fatalf("admission after normal hold = %q, %s, err=%v", reason, status, err)
	}
	if countResourceEvents(t, state, EventPressure) != 1 {
		t.Fatal("normal pressure transition event missing")
	}
}

func TestPressureWaitReasonNamesElevatedSignal(t *testing.T) {
	got := pressureWaitReason(ResourceSnapshot{Pressure: "memory warn"})
	if got != "host pressure (memory warn)" {
		t.Fatalf("pressure wait reason = %q", got)
	}
}

// case: S-237.27
func TestCgroupFailureFormatsVisibleAdvisory(t *testing.T) {
	if got := limitsAdvisory("cgroup v2 create child: permission denied"); got != "limits advisory: cgroup v2 create child: permission denied" {
		t.Fatalf("cgroup advisory = %q", got)
	}
}

type metricsProcessSource struct{ processes []Process }

func (source metricsProcessSource) Snapshot(string) ([]Process, error) { return source.processes, nil }
func (metricsProcessSource) Identity(int) (string, bool, error)        { return "", false, nil }
func (metricsProcessSource) Signal(int, syscall.Signal) error          { return nil }

// case: S-237.21
func TestUnreadableProcessRSSMarksMeasurementPartial(t *testing.T) {
	rec := record{PID: 700, RunID: "run"}
	source := metricsProcessSource{processes: []Process{
		{PID: 700, PPID: 1, Identity: "keeper"},
		{PID: 701, PPID: 700, Identity: "readable", RSSBytes: 11, CPUSeconds: 0.2, MetricsAvailable: true},
		{PID: 702, PPID: 700, Identity: "unreadable"},
	}}
	rss, cpu, partial, err := sampleRunMetricsWithSource(rec, source)
	if err != nil || !partial || rss != 11 || cpu != 0.2 {
		t.Fatalf("sample = rss %d, cpu %v, partial %t, err %v", rss, cpu, partial, err)
	}
}

// case: S-237.21
func TestUnverifiedTreeSampleMakesMeasurementPartial(t *testing.T) {
	started := time.Now().Add(-time.Second)
	source := metricsProcessSource{processes: []Process{
		{PID: 700, PPID: 1, Identity: "keeper", StartedAt: started},
		{PID: 702, PPID: 1, Identity: "unverified", StartedAt: time.Now(), RSSBytes: 80, MetricsAvailable: true, Unverified: true},
	}}
	rss, _, partial, err := sampleRunMetricsWithSource(record{PID: 700, RunID: "run"}, source)
	if err != nil || !partial || rss != 0 {
		t.Fatalf("unverified sample = rss %d, partial %t, err %v", rss, partial, err)
	}
}

// case: S-237.28
func TestRecordedResourceObservationEmitsOneOverBudgetEvent(t *testing.T) {
	state := t.TempDir()
	if err := os.MkdirAll(filepath.Join(state, "runs"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeRecordAtomic(record{RunID: "run", Tag: "run", StateDir: state, Status: StatusRunning, MemoryBytes: 100}); err != nil {
		t.Fatal(err)
	}
	for _, rss := range []uint64{126, 130} {
		if _, err := recordResourceObservation(state, "run", rss, 1.0, false); err != nil {
			t.Fatal(err)
		}
	}
	if countResourceEvents(t, state, EventOverBudget) != 1 {
		t.Fatal("expected one over-budget event")
	}
	rec, err := readRecordLocked(state, "run")
	if err != nil || !rec.OverBudget || rec.PeakRSSBytes != 130 {
		t.Fatalf("resource record = %+v, err=%v", rec, err)
	}
}

// case: S-237.21
func TestPartialMeasurementNeverEntersCompleteLearningHistory(t *testing.T) {
	records := []record{{CostKey: "build", Status: StatusDead, PeakRSSBytes: 12, AverageCPU: 1, MeasurementPartial: true}}
	if learned := learnedUsage(records, "build"); learned.MemoryBytes != 0 {
		t.Fatalf("partial observation learned as complete: %+v", learned)
	}
}
