// Package proc owns supervised command trees, their live registry, and lifecycle events.
// It supports Linux and macOS. Windows has no implementation by design.
package proc

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	EventSchema = "kb.proc-event/v1"
	stateEnv    = "KBLABS_CLIPROC_CONFIG"
	readyEnv    = "KBLABS_CLIPROC_READY_FD"
	runEnv      = "KBLABS_CLIPROC_RUN"
)

type Config struct {
	HelperPath string
	StateDir   string
	Tag        string
	Stall      time.Duration
	StdoutPath string
	StderrPath string
	Command    []string
	Dir        string
	Env        []string
}

type Event struct {
	Schema    string    `json:"schema"`
	At        time.Time `json:"at"`
	Type      string    `json:"type"`
	RunID     string    `json:"run_id"`
	Tag       string    `json:"tag"`
	KeeperPID int       `json:"keeper_pid"`
	Command   string    `json:"command,omitempty"`
	ExitCode  *int      `json:"exit_code,omitempty"`
	Signal    string    `json:"signal,omitempty"`
	Error     string    `json:"error,omitempty"`
	Leaves    []Process `json:"leaves,omitempty"`
}

type Run struct {
	PID       int
	RunID     string
	StartedAt string
	cmd       *exec.Cmd
	once      sync.Once
	err       error
	done      chan struct{}
}

type RunStatus string

const (
	StatusLive RunStatus = "live"
	StatusDead RunStatus = "dead"
)

type Process struct {
	PID        int       `json:"pid"`
	PPID       int       `json:"ppid"`
	Command    string    `json:"command"`
	StartedAt  time.Time `json:"started_at"`
	ElapsedMS  int64     `json:"elapsed_ms"`
	Identity   string    `json:"identity"`
	runMarker  bool
	Unverified bool `json:"unverified,omitempty"`
	uid        uint32
}

type RunInfo struct {
	RunID     string    `json:"run_id"`
	Tag       string    `json:"tag"`
	PID       int       `json:"pid"`
	StartedAt string    `json:"started_at"`
	Status    RunStatus `json:"status"`
	Command   []string  `json:"command"`
	Tree      []Process `json:"tree"`
}

type Listing struct {
	Runs  []RunInfo
	Error error
}

type record struct {
	RunID     string   `json:"run_id"`
	Tag       string   `json:"tag"`
	PID       int      `json:"pid"`
	StartedAt string   `json:"started_at"`
	Command   []string `json:"command"`
	StateDir  string   `json:"state_dir"`
}

type keeperConfig struct {
	Config
	RunID          string `json:"run_id"`
	ParentPID      int    `json:"parent_pid"`
	ParentIdentity string `json:"parent_identity"`
}

type readyMessage struct {
	Record record `json:"record"`
	Error  string `json:"error,omitempty"`
}

// Start launches this executable as the keeper helper. The executable must
// dispatch the hidden __keeper argument to KeeperMain before normal CLI parsing.
func Start(ctx context.Context, cfg Config) (*Run, error) {
	if ctx == nil {
		return nil, errors.New("proc: context is required")
	}
	if cfg.HelperPath == "" || cfg.StateDir == "" || cfg.Tag == "" || cfg.Stall <= 0 || cfg.StdoutPath == "" || cfg.StderrPath == "" || len(cfg.Command) == 0 {
		return nil, errors.New("proc: helper path, state directory, tag, positive stall duration, output paths, and command are required")
	}
	if err := os.MkdirAll(filepath.Join(cfg.StateDir, "runs"), 0o700); err != nil {
		return nil, fmt.Errorf("proc: create state directory: %w", err)
	}
	runID, err := newID()
	if err != nil {
		return nil, fmt.Errorf("proc: create run id: %w", err)
	}
	parentIdentity, ok := ProcessIdentity(os.Getpid())
	if !ok {
		return nil, errors.New("proc: cannot identify supervisor process")
	}
	kc := keeperConfig{Config: cfg, RunID: runID, ParentPID: os.Getpid(), ParentIdentity: parentIdentity}
	encoded, err := json.Marshal(kc)
	if err != nil {
		return nil, fmt.Errorf("proc: encode keeper configuration: %w", err)
	}
	readReady, writeReady, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("proc: create keeper handshake: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, errors.Join(fmt.Errorf("proc: start canceled: %w", err), readReady.Close(), writeReady.Close())
	}
	cmd := exec.Command(cfg.HelperPath, "__keeper")
	cmd.Env = append(filteredEnv(os.Environ()), stateEnv+"="+base64.RawStdEncoding.EncodeToString(encoded), readyEnv+"=3")
	cmd.ExtraFiles = []*os.File{writeReady}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	configureKeeper(cmd)
	if err := cmd.Start(); err != nil {
		return nil, errors.Join(fmt.Errorf("proc: start keeper: %w", err), readReady.Close(), writeReady.Close())
	}
	if err := writeReady.Close(); err != nil {
		killErr := cmd.Process.Kill()
		waitErr := cmd.Wait()
		return nil, errors.Join(fmt.Errorf("proc: close keeper handshake: %w", err), killErr, waitErr, readReady.Close())
	}
	type readyResult struct {
		record record
		err    error
	}
	readyCh := make(chan readyResult, 1)
	go func() {
		rec, err := decodeReady(readReady)
		readyCh <- readyResult{record: rec, err: err}
	}()
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	var ready record
	select {
	case result := <-readyCh:
		ready, err = result.record, result.err
	case <-ctx.Done():
		killErr := cmd.Process.Kill()
		closeErr := readReady.Close()
		<-readyCh
		waitErr := cmd.Wait()
		return nil, errors.Join(fmt.Errorf("proc: keeper startup canceled: %w", ctx.Err()), killErr, closeErr, waitErr)
	case <-timer.C:
		killErr := cmd.Process.Kill()
		closeErr := readReady.Close()
		<-readyCh
		waitErr := cmd.Wait()
		return nil, errors.Join(errors.New("proc: keeper did not become ready within 2s; HelperPath must dispatch __keeper"), killErr, closeErr, waitErr)
	}
	if err != nil {
		return nil, errors.Join(fmt.Errorf("proc: keeper helper did not complete the __keeper handshake; HelperPath must dispatch __keeper: %w", err), cmd.Process.Kill(), cmd.Wait(), readReady.Close())
	}
	if err := readReady.Close(); err != nil {
		return nil, errors.Join(fmt.Errorf("proc: close keeper handshake reader: %w", err), cmd.Process.Kill(), cmd.Wait())
	}
	run := &Run{PID: cmd.Process.Pid, RunID: runID, StartedAt: ready.StartedAt, cmd: cmd, done: make(chan struct{})}
	go func() {
		select {
		case <-ctx.Done():
			if err := run.Kill(); err != nil {
				log.Printf("proc: stop keeper after context cancellation: %v", err)
			}
			if err := run.Wait(); err != nil {
				log.Printf("proc: wait for canceled keeper: %v", err)
			}
		case <-run.done:
		}
	}()
	return run, nil
}

