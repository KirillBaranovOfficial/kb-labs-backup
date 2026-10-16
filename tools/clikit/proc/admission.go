//go:build darwin || linux

package proc

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const admissionPollInterval = 50 * time.Millisecond

type gateRecord struct {
	Name     string `json:"name"`
	RunID    string `json:"run_id"`
	PID      int    `json:"pid"`
	Identity string `json:"identity"`
}

type GateInfo struct {
	Name     string
	Tag      string
	PID      int
	Identity string
}

type admissionDecision struct {
	Reason        string
	Status        RunStatus
	Position      int
	DiskNeedBytes uint64
	DiskFreeBytes uint64
}

func GateStatus(stateDir, name string) (GateInfo, bool, error) {
	var info GateInfo
	var held bool
	err := withStateLock(stateDir, func() error {
		if err := pruneStateLocked(stateDir); err != nil {
			return err
		}
		gate, exists, err := readGateLocked(stateDir, name)
		if err != nil || !exists {
			return err
		}
		info = GateInfo{Name: gate.Name, PID: gate.PID, Identity: gate.Identity}
		rec, err := readRecordLocked(stateDir, gate.RunID)
		if err != nil {
			return fmt.Errorf("read gate holder run %s: %w", gate.RunID, err)
		}
		info.Tag = rec.Tag
		held = true
		return nil
	})
	return info, held, err
}

func reserveAdmission(ctx context.Context, cfg Config, runID string, parentPID int, parentIdentity string, source HostResourceSource) error {
	if cfg.Slots < 0 || (cfg.SlotGroup == "") != (cfg.Slots == 0) {
		return errors.New("proc: --slot-group and positive --slots must be supplied together")
	}
	if cfg.HoldGate != "" && cfg.WaitGate == cfg.HoldGate {
		return errors.New("proc: a run cannot wait on the gate it holds")
	}
	if err := os.MkdirAll(filepath.Join(cfg.StateDir, "runs"), 0o700); err != nil {
		return fmt.Errorf("proc: create run registry: %w", err)
	}
	if err := withStateLock(cfg.StateDir, func() error {
		if err := pruneStateLocked(cfg.StateDir); err != nil {
			return err
		}
		order, err := nextQueueOrderLocked(cfg.StateDir)
		if err != nil {
			return err
		}
		known, err := readRecordsLocked(cfg.StateDir)
		if err != nil {
			return err
		}
		learned := learnedUsage(known, cfg.CostKey)
		cost := effectiveQuotaUsage(quotaUsage{MemoryBytes: cfg.MemoryBytes, MinCPUs: cfg.CPUsMin, DiskBytes: cfg.DiskBytes}, learned)
		rec := record{
			RunID: runID, Tag: cfg.Tag, LatestTag: cfg.LatestTag, PID: parentPID, StartedAt: parentIdentity,
			Command: cfg.Command, StateDir: cfg.StateDir, Status: StatusQueued,
			SlotGroup: cfg.SlotGroup, Slots: cfg.Slots, QueueOrder: order,
			EnqueuedAt: time.Now().UTC(), WaitGate: cfg.WaitGate, HoldGate: cfg.HoldGate, MinFree: cfg.MinFree,
			CostKey: cfg.CostKey, MemoryBytes: cost.MemoryBytes, CPUsMin: cost.MinCPUs, CPUsMax: cfg.CPUsMax, DiskBytes: cost.DiskBytes,
		}
		if err := writeRecordAtomic(rec); err != nil {
			return fmt.Errorf("proc: record queued run: %w", err)
		}
		return appendEvent(cfg.StateDir, eventFor(rec, EventQueued))
	}); err != nil {
		return err
	}

	ticker := time.NewTicker(admissionPollInterval)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			cleanupAdmission(cfg.StateDir, runID, "cancelled before start")
			return fmt.Errorf("proc: admission cancelled: %w", err)
		}
		admitted := false
		err := withStateLock(cfg.StateDir, func() error {
			if err := pruneStateLocked(cfg.StateDir); err != nil {
				return err
			}
			rec, err := readRecordLocked(cfg.StateDir, runID)
			if err != nil {
				return err
			}
			decision, err := admissionStateLocked(cfg.StateDir, rec, source)
			if err != nil {
				return err
			}
			notice := hostResourceNotice(source.Snapshot())
			if decision.Reason == "" {
				quota, err := effectiveQuota(cfg.StateDir, source)
				if err != nil {
					return err
				}
				others, err := readRecordsLocked(cfg.StateDir)
				if err != nil {
					return err
				}
				usedCPUs := 0
				for _, other := range others {
					if other.RunID != rec.RunID && (other.Status == StatusStarting || other.Status == StatusRunning || other.Status == StatusStalled) {
						usedCPUs += max(other.CPUsMin, other.GrantedCPUs)
					}
				}
				if rec.CPUsMax > 0 {
					rec.GrantedCPUs = min(rec.CPUsMax, max(rec.CPUsMin, quota.CPUs-usedCPUs))
				}
				rec.ResourceNotice = notice
				rec.Status = StatusStarting
				rec.WaitReason = ""
				if err := writeRecordAtomic(rec); err != nil {
					return err
				}
				if rec.HoldGate != "" {
					if err := writeGateLocked(cfg.StateDir, gateRecord{Name: rec.HoldGate, RunID: runID, PID: parentPID, Identity: parentIdentity}); err != nil {
						return err
					}
				}
				admitted = true
				return nil
			}
			if rec.Status != decision.Status || rec.WaitReason != decision.Reason {
				statusChanged := rec.Status != decision.Status
				rec.Status = decision.Status
				rec.WaitReason = decision.Reason
				rec.ResourceNotice = notice
				if err := writeRecordAtomic(rec); err != nil {
					return err
				}
				if !statusChanged && decision.Status == StatusBlocked {
					return nil
				}
				eventType := EventQueued
				if decision.Status == StatusBlocked {
					eventType = EventBlocked
				}
				event := eventFor(rec, eventType)
				event.Reason = decision.Reason
				event.Position = decision.Position
				event.DiskNeedBytes = decision.DiskNeedBytes
				event.DiskFreeBytes = decision.DiskFreeBytes
				return appendEvent(cfg.StateDir, event)
			}
			if rec.ResourceNotice != notice {
				rec.ResourceNotice = notice
				return writeRecordAtomic(rec)
			}
			return nil
		})
		if err != nil {
			cleanupAdmission(cfg.StateDir, runID, err.Error())
			return fmt.Errorf("proc: admit run: %w", err)
		}
		if admitted {
			return nil
		}
		select {
		case <-ctx.Done():
		case <-ticker.C:
		}
	}
}

