//go:build windows

package v2cli

import (
	"os"
	"os/exec"
	"strconv"
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
	command := exec.Command(target, "-port", strconv.Itoa(port))
	command.Stdout, command.Stderr = os.Stdout, os.Stderr
	if err := command.Run(); err != nil {
		t.Fatalf("run fakehost: %v", err)
	}
}
