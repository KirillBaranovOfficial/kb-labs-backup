//go:build darwin || linux

package proc

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/kb-labs/clikit/proc/child"
)

type setsidBoundaryObserver struct{ pidFile string }

func (observer setsidBoundaryObserver) RootIdentity(pid int) (child.ProcessIdentity, error) {
	return ChildProcessObserver().RootIdentity(pid)
}

func (observer setsidBoundaryObserver) LiveMembers(groupID int, root child.ProcessIdentity) ([]child.ProcessMember, error) {
	data, err := os.ReadFile(observer.pidFile)
	if err != nil {
		return nil, err
	}
	escapedPID, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return nil, err
	}
	groupIDOfEscaped, err := syscall.Getpgid(escapedPID)
	if err != nil {
		return nil, err
	}
	startedAt, identity, _, err := readProcessStartIdentity(escapedPID)
	if err != nil {
		return nil, err
	}
	process := Process{PID: escapedPID, Identity: identity, StartedAt: startedAt, processGroupID: groupIDOfEscaped}
	return liveOwnedGroupMembers([]Process{process}, groupID, root.StartedAt), nil
}

// case: S-261.6
func TestSetsidDescendantIsOutsideChildGroupOwnership(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "escaped-pid")
	if os.Getenv("CHILD_GROUP_BOUNDARY_HELPER") == "1" {
		command := exec.Command("sleep", "60")
		command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		command.Stdin = nil
		command.Stdout = io.Discard
		command.Stderr = io.Discard
		if err := command.Start(); err != nil {
			os.Exit(2)
		}
		if err := os.WriteFile(os.Getenv("CHILD_GROUP_BOUNDARY_PID_FILE"), []byte(strconv.Itoa(command.Process.Pid)), 0600); err != nil {
			_ = command.Process.Kill()
			os.Exit(3)
		}
		os.Exit(0)
	}

	command := exec.Command(os.Args[0], "-test.run=^TestSetsidDescendantIsOutsideChildGroupOwnership$")
	command.Env = append(os.Environ(), "CHILD_GROUP_BOUNDARY_HELPER=1", "CHILD_GROUP_BOUNDARY_PID_FILE="+pidFile)
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	observer := setsidBoundaryObserver{pidFile: pidFile}
	started, err := child.StartManaged(command, child.ProcessGroup, observer)
	if err != nil {
		t.Fatal(err)
	}
	var escapedPID int
	t.Cleanup(func() {
		if escapedPID == 0 {
			data, err := os.ReadFile(pidFile)
			if err != nil {
				return
			}
			escapedPID, err = strconv.Atoi(strings.TrimSpace(string(data)))
			if err != nil {
				t.Errorf("parse escaped descendant PID during cleanup: %v", err)
				return
			}
		}
		if err := syscall.Kill(-escapedPID, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
			t.Errorf("kill setsid fixture group %d: %v", escapedPID, err)
			return
		}
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		timeout := time.NewTimer(2 * time.Second)
		defer timeout.Stop()
		for {
			_, alive, err := ProcessIdentity(escapedPID)
			if err == nil && !alive {
				return
			}
			select {
			case <-timeout.C:
				t.Errorf("setsid fixture process %d remained live after cleanup (observation error: %v)", escapedPID, err)
				return
			case <-ticker.C:
			}
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := started.Wait(ctx); err != nil {
		t.Fatalf("wait for owned process group: %v", err)
	}

	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("read escaped descendant PID: %v", err)
	}
	escapedPID, err = strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatalf("parse escaped descendant PID %q: %v", data, err)
	}
	if identity, alive, err := ProcessIdentity(escapedPID); err != nil || !alive || identity == "" {
		t.Fatalf("setsid descendant identity=(%q, alive=%t, err=%v); want live outside owned group", identity, alive, err)
	}
}
