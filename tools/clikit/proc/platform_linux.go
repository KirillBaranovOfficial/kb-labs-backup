//go:build linux

package proc

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

func appendEventPlatform(dir string, event Event) error {
	path := filepath.Join(dir, "events.jsonl")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX); err != nil {
		return errors.Join(fmt.Errorf("lock event stream: %w", err), f.Close())
	}
	data, err := json.Marshal(event)
	if err != nil {
		return errors.Join(err, unix.Flock(int(f.Fd()), unix.LOCK_UN), f.Close())
	}
	data = append(data, '\n')
	written, writeErr := f.Write(data)
	if writeErr == nil && written != len(data) {
		writeErr = io.ErrShortWrite
	}
	unlockErr := unix.Flock(int(f.Fd()), unix.LOCK_UN)
	closeErr := f.Close()
	return errors.Join(writeErr, unlockErr, closeErr)
}

func ProcessIdentity(pid int) (string, bool) {
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return "", false
	}
	close := strings.LastIndexByte(string(data), ')')
	if close < 0 {
		return "", false
	}
	fields := strings.Fields(string(data[close+2:]))
	if len(fields) <= 19 {
		return "", false
	}
	if fields[0] == "Z" {
		return "", false
	}
	return fields[19], true
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
		close := strings.LastIndexByte(string(stat), ')')
		if close < 0 {
			continue
		}
		fields := strings.Fields(string(stat[close+2:]))
		if len(fields) <= 19 {
			continue
		}
		if fields[0] == "Z" {
			continue
		}
		ppid, err := strconv.Atoi(fields[1])
		if err != nil {
			continue
		}
		startTicks, err := strconv.ParseInt(fields[19], 10, 64)
		if err != nil {
			continue
		}
		cmdline, _ := os.ReadFile(filepath.Join("/proc", entry.Name(), "cmdline"))
		command := strings.ReplaceAll(strings.TrimRight(string(cmdline), "\x00"), "\x00", " ")
		if command == "" {
			command = strings.TrimSpace(string(stat[1:close]))
		}
		started := startTime(startTicks)
		out = append(out, Process{PID: pid, PPID: ppid, Command: command, StartedAt: started, ElapsedMS: time.Since(started).Milliseconds(), Identity: strconv.FormatInt(startTicks, 10)})
	}
	return out
}

func startTime(ticks int64) time.Time {
	data, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return time.Now()
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return time.Now()
	}
	uptime, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return time.Now()
	}
	seconds := float64(ticks)/100 - uptime
	return time.Now().Add(-time.Duration(seconds * float64(time.Second)))
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
