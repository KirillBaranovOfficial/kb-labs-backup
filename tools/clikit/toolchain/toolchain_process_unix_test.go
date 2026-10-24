//go:build darwin || linux

package toolchain

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/kb-labs/clikit/proc"
)

// case: S-179.27
func TestExternalToolRunKillsGrandchildHoldingOutputPipes(t *testing.T) {
	dir := t.TempDir()
	ready := filepath.Join(dir, "tree-ready")
	grandchildPID := filepath.Join(dir, "grandchild.pid")
	rootPID := filepath.Join(dir, "root.pid")
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	started := time.Now()
	script := `sleep 30 &
printf '%s\n' "$$" > "$CLIKIT_PROCESS_TREE_ROOT_PID"
printf '%s\n' "$!" > "$CLIKIT_PROCESS_TREE_GRANDCHILD_PID"
printf 'grandchild-started\n' >&2
: > "$CLIKIT_PROCESS_TREE_READY"
wait`
	go func() {
		_, err := run(ctx, Invocation{
			Tool: "process-tree-fixture", Executable: "/bin/sh", Args: []string{"-c", script},
			Env:       append(os.Environ(), "CLIKIT_PROCESS_TREE_READY="+ready, "CLIKIT_PROCESS_TREE_GRANDCHILD_PID="+grandchildPID, "CLIKIT_PROCESS_TREE_ROOT_PID="+rootPID),
			Operation: OperationWorkspaceTask,
		}, 10*time.Second)
		errCh <- err
	}()
	t.Cleanup(func() {
		cancel()
		if data, err := os.ReadFile(rootPID); err == nil {
			pid, parseErr := strconv.Atoi(strings.TrimSpace(string(data)))
			if parseErr == nil {
				_ = syscall.Kill(-pid, syscall.SIGKILL)
			}
		}
		if data, err := os.ReadFile(grandchildPID); err == nil {
			pid, parseErr := strconv.Atoi(strings.TrimSpace(string(data)))
			if parseErr == nil {
				_ = syscall.Kill(-pid, syscall.SIGKILL)
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	})
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	readyPoll := time.NewTicker(time.Millisecond)
	defer readyPoll.Stop()
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		select {
		case <-deadline.C:
			t.Fatal("process-tree fixture did not signal readiness")
		case <-readyPoll.C:
		}
	}
	cancel()
	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("canceled process tree returned success")
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not return promptly after canceling the process group")
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("process tree cancellation took %s", elapsed)
	}
	data, err := os.ReadFile(grandchildPID)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	_, alive, err := proc.ProcessIdentity(pid)
	if err != nil {
		t.Fatalf("inspect grandchild %d: %v", pid, err)
	}
	if alive {
		t.Fatalf("grandchild %d remains alive after Run returned", pid)
	}
}
