//go:build darwin || linux

package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/kb-labs/clikit/command"
	"github.com/kb-labs/clikit/diag"
	"github.com/kb-labs/clikit/proc"
	"golang.org/x/sys/unix"
)

func main() {
	command.Exit(run(os.Args[1:]))
}

func run(args []string) diag.ExitStatus {
	if len(args) < 1 {
		return diag.ProcessExitStatus(2)
	}
	switch args[0] {
	case "double-fork":
		if len(args) != 2 && len(args) != 3 {
			return diag.ProcessExitStatus(2)
		}
		childArgs := []string{"intermediate", args[1]}
		if len(args) == 3 {
			childArgs = append(childArgs, args[2])
		}
		if err := launch(childArgs, false); err != nil {
			return fail(err)
		}
		if len(args) == 3 {
			if err := waitForFile(args[2]); err != nil {
				return fail(err)
			}
		}
	case "double-fork-held":
		if len(args) != 4 {
			return diag.ProcessExitStatus(2)
		}
		if err := launch([]string{"intermediate", args[1], args[2]}, true); err != nil {
			return fail(err)
		}
		if err := waitForFileWithin(args[3], time.Minute); err != nil {
			return fail(err)
		}
	case "double-fork-system-held":
		if len(args) != 4 {
			return diag.ProcessExitStatus(2)
		}
		if err := launch([]string{"system-intermediate", args[1], args[2], args[3]}, true); err != nil {
			return fail(err)
		}
		if err := waitForFileWithin(args[3], time.Minute); err != nil {
			return fail(err)
		}
	case "intermediate":
		if len(args) != 2 && len(args) != 3 {
			return diag.ProcessExitStatus(2)
		}
		if _, err := unix.Setsid(); err != nil {
			return fail(err)
		}
		childArgs := []string{"detached", args[1]}
		if len(args) == 3 {
			childArgs = append(childArgs, args[2])
		}
		if err := launch(childArgs, len(args) == 3); err != nil {
			return fail(err)
		}
		if len(args) == 3 {
			if err := waitForFile(args[2]); err != nil {
				return fail(err)
			}
		}
	case "system-intermediate":
		if len(args) != 4 {
			return diag.ProcessExitStatus(2)
		}
		if _, err := unix.Setsid(); err != nil {
			return fail(err)
		}
		duration, err := time.ParseDuration(args[1] + "s")
		if err != nil {
			return fail(err)
		}
		cmd := exec.Command("/bin/sleep", duration.String())
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
		if err := cmd.Start(); err != nil {
			return fail(err)
		}
		if err := writePID(args[2], cmd.Process.Pid); err != nil {
			return fail(err)
		}
		if err := waitForFileWithin(args[3], time.Minute); err != nil {
			return fail(err)
		}
	case "detached":
		if len(args) != 2 && len(args) != 3 {
			return diag.ProcessExitStatus(2)
		}
		cmd := exec.Command(os.Args[0], "leaf", args[1])
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if len(args) == 3 {
			cmd.Args = append(cmd.Args, args[2])
			cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
		} else {
			cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		}
		if err := cmd.Start(); err != nil {
			return fail(err)
		}
		if len(args) == 3 {
			if err := waitForFile(args[2]); err != nil {
				return fail(err)
			}
		}
	case "leaf":
		if len(args) != 2 && len(args) != 3 {
			return diag.ProcessExitStatus(2)
		}
		fmt.Printf("descendant-ready %d\n", os.Getpid())
		if len(args) == 3 {
			if err := os.WriteFile(args[2], []byte(fmt.Sprintf("%d\n", os.Getpid())), 0o600); err != nil {
				return fail(err)
			}
		}
		duration, err := time.ParseDuration(args[1] + "s")
		if err != nil {
			return fail(err)
		}
		time.Sleep(duration)
	case "probe-detached":
		if len(args) != 3 {
			return diag.ProcessExitStatus(2)
		}
		cmd := exec.Command(os.Args[0], "probe-intermediate", args[1], args[2])
		cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
		if err := cmd.Start(); err != nil {
			return fail(err)
		}
		if err := waitForFile(args[1]); err != nil {
			return fail(err)
		}
		time.Sleep(30 * time.Second)
	case "probe-intermediate":
		if len(args) != 3 {
			return diag.ProcessExitStatus(2)
		}
		shell := exec.Command("/bin/sh", "-c", "sleep "+args[2])
		shell.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		shell.Stdout, shell.Stderr = io.Discard, io.Discard
		if err := shell.Start(); err != nil {
			return fail(err)
		}
		sleep := exec.Command("/bin/sleep", args[2])
		sleep.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		sleep.Stdout, sleep.Stderr = io.Discard, io.Discard
		if err := sleep.Start(); err != nil {
			return fail(err)
		}
		if err := os.WriteFile(args[1], []byte(fmt.Sprintf("%d\n%d\n", shell.Process.Pid, sleep.Process.Pid)), 0o600); err != nil {
			return fail(err)
		}
	case "__keeper":
		return diag.ProcessExitStatus(proc.KeeperMain())
	default:
		return diag.ProcessExitStatus(2)
	}
	return diag.SuccessStatus()
}

func launch(args []string, quiet bool) error {
	cmd := exec.Command(os.Args[0], args...)
	if quiet {
		cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	} else {
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	}
	return cmd.Start()
}

func waitForFile(path string) error {
	return waitForFileWithin(path, 5*time.Second)
}

func writePID(path string, pid int) error {
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, []byte(fmt.Sprintf("%d\n", pid)), 0o600); err != nil {
		return err
	}
	if err := os.Rename(temporary, path); err != nil {
		return fmt.Errorf("publish PID file %s: %w", path, err)
	}
	return nil
}

func waitForFileWithin(path string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return nil
		}
		time.Sleep(5 * time.Millisecond)
	}
	return fmt.Errorf("descendant readiness timed out waiting for %s", path)
}

func fail(err error) diag.ExitStatus {
	fmt.Fprintln(os.Stderr, err)
	return diag.ProcessExitStatus(1)
}