func admissionStateLocked(stateDir string, candidate record, source HostResourceSource) (admissionDecision, error) {
	records, err := readRecordsLocked(stateDir)
	if err != nil {
		return admissionDecision{}, err
	}
	position := 1
	if candidate.SlotGroup != "" {
		active := 0
		queuedBefore := 0
		for _, rec := range records {
			if rec.RunID == candidate.RunID || rec.SlotGroup != candidate.SlotGroup {
				continue
			}
			if rec.Status == StatusStarting || rec.Status == StatusRunning || rec.Status == StatusStalled {
				active++
			}
			if (rec.Status == StatusQueued || rec.Status == StatusBlocked) && rec.QueueOrder < candidate.QueueOrder {
				queuedBefore++
			}
		}
		position += queuedBefore
		if active >= candidate.Slots {
			return admissionDecision{Reason: fmt.Sprintf("slot group %s is full", candidate.SlotGroup), Status: StatusQueued, Position: position}, nil
		}
		if queuedBefore > 0 {
			return admissionDecision{Reason: fmt.Sprintf("waiting behind %d earlier run(s) in slot group %s", queuedBefore, candidate.SlotGroup), Status: StatusQueued, Position: position}, nil
		}
	}
	if candidate.WaitGate != "" {
		if _, exists, err := readGateLocked(stateDir, candidate.WaitGate); err != nil {
			return admissionDecision{}, err
		} else if exists {
			return admissionDecision{Reason: "gate: " + candidate.WaitGate, Status: StatusBlocked, Position: position}, nil
		}
	}
	if candidate.HoldGate != "" {
		if _, exists, err := readGateLocked(stateDir, candidate.HoldGate); err != nil {
			return admissionDecision{}, err
		} else if exists {
			return admissionDecision{Reason: "gate: " + candidate.HoldGate, Status: StatusBlocked, Position: position}, nil
		}
		for _, rec := range records {
			if rec.RunID != candidate.RunID && rec.HoldGate == candidate.HoldGate &&
				(rec.Status == StatusQueued || rec.Status == StatusBlocked) && rec.QueueOrder < candidate.QueueOrder {
				return admissionDecision{Reason: "waiting for gate holder " + candidate.HoldGate, Status: StatusBlocked, Position: position}, nil
			}
		}
	}
	quota, err := effectiveQuota(stateDir, source)
	if err != nil {
		return admissionDecision{}, err
	}
	activeUsage := quotaUsage{}
	queuedBefore := 0
	for _, rec := range records {
		if rec.RunID == candidate.RunID {
			continue
		}
		if (rec.Status == StatusQueued || rec.Status == StatusBlocked) && rec.QueueOrder < candidate.QueueOrder {
			queuedBefore++
		}
		if rec.Status == StatusStarting || rec.Status == StatusRunning || rec.Status == StatusStalled {
			activeUsage.MemoryBytes += rec.MemoryBytes
			activeUsage.MinCPUs += max(rec.CPUsMin, rec.GrantedCPUs)
			activeUsage.DiskBytes += rec.DiskBytes
		}
	}
	if queuedBefore > 0 {
		return admissionDecision{Reason: fmt.Sprintf("waiting behind %d earlier run(s)", queuedBefore), Status: StatusQueued, Position: position + queuedBefore}, nil
	}
	if reason, err := admissionPressureLocked(stateDir, candidate, source); err != nil {
		return admissionDecision{}, err
	} else if reason != "" {
		return admissionDecision{Reason: reason, Status: StatusBlocked, Position: position}, nil
	}
	minFree := max(candidate.MinFree, quota.MinFree)
	if candidate.DiskBytes > 0 || minFree > 0 {
		free, err := freeDiskBytes(stateDir)
		if err != nil {
			return admissionDecision{}, err
		}
		if !diskFits(free, activeUsage.DiskBytes, candidate.DiskBytes, minFree) {
			need := candidate.DiskBytes + minFree
			reason := fmt.Sprintf("disk budget: need %s plus min-free %s, have %s with %s reserved", FormatResourceBytes(candidate.DiskBytes), FormatResourceBytes(minFree), FormatResourceBytes(free), FormatResourceBytes(activeUsage.DiskBytes))
			return admissionDecision{Reason: reason, Status: StatusBlocked, Position: position, DiskNeedBytes: need, DiskFreeBytes: free}, nil
		}
	}
	activeCostedRun := activeUsage.MemoryBytes > 0 || activeUsage.MinCPUs > 0 || activeUsage.DiskBytes > 0
	candidateCostedRun := candidate.MemoryBytes > 0 || candidate.CPUsMin > 0 || candidate.DiskBytes > 0
	if activeCostedRun && candidateCostedRun {
		if !quotaFits(activeUsage, quotaUsage{MemoryBytes: candidate.MemoryBytes, MinCPUs: candidate.CPUsMin}, quota) {
			reason := fmt.Sprintf("memory budget (reserved %s of %s, needs %s)", FormatResourceBytes(activeUsage.MemoryBytes), FormatResourceBytes(quota.MemoryBytes), FormatResourceBytes(candidate.MemoryBytes))
			if activeUsage.MemoryBytes+candidate.MemoryBytes <= quota.MemoryBytes {
				reason = fmt.Sprintf("cpu budget (reserved %d of %d, needs %d)", activeUsage.MinCPUs, quota.CPUs, candidate.CPUsMin)
			}
			return admissionDecision{Reason: reason, Status: StatusBlocked, Position: position}, nil
		}
		sample := source.Snapshot()
		if sample.MemoryAvailableKnown && memoryUnavailable(candidate.MemoryBytes, sample.AvailableMemory) {
			reason := fmt.Sprintf("available memory budget (have %s, needs %s)", FormatResourceBytes(sample.AvailableMemory), FormatResourceBytes(candidate.MemoryBytes))
			return admissionDecision{Reason: reason, Status: StatusBlocked, Position: position}, nil
		}
	}
	return admissionDecision{Status: StatusStarting, Position: position}, nil
}

