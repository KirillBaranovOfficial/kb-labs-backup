//go:build !darwin && !linux

package operationlock

import (
	"errors"
	"fmt"
	"os"
)

var errFileLocked = errors.New("operation lock is held")

func lockFile(*os.File) error {
	return fmt.Errorf("%w: installation operation locks are supported only on Linux and macOS", ErrUnsupportedPlatform)
}

func unlockFile(*os.File) {}
