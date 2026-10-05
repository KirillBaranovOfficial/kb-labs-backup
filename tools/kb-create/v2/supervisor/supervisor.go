// Package supervisor is the long-running launcher process behind
// `kb-create supervise`: it keeps the host up under a Runner and serves the
// local control channel. Autostart (later work) will run this process.
package supervisor

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/kb-labs/clikit/diag"
	"github.com/kb-labs/create/v2/contracts"
	"github.com/kb-labs/create/v2/control"
	"github.com/kb-labs/create/v2/host"
)

// Supervisor owns one host Runner and one control server.
type Supervisor struct {
	Runner       host.Runner
	State        host.State
	PlatformRoot string
	Version      string
}

// Run starts the host, publishes the control channel and blocks until ctx is
// cancelled; it then stops the host gracefully and removes its state files.
// A second supervisor for the same state home fails with KB_HOST_ALREADY_RUNNING.
func (s *Supervisor) Run(ctx context.Context) error {
	if err := s.State.Ensure(); err != nil {
		return contracts.NewDiag("KB_HOST_STATE_DIR_UNWRITABLE", "Cannot write to the state directory "+s.State.Dir+".", "Fix the directory permissions or free disk space.", err)
	}
	if record, ok := ReadRecord(s.State); ok {
		e := Refusal(record, s.PlatformRoot)
		return e
	}
	root, err := filepath.Abs(s.PlatformRoot)
	if err != nil {
		return contracts.NewDiag("KB_HOST_STATE_DIR_UNWRITABLE", "Cannot resolve the platform root for the launcher.", "Pass an accessible --platform-root.", err)
	}
	record := Record{PID: os.Getpid(), PlatformRoot: filepath.Clean(root)}
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	if err := os.WriteFile(s.State.SupervisorPID(), append(data, '\n'), 0o600); err != nil {
		return contracts.NewDiag("KB_HOST_STATE_DIR_UNWRITABLE", "Cannot write to the state directory "+s.State.Dir+".", "Fix the directory permissions or free disk space.", err)
	}
	defer os.Remove(s.State.SupervisorPID())
	if _, err := s.Runner.Start(ctx); err != nil {
		return err
	}
	server := &control.Server{Runner: s.Runner, State: s.State, Version: s.Version}
	if err := server.Start(); err != nil {
		_, _ = s.Runner.Stop(context.Background())
		return contracts.NewDiag("KB_HOST_CONTROL_UNAVAILABLE", "The launcher control channel is unavailable.", "Start the launcher or the desktop shell.", err)
	}
	<-ctx.Done()
	// Close the channel first so no restart races the shutdown stop.
	_ = server.Close()
	_, err = s.Runner.Stop(context.Background())
	return err
}

// livePID returns the pid of a running supervisor for this state home.
// Record identifies the platform root and process held by supervisor.pid.
type Record struct {
	PID          int    `json:"pid"`
	PlatformRoot string `json:"platformRoot"`
}

// ReadRecord reads supervisor.pid. A dead process is stale and its file is removed.
func ReadRecord(state host.State) (Record, bool) {
	data, err := os.ReadFile(state.SupervisorPID())
	if err != nil {
		return Record{}, false
	}
	var record Record
	if err := json.Unmarshal(data, &record); err != nil {
		// A pre-root record may still identify a live supervisor, but it cannot
		// authorize signaling that process from an unrelated platform root.
		record.PID, _ = strconv.Atoi(strings.TrimSpace(string(data)))
	}
	if record.PID <= 0 || !host.ProcessAlive(record.PID) {
		_ = os.Remove(state.SupervisorPID())
		return Record{}, false
	}
	return record, true
}

// LivePID reports only a supervisor serving the requested platform root.
func LivePID(state host.State, platformRoot string) (int, bool) {
	root, err := filepath.Abs(platformRoot)
	if err != nil {
		return 0, false
	}
	record, ok := ReadRecord(state)
	if !ok || record.PlatformRoot != filepath.Clean(root) {
		return 0, false
	}
	return record.PID, true
}

func Refusal(record Record, requestedRoot string) *diag.Diag {
	root, err := filepath.Abs(requestedRoot)
	if err != nil {
		root = requestedRoot
	}
	if record.PlatformRoot == filepath.Clean(root) {
		failure := contracts.NewDiag("KB_HOST_ALREADY_RUNNING", "The KB Labs host is already running.", "Use `kb-create status --platform-root "+requestedRoot+"` or `kb-create restart --platform-root "+requestedRoot+"`.", nil)
		failure.Meta = map[string]any{"pid": strconv.Itoa(record.PID), "platformRoot": record.PlatformRoot}
		return failure
	}
	return host.RootConflict(record.PlatformRoot, record.PID)
}