func admissionPressureLocked(stateDir string, candidate record, source HostResourceSource) (string, error) {
	sample := source.Snapshot()
	var hold pressureState
	pressurePath := filepath.Join(stateDir, "pressure-state.json")
	if data, readErr := os.ReadFile(pressurePath); readErr == nil {
		if err := json.Unmarshal(data, &hold); err != nil {
			return "", fmt.Errorf("decode pressure state: %w", err)
		}
	} else if !os.IsNotExist(readErr) {
		return "", readErr
	}
	pressureKnown := pressureSignalsAvailable(sample)
	elevated := isElevatedPressure(sample)
	previousLevel := hold.Level
	blocked := pressureKnown && hold.blocks(elevated, time.Now())
	if pressureKnown {
		hold.Level = sample.Pressure
		if elevated {
			hold.Level = pressureWaitReason(sample)
		}
		data, err := json.Marshal(hold)
		if err != nil {
			return "", err
		}
		if err := os.WriteFile(pressurePath, data, 0o600); err != nil {
			return "", err
		}
		if previousLevel != hold.Level {
			event := eventFor(candidate, EventPressure)
			event.Reason = hold.Level
			event.Pressure = sample
			if err := appendEvent(stateDir, event); err != nil {
				return "", err
			}
		}
	}
	if blocked {
		return pressureWaitReason(sample), nil
	}
	return "", nil
}

