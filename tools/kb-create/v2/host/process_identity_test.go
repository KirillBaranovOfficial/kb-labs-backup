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
	alive := func(int) bool { return true }
	if got := processIdentityStatusWithReader(pid, expected, reader, alive); got != ProcessIdentityMismatch {
		t.Fatal("a foreign process with the same boot and start value matched without the recorded instance token")
	}
	signals := 0
	if signalWithIdentityReader(pid, expected, reader, alive, func() error {
		signals++
		return nil
	}) {
		t.Fatal("foreign process was signalled")
	}
	if signals != 0 {
		t.Fatalf("foreign process received %d signals, want zero", signals)
	}
}

func TestProcessIdentityReaderReturnsThreeStates(t *testing.T) {
	reader := func(token string, err error) func(int) (string, error) {
		return func(int) (string, error) { return token, err }
	}
	alive := func(int) bool { return true }
	for _, test := range []struct {
		name   string
		reader func(int) (string, error)
		want   ProcessIdentityStatus
	}{
		{name: "match", reader: reader("recorded-token", nil), want: ProcessIdentityMatch},
		{name: "mismatch", reader: reader("other-token", nil), want: ProcessIdentityMismatch},
		{name: "missing launch token", reader: reader("", nil), want: ProcessIdentityMismatch},
		{name: "undetermined", reader: reader("", errors.New("process environment unavailable")), want: ProcessIdentityUndetermined},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := processIdentityStatusWithReader(42, "recorded-token", test.reader, alive); got != test.want {
				t.Fatalf("identity status = %v, want %v", got, test.want)
			}
		})
	}
	if got := processIdentityStatusWithReader(42, "recorded-token", reader("recorded-token", nil), func(int) bool { return false }); got != ProcessIdentityUndetermined {
		t.Fatalf("exited process identity status = %v, want undetermined", got)
	}
	if _, err := processIdentityFromEnvironment(nil); !errors.Is(err, errProcessEnvironmentNotReady) {
		t.Fatalf("empty environment error = %v, want not-ready", err)
	}
	if _, err := processIdentityFromEnvironment([]byte{0, 0}); !errors.Is(err, errProcessEnvironmentNotReady) {
		t.Fatalf("empty environment block error = %v, want not-ready", err)
	}
	if _, err := processIdentityFromEnvironment([]byte("PATH=/bin")); !errors.Is(err, errProcessEnvironmentNotReady) {
		t.Fatalf("unterminated environment error = %v, want not-ready", err)
	}
	if actual, err := processIdentityFromEnvironment([]byte("PATH=/bin\x00")); err != nil || actual != "" {
		t.Fatalf("readable environment without token = %q, %v; want mismatch input", actual, err)
	}
}

func TestProcessIdentitySignalDecisionForThreeStates(t *testing.T) {
	for _, test := range []struct {
		name   string
		actual string
		err    error
		want   int
	}{
		{name: "match", actual: "recorded-token", want: 1},
		{name: "mismatch", actual: "foreign-token"},
		{name: "undetermined", err: errors.New("environment is not readable yet")},
	} {
		t.Run(test.name, func(t *testing.T) {
			signals := 0
			reader := func(int) (string, error) { return test.actual, test.err }
			if sent := signalWithIdentityReader(42, "recorded-token", reader, func(int) bool { return true }, func() error {
				signals++
				return nil
			}); sent != (test.want == 1) {
				t.Fatalf("signal result = %v, want %v", sent, test.want == 1)
			}
			if signals != test.want {
				t.Fatalf("signals sent = %d, want %d", signals, test.want)
			}
		})
	}
}

func TestStartWaitRetriesUndeterminedIdentityWithoutSleep(t *testing.T) {
	for _, test := range []struct {
		name         string
		sequence     []ProcessIdentityStatus
		alive        bool
		continueWait bool
		waitCalled   bool
		want         ProcessIdentityStatus
	}{
		{name: "match", sequence: []ProcessIdentityStatus{ProcessIdentityMatch}, alive: true, want: ProcessIdentityMatch},
		{name: "mismatch", sequence: []ProcessIdentityStatus{ProcessIdentityMismatch}, alive: true, want: ProcessIdentityMismatch},
		{name: "not ready then match", sequence: []ProcessIdentityStatus{ProcessIdentityUndetermined, ProcessIdentityMatch}, alive: true, continueWait: true, waitCalled: true, want: ProcessIdentityMatch},
		{name: "not ready then mismatch", sequence: []ProcessIdentityStatus{ProcessIdentityUndetermined, ProcessIdentityMismatch}, alive: true, continueWait: true, waitCalled: true, want: ProcessIdentityMismatch},
		{name: "gone", sequence: []ProcessIdentityStatus{ProcessIdentityUndetermined}, alive: false, want: ProcessIdentityUndetermined},
		{name: "still unreadable at deadline", sequence: []ProcessIdentityStatus{ProcessIdentityUndetermined}, alive: true, waitCalled: true, want: ProcessIdentityUndetermined},
	} {
		t.Run(test.name, func(t *testing.T) {
			checks := 0
			waited := false
			got := waitForProcessIdentityWith(42, "token", func(int, string) ProcessIdentityStatus {
				index := checks
				checks++
				if index >= len(test.sequence) {
					return test.sequence[len(test.sequence)-1]
				}
				return test.sequence[index]
			}, func(int) bool { return test.alive }, func() bool {
				waited = true
				return test.continueWait
			})
			if got != test.want {
				t.Fatalf("wait result = %v, want %v", got, test.want)
			}
			if waited != test.waitCalled {
				t.Fatalf("wait invoked = %v, want %v", waited, test.waitCalled)
			}
		})
	}
}

func TestProcessIdentityRejectsRecordFromPreviousBoot(t *testing.T) {
	t.Setenv(ProcessIdentityEnv, "current-boot-process-token")
	if got := processIdentityStatusWithReader(os.Getpid(), "previous-boot-process-token", func(int) (string, error) {
		return currentProcessIdentity()
	}, processAlive); got != ProcessIdentityMismatch {
		t.Fatal("a process record from a previous boot matched the current process")
	}
}
