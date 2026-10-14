//go:build !darwin && !linux

package operationlock

import (
	"errors"
	"os"
)

var errFileLocked = errors.New("operation lock is held")

func lockFile(*os.File) error {
	return errors.New("installation operation locks are supported only on Linux and macOS")
}

func unlockFile(*os.File) {}