func learnedUsage(records []record, key string) quotaUsage {
	if key == "" {
		return quotaUsage{}
	}
	var history []record
	for _, rec := range records {
		if rec.CostKey == key && rec.Status == StatusDead && !rec.MeasurementPartial && rec.PeakRSSBytes > 0 {
			history = append(history, rec)
		}
	}
	if len(history) > 5 {
		history = history[len(history)-5:]
	}
	var peak record
	for _, rec := range history {
		if rec.PeakRSSBytes > peak.PeakRSSBytes {
			peak = rec
		}
	}
	return quotaUsage{MemoryBytes: peak.PeakRSSBytes, MinCPUs: int(peak.AverageCPU + 0.999)}
}

func withStateLock(stateDir string, action func() error) (err error) {
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return fmt.Errorf("create state directory: %w", err)
	}
	file, err := os.OpenFile(filepath.Join(stateDir, "state.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("open state lock: %w", err)
	}
	unlock, err := lockFile(file)
	if err != nil {
		return errors.Join(fmt.Errorf("lock run state: %w", err), file.Close())
	}
	defer func() { err = errors.Join(err, unlock(), file.Close()) }()
	return action()
}

func nextQueueOrderLocked(stateDir string) (uint64, error) {
	path := filepath.Join(stateDir, "queue-sequence")
	data, err := os.ReadFile(path)
	var order uint64
	if err != nil && !os.IsNotExist(err) {
		return 0, err
	}
	if len(data) != 0 {
		order, err = strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
		if err != nil {
			return 0, fmt.Errorf("decode queue sequence: %w", err)
		}
	}
	if order == ^uint64(0) {
		return 0, errors.New("queue sequence exhausted")
	}
	order++
	if err := os.WriteFile(path, []byte(strconv.FormatUint(order, 10)+"\n"), 0o600); err != nil {
		return 0, fmt.Errorf("write queue sequence: %w", err)
	}
	return order, nil
}

func readRecordsLocked(stateDir string) ([]record, error) {
	paths, err := filepath.Glob(filepath.Join(stateDir, "runs", "*.json"))
	if err != nil {
		return nil, err
	}
	records := make([]record, 0, len(paths))
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read run record %s: %w", path, err)
		}
		var rec record
		if err := json.Unmarshal(data, &rec); err != nil {
			return nil, fmt.Errorf("decode run record %s: %w", path, err)
		}
		records = append(records, rec)
	}
	sort.Slice(records, func(i, j int) bool { return records[i].QueueOrder < records[j].QueueOrder })
	return records, nil
}

func readRecordLocked(stateDir, runID string) (record, error) {
	data, err := os.ReadFile(filepath.Join(stateDir, "runs", runID+".json"))
	if err != nil {
		return record{}, fmt.Errorf("read queued run %s: %w", runID, err)
	}
	var rec record
	if err := json.Unmarshal(data, &rec); err != nil {
		return record{}, fmt.Errorf("decode queued run %s: %w", runID, err)
	}
	return rec, nil
}

