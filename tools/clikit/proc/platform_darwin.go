//go:build darwin

package proc

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

func configureKeeper(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

func configureCommandPlatform(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func prepareKeeperPlatform() error { return nil }

func monitorParent(parentPID int, parentIdentity string) (<-chan error, error) {
	dead := make(chan error, 1)
	if current, ok := ProcessIdentity(parentPID); !ok || current != parentIdentity {
		return nil, fmt.Errorf("supervisor PID %d is no longer the process that started the keeper", parentPID)
	}
	kqueue, err := unix.Kqueue()
	if err != nil {
		return nil, fmt.Errorf("create parent-exit kqueue: %w", err)
	}
	change := unix.Kevent_t{Ident: uint64(parentPID), Filter: unix.EVFILT_PROC, Flags: unix.EV_ADD | unix.EV_ENABLE, Fflags: unix.NOTE_EXIT}
	if _, err := unix.Kevent(kqueue, []unix.Kevent_t{change}, nil, nil); err != nil {
		return nil, errors.Join(fmt.Errorf("watch supervisor exit: %w", err), unix.Close(kqueue))
	}
	go func() {
		signals := make(chan os.Signal, 1)
		signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT)
		defer signal.Stop(signals)
		kqueueDone := make(chan error, 1)
		go func() {
			events := make([]unix.Kevent_t, 1)
			_, eventErr := unix.Kevent(kqueue, nil, events, nil)
			kqueueDone <- eventErr
		}()
		var monitorErr error
		select {
		case err := <-kqueueDone:
			if err != nil {
				monitorErr = fmt.Errorf("wait for supervisor exit: %w", err)
			}
		case <-signals:
		}
		if err := unix.Close(kqueue); err != nil {
			monitorErr = errors.Join(monitorErr, fmt.Errorf("close parent-exit kqueue: %w", err))
		}
		dead <- monitorErr
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
	info, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil || int(info.Proc.P_pid) != pid || info.Proc.P_stat == 5 {
		return "", false
	}
	return formatStart(info), true
}

func ProcessExists(pid int) bool {
	_, ok := ProcessIdentity(pid)
	return ok
}

func ProcessTree(root int, runID string) []Process {
	all := allProcesses(runID)
	children := make(map[int][]int)
	byPID := make(map[int]Process, len(all))
	for _, p := range all {
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
	rootStart := time.Time{}
	for _, p := range all {
		if p.PID == root {
			rootStart = p.StartedAt
		}
		if p.runMarker {
			seen[p.PID] = true
		}
	}
	verified := make(map[int]bool, len(seen))
	for pid := range seen {
		verified[pid] = true
	}
	for _, p := range all {
		if !seen[p.PID] && p.Unverified && p.uid == uint32(os.Getuid()) && p.StartedAt.After(rootStart) {
			seen[p.PID] = true
		}
	}
	out := make([]Process, 0, len(seen))
	for pid := range seen {
		if pid != root {
			if p, ok := byPID[pid]; ok {
				if verified[pid] {
					p.Unverified = false
				}
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
	targets := ProcessTree(root, runID)
	for _, p := range targets {
		if p.Unverified {
			continue
		}
		if current, ok := ProcessIdentity(p.PID); ok && current == p.Identity {
			if err := syscall.Kill(p.PID, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
				failures = append(failures, fmt.Errorf("send SIGTERM to process %d: %w", p.PID, err))
			}
		}
	}
	deadline := time.Now().Add(250 * time.Millisecond)
	for time.Now().Before(deadline) && targetsAlive(targets) {
		time.Sleep(10 * time.Millisecond)
	}
	if !targetsAlive(targets) {
		return errors.Join(failures...)
	}
	for _, p := range targets {
		if p.Unverified {
			continue
		}
		if current, ok := ProcessIdentity(p.PID); ok && current == p.Identity {
			if err := syscall.Kill(p.PID, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
				failures = append(failures, fmt.Errorf("send SIGKILL to process %d: %w", p.PID, err))
			}
		}
	}
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && targetsAlive(targets) {
		time.Sleep(20 * time.Millisecond)
	}
	if targetsAlive(targets) {
		failures = append(failures, fmt.Errorf("process tree under keeper %d did not exit after SIGKILL", root))
	}
	return errors.Join(failures...)
}

func targetsAlive(targets []Process) bool {
	for _, p := range targets {
		if p.Unverified {
			continue
		}
		if current, ok := ProcessIdentity(p.PID); ok && current == p.Identity {
			return true
		}
	}
	return false
}

func reapAdopted() error { return nil }

func allProcesses(runID string) []Process {
	return darwinProcessSnapshot(runID)
}

func formatStart(info *unix.KinfoProc) string {
	return timeIdentity(info.Proc.P_starttime.Sec, int64(info.Proc.P_starttime.Usec))
}

func platformExit(err error) (*int, string, string) {
	if err == nil {
		code := 0
		return &code, "", ""
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		if status, ok := exitErr.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			return nil, strconv.Itoa(int(status.Signal())), ""
		}
		code := exitErr.ExitCode()
		return &code, "", ""
	}
	return nil, "", err.Error()
}

func signalNumber(signal string) int { n, _ := strconv.Atoi(signal); return n }
