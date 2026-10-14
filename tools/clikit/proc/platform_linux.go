//go:build linux

package proc

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

func configureKeeper(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGTERM}
}

func configureCommandPlatform(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func prepareKeeperPlatform() error {
	return unix.Prctl(unix.PR_SET_CHILD_SUBREAPER, 1, 0, 0, 0)
}

func monitorParent(_ int, _ string) (<-chan error, error) {
	dead := make(chan error, 1)
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-signals
		signal.Stop(signals)
		dead <- nil
	}()
	return dead, nil
}

func lockEventFile(file *os.File) (func() error, error) {
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX); err != nil {
		return nil, err
	}
	return func() error { return unix.Flock(int(file.Fd()), unix.LOCK_UN) }, nil
}

func ProcessIdentity(pid int) (string, bool) {
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return "", false
	}
	stat, err := parseProcStat(string(data))
	if err != nil || stat.PID != pid || stat.State == 'Z' {
		return "", false
	}
	return stat.Identity, true
}

func ProcessExists(pid int) bool {
	_, ok := ProcessIdentity(pid)
	return ok
}

func ProcessTree(root int, _ string) []Process {
	snapshots := allProcesses()
	children := make(map[int][]int)
	byPID := make(map[int]Process, len(snapshots))
	for _, p := range snapshots {
		children[p.PPID] = append(children[p.PPID], p.PID)
		byPID[p.PID] = p
	}
	seen := map[int]bool{root: true}
	queue := []int{root}
	for len(queue) > 0 {
		pid := queue[0]
		queue = queue[1:]
		for _, child := range children[pid] {
			if !seen[child] {
				seen[child] = true
				queue = append(queue, child)
			}
		}
	}
	out := make([]Process, 0, len(seen))
	for pid := range seen {
		if pid != root {
			if p, ok := byPID[pid]; ok {
				out = append(out, p)
			}
		}
	}
	return out
}

func ProcessLeaves(root int, runID string) []Process {
	processes := ProcessTree(root, runID)
	parents := make(map[int]bool, len(processes))
	for _, p := range processes {
		parents[p.PPID] = true
	}
	leaves := make([]Process, 0, len(processes))
	for _, p := range processes {
		if !parents[p.PID] {
			leaves = append(leaves, p)
		}
	}
	return leaves
}

func killTree(root int, runID string) error {
	var failures []error
	for _, p := range ProcessTree(root, runID) {
		if current, ok := ProcessIdentity(p.PID); ok && current == p.Identity {
			if err := syscall.Kill(p.PID, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
				failures = append(failures, fmt.Errorf("send SIGTERM to process %d: %w", p.PID, err))
			}
		}
	}
	deadline := time.Now().Add(250 * time.Millisecond)
	for time.Now().Before(deadline) {
		if len(ProcessTree(root, runID)) == 0 {
			return errors.Join(failures...)
		}
		time.Sleep(10 * time.Millisecond)
	}
	for _, p := range ProcessTree(root, runID) {
		if current, ok := ProcessIdentity(p.PID); ok && current == p.Identity {
			if err := syscall.Kill(p.PID, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
				failures = append(failures, fmt.Errorf("send SIGKILL to process %d: %w", p.PID, err))
			}
		}
	}
	for attempts := 0; attempts < 150; attempts++ {
		if len(ProcessTree(root, runID)) == 0 {
			return errors.Join(failures...)
		}
		time.Sleep(20 * time.Millisecond)
	}
	return errors.Join(append(failures, fmt.Errorf("process tree under keeper %d did not exit after SIGKILL", root))...)
}

func reapAdopted() error {
	var status syscall.WaitStatus
	for {
		pid, err := syscall.Wait4(-1, &status, syscall.WNOHANG, nil)
		if err != nil {
			if errors.Is(err, syscall.EINTR) {
				continue
			}
			if errors.Is(err, syscall.ECHILD) {
				return nil
			}
			return fmt.Errorf("reap adopted child: %w", err)
		}
		if pid == 0 {
			return nil
		}
	}
}

func allProcesses() []Process {
	bootData, bootErr := os.ReadFile("/proc/stat")
	var boot time.Time
	if bootErr == nil {
		boot, bootErr = parseProcBootTime(string(bootData))
	}
	clockTicks, clockErr := procClockTicks()
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	var out []Process
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		stat, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "stat"))
		if err != nil {
			continue
		}
		parsed, err := parseProcStat(string(stat))
		if err != nil || parsed.PID != pid || parsed.State == 'Z' {
			continue
		}
		cmdline, _ := os.ReadFile(filepath.Join("/proc", entry.Name(), "cmdline"))
		command := strings.ReplaceAll(strings.TrimRight(string(cmdline), "\x00"), "\x00", " ")
		if command == "" {
			command = parsed.Comm
		}
		started := time.Time{}
		if bootErr == nil && clockErr == nil {
			started = procStartTime(boot, parsed.StartTicks, clockTicks)
		}
		elapsed := int64(0)
		if !started.IsZero() {
			elapsed = time.Since(started).Milliseconds()
			if elapsed < 0 {
				elapsed = 0
			}
		}
		out = append(out, Process{PID: pid, PPID: parsed.PPID, Command: command, StartedAt: started, ElapsedMS: elapsed, Identity: parsed.Identity})
	}
	return out
}

func procClockTicks() (int64, error) {
	data, err := os.ReadFile("/proc/self/auxv")
	if err != nil {
		return 0, err
	}
	wordSize := strconv.IntSize / 8
	entrySize := wordSize * 2
	for offset := 0; offset+entrySize <= len(data); offset += entrySize {
		var kind, value uint64
		if wordSize == 8 {
			kind = binary.NativeEndian.Uint64(data[offset : offset+wordSize])
			value = binary.NativeEndian.Uint64(data[offset+wordSize : offset+entrySize])
		} else {
			kind = uint64(binary.NativeEndian.Uint32(data[offset : offset+wordSize]))
			value = uint64(binary.NativeEndian.Uint32(data[offset+wordSize : offset+entrySize]))
		}
		if kind == 17 && value > 0 { // AT_CLKTCK, supplied by the Linux kernel.
			return int64(value), nil
		}
		if kind == 0 {
			break
		}
	}
	return 0, errors.New("Linux auxiliary vector has no AT_CLKTCK")
}

func platformExit(err error) (*int, string, string) {
	if err == nil {
		code := 0
		return &code, "", ""
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		status, ok := exitErr.Sys().(syscall.WaitStatus)
		if ok && status.Signaled() {
			return nil, strconv.Itoa(int(status.Signal())), ""
		}
		code := exitErr.ExitCode()
		return &code, "", ""
	}
	return nil, "", err.Error()
}

func signalNumber(signal string) int { n, _ := strconv.Atoi(signal); return n }
