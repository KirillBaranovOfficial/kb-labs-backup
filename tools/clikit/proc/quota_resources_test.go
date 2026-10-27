//go:build darwin || linux

package proc

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/kb-labs/clikit/internal/testdeadline"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func countResourceEvents(t *testing.T, state string, kind EventType) int {
	t.Helper()
	var stream bytes.Buffer
	if err := Events(context.Background(), state, false, []EventType{kind}, 0, &stream); err != nil {
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

type fakeHostResourceSource struct{ sample HostResourceSnapshot }

func (source fakeHostResourceSource) Snapshot() HostResourceSnapshot { return source.sample }

type sequenceHostResourceSource struct {
	mu        sync.Mutex
	samples   []HostResourceSnapshot
	snapshots int
}

func (source *sequenceHostResourceSource) Snapshot() HostResourceSnapshot {
	source.mu.Lock()
	defer source.mu.Unlock()
	index := source.snapshots
	source.snapshots++
	if index >= len(source.samples) {
		index = len(source.samples) - 1
	}
	return source.samples[index]
}

type mutableHostResourceSource struct {
	mu     sync.RWMutex
	sample HostResourceSnapshot
}

func (source *mutableHostResourceSource) Snapshot() HostResourceSnapshot {
	source.mu.RLock()
	defer source.mu.RUnlock()
	return source.sample
}

func (source *mutableHostResourceSource) set(sample HostResourceSnapshot) {
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
	timer := time.NewTimer(testdeadline.Budget(t))
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

// case: S-237.71
func TestGovernorHardPressureUsesSwapMemoryAndDiskSignals(t *testing.T) {
	normal := HostResourceSnapshot{PhysicalMemory: 1000, AvailableMemory: 500, MemoryAvailableKnown: true, Pressure: "normal", PressureAvailable: true}
	if got := governorHardPressure(normal, HostResourceSnapshot{}, 500, 100); got != "" {
		t.Fatalf("normal host decision = %q", got)
	}
	if got := governorHardPressure(HostResourceSnapshot{Pressure: "memory critical", PressureAvailable: true}, normal, 500, 100); got == "" {
		t.Fatal("critical memory pressure did not trigger pacing")
	}
	if got := governorHardPressure(HostResourceSnapshot{SwapOuts: 11, SwapOutsKnown: true}, HostResourceSnapshot{SwapOuts: 10, SwapOutsKnown: true}, 500, 100); got == "" {
		t.Fatal("growing swapouts did not trigger pacing")
	}
	if got := governorHardPressure(normal, HostResourceSnapshot{}, 99, 100); got == "" {
		t.Fatal("free disk below the floor did not trigger pacing")
	}
}

// case: S-237.69
func TestGovernorAdmissionWaitsOnlyForHeavyCostOnHostPressure(t *testing.T) {
	sample := HostResourceSnapshot{Pressure: "memory warn", PressureAvailable: true, LoadAvailable: true, MemoryAvailableKnown: true}
	if got := governorAdmissionReason(CostLight, sample, 1000, 100); got != "" {
		t.Fatalf("light admission was blocked: %q", got)
	}
	if got := governorAdmissionReason(CostHeavy, sample, 1000, 100); got == "" {
		t.Fatal("heavy admission did not wait on elevated pressure")
	}
	if got := governorAdmissionReason(CostHeavy, HostResourceSnapshot{}, 99, 100); got == "" {
		t.Fatal("heavy admission did not wait below disk floor")
	}
}

// case: S-237.69
func TestSecondHeavyAdmissionWaitsWithVisibleHostPressureReason(t *testing.T) {
	state := t.TempDir()
	identity, alive, err := ProcessIdentity(os.Getpid())
	if err != nil || !alive {
		t.Fatalf("inspect test process: alive=%t err=%v", alive, err)
	}
	first := record{RunID: "first-heavy", Tag: "first-heavy", StateDir: state, PID: os.Getpid(), StartedAt: identity,
		Status: StatusRunning, CostClass: CostHeavy, Priority: 10, QueueOrder: 1, EnqueuedAt: time.Now().Add(-time.Minute)}
	if err := writeRecordAtomic(first); err != nil {
		t.Fatal(err)
	}
	pressure := mutableHostResourceSource{sample: HostResourceSnapshot{Pressure: "memory warn", PressureAvailable: true, LoadAvailable: true,
		MemoryAvailableKnown: true, PhysicalMemory: 1 << 30, AvailableMemory: 1 << 29, LogicalCPUs: 4}}
	cfg := Config{StateDir: state, Tag: "second-heavy", Command: []string{"/bin/true"}, CostClass: CostHeavy, Priority: 10}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- reserveAdmission(ctx, cfg, "second-heavy", os.Getpid(), identity, &pressure) }()
	deadline := time.Now().Add(testdeadline.Budget(t))
	var waiting record
	for time.Now().Before(deadline) {
		waiting, err = readAdmissionRecord(state, "second-heavy")
		if err == nil && waiting.Status == StatusBlocked {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if waiting.Status != StatusBlocked || !strings.Contains(waiting.WaitReason, "waiting for memory pressure") {
		cancel()
		<-result
		t.Fatalf("second heavy run did not expose pressure wait: %+v, err=%v", waiting, err)
	}
	cancel()
	if err := <-result; err == nil {
		t.Fatal("cancelled pressure wait returned success")
	}
	data, err := os.ReadFile(filepath.Join(state, "events.jsonl"))
	if err != nil || !strings.Contains(string(data), `"type":"blocked"`) || !strings.Contains(string(data), `"reason":"waiting for memory pressure`) {
		t.Fatalf("pressure wait event missing reason: %s err=%v", data, err)
	}
}

// case: S-237.70
func TestGovernorSelectsLowestPriorityHeavyRun(t *testing.T) {
	records := []record{
		{RunID: "light", CostClass: CostLight, Status: StatusRunning, Priority: -1, EnqueuedAt: time.Unix(1, 0)},
		{RunID: "low", CostClass: CostHeavy, Status: StatusRunning, Priority: 1, QueueOrder: 1, EnqueuedAt: time.Unix(3, 0)},
		{RunID: "high", CostClass: CostHeavy, Status: StatusRunning, Priority: 10, QueueOrder: 2, EnqueuedAt: time.Unix(2, 0)},
	}
	got := lowestPriorityHeavy(records)
	if got == nil || got.RunID != "low" {
		t.Fatalf("lowest priority run = %+v, want priority 1 heavy run", got)
	}
}

// case: S-237.71
func TestGovernorPausesLowestPriorityRunAndResumesAfterDiskFloorRestored(t *testing.T) {
	state := t.TempDir()
	owned := make([]*exec.Cmd, 0, 2)
	var processes []Process
	records := make([]record, 0, 2)
	for index, priority := range []int{1, 10} {
		pidPath := filepath.Join(state, fmt.Sprintf("child-%d.pid", index))
		cmd := exec.Command("/bin/sh", "-c", `sleep 30 & echo $! > "$1"; wait`, "governor-fixture", pidPath)
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		owned = append(owned, cmd)
		identity, alive, err := ProcessIdentity(cmd.Process.Pid)
		if err != nil || !alive {
			t.Fatalf("inspect shell identity: alive=%t err=%v", alive, err)
		}
		var childPID int
		deadline := time.Now().Add(testdeadline.Budget(t))
		for time.Now().Before(deadline) {
			data, readErr := os.ReadFile(pidPath)
			if readErr == nil {
				childPID, _ = strconv.Atoi(strings.TrimSpace(string(data)))
				if childPID > 0 {
					break
				}
			}
			time.Sleep(10 * time.Millisecond)
		}
		if childPID <= 0 {
			t.Fatalf("shell %d did not report its child PID", cmd.Process.Pid)
		}
		childIdentity, childAlive, err := ProcessIdentity(childPID)
		if err != nil || !childAlive {
			t.Fatalf("inspect child identity: alive=%t err=%v", childAlive, err)
		}
		root := Process{PID: cmd.Process.Pid, PPID: os.Getpid(), Identity: identity}
		child := Process{PID: childPID, PPID: cmd.Process.Pid, Identity: childIdentity}
		processes = append(processes, root, child)
		rec := record{RunID: fmt.Sprintf("heavy-%d", index), Tag: fmt.Sprintf("heavy-%d", index), StateDir: state,
			PID: cmd.Process.Pid, StartedAt: identity, Status: StatusRunning, CostClass: CostHeavy, Priority: priority,
			MinFree: 100, StartedAtTime: time.Now().Add(-time.Minute), Observed: []Process{child}}
		records = append(records, rec)
		if err := writeRecordAtomic(rec); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		for _, cmd := range owned {
			_ = cmd.Process.Signal(syscall.SIGCONT)
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})
	procSource := ownedProcessTreeSource{processes: processes}
	pressure := mutableHostResourceSource{sample: HostResourceSnapshot{PhysicalMemory: 1 << 30, AvailableMemory: 1 << 29, MemoryAvailableKnown: true,
		Pressure: "normal", PressureAvailable: true, LoadAvailable: true, LogicalCPUs: 4, FreeDiskKnown: true, FreeDiskBytes: 0}}
	if err := reconcileHostGovernorWithProcessSource(state, &pressure, procSource); err != nil {
		t.Fatalf("pause under fake pressure: %v", err)
	}
	paused, err := readRecordLocked(state, records[0].RunID)
	if err != nil || paused.Status != StatusPaused {
		t.Fatalf("priority 1 record = %+v, err=%v", paused, err)
	}
	listing := List(state)
	if listing.Error != nil {
		t.Fatalf("list governor state as ps JSON: %v", listing.Error)
	}
	psJSON, err := json.Marshal(listing)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("ps --json governor snapshot: %s", psJSON)
	var lowPriorityRun *RunInfo
	for index := range listing.Runs {
		if listing.Runs[index].RunID == records[0].RunID {
			lowPriorityRun = &listing.Runs[index]
			break
		}
	}
	if lowPriorityRun == nil || !lowPriorityRun.Paused || lowPriorityRun.WaitReason == "" || listing.GovernorDecision != "paused heavy-0" {
		t.Fatalf("ps listing did not expose paused run and pressure decision: %+v", listing)
	}
	higher, err := readRecordLocked(state, records[1].RunID)
	if err != nil || higher.Status != StatusRunning {
		t.Fatalf("priority 10 record = %+v, err=%v", higher, err)
	}
	pressure.sample.FreeDiskBytes = 1000
	if err := reconcileHostGovernorWithProcessSource(state, &pressure, procSource); err != nil {
		t.Fatalf("observe restored disk floor: %v", err)
	}
	higher, err = readRecordLocked(state, records[1].RunID)
	if err != nil || higher.Status != StatusRunning {
		t.Fatalf("governor paused another run during recovery: %+v, err=%v", higher, err)
	}
	gov, err := readGovernorState(state)
	if err != nil {
		t.Fatal(err)
	}
	if gov.Decision != "recovering" {
		t.Fatalf("governor recovery decision = %q, want recovering", gov.Decision)
	}
	gov.Hold.NormalAt = time.Now().Add(-pressureNormalDuration - time.Second)
	if err := writeGovernorState(state, gov); err != nil {
		t.Fatal(err)
	}
	paused, err = readRecordLocked(state, records[0].RunID)
	if err != nil {
		t.Fatal(err)
	}
	paused.PausedAt = time.Now().Add(-time.Second)
	if err := writeRecordAtomic(paused); err != nil {
		t.Fatal(err)
	}
	if err := reconcileHostGovernorWithProcessSource(state, &pressure, procSource); err != nil {
		t.Fatalf("resume after disk floor restores: %v", err)
	}
	resumed, err := readRecordLocked(state, records[0].RunID)
	if err != nil || resumed.Status != StatusRunning || resumed.PausedMS < 900 {
		t.Fatalf("resumed record = %+v, err=%v", resumed, err)
	}
	data, err := os.ReadFile(filepath.Join(state, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	var pausedEvent, resumedEvent Event
	for scanner.Scan() {
		var event Event
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatal(err)
		}
		if event.RunID != records[0].RunID {
			continue
		}
		if event.Type == EventPaused {
			pausedEvent = event
		}
		if event.Type == EventResumed {
			resumedEvent = event
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if pausedEvent.Type != EventPaused || !strings.Contains(pausedEvent.Reason, "free disk") || pausedEvent.DiskFreeBytes != 0 || pausedEvent.DiskNeedBytes != 100 || resumedEvent.Type != EventResumed || resumedEvent.DurationMS < 900 {
		t.Fatalf("pause/resume events missing pressure reason or duration: paused=%+v resumed=%+v", pausedEvent, resumedEvent)
	}
}

func TestResourceAdmissionAllowsOversizedAlone(t *testing.T) {
	if !quotaFits(quotaUsage{}, quotaUsage{MemoryBytes: 16, MinCPUs: 8}, hostQuota{MemoryBytes: 4, CPUs: 2}) {
		t.Fatal("oversized run should be admitted when no resource-costed run is active")
	}
	if quotaFits(quotaUsage{MemoryBytes: 1, MinCPUs: 1}, quotaUsage{MemoryBytes: 16, MinCPUs: 8}, hostQuota{MemoryBytes: 4, CPUs: 2}) {
		t.Fatal("oversized run should wait while another resource-costed run is active")
	}
	if quotaFits(quotaUsage{MinCPUs: 1}, quotaUsage{DiskBytes: 1}, hostQuota{MemoryBytes: 4, CPUs: 0}) {
		t.Fatal("disk-costed run should not overlap an oversized CPU run")
	}
}

// case: S-237.29
func TestAdmissionAloneIgnoresMemoryAvailabilityButWaitsForPressure(t *testing.T) {
	identity, alive, err := ProcessIdentity(os.Getpid())
	if err != nil || !alive {
		t.Fatalf("identify test process for admission: alive=%t err=%v", alive, err)
	}
	normal := HostResourceSnapshot{
		PhysicalMemory: 1000, AvailableMemory: 100, MemoryAvailableKnown: true,
		PressureAvailable: true, LoadAvailable: true, LogicalCPUs: 4, Pressure: "normal",
	}
	quotaAndCost := func(state string) Config {
		t.Helper()
		if err := SetQuota(state, "50B", "1", "0"); err != nil {
			t.Fatal(err)
		}
		return Config{StateDir: state, Tag: "alone", Command: []string{"/bin/true"}, CostClass: CostHeavy, MemoryBytes: 200, CPUsMin: 2, CPUsMax: 2}
	}

	t.Run("normal pressure admits despite quota and live-memory shortfall", func(t *testing.T) {
		state := t.TempDir()
		cfg := quotaAndCost(state)
		ctx, cancel := context.WithTimeout(context.Background(), testdeadline.Budget(t))
		defer cancel()
		if err := reserveAdmission(ctx, cfg, "alone-normal", os.Getpid(), identity, fakeHostResourceSource{sample: normal}); err != nil {
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
		source := &mutableHostResourceSource{sample: elevated}
		ctx, cancel := context.WithTimeout(context.Background(), testdeadline.Budget(t))
		defer cancel()
		done := make(chan error, 1)
		go func() { done <- reserveAdmission(ctx, cfg, "alone-elevated", os.Getpid(), identity, source) }()
		blocked := waitForAdmissionRecord(t, state, "alone-elevated", func(rec record) bool {
			return rec.Status == StatusBlocked && strings.Contains(rec.WaitReason, "waiting for memory pressure")
		})
		if !strings.Contains(blocked.WaitReason, "drop below warn") || !strings.Contains(blocked.WaitReason, "current: warn since") {
			t.Fatalf("elevated-pressure wait = %q", blocked.WaitReason)
		}
		source.set(normal)
		select {
		case err := <-done:
			t.Fatalf("run admitted before pressure hysteresis elapsed: %v", err)
		case <-time.After(testdeadline.Scale(t, pressureNormalDuration/10)):
		}
		duringHold, err := readAdmissionRecord(state, "alone-elevated")
		if err != nil || duringHold.Status != StatusBlocked || !strings.Contains(duringHold.WaitReason, "hysteresis current: warn since") {
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

// case: S-237.31
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
	unavailable := HostResourceSnapshot{
		PhysicalMemory: 1000, AvailableMemory: 900, MemoryAvailableKnown: true,
		LoadAvailable: true, LogicalCPUs: 4, Unavailable: map[string]string{"memory pressure": "read denied"},
	}
	unavailable.PressureAvailable = false
	source := &mutableHostResourceSource{sample: unavailable}
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

// case: S-237.28
func TestResourceAdmissionBlocksMemoryAndKeepsCostedFIFO(t *testing.T) {
	state := t.TempDir()
	if err := os.MkdirAll(filepath.Join(state, "runs"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := SetQuota(state, "100B", "4", ""); err != nil {
		t.Fatal(err)
	}
	writeAdmissionFixtureRecord(t, state, record{RunID: "active", QueueOrder: 1, Status: StatusRunning, MemoryBytes: 80, CPUsMin: 1})
	source := fakeHostResourceSource{sample: HostResourceSnapshot{PhysicalMemory: 1000, AvailableMemory: 900, MemoryAvailableKnown: true, LogicalCPUs: 8, Pressure: "normal", PressureAvailable: true, LoadAvailable: true}}
	second := record{RunID: "second", QueueOrder: 2, MemoryBytes: 30, CPUsMin: 1}
	decision, err := admissionStateLocked(state, second, source)
	if err != nil || decision.Status != StatusBlocked || !strings.HasPrefix(decision.Reason, "memory budget (") {
		t.Fatalf("second admission = %+v, err=%v", decision, err)
	}
	writeAdmissionFixtureRecord(t, state, record{RunID: "second", QueueOrder: 2, Status: StatusBlocked, MemoryBytes: 30, CPUsMin: 1})
	third := record{RunID: "third", QueueOrder: 3, MemoryBytes: 30, CPUsMin: 1}
	decision, err = admissionStateLocked(state, third, source)
	if err != nil || decision.Status != StatusQueued || !strings.Contains(decision.Reason, "earlier run") {
		t.Fatalf("third admission = %+v, err=%v", decision, err)
	}
}

// case: S-237.51
func TestAdmissionFIFOOnlyOrdersMatchingResourceBudgets(t *testing.T) {
	cases := []struct {
		name          string
		candidate     record
		earlier       record
		globalMinFree uint64
		want          bool
	}{
		{name: "same memory budget", candidate: record{MemoryBytes: 10}, earlier: record{MemoryBytes: 10}, want: true},
		{name: "different budget dimensions", candidate: record{CPUsMin: 1}, earlier: record{MemoryBytes: 10}},
		{name: "same slot group", candidate: record{SlotGroup: "workers"}, earlier: record{SlotGroup: "workers"}, want: true},
		{name: "same gate", candidate: record{WaitGate: "batch"}, earlier: record{HoldGate: "batch"}, want: true},
		{name: "global min-free disk budget", candidate: record{}, earlier: record{DiskBytes: 10}, globalMinFree: 1, want: true},
		{name: "unrelated run", candidate: record{}, earlier: record{WaitGate: "batch"}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := admissionResourcesOverlap(test.candidate, test.earlier, test.globalMinFree); got != test.want {
				t.Fatalf("admissionResourcesOverlap() = %t, want %t", got, test.want)
			}
		})
	}
}

// case: S-237.51
func TestAdmissionGlobalMinFreeOrdersDiskReservationAgainstUnreservedRun(t *testing.T) {
	state := t.TempDir()
	if err := os.MkdirAll(filepath.Join(state, "runs"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := SetQuota(state, "", "", "1"); err != nil {
		t.Fatal(err)
	}
	writeAdmissionFixtureRecord(t, state, record{
		RunID: "earlier-disk-reservation", QueueOrder: 1, Status: StatusBlocked, DiskBytes: 10,
	})
	source := fakeHostResourceSource{sample: HostResourceSnapshot{
		PhysicalMemory: 1000, AvailableMemory: 900, MemoryAvailableKnown: true,
		LogicalCPUs: 8, Pressure: "normal", PressureAvailable: true, LoadAvailable: true,
	}}
	candidate := record{RunID: "later-unreserved", QueueOrder: 2}
	decision, err := admissionStateLocked(state, candidate, source)
	if err != nil || decision.Status != StatusQueued || !strings.Contains(decision.Reason, "earlier run") {
		t.Fatalf("global min-free FIFO decision = %+v, err=%v", decision, err)
	}
}

// case: S-237.36
func TestDiskReservationIsSubtractedBeforeAdmission(t *testing.T) {
	if diskFits(7, 3, 3, 2) {
		t.Fatal("7 - 3 reserved - 3 needed is below the 2 byte floor")
	}
	if !diskFits(8, 3, 3, 2) {
		t.Fatal("8 - 3 reserved - 3 needed meets the 2 byte floor")
	}
}

// case: S-237.10
func TestResourceQuotaMinFreeBlocksAdmission(t *testing.T) {
	state := t.TempDir()
	if err := os.MkdirAll(filepath.Join(state, "runs"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := SetQuota(state, "", "", "18446744073709551615"); err != nil {
		t.Fatal(err)
	}
	source := fakeHostResourceSource{sample: HostResourceSnapshot{
		PhysicalMemory: 1 << 30, AvailableMemory: 1 << 29, MemoryAvailableKnown: true,
		LogicalCPUs: 4, Pressure: "normal", PressureAvailable: true, LoadAvailable: true,
	}}
	decision, err := admissionStateLocked(state, record{RunID: "quota-floor", QueueOrder: 1}, source)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Status != StatusBlocked || decision.DiskNeedBytes != ^uint64(0) || decision.DiskFreeBytes >= decision.DiskNeedBytes {
		t.Fatalf("quota min-free admission = %+v; want a blocked disk-floor decision with observed free bytes", decision)
	}
}

// case: S-237.37
func TestLearnedEstimateNeverLowersDeclaredCost(t *testing.T) {
	got := effectiveQuotaUsage(quotaUsage{MemoryBytes: 4, MinCPUs: 2}, quotaUsage{MemoryBytes: 1, MinCPUs: 1})
	if got.MemoryBytes != 4 || got.MinCPUs != 2 {
		t.Fatalf("effective cost = %+v, want declared floor", got)
	}
}

// case: S-237.32
func TestLearnedEstimateUsesMaximumOfLatestFiveCompletedRuns(t *testing.T) {
	records := []record{
		{CostKey: "build", Status: StatusCompleted, QueueOrder: 1, PeakRSSBytes: 100, AverageCPU: 4},
		{CostKey: "build", Status: StatusCompleted, QueueOrder: 2, PeakRSSBytes: 10, AverageCPU: 1},
		{CostKey: "build", Status: StatusCompleted, QueueOrder: 3, PeakRSSBytes: 20, AverageCPU: 2},
		{CostKey: "other", Status: StatusCompleted, QueueOrder: 4, PeakRSSBytes: 200, AverageCPU: 8},
		{CostKey: "build", Status: StatusCompleted, QueueOrder: 5, PeakRSSBytes: 30, AverageCPU: 3},
		{CostKey: "build", Status: StatusCompleted, QueueOrder: 6, PeakRSSBytes: 40, AverageCPU: 1},
		{CostKey: "build", Status: StatusCompleted, QueueOrder: 7, PeakRSSBytes: 50, AverageCPU: 2},
	}
	got := learnedUsage(records, "build")
	if got.MemoryBytes != 50 || got.MinCPUs != 2 {
		t.Fatalf("learned usage = %+v, want latest-five maximum and matching CPU", got)
	}
	effective := effectiveQuotaUsage(quotaUsage{MemoryBytes: 1, MinCPUs: 1}, got)
	if effective.MemoryBytes != 50 || effective.MinCPUs != 2 {
		t.Fatalf("effective estimate = %+v, want learned estimate above declaration", effective)
	}
}

// case: S-237.38
func TestAvailableMemoryIsIndependentAdmissionCondition(t *testing.T) {
	if !memoryUnavailable(8, 7) {
		t.Fatal("a run that exceeds live available memory must wait")
	}
	if memoryUnavailable(8, 8) {
		t.Fatal("a run that fits live available memory should proceed")
	}
}

// case: S-237.40
func TestOverBudgetThresholdEmitsOnlyOnce(t *testing.T) {
	var state overBudgetState
	if !state.observe(126, 100) || state.observe(130, 100) {
		t.Fatal("over-budget transition should emit once after 125 percent")
	}
}

// case: S-237.38
func TestAdmissionBlocksWhenLiveMemoryIsBelowNeed(t *testing.T) {
	state := t.TempDir()
	if err := os.MkdirAll(filepath.Join(state, "runs"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := SetQuota(state, "200B", "4", "0"); err != nil {
		t.Fatal(err)
	}
	writeAdmissionFixtureRecord(t, state, record{RunID: "active", QueueOrder: 1, Status: StatusRunning, MemoryBytes: 10, CPUsMin: 1})
	source := fakeHostResourceSource{sample: HostResourceSnapshot{PhysicalMemory: 100, AvailableMemory: 50, MemoryAvailableKnown: true, LogicalCPUs: 4, Pressure: "normal", PressureAvailable: true, LoadAvailable: true}}
	candidate := record{RunID: "candidate", QueueOrder: 2, MemoryBytes: 80, CPUsMin: 1}
	decision, err := admissionStateLocked(state, candidate, source)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Status != StatusBlocked || decision.Reason != "available memory budget (have 50B, needs 80B)" {
		t.Fatalf("admission = %+v", decision)
	}
}

// case: S-237.31
func TestUnreadablePressureIsReportedAndDoesNotBlockBudgetAdmission(t *testing.T) {
	state := t.TempDir()
	if err := os.MkdirAll(filepath.Join(state, "runs"), 0o700); err != nil {
		t.Fatal(err)
	}
	source := fakeHostResourceSource{sample: HostResourceSnapshot{
		PhysicalMemory: 100, AvailableMemory: 80, MemoryAvailableKnown: true,
		LogicalCPUs: 4, Unavailable: map[string]string{"pressure": "permission denied"},
	}}
	if pressureSignalsAvailable(source.Snapshot()) {
		t.Fatal("unreadable pressure signal was classified as available/normal")
	}
	decision, err := admissionStateLocked(state, record{RunID: "candidate", QueueOrder: 1}, source)
	if err != nil || decision.Reason != "" || decision.Status != StatusStarting {
		t.Fatalf("unreadable pressure admission = %+v, err=%v", decision, err)
	}
	if got := hostResourceNotice(source.Snapshot()); got != "pressure unavailable: permission denied" {
		t.Fatalf("resource notice = %q", got)
	}
}

// case: S-237.30
func TestAdmissionPressureTransitionWritesEvent(t *testing.T) {
	state := t.TempDir()
	if err := os.MkdirAll(filepath.Join(state, "runs"), 0o700); err != nil {
		t.Fatal(err)
	}
	candidate := record{RunID: "candidate", QueueOrder: 1, CostClass: CostHeavy}
	baseline := fakeHostResourceSource{sample: HostResourceSnapshot{PhysicalMemory: 100, AvailableMemory: 80, MemoryAvailableKnown: true, LogicalCPUs: 4, Pressure: "normal", PressureAvailable: true, LoadAvailable: true}}
	source := fakeHostResourceSource{sample: HostResourceSnapshot{PhysicalMemory: 100, AvailableMemory: 50, MemoryAvailableKnown: true, LogicalCPUs: 4, Pressure: "memory warn", PressureAvailable: true, LoadAvailable: true}}
	var decision admissionDecision
	if err := withStateLock(state, func() error {
		if _, err := admissionStateLocked(state, candidate, baseline); err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got := countResourceEvents(t, state, EventPressure); got != 0 {
		t.Fatalf("initial normal pressure sample emitted %d transition event(s), want 0", got)
	}
	err := withStateLock(state, func() error {
		var decisionErr error
		decision, decisionErr = admissionStateLocked(state, candidate, source)
		return decisionErr
	})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Status != StatusBlocked || !strings.Contains(decision.Reason, "waiting for memory pressure") {
		t.Fatalf("admission = %+v", decision)
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
	baseline := fakeHostResourceSource{sample: HostResourceSnapshot{
		PhysicalMemory: 100, AvailableMemory: 80, MemoryAvailableKnown: true,
		LogicalCPUs: 4, Pressure: "normal", PressureAvailable: true, LoadAvailable: true,
	}}
	source := fakeHostResourceSource{sample: HostResourceSnapshot{
		PhysicalMemory: 100, AvailableMemory: 50, MemoryAvailableKnown: true,
		LogicalCPUs: 4, Pressure: "memory warn", PressureAvailable: true, LoadAvailable: true,
	}}
	if _, err := admissionStateLocked(state, record{RunID: "candidate", QueueOrder: 1, CostClass: CostHeavy}, baseline); err != nil {
		t.Fatal(err)
	}
	if got := countResourceEvents(t, state, EventPressure); got != 0 {
		t.Fatalf("initial normal pressure sample emitted %d transition event(s), want 0", got)
	}
	const observers = 8
	start := make(chan struct{})
	errs := make(chan error, observers)
	for range observers {
		go func() {
			<-start
			errs <- withStateLock(state, func() error {
				_, err := admissionStateLocked(state, record{RunID: "candidate", QueueOrder: 1}, source)
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

// case: S-237.30
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
	source := fakeHostResourceSource{sample: HostResourceSnapshot{PhysicalMemory: 100, AvailableMemory: 80, MemoryAvailableKnown: true, LogicalCPUs: 4, Pressure: "normal", PressureAvailable: true, LoadAvailable: true}}
	decision, err := admissionStateLocked(state, record{RunID: "candidate", QueueOrder: 1}, source)
	if err != nil || decision.Reason != "" || decision.Status != StatusStarting {
		t.Fatalf("admission after normal hold = %+v, err=%v", decision, err)
	}
	if countResourceEvents(t, state, EventPressure) != 1 {
		t.Fatal("normal pressure transition event missing")
	}
}

// case: S-237.74
func TestAdmissionUsesPressureLevelAndCostClass(t *testing.T) {
	state := t.TempDir()
	cases := []struct {
		name     string
		pressure string
		class    CostClass
		status   RunStatus
	}{
		{name: "normal light", pressure: "normal", class: CostLight, status: StatusStarting},
		{name: "normal heavy", pressure: "normal", class: CostHeavy, status: StatusStarting},
		{name: "warn light", pressure: "memory warn", class: CostLight, status: StatusStarting},
		{name: "warn heavy", pressure: "memory warn", class: CostHeavy, status: StatusBlocked},
		{name: "critical light", pressure: "memory critical", class: CostLight, status: StatusBlocked},
		{name: "critical heavy", pressure: "memory critical", class: CostHeavy, status: StatusBlocked},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			classState := filepath.Join(state, strings.ReplaceAll(test.name, " ", "-"))
			if err := os.MkdirAll(classState, 0o700); err != nil {
				t.Fatal(err)
			}
			sample := HostResourceSnapshot{Pressure: test.pressure, PressureAvailable: true, LoadAvailable: true}
			decision, err := admissionStateLocked(classState, record{RunID: "candidate", QueueOrder: 1, CostClass: test.class}, fakeHostResourceSource{sample: sample})
			if err != nil {
				t.Fatal(err)
			}
			if decision.Status != test.status {
				t.Fatalf("admission at %s for %s = %+v, want %s", test.pressure, test.class, decision, test.status)
			}
		})
	}
}

// case: S-237.74
func TestInitialPressureSampleEstablishesBaselineWithoutTransitionEvent(t *testing.T) {
	normal := HostResourceSnapshot{Pressure: "normal", PressureAvailable: true, LoadAvailable: true}
	critical := HostResourceSnapshot{Pressure: "memory critical", PressureAvailable: true, LoadAvailable: true}
	for _, test := range []struct {
		name          string
		initial       HostResourceSnapshot
		candidateCost CostClass
		next          HostResourceSnapshot
	}{
		{name: "normal baseline", initial: normal, candidateCost: CostLight, next: critical},
		{name: "unknown baseline", initial: HostResourceSnapshot{}, candidateCost: CostHeavy, next: normal},
	} {
		t.Run(test.name, func(t *testing.T) {
			state := t.TempDir()
			candidate := record{RunID: "candidate", QueueOrder: 1, CostClass: test.candidateCost}
			if _, err := admissionStateLocked(state, candidate, fakeHostResourceSource{sample: test.initial}); err != nil {
				t.Fatal(err)
			}
			if got := countResourceEvents(t, state, EventPressure); got != 0 {
				t.Fatalf("initial pressure sample emitted %d transition event(s), want 0", got)
			}
			if _, err := admissionStateLocked(state, candidate, fakeHostResourceSource{sample: test.next}); err != nil {
				t.Fatal(err)
			}
			if got := countResourceEvents(t, state, EventPressure); got != 1 {
				t.Fatalf("actual pressure-level change emitted %d transition event(s), want 1", got)
			}
		})
	}
}

// case: S-237.74
func TestUnknownPressurePreservesFreshStateAndBoundsStaleness(t *testing.T) {
	for _, test := range []struct {
		name       string
		age        time.Duration
		level      pressureLevel
		class      CostClass
		wantStatus RunStatus
		wantReason string
	}{
		{name: "fresh critical holds heavy", age: time.Minute, level: pressureCritical, class: CostHeavy, wantStatus: StatusBlocked, wantReason: "last known critical sample"},
		{name: "fresh warn holds heavy", age: time.Minute, level: pressureWarn, class: CostHeavy, wantStatus: StatusBlocked, wantReason: "last known warn sample"},
		{name: "stale unknown holds heavy", age: 2*time.Minute + time.Second, level: pressureCritical, class: CostHeavy, wantStatus: StatusBlocked, wantReason: "pressure unknown since"},
		{name: "stale unknown admits light", age: 2*time.Minute + time.Second, level: pressureCritical, class: CostLight, wantStatus: StatusStarting},
	} {
		t.Run(test.name, func(t *testing.T) {
			state := t.TempDir()
			lastKnownAt := time.Now().UTC().Add(-test.age)
			persisted := pressureState{
				Level:       string(test.level),
				LevelClass:  test.level,
				LevelSince:  lastKnownAt.Add(-time.Minute),
				LastKnownAt: lastKnownAt,
				ElevatedAt:  lastKnownAt,
			}
			data, err := json.Marshal(persisted)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(state, "pressure-state.json"), data, 0o600); err != nil {
				t.Fatal(err)
			}
			decision, err := admissionStateLocked(state, record{RunID: "candidate", QueueOrder: 1, CostClass: test.class}, fakeHostResourceSource{sample: HostResourceSnapshot{}})
			if err != nil {
				t.Fatal(err)
			}
			if decision.Status != test.wantStatus || (test.wantReason != "" && !strings.Contains(decision.Reason, test.wantReason)) {
				t.Fatalf("unknown pressure at age %s for %s = %+v, want status %s and reason containing %q", test.age, test.class, decision, test.wantStatus, test.wantReason)
			}
			if test.name == "stale unknown holds heavy" {
				normal := HostResourceSnapshot{Pressure: "normal", PressureAvailable: true, LoadAvailable: true}
				decision, err = admissionStateLocked(state, record{RunID: "candidate", QueueOrder: 1, CostClass: CostHeavy}, fakeHostResourceSource{sample: normal})
				if err != nil {
					t.Fatal(err)
				}
				if decision.Status != StatusBlocked || !strings.Contains(decision.Reason, "hysteresis current: critical") {
					t.Fatalf("known normal sample cleared persisted critical before hysteresis: %+v", decision)
				}
			}
		})
	}
}

// case: S-237.74
func TestUnknownPressureWaitIsVisibleInRecordAndEvent(t *testing.T) {
	state := t.TempDir()
	lastKnownAt := time.Now().UTC().Add(-pressureStateStalenessBound - time.Second)
	persisted, err := json.Marshal(pressureState{
		Level: "host pressure (memory critical)", LevelClass: pressureCritical,
		LevelSince: lastKnownAt.Add(-time.Minute), LastKnownAt: lastKnownAt, ElevatedAt: lastKnownAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(state, "pressure-state.json"), persisted, 0o600); err != nil {
		t.Fatal(err)
	}
	identity, alive, err := ProcessIdentity(os.Getpid())
	if err != nil || !alive {
		t.Fatalf("inspect test process: alive=%t err=%v", alive, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		result <- reserveAdmission(ctx, Config{StateDir: state, Tag: "unknown-pressure", Command: []string{"/bin/true"}, CostClass: CostHeavy}, "unknown-pressure", os.Getpid(), identity, fakeHostResourceSource{sample: HostResourceSnapshot{}})
	}()
	t.Cleanup(func() {
		cancel()
		<-finished
	})
	blocked := waitForAdmissionRecord(t, state, "unknown-pressure", func(rec record) bool { return rec.Status == StatusBlocked })
	wantWait := pressureAdmissionWait(pressureUnknown, lastKnownAt.Add(pressureStateStalenessBound), false)
	if blocked.WaitReason != wantWait {
		cancel()
		<-result
		t.Fatalf("unknown pressure wait = %q, want %q", blocked.WaitReason, wantWait)
	}
	listing := List(state)
	if listing.Error != nil {
		cancel()
		<-result
		t.Fatalf("list unknown-pressure run: %v", listing.Error)
	}
	listed := false
	for _, run := range listing.Runs {
		if run.RunID == "unknown-pressure" && run.WaitReason == wantWait {
			listed = true
			break
		}
	}
	if !listed {
		cancel()
		<-result
		t.Fatalf("ps listing did not show unknown-pressure wait %q: %+v", wantWait, listing.Runs)
	}

	data, err := os.ReadFile(filepath.Join(state, "events.jsonl"))
	if err != nil {
		cancel()
		<-result
		t.Fatal(err)
	}
	var blockedEvent *Event
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var event Event
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			cancel()
			<-result
			t.Fatal(err)
		}
		if event.Type == EventBlocked {
			blockedEvent = &event
			break
		}
	}
	if blockedEvent == nil || blockedEvent.Reason != wantWait || blockedEvent.Pressure.PressureAvailable || blockedEvent.Pressure.LoadAvailable || blockedEvent.Pressure.MemoryAvailableKnown {
		cancel()
		<-result
		t.Fatalf("blocked event did not report unknown pressure sample and wait: %+v", blockedEvent)
	}
	cancel()
	if err := <-result; err == nil {
		t.Fatal("cancelled unknown-pressure admission returned success")
	}
}

// case: S-237.74
func TestInitialQueuedEventUsesFirstAdmissionSample(t *testing.T) {
	state := t.TempDir()
	identity, alive, err := ProcessIdentity(os.Getpid())
	if err != nil || !alive {
		t.Fatalf("inspect test process: alive=%t err=%v", alive, err)
	}
	normal := HostResourceSnapshot{PhysicalMemory: 1 << 30, AvailableMemory: 1 << 29, MemoryAvailableKnown: true,
		Pressure: "normal", PressureAvailable: true, LoadAvailable: true, LogicalCPUs: 4}
	warn := normal
	warn.Pressure = "memory warn"
	source := &sequenceHostResourceSource{samples: []HostResourceSnapshot{normal, warn}}
	ctx, cancel := context.WithTimeout(context.Background(), testdeadline.Budget(t))
	finished := make(chan struct{})
	defer func() {
		cancel()
		<-finished
	}()
	result := make(chan error, 1)
	go func() {
		defer close(finished)
		result <- reserveAdmission(ctx, Config{StateDir: state, Tag: "sampled", Command: []string{"/bin/true"}, CostClass: CostHeavy}, "sampled", os.Getpid(), identity, source)
	}()
	decision := waitForAdmissionRecord(t, state, "sampled", func(rec record) bool {
		return rec.Status == StatusStarting || rec.Status == StatusBlocked
	})
	if decision.Status != StatusStarting {
		cancel()
		<-result
		t.Fatalf("first admission used a later pressure sample: status=%s reason=%q", decision.Status, decision.WaitReason)
	}
	if err := <-result; err != nil {
		t.Fatalf("admission using first pressure sample: %v", err)
	}
	cleanupAdmission(state, "sampled", "test complete")
	data, err := os.ReadFile(filepath.Join(state, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var queued *Event
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var event Event
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatal(err)
		}
		if event.Type == EventQueued {
			queued = &event
			break
		}
	}
	if queued == nil {
		t.Fatal("initial queued event was not recorded")
	}
	gotSample, err := json.Marshal(queued.Pressure)
	if err != nil {
		t.Fatal(err)
	}
	wantSample, err := json.Marshal(normal)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotSample, wantSample) {
		t.Fatalf("initial queued event pressure = %s, want first decision sample %s", gotSample, wantSample)
	}
	source.mu.Lock()
	snapshotCount := source.snapshots
	source.mu.Unlock()
	if snapshotCount < 2 {
		t.Fatalf("test source returned only %d sample(s); consecutive samples were not distinguishable", snapshotCount)
	}
}

// case: S-237.74
func TestBlockedPressureEventKeepsDecisionSampleAndWaitCondition(t *testing.T) {
	state := t.TempDir()
	identity, alive, err := ProcessIdentity(os.Getpid())
	if err != nil || !alive {
		t.Fatalf("inspect test process: alive=%t err=%v", alive, err)
	}
	sample := HostResourceSnapshot{PhysicalMemory: 1 << 30, AvailableMemory: 1 << 29, MemoryAvailableKnown: true,
		Pressure: "memory warn", PressureAvailable: true, LoadAvailable: true, LogicalCPUs: 4, SwapUsedBytes: 1234}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		result <- reserveAdmission(ctx, Config{StateDir: state, Tag: "waiter", Command: []string{"/bin/true"}, CostClass: CostHeavy}, "waiter", os.Getpid(), identity, fakeHostResourceSource{sample: sample})
	}()
	blocked := waitForAdmissionRecord(t, state, "waiter", func(rec record) bool { return rec.Status == StatusBlocked })
	if !strings.Contains(blocked.WaitReason, "drop below warn") || !strings.Contains(blocked.WaitReason, "current: warn since") {
		cancel()
		<-result
		t.Fatalf("pressure wait lacks its condition and age: %q", blocked.WaitReason)
	}
	listing := List(state)
	if listing.Error != nil {
		cancel()
		<-result
		t.Fatalf("list held run: %v", listing.Error)
	}
	var listedWait string
	for _, run := range listing.Runs {
		if run.RunID == "waiter" {
			listedWait = run.WaitReason
		}
	}
	if listedWait != blocked.WaitReason {
		cancel()
		<-result
		t.Fatalf("ps wait reason = %q, persisted decision = %q", listedWait, blocked.WaitReason)
	}
	data, err := os.ReadFile(filepath.Join(state, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var events []Event
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var event Event
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatal(err)
		}
		if event.Type == EventBlocked {
			events = append(events, event)
		}
	}
	if len(events) != 1 || (events[0].Pressure.Pressure != sample.Pressure || events[0].Pressure.SwapUsedBytes != sample.SwapUsedBytes || !events[0].Pressure.MemoryAvailableKnown) || !strings.Contains(events[0].Reason, "current: warn since") {
		cancel()
		<-result
		t.Fatalf("blocked pressure event does not describe the decision sample and wait: %+v", events)
	}
	cancel()
	if err := <-result; err == nil {
		t.Fatal("cancelled pressure admission returned success")
	}
}

func TestPressureWaitReasonNamesElevatedSignal(t *testing.T) {
	got := pressureWaitReason(HostResourceSnapshot{Pressure: "memory warn"})
	if got != "host pressure (memory warn)" {
		t.Fatalf("pressure wait reason = %q", got)
	}
}

// case: S-237.39
func TestCgroupFailureFormatsVisibleAdvisory(t *testing.T) {
	if got := limitsAdvisory("cgroup v2 create child: permission denied"); got != "limits advisory: cgroup v2 create child: permission denied" {
		t.Fatalf("cgroup advisory = %q", got)
	}
}

type metricsProcessSource struct{ processes []Process }

func (source metricsProcessSource) Snapshot(string) ([]Process, error) { return source.processes, nil }
func (metricsProcessSource) Identity(int) (string, bool, error)        { return "", false, nil }
func (metricsProcessSource) Signal(int, syscall.Signal) error          { return nil }

// case: S-237.33
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

// case: S-237.33
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

// case: S-237.40
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

// case: S-237.33
func TestPartialMeasurementNeverEntersCompleteLearningHistory(t *testing.T) {
	records := []record{{CostKey: "build", Status: StatusCompleted, PeakRSSBytes: 12, AverageCPU: 1, MeasurementPartial: true}}
	if learned := learnedUsage(records, "build"); learned.MemoryBytes != 0 {
		t.Fatalf("partial observation learned as complete: %+v", learned)
	}
}
