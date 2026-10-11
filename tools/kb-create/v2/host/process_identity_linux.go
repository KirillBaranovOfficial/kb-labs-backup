//go:build linux

package host

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func ProcessIdentityForPID(pid int) (string, error) {
	stat, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return "", fmt.Errorf("read process %d start time: %w", pid, err)
	}
	// comm is parenthesized and may contain spaces or parentheses, so fields
	// after the final ')' are the only safe place to index the start-time field.
	closing := strings.LastIndexByte(string(stat), ')')
	if closing < 0 {
		return "", fmt.Errorf("process %d stat record is malformed", pid)
	}
	fields := strings.Fields(string(stat[closing+1:]))
	if len(fields) <= 19 {
		return "", fmt.Errorf("process %d stat record has no start time", pid)
	}
	startTicks := fields[19]
	executable, err := os.Readlink(filepath.Join("/proc", strconv.Itoa(pid), "exe"))
	if err != nil {
		return "", fmt.Errorf("read process %d executable path: %w", pid, err)
	}
	return startTicks + "\n" + filepath.Clean(executable), nil
}

func ProcessIdentityMatches(pid int, expected string) bool {
	if pid <= 0 || expected == "" || !processAlive(pid) {
		return false
	}
	actual, err := ProcessIdentityForPID(pid)
	return err == nil && actual == expected
}
