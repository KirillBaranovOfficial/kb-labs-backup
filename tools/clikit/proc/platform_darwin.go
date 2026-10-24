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

const ancestrySampleInterval = 100 * time.Millisecond

func configureKeeper(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

func prepareKeeperPlatform() error { return nil }

func monitorParent(parentPID int, parentIdentity string) (<-chan error, error) {
	dead := make(chan error, 1)
	current, ok, identityErr := ProcessIdentity(parentPID)
	if identityErr != nil {
		return nil, fmt.Errorf("inspect supervisor process %d: %w", parentPID, identityErr)
	}
	if !ok || current != parentIdentity {
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

func lockFile(file *os.File) (func() error, error) {
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX); err != nil {
		return nil, err
	}
	return func() error { return unix.Flock(int(file.Fd()), unix.LOCK_UN) }, nil
}

func freeDiskBytes(path string) (uint64, error) {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return 0, fmt.Errorf("inspect free space on %s: %w", path, err)
	}
	return uint64(stat.Bavail) * uint64(stat.Bsize), nil
}

type osProcessSource struct{}

func (osProcessSource) Snapshot(runID string) ([]Process, error) { return allProcesses(runID) }
func (osProcessSource) Identity(pid int) (string, bool, error)   { return readProcessIdentity(pid) }
func (osProcessSource) Signal(pid int, signal syscall.Signal) error {
	return syscall.Kill(pid, signal)
}

func readProcessIdentity(pid int) (string, bool, error) {
	info, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		if processGone(err) {
			return "", false, nil
		}
		probeErr := syscall.Kill(pid, 0)
		if errors.Is(probeErr, syscall.ESRCH) {
			return "", false, nil
		}
		if probeErr != nil {
			return "", false, errors.Join(fmt.Errorf("query kern.proc.pid for %d: %w", pid, err), fmt.Errorf("verify PID existence: %w", probeErr))
		}
		return "", false, fmt.Errorf("query kern.proc.pid for %d: %w", pid, err)
	}
	if int(info.Proc.P_pid) != pid || info.Proc.P_stat == 5 {
		return "", false, nil
	}
	return formatStart(info), true, nil
}

func readProcessArguments(pid int) ([]string, error) {
	data, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil {
		if processGone(err) {
			return nil, err
		}
		return nil, fmt.Errorf("query kern.procargs2 for PID %d: %w", pid, err)
	}
	argv, err := procargs2Arguments(data)
	if err != nil {
		return nil, fmt.Errorf("parse kern.procargs2 for PID %d: %w", pid, err)
	}
	return argv, nil
}

func allProcesses(runID string) ([]Process, error) {
	return darwinProcessSnapshot(runID)
}

func reapAdopted() error { return nil }

func formatStart(info *unix.KinfoProc) string {
	return strconv.FormatInt(info.Proc.P_starttime.Sec, 10) + ":" + strconv.FormatInt(int64(info.Proc.P_starttime.Usec), 10)
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
