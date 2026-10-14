//go:build darwin || linux

package operationlock

import (
	"errors"
	"os"
	"syscall"
)

var errFileLocked = errors.New("operation lock is held")

func lockFile(file *os.File) error {
	err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
		return errFileLocked
	}
	return err
}

func unlockFile(file *os.File) { _ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN) }
