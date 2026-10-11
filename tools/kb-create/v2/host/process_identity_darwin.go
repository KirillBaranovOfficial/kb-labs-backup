//go:build darwin

package host

import (
	"fmt"
	"strconv"

	"golang.org/x/sys/unix"
)

func ProcessIdentityForPID(pid int) (string, error) {
	process, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return "", fmt.Errorf("read process %d start identity: %w", pid, err)
	}
	if int(process.Proc.P_pid) != pid {
		return "", fmt.Errorf("process identity returned PID %d for requested PID %d", process.Proc.P_pid, pid)
	}
	return strconv.FormatInt(process.Proc.P_starttime.Sec, 10) + "." + strconv.FormatInt(int64(process.Proc.P_starttime.Usec), 10), nil
}

func ProcessIdentityMatches(pid int, expected string) bool {
	if pid <= 0 || expected == "" || !processAlive(pid) {
		return false
	}
	actual, err := ProcessIdentityForPID(pid)
	return err == nil && actual == expected
}
