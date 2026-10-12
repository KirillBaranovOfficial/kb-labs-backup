package versionroot

import (
	"os"
	"syscall"
)

func syncOwnershipFile(file *os.File) error {
	_, _, errno := syscall.Syscall(syscall.SYS_FCNTL, file.Fd(), syscall.F_FULLFSYNC, 0)
	if errno != 0 {
		return errno
	}
	return nil
}
