//go:build darwin || linux

package proc

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"syscall"
	"time"
)

type governorState struct {
	At       time.Time            `json:"at"`
	Host     HostResourceSnapshot `json:"host"`
	Decision string               `json:"decision"`
	Reason   string               `json:"reason,omitempty"`
	Hold     pressureState        `json:"hold"`
}

func governorHardPressure(sample, previous HostResourceSnapshot, freeDisk, diskFloor uint64) string {
	if diskFloor > 0 && freeDisk < diskFloor {
		return fmt.Sprintf("free disk %s is below floor %s", FormatResourceBytes(freeDisk), FormatResourceBytes(diskFloor))
	}
	if sample.Pressure == "memory critical" || sample.Pressure == "memory psi elevated" {
		return "hard host memory pressure (" + sample.Pressure + ")"
	}
	if sample.PhysicalMemory > 0 && sample.MemoryAvailableKnown && sample.AvailableMemory < sample.PhysicalMemory*lowAvailableMemoryFraction/100 {
		return "hard host pressure (available memory below 10% of physical)"
	}
	if sample.CPUPressure >= pressurePSIThreshold {
		return fmt.Sprintf("hard host CPU pressure (%.2f%% PSI)", sample.CPUPressure)
	}
	if sample.SwapOutsKnown && previous.SwapOutsKnown && sample.SwapOuts > previous.SwapOuts {
		return fmt.Sprintf("swapouts increased from %d to %d", previous.SwapOuts, sample.SwapOuts)
	}
	if sample.SwapUsedBytes > previous.SwapUsedBytes && previous.SwapUsedBytes > 0 {
		return fmt.Sprintf("swap used increased from %s to %s", FormatResourceBytes(previous.SwapUsedBytes), FormatResourceBytes(sample.SwapUsedBytes))
	}
	return ""
}

func governorAdmissionReason(class CostClass, sample HostResourceSnapshot, freeDisk, diskFloor uint64) string {
	if class != CostHeavy {
		return ""
	}
	if reason := pressureWaitReason(sample); isElevatedPressure(sample) {
		return reason
	}
	if diskFloor > 0 && freeDisk < diskFloor {
		return governorHardPressure(sample, HostResourceSnapshot{}, freeDisk, diskFloor)
	}
	return ""
}

func lowestPriorityHeavy(records []record) *record {
	var target *record
	for index := range records {
		rec := &records[index]
		if rec.CostClass != CostHeavy || rec.Status == StatusPaused ||
			(rec.Status != StatusRunning && rec.Status != StatusStalled && rec.Status != StatusStarting) {
			continue
		}
		if target == nil || rec.Priority < target.Priority ||
			(rec.Priority == target.Priority && (rec.QueueOrder > target.QueueOrder ||
				(rec.QueueOrder == target.QueueOrder && rec.EnqueuedAt.After(target.EnqueuedAt)))) {
			target = rec
		}
	}
	return target
}

func readGovernorState(stateDir string) (governorState, error) {
	var state governorState
	data, err := os.ReadFile(filepath.Join(stateDir, "governor-state.json"))
	if os.IsNotExist(err) {
		return state, nil
	}
	if err != nil {
		return state, err
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return state, fmt.Errorf("decode governor state: %w", err)
	}
	return state, nil
}

func writeGovernorState(stateDir string, state governorState) error {
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	path := filepath.Join(stateDir, "governor-state.json")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return errors.Join(err, os.Remove(tmp))
	}
	return nil
}

func reconcileHostGovernor(stateDir string, source HostResourceSource) error {
	return reconcileHostGovernorWithProcessSource(stateDir, source, osProcessSource{})
}