func writeRecordAtomic(rec record) error {
	path := filepath.Join(rec.StateDir, "runs", rec.RunID+".json")
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return errors.Join(err, os.Remove(tmp))
	}
	return nil
}

func gatePath(stateDir, name string) string {
	encoded := base64.RawURLEncoding.EncodeToString([]byte(name))
	return filepath.Join(stateDir, "gates", encoded+".json")
}

func writeGateLocked(stateDir string, gate gateRecord) error {
	if err := os.MkdirAll(filepath.Dir(gatePath(stateDir, gate.Name)), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(gate)
	if err != nil {
		return err
	}
	path := gatePath(stateDir, gate.Name)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return errors.Join(err, os.Remove(tmp))
	}
	return nil
}

func readGateLocked(stateDir, name string) (gateRecord, bool, error) {
	path := gatePath(stateDir, name)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return gateRecord{}, false, nil
	}
	if err != nil {
		return gateRecord{}, false, err
	}
	var gate gateRecord
	if err := json.Unmarshal(data, &gate); err != nil {
		return gateRecord{}, false, fmt.Errorf("decode gate %q: %w", name, err)
	}
	identity, alive, identityErr := inspectIdentity(gate.PID)
	if identityErr != nil {
		return gateRecord{}, false, fmt.Errorf("inspect gate holder %d: %w", gate.PID, identityErr)
	}
	if !alive || identity != gate.Identity {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return gateRecord{}, false, err
		}
		return gateRecord{}, false, nil
	}
	return gate, true, nil
}

func pruneStateLocked(stateDir string) error {
	records, err := readRecordsLocked(stateDir)
	if err != nil {
		return err
	}
	for _, rec := range records {
		if rec.Status != StatusQueued && rec.Status != StatusBlocked && rec.Status != StatusStarting && rec.Status != StatusRunning && rec.Status != StatusStalled {
			continue
		}
		if rec.Status == StatusRunning || rec.Status == StatusStalled {
			if err := markLostLocked(stateDir, rec); err != nil {
				return fmt.Errorf("prune run %s: %w", rec.RunID, err)
			}
			continue
		}
		identity, alive, identityErr := inspectIdentity(rec.PID)
		if identityErr != nil {
			return fmt.Errorf("inspect keeper %d for run %s: %w", rec.PID, rec.RunID, identityErr)
		}
		if alive && identity == rec.StartedAt {
			continue
		}
		if rec.Status == StatusQueued || rec.Status == StatusBlocked || rec.Status == StatusStarting {
			if err := os.Remove(filepath.Join(stateDir, "runs", rec.RunID+".json")); err != nil && !os.IsNotExist(err) {
				return err
			}
			continue
		}

	}
	paths, err := filepath.Glob(filepath.Join(stateDir, "gates", "*.json"))
	if err != nil {
		return err
	}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var gate gateRecord
		if err := json.Unmarshal(data, &gate); err != nil {
			return fmt.Errorf("decode gate record %s: %w", path, err)
		}
		identity, alive, identityErr := inspectIdentity(gate.PID)
		if identityErr != nil {
			return fmt.Errorf("inspect gate holder %d: %w", gate.PID, identityErr)
		}
		if !alive || identity != gate.Identity {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
	}
	return nil
}

// recordObservedKeeperLoss finalizes a keeper that this run's own Wait observed
// exit. Registry readers use the same locked transition when they discover a
// stale identity first.
func recordObservedKeeperLoss(stateDir, runID string) error {
	return withStateLock(stateDir, func() error {
		rec, err := readRecordLocked(stateDir, runID)
		if err != nil {
			return err
		}
		return markLostLocked(stateDir, rec)
	})
}

