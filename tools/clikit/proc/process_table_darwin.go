//go:build darwin && cgo

package proc

/*
#cgo LDFLAGS: -lproc
#include <libproc.h>
#include <sys/proc.h>
#include <stdlib.h>
#include <string.h>
#include <errno.h>
#include <signal.h>

typedef struct {
	int pid;
	int ppid;
	long long start_sec;
	long long start_usec;
	unsigned int uid;
	char command[64];
	unsigned long long rss;
	unsigned long long cpu_ns;
	int metrics_available;
} kb_proc_info;

static kb_proc_info *kb_proc_snapshot(int *count, int *failure, int *failure_pid) {
	*failure = 0;
	*failure_pid = 0;
	errno = 0;
	int bytes = proc_listpids(PROC_ALL_PIDS, 0, NULL, 0);
	if (bytes < 0) { *failure = errno ? errno : EIO; *count = 0; return NULL; }
	if (bytes == 0) { *count = 0; return NULL; }
	int slots = bytes / (int)sizeof(pid_t) + 32;
	pid_t *pids = calloc((size_t)slots, sizeof(pid_t));
	if (pids == NULL) { *failure = ENOMEM; *count = 0; return NULL; }
	errno = 0;
	bytes = proc_listpids(PROC_ALL_PIDS, 0, pids, slots * (int)sizeof(pid_t));
	if (bytes < 0) { *failure = errno ? errno : EIO; free(pids); *count = 0; return NULL; }
	int max = bytes / (int)sizeof(pid_t);
	kb_proc_info *out = calloc((size_t)max, sizeof(kb_proc_info));
	if (out == NULL && max > 0) { *failure = ENOMEM; free(pids); *count = 0; return NULL; }
	int used = 0;
	for (int i = 0; i < max; i++) {
		if (pids[i] <= 0) continue;
		struct proc_bsdinfo info;
		errno = 0;
		int got = proc_pidinfo(pids[i], PROC_PIDTBSDINFO, 0, &info, sizeof(info));
		if (got != sizeof(info)) {
			int query_errno = errno;
			errno = 0;
			if (query_errno == ESRCH || query_errno == ENOENT || (kill(pids[i], 0) < 0 && (errno == ESRCH || errno == ENOENT))) continue;
			if (*failure == 0) {
				*failure_pid = (int)pids[i];
				*failure = query_errno ? query_errno : EIO;
			}
			continue;
		}
		if (info.pbi_status == SZOMB) continue;
		out[used].pid = (int)info.pbi_pid;
		out[used].ppid = (int)info.pbi_ppid;
		out[used].start_sec = info.pbi_start_tvsec;
		out[used].start_usec = info.pbi_start_tvusec;
		out[used].uid = info.pbi_uid;
		strncpy(out[used].command, info.pbi_comm, sizeof(out[used].command) - 1);
		struct rusage_info_v2 usage;
		if (proc_pid_rusage(pids[i], RUSAGE_INFO_V2, (rusage_info_t *)&usage) == 0) {
			out[used].rss = usage.ri_resident_size;
			out[used].cpu_ns = usage.ri_user_time + usage.ri_system_time;
			out[used].metrics_available = 1;
		}
		used++;
	}
	free(pids);
	*count = used;
	return out;
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
	"unsafe"

	"golang.org/x/sys/unix"
)

func darwinProcessSnapshot(runID string) ([]Process, error) {
	count := C.int(0)
	failure := C.int(0)
	failurePID := C.int(0)
	list := C.kb_proc_snapshot(&count, &failure, &failurePID)
	if count == 0 {
		if failure != 0 {
			return []Process{}, fmt.Errorf("inspect kern.proc entry for PID %d: %w", int(failurePID), syscall.Errno(failure))
		}
		return []Process{}, nil
	}
	if list == nil {
		return nil, errors.New("enumerate kern.proc process table: no snapshot returned")
	}
	defer C.free(unsafe.Pointer(list))
	infos := unsafe.Slice(list, int(count))
	processes := make([]Process, 0, len(infos))
	for _, info := range infos {
		started := time.Unix(int64(info.start_sec), int64(info.start_usec)*1000)
		pid := int(info.pid)
		marker := false
		unverified := false
		if runID != "" {
			var err error
			marker, unverified, err = inspectProcessMarker(pid, runID)
			if err != nil {
				return nil, fmt.Errorf("inspect process %d environment: %w", pid, err)
			}
		}
		processes = append(processes, Process{
			PID: pid, PPID: int(info.ppid), Command: C.GoString(&info.command[0]),
			StartedAt: started, ElapsedMS: max(int64(0), time.Since(started).Milliseconds()),
			Identity: timeIdentity(int64(info.start_sec), int64(info.start_usec)), runMarker: marker,
			Unverified: unverified, uid: uint32(info.uid), RSSBytes: uint64(info.rss), CPUSeconds: float64(info.cpu_ns) / 1e9, MetricsAvailable: info.metrics_available != 0,
		})
	}
	if failure != 0 {
		return processes, fmt.Errorf("inspect kern.proc entry for PID %d: %w", int(failurePID), syscall.Errno(failure))
	}
	return processes, nil
}

func inspectProcessMarker(pid int, runID string) (bool, bool, error) {
	args, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil {
		if processGone(err) {
			return false, false, nil
		}
		return false, true, nil
	}
	if bytes.Contains(args, []byte(runEnv+"="+runID)) {
		return true, false, nil
	}
	return false, !procargs2HasEnvironment(args), nil
}

func procargs2HasEnvironment(data []byte) bool {
	if len(data) < 5 {
		return false
	}
	argc := int(int32(binary.NativeEndian.Uint32(data[:4])))
	if argc < 0 || argc > 1<<16 {
		return false
	}
	position := 4
	end := bytes.IndexByte(data[position:], 0)
	if end < 0 {
		return false
	}
	position += end + 1
	for range argc {
		if position >= len(data) {
			return false
		}
		end := bytes.IndexByte(data[position:], 0)
		if end < 0 {
			return false
		}
		position += end + 1
	}
	for _, item := range bytes.Split(data[position:], []byte{0}) {
		if bytes.Contains(item, []byte{'='}) {
			return true
		}
	}
	return false
}

func timeIdentity(seconds, microseconds int64) string {
	return fmt.Sprintf("%d:%d", seconds, microseconds)
}