func reconcileHostGovernorWithProcessSource(stateDir string, source HostResourceSource, processSource processSource) error {
	return withStateLock(stateDir, func() error {
		state, err := readGovernorState(stateDir)
		if err != nil {
			return err
		}
		records, err := readLiveRecordsLocked(stateDir)
		if err != nil {
			return err
		}
		sample := source.Snapshot()
		free, diskErr := hostFreeDiskBytes(stateDir, source)
		if diskErr != nil {
			return diskErr
		}
		sample.FreeDiskBytes, sample.FreeDiskKnown = free, true
		quota, err := effectiveQuota(stateDir, source)
		if err != nil {
			return err
		}
		floor := quota.MinFree
		for _, rec := range records {
			if rec.CostClass == CostHeavy && !rec.Status.Terminal() {
				floor = max(floor, rec.MinFree)
			}
		}
		reason := governorHardPressure(sample, state.Host, free, floor)
		recovering := false
		if reason != "" {
			state.Hold.blocks(true, time.Now())
			state.Reason = reason
		} else if state.Hold.blocks(false, time.Now()) {
			recovering = true
		} else {
			state.Reason = ""
		}
		state.At, state.Host = time.Now().UTC(), sample
		state.Decision = "normal"
		if reason != "" {
			state.Decision = "pressure"
			target := lowestPriorityHeavy(records)
			if target != nil {
				if signalErr := signalRunTreeWithSource(*target, syscall.SIGSTOP, processSource); signalErr != nil {
					state.Decision = "pause unavailable"
					event := eventFor(*target, EventPressure)
					event.Reason, event.Error, event.Pressure, event.GovernorDecision = reason, signalErr.Error(), sample, state.Decision
					if err := appendEvent(stateDir, event); err != nil {
						return errors.Join(signalErr, err)
					}
					return writeGovernorState(stateDir, state)
				}
				target.Status, target.PausedAt = StatusPaused, time.Now().UTC()
				event := eventFor(*target, EventPaused)
				state.Decision = "paused " + target.Tag
				event.Reason, event.Pressure, event.GovernorDecision = reason, sample, state.Decision
				event.DiskFreeBytes, event.DiskNeedBytes = free, floor
				if err := appendEvent(stateDir, event); err != nil {
					return err
				}
				if err := writeRecordAtomic(*target); err != nil {
					return err
				}
			}
		} else if !recovering {
			for index := range records {
				rec := &records[index]
				if rec.Status != StatusPaused {
					continue
				}
				pausedFor := time.Since(rec.PausedAt)
				if err := signalRunTreeWithSource(*rec, syscall.SIGCONT, processSource); err != nil {
					return err
				}
				rec.Status, rec.PausedAt = StatusRunning, time.Time{}
				rec.PausedMS += pausedFor.Milliseconds()
				event := eventFor(*rec, EventResumed)
				event.DurationMS, event.PausedMS, event.Pressure = pausedFor.Milliseconds(), rec.PausedMS, sample
				event.GovernorDecision = "normal"
				if err := appendEvent(stateDir, event); err != nil {
					return err
				}
				if err := writeRecordAtomic(*rec); err != nil {
					return err
				}
			}
		} else {
			state.Decision = "recovering"
		}
		return writeGovernorState(stateDir, state)
	})
}

func hostFreeDiskBytes(stateDir string, source HostResourceSource) (uint64, error) {
	if sample := source.Snapshot(); sample.FreeDiskKnown {
		return sample.FreeDiskBytes, nil
	}
	return freeDiskBytes(stateDir)
}

func signalRunTree(rec record, signal syscall.Signal) error {
	return signalRunTreeWithSource(rec, signal, osProcessSource{})
}

func signalRunTreeWithSource(rec record, signal syscall.Signal, source processSource) error {
	tree, err := processTreeWithHistorySource(source, rec.PID, rec.RunID, rec.Observed)
	if err != nil {
		return err
	}
	if len(tree) == 0 {
		return fmt.Errorf("run %s has no verified process tree to signal with %s", rec.Tag, signal)
	}
	sort.Slice(tree, func(i, j int) bool {
		if signal == syscall.SIGSTOP {
			return tree[i].PID > tree[j].PID
		}
		return tree[i].PID < tree[j].PID
	})
	var signaled []int
	for _, process := range tree {
		if process.Unverified {
			continue
		}
		identity, live, identityErr := source.Identity(process.PID)
		if identityErr != nil {
			return errors.Join(fmt.Errorf("verify process %d before %s: %w", process.PID, signal, identityErr), resumeSignaled(source, signaled))
		}
		if !live || identity != process.Identity {
			continue
		}
		if err := source.Signal(process.PID, signal); err != nil && !errors.Is(err, syscall.ESRCH) {
			return errors.Join(fmt.Errorf("signal process %d with %s: %w", process.PID, signal, err), resumeSignaled(source, signaled))
		}
		signaled = append(signaled, process.PID)
	}
	if len(signaled) == 0 {
		return fmt.Errorf("run %s has no verified process identities to signal with %s", rec.Tag, signal)
	}
	return nil
}

func resumeSignaled(source processSource, pids []int) error {
	var failures []error
	for _, pid := range pids {
		if err := source.Signal(pid, syscall.SIGCONT); err != nil && !errors.Is(err, syscall.ESRCH) {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}
