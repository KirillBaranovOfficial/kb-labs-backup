//go:build !windows

package host

import (
	"errors"
	"os/exec"
	"syscall"
)

// configureHostProcess puts the host in its own process group so a signal to
// the group reaches every child it spawned.
func configureHostProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// ConfigureDetached starts cmd in a new session so it outlives the caller's
// terminal (used to spawn the supervise process).
func ConfigureDetached(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

func TerminateProcessIfIdentity(pid int, processIdentity string) bool {
	return signalWithProcessIdentity(pid, processIdentity, func() error { return syscall.Kill(pid, syscall.SIGTERM) })
}

func KillProcessIfIdentity(pid int, processIdentity string) bool {
	return signalWithProcessIdentity(pid, processIdentity, func() error { return syscall.Kill(pid, syscall.SIGKILL) })
}

func TerminateProcessGroupIfIdentity(pid int, processIdentity string) bool {
	return signalWithProcessIdentity(pid, processIdentity, func() error { return syscall.Kill(-pid, syscall.SIGTERM) })
}

func KillProcessGroupIfIdentity(pid int, processIdentity string) bool {
	return signalWithProcessIdentity(pid, processIdentity, func() error { return syscall.Kill(-pid, syscall.SIGKILL) })
}

// ProcessAlive reports whether pid exists (EPERM still means it exists).
func ProcessAlive(pid int) bool { return processAlive(pid) }

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
