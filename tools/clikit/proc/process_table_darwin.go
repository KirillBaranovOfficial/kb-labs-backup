//go:build darwin && cgo

package proc

/*
#cgo LDFLAGS: -lproc
#include <libproc.h>
#include <sys/proc.h>
#include <stdlib.h>
#include <string.h>

typedef struct {
	int pid;
	int ppid;
	long long start_sec;
	long long start_usec;
	char command[64];
} kb_proc_info;

static kb_proc_info *kb_proc_snapshot(int *count) {
	int bytes = proc_listpids(PROC_ALL_PIDS, 0, NULL, 0);
	if (bytes <= 0) { *count = 0; return NULL; }
	int slots = bytes / (int)sizeof(pid_t) + 32;
	pid_t *pids = calloc((size_t)slots, sizeof(pid_t));
	if (pids == NULL) { *count = 0; return NULL; }
	bytes = proc_listpids(PROC_ALL_PIDS, 0, pids, slots * (int)sizeof(pid_t));
	int max = bytes / (int)sizeof(pid_t);
	kb_proc_info *out = calloc((size_t)max, sizeof(kb_proc_info));
	if (out == NULL) { free(pids); *count = 0; return NULL; }
	int used = 0;
	for (int i = 0; i < max; i++) {
		if (pids[i] <= 0) continue;
		struct proc_bsdinfo info;
		int got = proc_pidinfo(pids[i], PROC_PIDTBSDINFO, 0, &info, sizeof(info));
		if (got != sizeof(info)) continue;
		if (info.pbi_status == SZOMB) continue;
		out[used].pid = (int)info.pbi_pid;
		out[used].ppid = (int)info.pbi_ppid;
		out[used].start_sec = info.pbi_start_tvsec;
		out[used].start_usec = info.pbi_start_tvusec;
		strncpy(out[used].command, info.pbi_comm, sizeof(out[used].command) - 1);
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
	"fmt"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

func darwinProcessSnapshot(runID string) []Process {
	count := C.int(0)
	list := C.kb_proc_snapshot(&count)
	if list == nil || count == 0 {
		return nil
	}
	defer C.free(unsafe.Pointer(list))
	infos := unsafe.Slice(list, int(count))
	processes := make([]Process, 0, len(infos))
	for _, info := range infos {
		started := time.Unix(int64(info.start_sec), int64(info.start_usec)*1000)
		pid := int(info.pid)
		marker := false
		if runID != "" {
			args, err := unix.SysctlRaw("kern.procargs2", pid)
			marker = err == nil && bytes.Contains(args, []byte(runEnv+"="+runID))
		}
		processes = append(processes, Process{
			PID: pid, PPID: int(info.ppid), Command: C.GoString(&info.command[0]),
			StartedAt: started, ElapsedMS: time.Since(started).Milliseconds(),
			Identity: timeIdentity(int64(info.start_sec), int64(info.start_usec)), runMarker: marker,
		})
	}
	return processes
}

func timeIdentity(seconds, microseconds int64) string {
	return fmt.Sprintf("%d:%d", seconds, microseconds)
}
