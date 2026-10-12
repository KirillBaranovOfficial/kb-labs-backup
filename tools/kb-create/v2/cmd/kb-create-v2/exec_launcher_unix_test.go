//go:build !windows

package v2cli

import (
	"os"
	"strconv"
	"syscall"
	"testing"
)

func TestHostExecLauncher(t *testing.T) {
	target := os.Getenv("KB_TEST_FAKEHOST")
	if target == "" {
		return
	}
	port, err := strconv.Atoi(os.Getenv("KB_TEST_FAKEHOST_PORT"))
	if err != nil {
		t.Fatalf("fakehost port: %v", err)
	}
	args := []string{target, "-port", strconv.Itoa(port)}
	if err := syscall.Exec(target, args, os.Environ()); err != nil {
		t.Fatalf("exec fakehost: %v", err)
	}
}
