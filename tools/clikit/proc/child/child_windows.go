//go:build windows

package child

import (
	"context"
	"errors"
	"os/exec"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func configureCommand(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP}
}

func runCommand(ctx context.Context, command *exec.Cmd, waitDelay time.Duration) error {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return err
	}
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		return errors.Join(err, windows.CloseHandle(job))
	}
	configureCommand(command)
	command.WaitDelay = waitDelay
	if err := command.Start(); err != nil {
		return errors.Join(err, windows.CloseHandle(job))
	}
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(command.Process.Pid))
	if err != nil {
		return errors.Join(err, command.Process.Kill(), command.Wait(), windows.CloseHandle(job))
	}
	assignErr := windows.AssignProcessToJobObject(job, process)
	closeProcessErr := windows.CloseHandle(process)
	if assignErr != nil {
		return errors.Join(assignErr, closeProcessErr, command.Process.Kill(), command.Wait(), windows.CloseHandle(job))
	}
	finished := make(chan struct{})
	watcherDone := make(chan struct{})
	var cancelErr error
	go func() {
		defer close(watcherDone)
		select {
		case <-ctx.Done():
			cancelErr = windows.TerminateJobObject(job, 1)
		case <-finished:
		}
	}()
	waitErr := command.Wait()
	close(finished)
	<-watcherDone
	return errors.Join(waitErr, cancelErr, windows.CloseHandle(job))
}