func markLostLocked(stateDir string, rec record) error {
	if rec.Status != StatusRunning && rec.Status != StatusStalled {
		return nil
	}
	identity, alive, err := inspectIdentity(rec.PID)
	if err != nil {
		return fmt.Errorf("inspect keeper %d for run %s: %w", rec.PID, rec.RunID, err)
	}
	if alive && identity == rec.StartedAt {
		return nil
	}
	cleanup := cleanupLostRun(rec, osProcessSource{})
	resources, resourceErr := cleanupResourcesLocked(stateDir, rec.RunID)
	resourceErr = errors.Join(resourceErr, resourceOutcomesError(resources))
	scratch := rec.Scratch
	if scratch == "" {
		scratch = filepath.Join(stateDir, "runs", rec.RunID, "scratch")
	}
	scratchOutcome, scratchErr := cleanupScratchOutcome(stateDir, scratch, fileIdentity{Device: rec.ScratchDevice, Inode: rec.ScratchInode}, rec.Keep)
	resources = append(resources, scratchOutcome)
	if scratchErr != nil {
		resourceErr = errors.Join(resourceErr, fmt.Errorf("remove run scratch: %w", scratchErr))
	}
	rec.Status = StatusLost
	rec.Error = fmt.Sprintf("keeper PID %d identity %s is no longer live", rec.PID, rec.StartedAt)
	if cleanup.err != nil {
		rec.Error += "; " + cleanup.err.Error()
	}
	if resourceErr != nil {
		rec.Status = StatusDirty
		rec.Error += "; resource or scratch cleanup failed: " + resourceErr.Error()
		if scratchErr != nil {
			rec.Error += fmt.Sprintf("; scratch path %q cleanup failed: %v", scratch, scratchErr)
		}
	}
	if err := writeRecordAtomic(rec); err != nil {
		return fmt.Errorf("record lost run %s: %w", rec.RunID, err)
	}
	lost := eventFor(rec, EventLost)
	lost.Error = rec.Error
	if err := appendEvent(stateDir, lost); err != nil {
		return fmt.Errorf("record lost event for run %s: %w", rec.RunID, err)
	}
	teardown := eventFor(rec, EventTeardown)
	teardown.Reaped = cleanup.Reaped
	teardown.Unverified = cleanup.Unverified
	teardown.Remaining = cleanup.Remaining
	teardown.Resources = resources
	if cleanup.err != nil {
		teardown.Error = cleanup.err.Error()
	}
	if err := appendEvent(stateDir, teardown); err != nil {
		return fmt.Errorf("record lost teardown for run %s: %w", rec.RunID, err)
	}
	if rec.HoldGate != "" {
		gate, exists, err := readGateLocked(stateDir, rec.HoldGate)
		if err != nil {
			return err
		}
		if exists && gate.RunID == rec.RunID {
			if err := os.Remove(gatePath(stateDir, rec.HoldGate)); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
	}
	return nil
}

func cleanupAdmission(stateDir, runID, reason string) {
	if err := withStateLock(stateDir, func() error {
		rec, err := readRecordLocked(stateDir, runID)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if rec.HoldGate != "" {
			gate, exists, err := readGateLocked(stateDir, rec.HoldGate)
			if err != nil {
				return err
			}
			if exists && gate.RunID == runID {
				if err := os.Remove(gatePath(stateDir, rec.HoldGate)); err != nil && !os.IsNotExist(err) {
					return err
				}
			}
		}
		scratch := rec.Scratch
		if scratch == "" {
			scratch = filepath.Join(stateDir, "runs", runID, "scratch")
		}
		scratchOutcome, scratchErr := cleanupScratchOutcome(stateDir, scratch, fileIdentity{Device: rec.ScratchDevice, Inode: rec.ScratchInode}, rec.Keep)
		event := eventFor(rec, EventTeardown)
		event.Reason = reason
		event.Resources = append(event.Resources, scratchOutcome)
		if scratchErr != nil {
			event.Error = scratchErr.Error()
			rec.Status, rec.Error = StatusDirty, scratchErr.Error()
			if err := writeRecordAtomic(rec); err != nil {
				return errors.Join(scratchErr, err)
			}
		} else if err := os.Remove(filepath.Join(stateDir, "runs", runID+".json")); err != nil && !os.IsNotExist(err) {
			return err
		}
		return appendEvent(stateDir, event)
	}); err != nil {
		fmt.Fprintf(os.Stderr, "proc: remove incomplete admission %s: %v\n", runID, err)
	}
}

func updateRecord(stateDir, runID string, update func(*record) error) error {
	return withStateLock(stateDir, func() error {
		rec, err := readRecordLocked(stateDir, runID)
		if err != nil {
			return err
		}
		if err := update(&rec); err != nil {
			return err
		}
		return writeRecordAtomic(rec)
	})
}

func completeRecord(stateDir, runID string, code *int, signal string, duration time.Duration, final record) error {
	return updateRecord(stateDir, runID, func(rec *record) error {
		if !rec.Status.Terminal() {
			rec.Status = StatusDead
		}
		rec.ExitCode = code
		rec.Signal = signal
		rec.DurationMS = duration.Milliseconds()
		rec.Observed = final.Observed
		rec.PeakRSSBytes, rec.AverageCPU, rec.MeasurementPartial = final.PeakRSSBytes, final.AverageCPU, final.MeasurementPartial
		rec.OverBudget, rec.LimitsAdvisory, rec.GrantedCPUs = final.OverBudget, final.LimitsAdvisory, final.GrantedCPUs
		rec.WaitReason = ""
		return nil
	})
}

func WaitTag(ctx context.Context, stateDir, tag string) (RunInfo, error) {
	return waitRun(ctx, stateDir, tag, false)
}

func WaitLatestTag(ctx context.Context, stateDir, tag string) (RunInfo, error) {
	return waitRun(ctx, stateDir, tag, true)
}

func waitRun(ctx context.Context, stateDir, tag string, latest bool) (RunInfo, error) {
	if ctx == nil {
		return RunInfo{}, errors.New("proc: wait context is required")
	}
	ticker := time.NewTicker(admissionPollInterval)
	defer ticker.Stop()
	for {
		var match *record
		err := withStateLock(stateDir, func() error {
			if err := pruneStateLocked(stateDir); err != nil {
				return err
			}
			records, err := readRecordsLocked(stateDir)
			if err != nil {
				return err
			}
			for i := range records {
				matches := records[i].Tag == tag
				if latest {
					matches = records[i].LatestTag == tag
				}
				if !matches {
					continue
				}
				if !latest && match != nil {
					return fmt.Errorf("tag %q identifies more than one run", tag)
				}
				if match == nil || records[i].EnqueuedAt.After(match.EnqueuedAt) ||
					(records[i].EnqueuedAt.Equal(match.EnqueuedAt) && records[i].RunID > match.RunID) {
					copy := records[i]
					match = &copy
				}
			}
			return nil
		})
		if err != nil {
			return RunInfo{}, err
		}
		if match != nil && match.Status.Terminal() {
			switch match.Status {
			case StatusDead:
				if match.ExitCode != nil || match.Signal != "" {
					return RunInfo{RunID: match.RunID, Tag: match.Tag, PID: match.PID, StartedAt: match.StartedAt, Status: match.Status, Command: match.Command, SlotGroup: match.SlotGroup, ExitCode: match.ExitCode, Signal: match.Signal, DurationMS: match.DurationMS}, nil
				}
			case StatusLost, StatusDirty:
				return RunInfo{RunID: match.RunID, Tag: match.Tag, PID: match.PID, StartedAt: match.StartedAt, Status: match.Status, Command: match.Command, Error: match.Error, DurationMS: match.DurationMS}, nil
			}
		}
		select {
		case <-ctx.Done():
			return RunInfo{}, fmt.Errorf("wait for run %q: %w", tag, ctx.Err())
		case <-ticker.C:
		}
	}
}

func claimGate(stateDir, runID, name string, pid int, identity string) error {
	if name == "" {
		return nil
	}
	return withStateLock(stateDir, func() error {
		gate, exists, err := readGateLocked(stateDir, name)
		if err != nil {
			return err
		}
		if !exists || gate.RunID != runID {
			return fmt.Errorf("gate %q reservation was lost before keeper startup", name)
		}
		gate.PID, gate.Identity = pid, identity
		return writeGateLocked(stateDir, gate)
	})
}

func releaseGate(stateDir, runID, name string) error {
	if name == "" {
		return nil
	}
	return withStateLock(stateDir, func() error {
		gate, exists, err := readGateLocked(stateDir, name)
		if err != nil || !exists || gate.RunID != runID {
			return err
		}
		if err := os.Remove(gatePath(stateDir, name)); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	})
}
