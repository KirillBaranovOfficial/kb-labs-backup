//go:build darwin || linux

package proc

import (
	"fmt"
	"strings"
	"sync"
	"syscall"
	"time"
)

func applyRunTokenDescriptors(processes []Process, runID string, inspect func(int, fileIdentity) (bool, error)) ([]Process, error) {
	token, exists, err := runTokenIdentity(runID)
	if err != nil {
		return processes, err
	}
	if !exists {
		return processes, nil
	}
	out := append([]Process(nil), processes...)
	currentUID := uint32(syscall.Getuid())
	for index := range out {
		if out[index].uid != currentUID {
			out[index].tokenResult = "out-of-scope"
			continue
		}
		matched, inspectErr := inspect(out[index].PID, token)
		if processGone(inspectErr) {
			out[index].tokenResult = "gone-during-token-scan"
			out[index].tokenReadError = inspectErr.Error()
			continue
		}
		if inspectErr != nil {
			out[index].tokenResult = "unreadable"
			out[index].tokenReadError = inspectErr.Error()
			continue
		}
		if matched {
			out[index].runToken = true
			out[index].tokenResult = "match"
		} else {
			out[index].tokenResult = "absent"
		}
	}
	return out, nil
}

type processMarkerResult struct {
	runID        string
	unverified   bool
	markerResult string
	markerError  string
}

type processMarkerCache struct {
	mu      sync.Mutex
	entries map[string]processMarkerResult
}

func newProcessMarkerCache() *processMarkerCache {
	return &processMarkerCache{entries: make(map[string]processMarkerResult)}
}

var hostProcessMarkerCache = newProcessMarkerCache()

func (cache *processMarkerCache) lookup(process Process, inspect func(int) (string, bool, error)) processMarkerResult {
	key := processObservationKey(process)
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if result, exists := cache.entries[key]; exists {
		return result
	}
	marker, unverified, err := inspect(process.PID)
	if processGone(err) {
		return processMarkerResult{markerResult: "gone-during-marker-read", markerError: err.Error()}
	}
	if err != nil {
		unverified = true
	}
	markerResult := "absent"
	if err != nil || unverified {
		markerResult = "unreadable"
	} else if marker != "" {
		markerResult = "other-run"
	}
	markerError := ""
	if err != nil {
		markerError = err.Error()
	}
	result := processMarkerResult{runID: marker, unverified: unverified, markerResult: markerResult, markerError: markerError}
	cache.entries[key] = result
	return result
}

func processInspectionCandidates(processes []Process, rootPID int) map[int]bool {
	candidates := make(map[int]bool)
	for _, process := range processes {
		if process.PID != rootPID {
			candidates[process.PID] = true
		}
	}
	return candidates
}

func runMarkerRecoveryCandidates(processes []Process, runStartedAt time.Time) ([]Process, error) {
	if runStartedAt.IsZero() {
		return nil, fmt.Errorf("lost-run recovery requires the durable run start time")
	}
	var candidates []Process
	for _, process := range processes {
		if !process.runMarker {
			continue
		}
		if process.StartedAt.IsZero() {
			return nil, fmt.Errorf("run-marked process %d has no process start time", process.PID)
		}
		if process.StartedAt.Before(runStartedAt) {
			continue
		}
		candidates = append(candidates, process)
	}
	return candidates, nil
}

func applyCachedProcessMarkers(processes []Process, candidates map[int]bool, runID string, cache *processMarkerCache, inspect func(int) (string, bool, error)) []Process {
	if runID == "" {
		return processes
	}
	out := make([]Process, 0, len(processes))
	for _, process := range processes {
		if !candidates[process.PID] {
			out = append(out, process)
			continue
		}
		result := cache.lookup(process, inspect)
		process.runMarker = result.runID == runID
		switch {
		case result.markerResult == "gone-during-marker-read":
			process.markerResult = result.markerResult
		case result.unverified:
			process.markerResult = "unreadable"
		case result.runID == runID:
			process.markerResult = "match"
		case result.runID == "":
			process.markerResult = "absent"
		default:
			process.markerResult = "other-run"
		}
		process.Unverified = process.Unverified || result.unverified
		process.markerReadError = result.markerError
		out = append(out, process)
	}
	return out
}

func processMarkerValue(data []byte) string {
	for _, value := range strings.Split(string(data), "\x00") {
		name, result, found := strings.Cut(value, "=")
		if found && name == runEnv {
			return result
		}
	}
	return ""
}
