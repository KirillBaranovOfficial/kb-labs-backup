//go:build darwin || linux

package proc

func processInspectionCandidates(processes []Process, rootPID int, uid uint32) map[int]bool {
	children := make(map[int][]int)
	for _, process := range processes {
		children[process.PPID] = append(children[process.PPID], process.PID)
	}
	candidates := make(map[int]bool)
	queue := []int{rootPID}
	candidates[rootPID] = true
	for len(queue) > 0 {
		pid := queue[0]
		queue = queue[1:]
		for _, child := range children[pid] {
			if !candidates[child] {
				candidates[child] = true
				queue = append(queue, child)
			}
		}
	}
	for _, process := range processes {
		if process.uid == uid {
			candidates[process.PID] = true
		}
	}
	return candidates
}

func applyProcessMarkers(processes []Process, candidates map[int]bool, runID string, inspect func(int, string) (bool, bool, error)) []Process {
	if runID == "" {
		return processes
	}
	out := make([]Process, 0, len(processes))
	for _, process := range processes {
		if !candidates[process.PID] {
			out = append(out, process)
			continue
		}
		marker, unverified, err := inspect(process.PID, runID)
		if processGone(err) {
			continue
		}
		if err != nil {
			unverified = true
		}
		process.runMarker = marker
		process.Unverified = process.Unverified || unverified
		out = append(out, process)
	}
	return out
}
