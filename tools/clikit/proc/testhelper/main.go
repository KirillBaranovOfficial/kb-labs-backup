//go:build darwin || linux

package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/kb-labs/clikit/proc"
	"golang.org/x/sys/unix"
)

func main() {
	if len(os.Args) < 2 {
		os.Exit(2)
	}
	switch os.Args[1] {
	case "double-fork":
		if len(os.Args) != 3 && len(os.Args) != 4 {
			os.Exit(2)
		}
		args := []string{"intermediate", os.Args[2]}
		if len(os.Args) == 4 {
			args = append(args, os.Args[3])
		}
		launch(args, false)
		if len(os.Args) == 4 {
			waitForFile(os.Args[3])
		}
	case "double-fork-held":
		if len(os.Args) != 5 {
			os.Exit(2)
		}
		launch([]string{"intermediate", os.Args[2], os.Args[3]}, true)
		waitForFileWithin(os.Args[4], time.Minute)
	case "intermediate":
		if len(os.Args) != 3 && len(os.Args) != 4 {
			os.Exit(2)
		}
		if _, err := unix.Setsid(); err != nil {
			fail(err)
		}
		args := []string{"detached", os.Args[2]}
		if len(os.Args) == 4 {
			args = append(args, os.Args[3])
		}
		launch(args, len(os.Args) == 4)
		if len(os.Args) == 4 {
			waitForFile(os.Args[3])
		}
	case "detached":
		if len(os.Args) != 3 && len(os.Args) != 4 {
			os.Exit(2)
		}
		cmd := exec.Command(os.Args[0], "leaf", os.Args[2])
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if len(os.Args) == 4 {
			cmd.Args = append(cmd.Args, os.Args[3])
			cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
		} else {
			cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		}
		if err := cmd.Start(); err != nil {
			fail(err)
		}
		if len(os.Args) == 4 {
			waitForFile(os.Args[3])
		}
	case "leaf":
		if len(os.Args) != 3 && len(os.Args) != 4 {
			os.Exit(2)
		}
		fmt.Printf("descendant-ready %d\n", os.Getpid())
		if len(os.Args) == 4 {
			if err := os.WriteFile(os.Args[3], []byte(fmt.Sprintf("%d\n", os.Getpid())), 0o600); err != nil {
				fail(err)
			}
		}
		duration, err := time.ParseDuration(os.Args[2] + "s")
		if err != nil {
			fail(err)
		}
		time.Sleep(duration)
	case "probe-detached":
		if len(os.Args) != 4 {
			os.Exit(2)
		}
		cmd := exec.Command(os.Args[0], "probe-intermediate", os.Args[2], os.Args[3])
		cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
		if err := cmd.Start(); err != nil {
			fail(err)
		}
		waitForFile(os.Args[2])
		time.Sleep(30 * time.Second)
	case "probe-intermediate":
		if len(os.Args) != 4 {
			os.Exit(2)
		}
		shell := exec.Command("/bin/sh", "-c", "sleep "+os.Args[3])
		shell.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		shell.Stdout, shell.Stderr = io.Discard, io.Discard
		if err := shell.Start(); err != nil {
			fail(err)
		}
		sleep := exec.Command("/bin/sleep", os.Args[3])
		sleep.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		sleep.Stdout, sleep.Stderr = io.Discard, io.Discard
		if err := sleep.Start(); err != nil {
			fail(err)
		}
		if err := os.WriteFile(os.Args[2], []byte(fmt.Sprintf("%d\n%d\n", shell.Process.Pid, sleep.Process.Pid)), 0o600); err != nil {
			fail(err)
		}
	case "__keeper":
		os.Exit(proc.KeeperMain())
	default:
		os.Exit(2)
	}
}

func launch(args []string, quiet bool) {
	cmd := exec.Command(os.Args[0], args...)
	if quiet {
		cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	} else {
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	}
	if err := cmd.Start(); err != nil {
		fail(err)
	}
}

func waitForFile(path string) {
	waitForFileWithin(path, 5*time.Second)
}

func waitForFileWithin(path string, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	fail(fmt.Errorf("descendant readiness timed out waiting for %s", path))
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
