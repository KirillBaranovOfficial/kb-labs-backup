//go:build darwin

package proc

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
	return applyProcessMarkers(processes, candidates, runID, inspectProcessMarker), nil
}

func darwinCommandName(comm []byte) string {
	if end := bytes.IndexByte(comm, 0); end >= 0 {
		comm = comm[:end]
	}
	return string(comm)
}

func inspectProcessMarker(pid int, runID string) (bool, bool, error) {
	args, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil {
		if processGone(err) {
			return false, false, err
		}
		return false, true, nil
	}
	if bytes.Contains(args, []byte(runEnv+"="+runID)) {
		return true, false, nil
	}
	return false, !procargs2HasEnvironment(args), nil
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
