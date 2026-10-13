package host

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/kb-labs/clikit/diag"
	"github.com/kb-labs/clikit/operationlock"
	"github.com/kb-labs/create/v2/contracts"
	"github.com/kb-labs/create/v2/versionroot"
)

// KBHomeEnv overrides the machine-level KB Labs home (default ~/.kb). It is the
// same variable the TS side uses for the project registry, so one setting
// isolates every per-user file in tests and sandboxes.
const KBHomeEnv = "KB_HOME"

// State names the per-installation host lifecycle files.
type State struct{ Dir string }

// StateForRoot returns lifecycle files inside the installation container so
// the running host record survives a switch between immutable version roots.
func StateForRoot(platformRoot string) (State, error) {
	if strings.TrimSpace(platformRoot) == "" {
		return State{}, fmt.Errorf("platform root is empty")
	}
	layout, err := versionroot.ForPlatformPath(platformRoot)
	if err != nil {
		return State{}, fmt.Errorf("resolve platform root for lifecycle state: %w", err)
	}
	return State{Dir: layout.HostStateDir()}, nil
}

// KBHome resolves the machine-level KB Labs home: $KB_HOME when set, else ~/.kb.
func KBHome() (string, error) {
	if override := strings.TrimSpace(os.Getenv(KBHomeEnv)); override != "" {
		return override, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve the user home directory: %w", err)
	}
	return filepath.Join(home, ".kb"), nil
}

// StateHome resolves the per-user launcher state directory.
func StateHome() (State, error) {
	root, err := KBHome()
	if err != nil {
		return State{}, contracts.NewDiag(contracts.CodeHostStateDirUnwritable, "Cannot resolve the launcher state directory.", "Set KB_HOME to a writable directory.", err)
	}
	return State{Dir: filepath.Join(root, "launcher")}, nil
}

func (s State) HostPID() string        { return filepath.Join(s.Dir, "host.pid") }
func (s State) HostLog() string        { return filepath.Join(s.Dir, "host.log") }
func (s State) LauncherLog() string    { return filepath.Join(s.Dir, "launcher.log") }
func (s State) SupervisorPID() string  { return filepath.Join(s.Dir, "supervisor.pid") }
func (s State) SupervisorLog() string  { return filepath.Join(s.Dir, "supervisor.log") }
func (s State) Control() string        { return filepath.Join(s.Dir, "control.json") }
func (s State) UpdateRequests() string { return filepath.Join(s.Dir, "update-requests.json") }
func (s State) OperationLock() string  { return filepath.Join(s.Dir, "operation.lock") }

// Ensure creates the state directory with owner-only permissions.
func (s State) Ensure() error { return os.MkdirAll(s.Dir, 0o700) }

func (s State) unwritable(cause error) *diag.Diag {
	return contracts.NewDiag(contracts.CodeHostStateDirUnwritable, "Cannot write to the state directory "+s.Dir+".", "Fix the directory permissions or free disk space.", cause)
}

// AcquireOperation takes the per-installation lifecycle lock so two launchers never
// swap the same installation at once. The lock records the holder's pid; a lock
// whose holder is gone is stale and is replaced. The returned function releases
// it and is safe to call more than once.
func (s State) AcquireOperation(operation string) (func(), error) {
	if err := s.Ensure(); err != nil {
		return nil, s.unwritable(err)
	}
	path := s.OperationLock()
	lease, err := operationlock.Acquire(path, operation)
	if err == nil {
		return lease.Release, nil
	}
	if !errors.Is(err, operationlock.ErrInProgress) {
		return nil, s.unwritable(err)
	}
	holder, _ := operationlock.Holder(path)
	failure := contracts.NewDiag(contracts.CodeOperationInProgress, fmt.Sprintf("Another launcher operation is already running (process %d).", holder), "Wait for it to finish; if no launcher is running, remove the stale lock with `"+contracts.ShellCommand("rm", path)+"`.", nil)
	failure.Meta = map[string]any{"pid": strconv.Itoa(holder), "path": path}
	return nil, failure
}
