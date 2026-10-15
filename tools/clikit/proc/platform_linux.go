//go:build linux

package proc

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const ancestrySampleInterval = time.Duration(0)

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
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		if processGone(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("read /proc/%d/stat: %w", pid, err)
	}
	stat, err := parseProcStat(string(data))
	if err != nil {
		return "", false, fmt.Errorf("parse /proc/%d/stat: %w", pid, err)
	}
	if stat.PID != pid || stat.State == 'Z' {
		return "", false, nil
	}
	return stat.Identity, true, nil
}

func readProcessArguments(pid int) ([]string, error) {
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "cmdline"))
	if err != nil {
		if processGone(err) {
			return nil, err
		}
		return nil, fmt.Errorf("read /proc/%d/cmdline: %w", pid, err)
	}
	argv, err := parseProcCmdline(data)
	if err != nil {
		return nil, fmt.Errorf("read /proc/%d/cmdline: %w", pid, err)
	}
	return argv, nil
}

// parseProcCmdline rejects an empty file: Linux exposes it while a task has no
// readable mm (including exec/exit transitions), so it is not argv evidence.
// inventoryEntry settles this error against the same PID/start identity.
func parseProcCmdline(data []byte) ([]string, error) {
	if len(data) == 0 {
		return nil, errors.New("empty cmdline: argv is not observable")
	}
	parts := bytes.Split(data, []byte{0})
	if len(parts[len(parts)-1]) == 0 {
		parts = parts[:len(parts)-1]
	}
	argv := make([]string, len(parts))
	for index, part := range parts {
		argv[index] = string(part)
	}
	return argv, nil
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

func allProcesses(runID string) ([]Process, error) {
	bootData, err := os.ReadFile("/proc/stat")
	if err != nil {
		return nil, fmt.Errorf("read /proc/stat: %w", err)
	}
	boot, err := parseProcBootTime(string(bootData))
	if err != nil {
		return nil, fmt.Errorf("parse /proc/stat boot time: %w", err)
	}
	clockTicks, err := procClockTicks()
	if err != nil {
		return nil, fmt.Errorf("read process clock ticks: %w", err)
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, fmt.Errorf("enumerate /proc: %w", err)
	}
	var out []Process
	uid := uint32(os.Getuid())
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		stat, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "stat"))
		if err != nil {
			if processGone(err) {
				continue
			}
			return out, fmt.Errorf("read /proc/%d/stat: %w", pid, err)
		}
		parsed, err := parseProcStat(string(stat))
		if err != nil {
			return out, fmt.Errorf("parse /proc/%d/stat: %w", pid, err)
		}
		if parsed.PID != pid {
			return out, fmt.Errorf("/proc/%d/stat described PID %d", pid, parsed.PID)
		}
		if parsed.State == 'Z' {
			continue
		}
		entryInfo, err := os.Stat(filepath.Join("/proc", entry.Name()))
		if err != nil {
			if processGone(err) {
				continue
			}
			return out, fmt.Errorf("inspect /proc/%d owner: %w", pid, err)
		}
		statInfo, ok := entryInfo.Sys().(*syscall.Stat_t)
		if !ok {
			return out, fmt.Errorf("inspect /proc/%d owner: unexpected stat type %T", pid, entryInfo.Sys())
		}
		command := parsed.Comm
		started := time.Time{}
		started = procStartTime(boot, parsed.StartTicks, clockTicks)
		elapsed := int64(0)
		if !started.IsZero() {
			elapsed = time.Since(started).Milliseconds()
			if elapsed < 0 {
				elapsed = 0
			}
		}
		out = append(out, Process{PID: pid, PPID: parsed.PPID, Command: command, StartedAt: started, ElapsedMS: elapsed, Identity: parsed.Identity, uid: statInfo.Uid})
	}
	candidates := processInspectionCandidates(out, syscall.Getpid(), uid)
	return applyProcessMarkers(out, candidates, runID, func(pid int, runID string) (bool, bool, error) {
		env, envErr := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "environ"))
		marker, unverified, gone := processMarkerReadResult(env, envErr, runEnv+"="+runID)
		if gone {
			return false, false, envErr
		}
		return marker, unverified, nil
	}), nil
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
