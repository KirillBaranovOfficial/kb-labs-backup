//go:build darwin || linux

package proc

import (
	"strings"
	"sync"
)

type processMarkerResult struct {
	runID      string
	unverified bool
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
		return processMarkerResult{}
	}
	if err != nil {
		unverified = true
	}
	result := processMarkerResult{runID: marker, unverified: unverified}
	cache.entries[key] = result
	return result
}

func processInspectionCandidates(processes []Process, rootPID int, uid uint32) map[int]bool {
	children := make(map[int][]int)
	for _, process := range processes {
		children[process.PPID] = append(children[process.PPID], process.PID)
	}
	ancestry := make(map[int]bool)
	queue := []int{rootPID}
	ancestry[rootPID] = true
	for len(queue) > 0 {
		pid := queue[0]
		queue = queue[1:]
		for _, child := range children[pid] {
			if !ancestry[child] {
				ancestry[child] = true
				queue = append(queue, child)
			}
		}
	}
	candidates := make(map[int]bool)
	for _, process := range processes {
		if process.uid == uid && !ancestry[process.PID] {
			candidates[process.PID] = true
		}
	}
	return candidates
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
		process.Unverified = process.Unverified || result.unverified
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
