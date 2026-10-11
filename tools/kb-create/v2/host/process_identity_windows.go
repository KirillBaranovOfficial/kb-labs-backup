//go:build windows

package host

import (
	"strconv"

	"golang.org/x/sys/windows"
)

func ProcessIdentityForPID(pid int) (string, error) {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(handle)
	var created, exited, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(handle, &created, &exited, &kernel, &user); err != nil {
		return "", err
	}
	return strconv.FormatInt(created.Nanoseconds(), 10), nil
}

func ProcessIdentityMatches(pid int, expectedStart string) bool {
	if pid <= 0 || expectedStart == "" || !processAlive(pid) {
		return false
	}
	actualStart, err := ProcessIdentityForPID(pid)
	if err != nil {
		return false
	}
	return actualStart == expectedStart
}
