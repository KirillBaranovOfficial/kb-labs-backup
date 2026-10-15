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
		cmdline, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "cmdline"))
		if err != nil {
			if processGone(err) {
				continue
			}
			return out, fmt.Errorf("read /proc/%d/cmdline: %w", pid, err)
		}
		command := strings.ReplaceAll(strings.TrimRight(string(cmdline), "\x00"), "\x00", " ")
		if command == "" {
			command = parsed.Comm
		}
		started := time.Time{}
		started = procStartTime(boot, parsed.StartTicks, clockTicks)
		elapsed := int64(0)
		if !started.IsZero() {
			elapsed = time.Since(started).Milliseconds()
			if elapsed < 0 {
				elapsed = 0
			}
		}
		marker := false
		unverified := false
		if runID != "" {
			env, envErr := os.ReadFile(filepath.Join("/proc", entry.Name(), "environ"))
			var gone bool
			marker, unverified, gone = processMarkerReadResult(env, envErr, runEnv+"="+runID)
			if gone {
				continue
			}
		}
		out = append(out, Process{PID: pid, PPID: parsed.PPID, Command: command, StartedAt: started, ElapsedMS: elapsed, Identity: parsed.Identity, runMarker: marker, Unverified: unverified, RSSBytes: uint64(parsed.ResidentPages) * uint64(os.Getpagesize()), CPUSeconds: float64(parsed.UserTicks+parsed.SystemTicks) / float64(clockTicks), MetricsAvailable: true})
	}
	return out, nil
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
