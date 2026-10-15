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

func reserveAdmission(ctx context.Context, cfg Config, runID string, parentPID int, parentIdentity string) error {
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
		rec := record{
			RunID: runID, Tag: cfg.Tag, PID: parentPID, StartedAt: parentIdentity,
			Command: cfg.Command, StateDir: cfg.StateDir, Status: StatusQueued,
			SlotGroup: cfg.SlotGroup, Slots: cfg.Slots, QueueOrder: order,
			EnqueuedAt: time.Now().UTC(), WaitGate: cfg.WaitGate, HoldGate: cfg.HoldGate, MinFree: cfg.MinFree,
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
			reason, status, position, err := admissionStateLocked(cfg.StateDir, rec)
			if err != nil {
				return err
			}
			if reason == "" {
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
				statusChanged := rec.Status != status
				rec.Status = status
				rec.WaitReason = reason
				if err := writeRecordAtomic(rec); err != nil {
					return err
				}
				if !statusChanged && status == StatusBlocked {
					return nil
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
	if candidate.MinFree > 0 {
		free, err := freeDiskBytes(stateDir)
		if err != nil {
			return "", "", 0, err
		}
		if free < candidate.MinFree {
			return fmt.Sprintf("disk budget: need %d bytes, have %d", candidate.MinFree, free), StatusBlocked, position, nil
		}
	}
	return "", StatusStarting, position, nil
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
			resources, resourceErr := cleanupResourcesLocked(stateDir, rec.RunID)
			resourceErr = errors.Join(resourceErr, resourceOutcomesError(resources))
			if rec.Scratch != "" {
				scratchOutcome, scratchErr := cleanupScratchOutcome(rec.StateDir, rec.Scratch, fileIdentity{Device: rec.ScratchDevice, Inode: rec.ScratchInode}, rec.Keep)
				resources = append(resources, scratchOutcome)
				if scratchErr != nil {
					resourceErr = errors.Join(resourceErr, fmt.Errorf("remove run scratch: %w", scratchErr))
				}
			}
			rec.Status = StatusLost
			rec.Error = fmt.Sprintf("keeper PID %d identity %s is no longer live", rec.PID, rec.StartedAt)
			if cleanup.err != nil {
				rec.Error += "; " + cleanup.err.Error()
			}
			if resourceErr != nil {
				rec.Status = StatusDirty
				rec.Error += "; resource cleanup: " + resourceErr.Error()
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

func completeRecord(stateDir, runID string, code *int, signal string, duration time.Duration, observed []Process) error {
	return updateRecord(stateDir, runID, func(rec *record) error {
		if rec.Status != StatusDirty && rec.Status != StatusLost {
			rec.Status = StatusDead
		}
		rec.ExitCode = code
		rec.Signal = signal
		rec.DurationMS = duration.Milliseconds()
		rec.Observed = observed
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
