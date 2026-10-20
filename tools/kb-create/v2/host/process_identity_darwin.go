//go:build darwin

package host

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func ProcessIdentityForPID(pid int) (string, error) {
	if pid == os.Getpid() {
		return currentProcessIdentity()
	}
	data, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil {
		return "", fmt.Errorf("read process %d arguments and environment: %w", pid, err)
	}
	return processIdentityFromProcArgs(data)
}

func processIdentityFromProcArgs(data []byte) (string, error) {
	if len(data) < 4 {
		return "", errors.New("process argument record is truncated")
	}
	argc := int(binary.NativeEndian.Uint32(data[:4]))
	if argc < 0 {
		return "", errors.New("process argument count is invalid")
	}
	offset := 4
	if offset = skipProcString(data, offset); offset < 0 {
		return "", errors.New("process executable path is missing")
	}
	for offset < len(data) && data[offset] == 0 {
		offset++
	}
	for range argc {
		offset = skipProcString(data, offset)
		if offset < 0 {
			return "", errors.New("process argument list is truncated")
		}
	}
	return processIdentityFromEnvironment(data[offset:])
}

func skipProcString(data []byte, offset int) int {
	if offset >= len(data) {
		return -1
	}
	for index := offset; index < len(data); index++ {
		if data[index] == 0 {
			return index + 1
		}
	}
	return -1
}
