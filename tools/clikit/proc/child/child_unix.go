//go:build darwin || linux

package child

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"
)

func configureCommand(command *exec.Cmd, mode Mode) error {
	attributes := &syscall.SysProcAttr{}
	switch mode {
	case ProcessGroup:
		attributes.Setpgid = true
	case Detached:
		attributes.Setsid = true
	default:
		return fmt.Errorf("start child: unknown mode %d", mode)
	}
	command.SysProcAttr = attributes
	return nil
}

func runCommand(ctx context.Context, command *exec.Cmd, waitDelay time.Duration) error {
	if err := configureCommand(command, ProcessGroup); err != nil {
		return err
	}
	command.WaitDelay = waitDelay
	if err := command.Start(); err != nil {
		return err
	}
	finished := make(chan struct{})
	watcherDone := make(chan struct{})
	go func() {
		defer close(watcherDone)
		select {
		case <-ctx.Done():
			_ = terminateGroup(command)
		case <-finished:
		}
	}()
	err := command.Wait()
	close(finished)
	<-watcherDone
	return errors.Join(err, terminateGroup(command))
}

func terminateTree(pid int) error {
	if pid <= 0 {
		return fmt.Errorf("terminate child tree: invalid pid %d", pid)
	}
	err := syscall.Kill(-pid, syscall.SIGTERM)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}

func killTree(pid int) error {
	if pid <= 0 {
		return fmt.Errorf("kill child tree: invalid pid %d", pid)
	}
	err := syscall.Kill(-pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}

func terminateGroup(command *exec.Cmd) error {
	if command.Process == nil {
		return os.ErrProcessDone
	}
	return killTree(command.Process.Pid)
}
