package proc

import (
	"bytes"
	"errors"
	"os"
	"syscall"
)

func processGone(err error) bool {
	return errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.ESRCH)
}

func processMarkerReadResult(data []byte, err error, marker string) (matched, unverified, gone bool) {
	if err == nil {
		return bytes.Contains(data, []byte(marker)), false, false
	}
	if processGone(err) {
		return false, false, true
	}
	return false, true, false
}
