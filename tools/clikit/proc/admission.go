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

func reserveAdmission(ctx context.Context, cfg Config, runID string, parentPID int, parentIdentity string, source ResourceSource) error {
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
		cost := effectiveResourceUsage(resourceUsage{MemoryBytes: cfg.MemoryBytes, MinCPUs: cfg.CPUsMin, DiskBytes: cfg.DiskBytes}, learned)
		rec := record{
			RunID: runID, Tag: cfg.Tag, PID: parentPID, StartedAt: parentIdentity,
			Command: cfg.Command, StateDir: cfg.StateDir, Status: StatusQueued,
			SlotGroup: cfg.SlotGroup, Slots: cfg.Slots, QueueOrder: order,
			EnqueuedAt: time.Now().UTC(), WaitGate: cfg.WaitGate, HoldGate: cfg.HoldGate,
			CostKey: cfg.CostKey, MemoryBytes: cost.MemoryBytes, CPUsMin: cost.MinCPUs, CPUsMax: cfg.CPUsMax, DiskBytes: cost.DiskBytes,
		}
		if err := writeRecordAtomic(rec); err != nil {
			return fmt.Errorf("proc: record queued run: %w", err)
		}
		event := eventFor(rec, EventQueued)
		return appendEvent(cfg.StateDir, event)
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
			reason, status, position, err := admissionStateLockedWithSource(cfg.StateDir, rec, source)
			if err != nil {
				return err
			}
			notice := resourceNotice(source.Snapshot())
			if reason == "" {
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
			if rec.Status != status || rec.WaitReason != reason {
				rec.Status = status
				rec.WaitReason = reason
				rec.ResourceNotice = notice
				if err := writeRecordAtomic(rec); err != nil {
					return err
				}
				eventType := EventQueued
				if status == StatusBlocked {
					eventType = EventBlocked
				}
				event := eventFor(rec, eventType)
				event.Reason = reason
				event.Position = position
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

func admissionStateLocked(stateDir string, candidate record) (string, RunStatus, int, error) {
	return admissionStateLockedWithSource(stateDir, candidate, systemResourceSource())
}

func admissionStateLockedWithSource(stateDir string, candidate record, source ResourceSource) (string, RunStatus, int, error) {
	records, err := readRecordsLocked(stateDir)
	if err != nil {
		return "", "", 0, err
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
			return fmt.Sprintf("slot group %s is full", candidate.SlotGroup), StatusQueued, position, nil
		}
		if queuedBefore > 0 {
			return fmt.Sprintf("waiting behind %d earlier run(s) in slot group %s", queuedBefore, candidate.SlotGroup), StatusQueued, position, nil
		}
	}
	if candidate.WaitGate != "" {
		if _, exists, err := readGateLocked(stateDir, candidate.WaitGate); err != nil {
			return "", "", 0, err
		} else if exists {
			return "gate: " + candidate.WaitGate, StatusBlocked, position, nil
		}
	}
	if candidate.HoldGate != "" {
		if _, exists, err := readGateLocked(stateDir, candidate.HoldGate); err != nil {
			return "", "", 0, err
		} else if exists {
			return "gate: " + candidate.HoldGate, StatusBlocked, position, nil
		}
		for _, rec := range records {
			if rec.RunID != candidate.RunID && rec.HoldGate == candidate.HoldGate &&
				(rec.Status == StatusQueued || rec.Status == StatusBlocked) && rec.QueueOrder < candidate.QueueOrder {
				return "waiting for gate holder " + candidate.HoldGate, StatusBlocked, position, nil
			}
		}
	}
	quota, err := effectiveQuota(stateDir, source)
	if err != nil {
		return "", "", 0, err
	}
	activeUsage := resourceUsage{}
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
		return fmt.Sprintf("waiting behind %d earlier run(s)", queuedBefore), StatusQueued, position + queuedBefore, nil
	}
	if reason, err := admissionPressureLocked(stateDir, candidate, source); err != nil {
		return "", "", 0, err
	} else if reason != "" {
		return reason, StatusBlocked, position, nil
	}
	if candidate.DiskBytes > 0 || quota.MinFree > 0 {
		free, err := freeDiskBytes(stateDir)
		if err != nil {
			return "", "", 0, err
		}
		if !diskFits(free, activeUsage.DiskBytes, candidate.DiskBytes, quota.MinFree) {
			return fmt.Sprintf("disk budget (free %s, reserved %s, needs %s, min-free %s)", FormatResourceBytes(free), FormatResourceBytes(activeUsage.DiskBytes), FormatResourceBytes(candidate.DiskBytes), FormatResourceBytes(quota.MinFree)), StatusBlocked, position, nil
		}
	}
	activeCostedRun := activeUsage != (resourceUsage{})
	candidateCostedRun := candidate.MemoryBytes > 0 || candidate.CPUsMin > 0 || candidate.DiskBytes > 0
	if activeCostedRun && candidateCostedRun {
		if !resourceFits(activeUsage, resourceUsage{MemoryBytes: candidate.MemoryBytes, MinCPUs: candidate.CPUsMin}, quota) {
			reason := fmt.Sprintf("memory budget (reserved %s of %s, needs %s)", FormatResourceBytes(activeUsage.MemoryBytes), FormatResourceBytes(quota.MemoryBytes), FormatResourceBytes(candidate.MemoryBytes))
			if activeUsage.MemoryBytes+candidate.MemoryBytes <= quota.MemoryBytes {
				reason = fmt.Sprintf("cpu budget (reserved %d of %d, needs %d)", activeUsage.MinCPUs, quota.CPUs, candidate.CPUsMin)
			}
			return reason, StatusBlocked, position, nil
		}
		sample := source.Snapshot()
		if sample.MemoryAvailableKnown && memoryUnavailable(candidate.MemoryBytes, sample.AvailableMemory) {
			return fmt.Sprintf("available memory budget (have %s, needs %s)", FormatResourceBytes(sample.AvailableMemory), FormatResourceBytes(candidate.MemoryBytes)), StatusBlocked, position, nil
		}
	}
	return "", StatusStarting, position, nil
}

func admissionPressureLocked(stateDir string, candidate record, source ResourceSource) (string, error) {
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

func learnedUsage(records []record, key string) resourceUsage {
	if key == "" {
		return resourceUsage{}
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
	return resourceUsage{MemoryBytes: peak.PeakRSSBytes, MinCPUs: int(peak.AverageCPU + 0.999)}
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
		if rec.Status == StatusRunning || rec.Status == StatusStalled {
			cleanup := killTreeWithHistory(rec.PID, rec.RunID, rec.Observed)
			rec.Status = StatusLost
			rec.Error = fmt.Sprintf("keeper PID %d identity %s is no longer live", rec.PID, rec.StartedAt)
			if cleanup.err != nil {
				rec.Error += "; " + cleanup.err.Error()
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
			if cleanup.err != nil {
				teardown.Error = cleanup.err.Error()
			}
			if err := appendEvent(stateDir, teardown); err != nil {
				return fmt.Errorf("record lost teardown for run %s: %w", rec.RunID, err)
			}
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
		if err := os.Remove(filepath.Join(stateDir, "runs", runID+".json")); err != nil && !os.IsNotExist(err) {
			return err
		}
		event := eventFor(rec, EventTeardown)
		event.Reason = reason
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
		rec.Status = StatusDead
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
				if records[i].Tag != tag {
					continue
				}
				if match != nil {
					return fmt.Errorf("tag %q identifies more than one run", tag)
				}
				copy := records[i]
				match = &copy
			}
			return nil
		})
		if err != nil {
			return RunInfo{}, err
		}
		if match != nil && match.Status == StatusDead && (match.ExitCode != nil || match.Signal != "") {
			return RunInfo{RunID: match.RunID, Tag: match.Tag, PID: match.PID, StartedAt: match.StartedAt, Status: match.Status, Command: match.Command, SlotGroup: match.SlotGroup, ExitCode: match.ExitCode, Signal: match.Signal, DurationMS: match.DurationMS}, nil
		}
		if match != nil && match.Status == StatusLost {
			return RunInfo{RunID: match.RunID, Tag: match.Tag, PID: match.PID, StartedAt: match.StartedAt, Status: match.Status, Command: match.Command, Error: match.Error, DurationMS: match.DurationMS}, nil
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