func (r *Run) Wait() error {
	r.once.Do(func() {
		r.err = r.cmd.Wait()
		close(r.done)
	})
	return r.err
}

// Kill asks the keeper to reap the tree before exiting.
func (r *Run) Kill() error {
	if r.cmd == nil || r.cmd.Process == nil {
		return errors.New("proc: run has no keeper process")
	}
	select {
	case <-r.done:
		return nil
	default:
	}
	return signalKeeper(r.cmd.Process.Pid, r.StartedAt)
}

// KeeperMain is the helper entry point. Call it only for the private __keeper
// command dispatched by the executable that Start re-executes.
func KeeperMain() int {
	encoded := os.Getenv(stateEnv)
	readyFD := os.Getenv(readyEnv)
	if encoded == "" || readyFD != "3" {
		fmt.Fprintln(os.Stderr, "proc: keeper must be started by proc.Start")
		return 2
	}
	data, err := base64.RawStdEncoding.DecodeString(encoded)
	if err != nil {
		fmt.Fprintln(os.Stderr, "proc: invalid keeper configuration")
		return 2
	}
	var cfg keeperConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		fmt.Fprintln(os.Stderr, "proc: invalid keeper configuration")
		return 2
	}
	return runKeeper(cfg)
}

// List reads the keeper registry and checks PID identity before reporting a run live.
func List(stateDir string) Listing {
	info, err := os.Stat(stateDir)
	if err != nil {
		return Listing{Error: fmt.Errorf("proc: inspect state directory: %w", err)}
	}
	if !info.IsDir() {
		return Listing{Error: fmt.Errorf("proc: state path %q is not a directory", stateDir)}
	}
	paths, err := filepath.Glob(filepath.Join(stateDir, "runs", "*.json"))
	if err != nil {
		return Listing{Error: err}
	}
	out := Listing{Runs: make([]RunInfo, 0, len(paths))}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			out.Error = fmt.Errorf("proc: read run record %s: %w", path, err)
			return out
		}
		var rec record
		if err := json.Unmarshal(data, &rec); err != nil {
			out.Error = fmt.Errorf("proc: decode run record %s: %w", path, err)
			return out
		}
		info := RunInfo{RunID: rec.RunID, Tag: rec.Tag, PID: rec.PID, StartedAt: rec.StartedAt, Status: StatusDead, Command: rec.Command}
		if current, exists := ProcessIdentity(rec.PID); exists && current == rec.StartedAt {
			info.Status = StatusLive
			info.Tree = ProcessTree(rec.PID, rec.RunID)
		}
		out.Runs = append(out.Runs, info)
	}
	return out
}

func newID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", raw), nil
}

func filteredEnv(env []string) []string {
	out := make([]string, 0, len(env)+1)
	for _, item := range env {
		if strings.HasPrefix(item, stateEnv+"=") || strings.HasPrefix(item, readyEnv+"=") || strings.HasPrefix(item, runEnv+"=") {
			continue
		}
		out = append(out, item)
	}
	return out
}

func decodeReady(r *os.File) (record, error) {
	var ready readyMessage
	if err := json.NewDecoder(r).Decode(&ready); err != nil {
		return record{}, err
	}
	if ready.Error != "" {
		return record{}, errors.New(ready.Error)
	}
	if ready.Record.StartedAt == "" {
		return record{}, errors.New("keeper returned no process identity")
	}
	return ready.Record, nil
}

func writeRecord(rec record) error {
	path := filepath.Join(rec.StateDir, "runs", rec.RunID+".json")
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func eventFor(rec record, kind string) Event {
	return Event{Schema: EventSchema, At: time.Now().UTC(), Type: kind, RunID: rec.RunID, Tag: rec.Tag, KeeperPID: rec.PID, Command: commandText(rec.Command)}
}

func commandText(command []string) string { return strings.Join(command, " ") }

func configureCommand(cmd *exec.Cmd, runID string) {
	cmd.Env = append(filteredEnv(cmd.Env), runEnv+"="+runID)
	configureCommandPlatform(cmd)
}
