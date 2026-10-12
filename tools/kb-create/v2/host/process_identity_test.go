package host

import (
	"errors"
	"os"
	"testing"
)

// case: S-025.8, PC-001.9
func TestProcessIdentityRejectsForeignProcessWithEqualStartValue(t *testing.T) {
	pid := os.Getpid()
	const expected = "boot=boot-a;start=4242;token=recorded-owner"
	const foreign = "boot=boot-a;start=4242;token=foreign-process"
	reader := func(gotPID int) (string, error) {
		if gotPID != pid {
			t.Fatalf("identity reader PID = %d, want %d", gotPID, pid)
		}
		return foreign, nil
	}
	matched := processIdentityMatchesWithReader(pid, expected, reader)
	if matched {
		t.Fatal("a foreign process with the same boot and start value matched without the recorded instance token")
	}
	signals := 0
	if signalWithIdentityReader(pid, expected, reader, func() error {
		signals++
		return nil
	}) {
		t.Fatal("foreign process was signalled")
	}
	if signals != 0 {
		t.Fatalf("foreign process received %d signals, want zero", signals)
	}
}

func TestProcessIdentityReaderFailureFailsClosed(t *testing.T) {
	if processIdentityMatchesWithReader(1, "recorded-token", func(int) (string, error) {
		return "", errors.New("process environment unavailable")
	}) {
		t.Fatal("unreadable process identity matched")
	}
}

func TestProcessIdentityRejectsRecordFromPreviousBoot(t *testing.T) {
	t.Setenv(ProcessIdentityEnv, "current-boot-process-token")
	if processIdentityMatchesWithReader(os.Getpid(), "previous-boot-process-token", func(int) (string, error) {
		return currentProcessIdentity()
	}) {
		t.Fatal("a process record from a previous boot matched the current process")
	}
}
