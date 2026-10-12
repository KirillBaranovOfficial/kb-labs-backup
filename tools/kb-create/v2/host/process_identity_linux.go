//go:build linux

package host

import (
	"fmt"
	"os"
	"strconv"
)

func ProcessIdentityForPID(pid int) (string, error) {
	if pid == os.Getpid() {
		return currentProcessIdentity()
	}
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/environ")
	if err != nil {
		return "", fmt.Errorf("read process %d environment: %w", pid, err)
	}
	return processIdentityFromEnvironment(data)
}
