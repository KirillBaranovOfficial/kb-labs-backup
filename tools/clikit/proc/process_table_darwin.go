//go:build darwin

package proc

/*
#cgo LDFLAGS: -lproc
#include <libproc.h>
#include <sys/proc.h>
#include <stdint.h>

static int kb_proc_metrics(pid_t pid, uint64_t *rss, uint64_t *cpu_ns) {
	struct rusage_info_v2 usage;
	if (proc_pid_rusage(pid, RUSAGE_INFO_V2, (rusage_info_t *)&usage) != 0) return 0;
	*rss = usage.ri_resident_size;
	*cpu_ns = usage.ri_user_time + usage.ri_system_time;
	return 1;
}
*/
import "C"
import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

func darwinProcessSnapshot(runID string) ([]Process, error) {
	infos, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil, err
	}
	processes := make([]Process, 0, len(infos))
	for _, info := range infos {
		if info.Proc.P_pid <= 0 || info.Proc.P_stat == 5 {
			continue
		}
		started := time.Unix(info.Proc.P_starttime.Sec, int64(info.Proc.P_starttime.Usec)*1000)
		processes = append(processes, Process{
			PID: int(info.Proc.P_pid), PPID: int(info.Eproc.Ppid),
			Command:   darwinCommandName(info.Proc.P_comm[:]),
			StartedAt: started, ElapsedMS: max(int64(0), time.Since(started).Milliseconds()),
			Identity: formatStart(&info), uid: info.Eproc.Ucred.Uid,
		})
	}
	candidates := processInspectionCandidates(processes, syscall.Getpid(), uint32(unix.Getuid()))
	return applyCachedProcessMarkers(processes, candidates, runID, hostProcessMarkerCache, inspectProcessMarker), nil
}

func (osProcessSource) Metrics(tree []Process) ([]Process, error) {
	return darwinProcessMetrics(tree)
}

func darwinProcessMetrics(tree []Process) ([]Process, error) {
	out := append([]Process(nil), tree...)
	var failures []error
	for index := range out {
		if out[index].Unverified {
			continue
		}
		info, err := unix.SysctlKinfoProc("kern.proc.pid", out[index].PID)
		if err != nil {
			failures = append(failures, fmt.Errorf("inspect process %d for metrics: %w", out[index].PID, err))
			continue
		}
		if formatStart(info) != out[index].Identity {
			continue
		}
		var rss, cpuNS C.uint64_t
		if C.kb_proc_metrics(C.pid_t(out[index].PID), &rss, &cpuNS) == 0 {
			failures = append(failures, fmt.Errorf("read resource metrics for process %d", out[index].PID))
			continue
		}
		out[index].RSSBytes = uint64(rss)
		out[index].CPUSeconds = float64(cpuNS) / 1e9
		out[index].MetricsAvailable = true
	}
	return out, errors.Join(failures...)
}

func darwinCommandName(comm []byte) string {
	if end := bytes.IndexByte(comm, 0); end >= 0 {
		comm = comm[:end]
	}
	return string(comm)
}

// kern.procargs2 exposes the current exec image's argv and environment. execve
// can replace the environment without changing PID or process start time, so
// the cache captures it at the first non-ancestral observation. A process
// already seen in ancestry remains owned by persisted identity history if a
// later exec removes the marker; an unrelated process first seen without a
// marker is not retroactively adopted if its later exec adds one.
func inspectProcessMarker(pid int) (string, bool, error) {
	args, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil {
		if processGone(err) {
			return "", false, err
		}
		return "", true, nil
	}
	_, position, err := parseProcargs2(args)
	if err != nil {
		return "", true, err
	}
	return processMarkerValue(args[position:]), false, nil
}

func procargs2Arguments(data []byte) ([]string, error) {
	argv, _, err := parseProcargs2(data)
	return argv, err
}

func parseProcargs2(data []byte) ([]string, int, error) {
	if len(data) < 5 {
		return nil, 0, errors.New("kern.procargs2 record is truncated before argv")
	}
	argc := int(int32(binary.NativeEndian.Uint32(data[:4])))
	if argc < 0 || argc > 1<<16 {
		return nil, 0, fmt.Errorf("kern.procargs2 record has invalid argc %d", argc)
	}
	position := 4
	execPathEnd := bytes.IndexByte(data[position:], 0)
	if execPathEnd < 0 {
		return nil, 0, errors.New("kern.procargs2 record has no exec-path terminator")
	}
	position += execPathEnd + 1
	for position < len(data) && data[position] == 0 {
		position++
	}
	argv := make([]string, 0, argc)
	for index := 0; index < argc; index++ {
		if position >= len(data) {
			return nil, 0, fmt.Errorf("kern.procargs2 record ends before argv element %d", index)
		}
		end := bytes.IndexByte(data[position:], 0)
		if end < 0 {
			return nil, 0, fmt.Errorf("kern.procargs2 argv element %d has no NUL terminator", index)
		}
		argv = append(argv, string(data[position:position+end]))
		position += end + 1
	}
	return argv, position, nil
}

func procargs2HasEnvironment(data []byte) bool {
	_, position, err := parseProcargs2(data)
	if err != nil {
		return false
	}
	for _, item := range bytes.Split(data[position:], []byte{0}) {
		if bytes.Contains(item, []byte{'='}) {
			return true
		}
	}
	return false
}
