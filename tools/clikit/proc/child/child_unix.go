//go:build darwin || linux

package child

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"
)

func configureCommand(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func runCommand(ctx context.Context, command *exec.Cmd, waitDelay time.Duration) error {
	configureCommand(command)
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

func terminateGroup(command *exec.Cmd) error {
	if command.Process == nil {
		return os.ErrProcessDone
	}
	err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}
