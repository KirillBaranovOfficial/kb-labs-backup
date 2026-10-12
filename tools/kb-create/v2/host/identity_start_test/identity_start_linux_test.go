//go:build linux

package identity_start

import (
	"io"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/kb-labs/create/v2/host"
)

const helperEnv = "KB_PROCESS_IDENTITY_START_HELPER"

// TestPostStartIdentityRegression checks identity immediately after Start and
// exercises the bounded retry without waiting for child output or readiness.
// case: PC-002.1
func TestPostStartIdentityRegression(t *testing.T) {
	if os.Getenv(helperEnv) == "child" {
		_, _ = io.Copy(io.Discard, os.Stdin)
		return
	} else if os.Getenv(helperEnv) == "exit" {
		return
	}

	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	const starts = 2000
	failures := make([]int, 0, 8)
	failureCount := 0
	for index := 0; index < starts; index++ {
		identity, err := host.NewProcessIdentityToken()
		if err != nil {
			t.Fatal(err)
		}
		command := exec.Command(executable, "-test.run=^TestPostStartIdentityRegression$")
		command.Env = host.ProcessEnvironmentWithIdentity(append(os.Environ(), helperEnv+"=child"), identity)
		command.Stdout, command.Stderr = io.Discard, io.Discard
		stdin, err := command.StdinPipe()
		if err != nil {
			t.Fatal(err)
		}
		if err := command.Start(); err != nil {
			t.Fatal(err)
		}
		exited := make(chan struct{})
		waitErr := make(chan error, 1)
		go func() {
			waitErr <- command.Wait()
			close(exited)
		}()
		identityStatus := host.WaitForProcessIdentity(command.Process.Pid, identity, exited, time.Second)
		_ = stdin.Close()
		if err := <-waitErr; err != nil {
			t.Fatalf("child %d exit: %v", index, err)
		}
		if identityStatus != host.ProcessIdentityMatch && len(failures) < cap(failures) {
			failures = append(failures, index)
		}
		if identityStatus != host.ProcessIdentityMatch {
			failureCount++
		}
	}
	if failureCount != 0 {
		t.Fatalf("immediate post-Start process identity failed %d/%d times; sample child indexes: %v", failureCount, starts, failures)
	}
	t.Logf("post-Start process identity became verifiable for %d/%d children", starts, starts)
}

// case: PC-002.1
func TestExitedProcessIdentityIsUndetermined(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	identity, err := host.NewProcessIdentityToken()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(executable, "-test.run=^TestPostStartIdentityRegression$")
	command.Env = host.ProcessEnvironmentWithIdentity(append(os.Environ(), helperEnv+"=exit"), identity)
	command.Stdout, command.Stderr = io.Discard, io.Discard
	if err := command.Run(); err != nil {
		t.Fatalf("exited child: %v", err)
	}
	if got := host.ProcessIdentityOf(command.Process.Pid, identity); got != host.ProcessIdentityUndetermined {
		t.Fatalf("exited process identity status = %v, want undetermined", got)
	}
}
