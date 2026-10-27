// Package proc owns supervised command trees, their live registry, and lifecycle events.
// It supports Linux and macOS. Windows has no implementation by design.
package proc

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/kb-labs/clikit/command"
	"github.com/kb-labs/clikit/diag"
	"github.com/kb-labs/clikit/proc/child"
)

const (
	EventSchema         = "kb.proc-event/v2"
	stateEnv            = "KBLABS_CLIPROC_CONFIG"
	readyEnv            = "KBLABS_CLIPROC_READY_FD"
	keeperDispatchEnv   = "KBLABS_INTERNAL_PROC_KEEPER"
	keeperDispatchValue = "start-v1"
	runEnv              = "KBLABS_CLIPROC_RUN"
)

func init() {
	if os.Getenv(keeperDispatchEnv) == keeperDispatchValue {
		command.Exit(keeperMain())
	}
}

type EventType string

const (
	EventQueued     EventType = "queued"
	EventBlocked    EventType = "blocked"
	EventIdle       EventType = "idle"
	EventPressure   EventType = "pressure"
	EventPaused     EventType = "paused"
	EventResumed    EventType = "resumed"
	EventOverBudget EventType = "over-budget"
	EventStart      EventType = "start"
	EventStall      EventType = "stall"
	EventExit       EventType = "exit"
	EventLost       EventType = "lost"
	EventTeardown   EventType = "teardown"
)

type Config struct {
	StateDir    string
	Tag         string
	LatestTag   string
	Stall       time.Duration
	StdoutPath  string
	StderrPath  string
	StdinPath   string
	SlotGroup   string
	Slots       int
	WaitGate    string
	HoldGate    string
	MinFree     uint64
	CostKey     string
	CostClass   CostClass
	Priority    int
	MemoryBytes uint64
	CPUsMin     int
	CPUsMax     int
	DiskBytes   uint64
	GrantedCPUs int
	Command     []string
	Dir         string
	Env         []string
	// CleanEnv makes Env the complete environment for both the keeper and its command.
	CleanEnv bool
	Keep     []string
}

type Event struct {
	Schema             string               `json:"schema"`
	At                 time.Time            `json:"at"`
	Type               EventType            `json:"type"`
	Status             RunStatus            `json:"status,omitempty"`
	RunID              string               `json:"run_id"`
	Tag                string               `json:"tag"`
	KeeperPID          int                  `json:"keeper_pid"`
	Command            string               `json:"command,omitempty"`
	ExitCode           *int                 `json:"exit_code,omitempty"`
	Signal             string               `json:"signal,omitempty"`
	Error              string               `json:"error,omitempty"`
	Reason             string               `json:"reason,omitempty"`
	WaitingItems       []string             `json:"waiting_items,omitempty"`
	Position           int                  `json:"position,omitempty"`
	DiskNeedBytes      uint64               `json:"disk_need_bytes,omitempty"`
	DiskFreeBytes      uint64               `json:"disk_free_bytes,omitempty"`
	DurationMS         int64                `json:"duration_ms,omitempty"`
	Leaves             []Process            `json:"leaves,omitempty"`
	Reaped             []Process            `json:"reaped,omitempty"`
	Unverified         []Process            `json:"unverified,omitempty"`
	MemoryBytes        uint64               `json:"memory_bytes,omitempty"`
	CPUsMin            int                  `json:"cpus_min,omitempty"`
	CPUsMax            int                  `json:"cpus_max,omitempty"`
	DiskBytes          uint64               `json:"disk_bytes,omitempty"`
	GrantedCPUs        int                  `json:"granted_cpus,omitempty"`
	PeakRSSBytes       uint64               `json:"peak_rss_bytes,omitempty"`
	AverageCPU         float64              `json:"average_cpu,omitempty"`
	MeasurementPartial bool                 `json:"measurement_partial,omitempty"`
	LimitsAdvisory     string               `json:"limits_advisory,omitempty"`
	OverBudget         bool                 `json:"over_budget,omitempty"`
	ResourceNotice     string               `json:"resource_notice,omitempty"`
	CostClass          CostClass            `json:"cost_class,omitempty"`
	PausedMS           int64                `json:"paused_ms,omitempty"`
	GovernorDecision   string               `json:"governor_decision,omitempty"`
	Pressure           HostResourceSnapshot `json:"pressure,omitempty"`
	Remaining          []Process            `json:"remaining,omitempty"`
	Resources          []ResourceOutcome    `json:"resources,omitempty"`
}

type Run struct {
	PID       int
	RunID     string
	StartedAt string
	stateDir  string
	cmd       *exec.Cmd
	wait      *processWait
	once      sync.Once
	err       error
	done      chan struct{}
}

type processWait struct {
	done   chan struct{}
	result processWaitResult
}

// processWaitResult is published by the sole goroutine that calls Cmd.Wait.
// No other goroutine reads Cmd.ProcessState.
type processWaitResult struct {
	err    error
	status string
}

func startProcessWait(cmd *exec.Cmd) *processWait {
	wait := &processWait{done: make(chan struct{})}
	go func() {
		wait.result.err = cmd.Wait()
		if state := cmd.ProcessState; state != nil {
			wait.result.status = state.String()
		}
		close(wait.done)
	}()
	return wait
}

func (wait *processWait) outcome() processWaitResult {
	<-wait.done
	return wait.result
}

type RunStatus string

const (
	StatusRunning   RunStatus = "running"
	StatusQueued    RunStatus = "queued"
	StatusBlocked   RunStatus = "blocked"
	StatusStalled   RunStatus = "stalled"
	StatusPaused    RunStatus = "paused"
	StatusStarting  RunStatus = "starting"
	StatusCompleted RunStatus = "completed"
	StatusFailed    RunStatus = "failed"
	StatusDead      RunStatus = "dead"
	StatusLost      RunStatus = "lost"
	StatusDirty     RunStatus = "dirty"
)

// Terminal reports whether the run has reached an outcome that waiters should
// observe without continuing to poll.
func (status RunStatus) Terminal() bool {
	switch status {
	case StatusCompleted, StatusFailed, StatusDead, StatusLost, StatusDirty:
		return true
	case StatusRunning, StatusQueued, StatusBlocked, StatusStalled, StatusStarting, StatusPaused:
		return false
	}
	return false
}

type Process struct {
	PID              int `json:"pid"`
	PPID             int `json:"ppid"`
	processGroupID   int
	Command          string    `json:"command"` // OS process command name, never the argument vector.
	StartedAt        time.Time `json:"started_at"`
	ElapsedMS        int64     `json:"elapsed_ms"`
	Identity         string    `json:"identity"`
	runMarker        bool
	Unverified       bool `json:"unverified,omitempty"`
	uid              uint32
	RSSBytes         uint64  `json:"rss_bytes,omitempty"`
	CPUSeconds       float64 `json:"cpu_seconds,omitempty"`
	MetricsAvailable bool    `json:"metrics_available,omitempty"`
}

type RunInfo struct {
	RunID              string     `json:"run_id"`
	Tag                string     `json:"tag"`
	PID                int        `json:"pid"`
	StartedAt          *time.Time `json:"started_at,omitempty"`
	Identity           string     `json:"identity"`
	Status             RunStatus  `json:"status"`
	Command            []string   `json:"command"`
	Tree               []Process  `json:"tree"`
	SlotGroup          string     `json:"slot_group,omitempty"`
	QueuePosition      int        `json:"queue_position,omitempty"`
	WaitReason         string     `json:"wait_reason,omitempty"`
	ElapsedMS          int64      `json:"elapsed_ms,omitempty"`
	StallMS            int64      `json:"stall_ms,omitempty"`
	ExitCode           *int       `json:"exit_code,omitempty"`
	Signal             string     `json:"signal,omitempty"`
	Error              string     `json:"error,omitempty"`
	CostKey            string     `json:"cost_key,omitempty"`
	CostClass          CostClass  `json:"cost_class"`
	Priority           int        `json:"priority"`
	MemoryBytes        uint64     `json:"memory_bytes,omitempty"`
	CPUsMin            int        `json:"cpus_min,omitempty"`
	CPUsMax            int        `json:"cpus_max,omitempty"`
	DiskBytes          uint64     `json:"disk_bytes,omitempty"`
	GrantedCPUs        int        `json:"granted_cpus,omitempty"`
	PeakRSSBytes       uint64     `json:"peak_rss_bytes"`
	AverageCPU         float64    `json:"average_cpu"`
	Paused             bool       `json:"paused"`
	PausedMS           int64      `json:"paused_ms,omitempty"`
	MeasurementPartial bool       `json:"measurement_partial,omitempty"`
	LimitsAdvisory     string     `json:"limits_advisory,omitempty"`
	OverBudget         bool       `json:"over_budget,omitempty"`
	ResourceNotice     string     `json:"resource_notice,omitempty"`
	DurationMS         int64      `json:"duration_ms,omitempty"`
}

type Listing struct {
	Runs             []RunInfo            `json:"runs"`
	Host             HostResourceSnapshot `json:"host"`
	GovernorDecision string               `json:"governor_decision"`
	GovernorReason   string               `json:"governor_reason,omitempty"`
	Error            error                `json:"-"`
}

type treeCleanup struct {
	Reaped     []Process
	Unverified []Process
	Remaining  []Process
	err        error
}

// processSource is the OS observation and signalling boundary used by every
// tree, liveness, and cleanup operation. Tests inject a source at this seam.
type processSource interface {
	Snapshot(runID string) ([]Process, error)
	Identity(pid int) (string, bool, error)
	Signal(pid int, signal syscall.Signal) error
}

type processMetricsSource interface {
	Metrics(tree []Process) ([]Process, error)
}

func inspectIdentity(pid int) (string, bool, error) {
	return osProcessSource{}.Identity(pid)
}

func ProcessIdentity(pid int) (string, bool, error) {
	return inspectIdentity(pid)
}

func ProcessExists(pid int) (bool, error) {
	_, alive, err := ProcessIdentity(pid)
	return alive, err
}

func killTree(root int, runID string) error {
	return killTreeWithHistory(root, runID, nil).err
}

func killTreeWithHistory(root int, runID string, observed []Process) treeCleanup {
	return cleanupTreeWithSource(root, runID, observed, osProcessSource{})
}

func cleanupLostRun(rec record, source processSource) treeCleanup {
	cleanup := cleanupTreeWithSource(rec.PID, rec.RunID, rec.Observed, source)
	if len(rec.Observed) == 0 && len(cleanup.Reaped) == 0 && len(cleanup.Unverified) == 0 && len(cleanup.Remaining) == 0 {
		cleanup.err = errors.Join(cleanup.err, errors.New("unverified/failed observation: lost run has no persisted owned process identity"))
	}
	return cleanup
}

type record struct {
	RunID              string    `json:"run_id"`
	Tag                string    `json:"tag"`
	LatestTag          string    `json:"latest_tag,omitempty"`
	PID                int       `json:"pid"`
	StartedAt          string    `json:"started_at"`
	Command            []string  `json:"command"`
	StateDir           string    `json:"state_dir"`
	Status             RunStatus `json:"status,omitempty"`
	Error              string    `json:"error,omitempty"`
	SlotGroup          string    `json:"slot_group,omitempty"`
	Slots              int       `json:"slots,omitempty"`
	QueueOrder         uint64    `json:"queue_order,omitempty"`
	EnqueuedAt         time.Time `json:"enqueued_at,omitempty"`
	IdleReported       bool      `json:"idle_reported,omitempty"`
	WaitReason         string    `json:"wait_reason,omitempty"`
	WaitGate           string    `json:"wait_gate,omitempty"`
	HoldGate           string    `json:"hold_gate,omitempty"`
	MinFree            uint64    `json:"min_free,omitempty"`
	StartedAtTime      time.Time `json:"started_at_time,omitempty"`
	LastOutputAt       time.Time `json:"last_output_at,omitempty"`
	StallMS            int64     `json:"stall_ms,omitempty"`
	ExitCode           *int      `json:"exit_code,omitempty"`
	Signal             string    `json:"signal,omitempty"`
	DurationMS         int64     `json:"duration_ms,omitempty"`
	Observed           []Process `json:"observed,omitempty"`
	CostKey            string    `json:"cost_key,omitempty"`
	CostClass          CostClass `json:"cost_class"`
	Priority           int       `json:"priority,omitempty"`
	MemoryBytes        uint64    `json:"memory_bytes,omitempty"`
	CPUsMin            int       `json:"cpus_min,omitempty"`
	CPUsMax            int       `json:"cpus_max,omitempty"`
	DiskBytes          uint64    `json:"disk_bytes,omitempty"`
	GrantedCPUs        int       `json:"granted_cpus,omitempty"`
	PeakRSSBytes       uint64    `json:"peak_rss_bytes,omitempty"`
	AverageCPU         float64   `json:"average_cpu,omitempty"`
	MeasurementPartial bool      `json:"measurement_partial,omitempty"`
	LimitsAdvisory     string    `json:"limits_advisory,omitempty"`
	OverBudget         bool      `json:"over_budget,omitempty"`
	ResourceNotice     string    `json:"resource_notice,omitempty"`
	PausedAt           time.Time `json:"paused_at,omitempty"`
	PausedMS           int64     `json:"paused_ms,omitempty"`
	Scratch            string    `json:"scratch,omitempty"`
	ScratchDevice      uint64    `json:"scratch_device,omitempty"`
	ScratchInode       uint64    `json:"scratch_inode,omitempty"`
	Keep               []string  `json:"keep,omitempty"`
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

type readyResult struct {
	record record
	err    error
}

type startupOutcomeKind uint8

const (
	startupReady startupOutcomeKind = iota
	startupKeeperExited
	startupCanceled
)

type startupOutcome struct {
	kind          startupOutcomeKind
	result        readyResult
	readyConsumed bool
}

// Cancellation wins when it arrives with readiness, so Start never publishes
// a Run after its caller has canceled.
func selectStartupOutcome(ctx context.Context, ready <-chan readyResult, keeperExited <-chan struct{}) startupOutcome {
	select {
	case <-ctx.Done():
		return startupOutcome{kind: startupCanceled}
	case result := <-ready:
		if ctx.Err() != nil {
			return startupOutcome{kind: startupCanceled, result: result, readyConsumed: true}
		}
		return startupOutcome{kind: startupReady, result: result}
	case <-keeperExited:
		return startupOutcome{kind: startupKeeperExited}
	}
}

// KeeperExitError reports that the keeper process exited before a valid ready
// handshake was observed.
type KeeperExitError struct {
	Status string
	Err    error
}

func (e *KeeperExitError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("proc: keeper exited before ready: %s: %v", e.Status, e.Err)
	}
	return fmt.Sprintf("proc: keeper exited before ready: %s", e.Status)
}

func (e *KeeperExitError) Unwrap() error {
	return e.Err
}

// Start launches this executable as the keeper helper.
func Start(ctx context.Context, cfg Config) (*Run, error) {
	return StartWithHostResourceSource(ctx, cfg, systemHostResourceSource())
}

// StartWithHostResourceSource launches a run using the supplied host resource
// reader for admission. All clients sharing a state directory should use the
// same source so their decisions observe the same host contract.
func StartWithHostResourceSource(ctx context.Context, cfg Config, source HostResourceSource) (*Run, error) {
	if err := validateStartContext(ctx); err != nil {
		return nil, err
	}
	executable, err := os.Executable()
	if err != nil {
		return nil, diag.New("KB_RUNTIME_EXECUTABLE_PATH_UNAVAILABLE", "The current executable path could not be determined.", diag.WithReason(err.Error()))
	}
	return startWithExecutable(ctx, cfg, source, executable)
}

func validateStartContext(ctx context.Context) error {
	if ctx != nil && ctx.Done() != nil {
		return nil
	}
	diagnostic, err := diag.FromCatalog("KB_RUNTIME_START_CONTEXT_NOT_CANCELLABLE", nil)
	if err != nil {
		return err
	}
	return diagnostic
}

func startWithExecutable(ctx context.Context, cfg Config, source HostResourceSource, executable string) (*Run, error) {
	if source == nil {
		return nil, errors.New("proc: resource source is required")
	}
	if executable == "" || cfg.StateDir == "" || cfg.Tag == "" || cfg.Stall <= 0 || cfg.StdoutPath == "" || cfg.StderrPath == "" || len(cfg.Command) == 0 {
		return nil, errors.New("proc: executable path, state directory, tag, positive stall duration, output paths, and command are required")
	}
	if cfg.CostClass == "" {
		cfg.CostClass = CostLight
	}
	if cfg.CostClass != CostLight && cfg.CostClass != CostHeavy {
		return nil, errors.New("proc: cost class must be light or heavy")
	}
	if err := os.MkdirAll(liveRecordsDir(cfg.StateDir), 0o700); err != nil {
		return nil, fmt.Errorf("proc: create state directory: %w", err)
	}
	runID, err := newID()
	if err != nil {
		return nil, fmt.Errorf("proc: create run id: %w", err)
	}
	if _, err := runScratchPath(runID); err != nil {
		return nil, err
	}
	parentIdentity, ok, err := ProcessIdentity(os.Getpid())
	if err != nil {
		return nil, fmt.Errorf("proc: inspect supervisor process identity: %w", err)
	}
	if !ok {
		return nil, errors.New("proc: cannot identify supervisor process")
	}
	if err := reserveAdmission(ctx, cfg, runID, os.Getpid(), parentIdentity, source); err != nil {
		return nil, err
	}
	if err := withStateLock(cfg.StateDir, func() error {
		rec, err := readRecordLocked(cfg.StateDir, runID)
		if err != nil {
			return err
		}
		cfg.MemoryBytes, cfg.CPUsMin, cfg.CPUsMax, cfg.DiskBytes, cfg.GrantedCPUs = rec.MemoryBytes, rec.CPUsMin, rec.CPUsMax, rec.DiskBytes, rec.GrantedCPUs
		return nil
	}); err != nil {
		cleanupAdmission(cfg.StateDir, runID, err.Error())
		return nil, err
	}
	admissionPending := true
	defer func() {
		if admissionPending {
			cleanupAdmission(cfg.StateDir, runID, "start failed")
		}
	}()
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
	cmd := exec.Command(executable)
	keeperEnv := environmentFor(cfg, os.Environ())
	cmd.Env = append(keeperEnv, stateEnv+"="+base64.RawStdEncoding.EncodeToString(encoded), readyEnv+"=3", keeperDispatchEnv+"="+keeperDispatchValue)
	cmd.ExtraFiles = []*os.File{writeReady}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	configureKeeper(cmd)
	if err := cmd.Start(); err != nil {
		return nil, errors.Join(fmt.Errorf("proc: start keeper: %w", err), readReady.Close(), writeReady.Close())
	}
	keeperWait := startProcessWait(cmd)
	if err := writeReady.Close(); err != nil {
		return nil, errors.Join(fmt.Errorf("proc: close keeper handshake: %w", err), cleanupStartedKeeper(cmd, keeperWait, runID), readReady.Close())
	}
	readyCh := make(chan readyResult, 1)
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		rec, err := decodeReady(readReady)
		readyCh <- readyResult{record: rec, err: err}
	}()
	var ready record
	var readerResult *readyResult
	readReadyClosed := false
	for {
		switch outcome := selectStartupOutcome(ctx, readyCh, keeperWait.done); outcome.kind {
		case startupReady:
			result := outcome.result
			readerResult = &result
			readyCh = nil
			ready, err = result.record, result.err
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				// EOF is only a transport event. A live keeper may still produce
				// readiness through another writer; wait for process exit or ctx.
				readyCh = nil
				continue
			}
			if err != nil {
				select {
				case <-keeperWait.done:
					cleanupErr := cleanupStartedKeeper(cmd, keeperWait, runID)
					closeErr := readReady.Close()
					<-readerDone
					return nil, errors.Join(keeperExitError(keeperWait, result.err), cleanupErr, closeErr)
				default:
				}
				cleanupErr := cleanupStartedKeeper(cmd, keeperWait, runID)
				closeErr := readReady.Close()
				<-readerDone
				return nil, errors.Join(fmt.Errorf("proc: keeper startup failed: %w", err), closeErr, cleanupErr)
			}
			<-readerDone
			goto keeperReady
		case startupKeeperExited:
			// Expire and join the reader before tree cleanup: a writer outside the
			// discoverable tree must not make keeper-exit resolution wait for EOF.
			var result readyResult
			var closeErr error
			if readerResult != nil {
				result = *readerResult
				<-readerDone
				closeErr = readReady.Close()
				readReadyClosed = true
			} else {
				result, closeErr = resolveReadyAfterKeeperExit(readReady, readyCh, readerDone)
				readReadyClosed = true
			}
			treeErr := cleanupStartedKeeperTree(cmd.Process.Pid, runID)
			readyCh = nil
			if result.err == nil {
				if treeErr != nil || closeErr != nil {
					return nil, errors.Join(fmt.Errorf("proc: finish exited keeper startup: %w", errors.Join(treeErr, closeErr)))
				}
				ready, err = result.record, nil
				goto keeperReady
			}
			exitErr := keeperExitError(keeperWait, result.err)
			return nil, errors.Join(exitErr, treeErr, closeErr)
		case startupCanceled:
			if outcome.readyConsumed {
				// The selector consumed readiness before observing cancellation;
				// cleanup must join the reader without waiting for a second result.
				readyCh = nil
			}
			cleanupErr := cleanupStartedKeeper(cmd, keeperWait, runID)
			closeErr := readReady.Close()
			if readyCh != nil {
				<-readyCh
			}
			<-readerDone
			return nil, errors.Join(fmt.Errorf("proc: keeper startup canceled: %w", ctx.Err()), cleanupErr, closeErr)
		}
	}

keeperReady:
	if err != nil {
		cleanupErr := cleanupStartedKeeper(cmd, keeperWait, runID)
		return nil, errors.Join(fmt.Errorf("proc: keeper startup failed: %w", err), readReady.Close(), cleanupErr)
	}
	if !readReadyClosed {
		if err := readReady.Close(); err != nil {
			return nil, errors.Join(fmt.Errorf("proc: close keeper handshake reader: %w", err), cleanupStartedKeeper(cmd, keeperWait, runID))
		}
	}
	admissionPending = false
	run := &Run{PID: cmd.Process.Pid, RunID: runID, StartedAt: ready.StartedAt, stateDir: cfg.StateDir, cmd: cmd, wait: keeperWait, done: make(chan struct{})}
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

func keeperExitError(wait *processWait, handshakeErr error) *KeeperExitError {
	outcome := wait.outcome()
	if outcome.status == "" {
		outcome.status = "unknown exit status"
	}
	return &KeeperExitError{Status: outcome.status, Err: errors.Join(outcome.err, handshakeErr)}
}

func resolveReadyAfterKeeperExit(readReady *os.File, results <-chan readyResult, readerDone <-chan struct{}) (readyResult, error) {
	// Wait returning makes the descriptor terminal: expire reads immediately
	// so a surviving writer cannot hold startup open while the decoder joins.
	if err := readReady.SetReadDeadline(time.Now()); err != nil {
		closeErr := readReady.Close()
		result := <-results
		<-readerDone
		return result, errors.Join(fmt.Errorf("proc: expire keeper readiness read: %w", err), closeErr)
	}
	result := <-results
	<-readerDone
	return result, readReady.Close()
}

func cleanupStartedKeeper(cmd *exec.Cmd, wait *processWait, runID string) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	cleanupErr := cleanupStartedKeeperTree(cmd.Process.Pid, runID)
	if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		cleanupErr = errors.Join(cleanupErr, fmt.Errorf("proc: kill keeper: %w", err))
	}
	if wait != nil {
		// ErrProcessDone means the waiter already owns the terminal outcome.
		cleanupErr = errors.Join(cleanupErr, wait.outcome().err)
	}
	return cleanupErr
}

func cleanupStartedKeeperTree(pid int, runID string) error {
	return errors.Join(killTree(pid, runID), child.KillTree(pid))
}

func (r *Run) Wait() error {
	r.once.Do(func() {
		r.err = r.wait.outcome().err
		if r.err != nil {
			if lostErr := recordObservedKeeperLoss(r.stateDir, r.RunID); lostErr != nil {
				r.err = errors.Join(r.err, fmt.Errorf("proc: record keeper loss for run %s: %w", r.RunID, lostErr))
			}
		}
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

// keeperMain runs the process selected by Start's private environment marker.
func keeperMain() diag.ExitStatus {
	encoded := os.Getenv(stateEnv)
	readyFD := os.Getenv(readyEnv)
	if encoded == "" || readyFD != "3" {
		fmt.Fprintln(os.Stderr, "proc: keeper must be started by proc.Start")
		return diag.ProcessExitStatus(2)
	}
	data, err := base64.RawStdEncoding.DecodeString(encoded)
	if err != nil {
		fmt.Fprintln(os.Stderr, "proc: invalid keeper configuration")
		return diag.ProcessExitStatus(2)
	}
	var cfg keeperConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		fmt.Fprintln(os.Stderr, "proc: invalid keeper configuration")
		return diag.ProcessExitStatus(2)
	}
	return diag.ProcessExitStatus(runKeeper(cfg))
}

// List reads run and queue records under their state lock, then verifies each PID identity.
func List(stateDir string) Listing {
	host := systemHostResourceSource().Snapshot()
	freeDisk, diskErr := freeDiskBytes(stateDir)
	if diskErr != nil {
		return Listing{Error: fmt.Errorf("proc: inspect free disk for governor: %w", diskErr)}
	}
	host.FreeDiskBytes, host.FreeDiskKnown = freeDisk, true
	hostResourceNoticeNow := hostResourceNotice(host)
	info, err := os.Stat(stateDir)
	if err != nil {
		return Listing{Error: fmt.Errorf("proc: inspect state directory: %w", err)}
	}
	if !info.IsDir() {
		return Listing{Error: fmt.Errorf("proc: state path %q is not a directory", stateDir)}
	}
	var records []record
	if err := withStateLock(stateDir, func() error {
		if err := pruneStateLocked(stateDir); err != nil {
			return err
		}
		var readErr error
		records, readErr = readAllRecordsLocked(stateDir)
		return readErr
	}); err != nil {
		return Listing{Error: fmt.Errorf("proc: read run registry: %w", err)}
	}
	gov, govErr := readGovernorState(stateDir)
	if govErr != nil {
		return Listing{Error: fmt.Errorf("proc: read governor state: %w", govErr)}
	}
	out := Listing{Runs: make([]RunInfo, 0, len(records)), Host: host, GovernorDecision: gov.Decision, GovernorReason: gov.Reason}
	if out.GovernorDecision == "" {
		out.GovernorDecision = "normal"
	}
	now := time.Now()
	for _, rec := range records {
		status := rec.Status
		identity, live, identityErr := inspectIdentity(rec.PID)
		if identityErr != nil {
			return Listing{Error: fmt.Errorf("proc: inspect keeper %d: %w", rec.PID, identityErr)}
		}
		if rec.Status == StatusQueued || rec.Status == StatusBlocked || rec.Status == StatusStarting {
			if !live || identity != rec.StartedAt {
				continue
			}
			if status == StatusStarting {
				status = StatusQueued
			}
		} else if rec.Status.Terminal() {
			status = rec.Status
		} else if live && identity == rec.StartedAt {
			status = StatusRunning
			if rec.Status == StatusPaused {
				status = StatusPaused
			}
			if status != StatusPaused && rec.StallMS > 0 && !rec.LastOutputAt.IsZero() && now.Sub(rec.LastOutputAt) >= time.Duration(rec.StallMS)*time.Millisecond {
				status = StatusStalled
			}
		} else {
			status = StatusDead
		}
		elapsed := rec.DurationMS
		if status == StatusQueued || status == StatusBlocked {
			if !rec.EnqueuedAt.IsZero() {
				elapsed = now.Sub(rec.EnqueuedAt).Milliseconds()
			}
		} else if !status.Terminal() && !rec.StartedAtTime.IsZero() {
			pausedMS := rec.PausedMS
			if status == StatusPaused && !rec.PausedAt.IsZero() {
				pausedMS += now.Sub(rec.PausedAt).Milliseconds()
			}
			elapsed = now.Sub(rec.StartedAtTime).Milliseconds() - pausedMS
		}
		position := 0
		if status == StatusQueued || status == StatusBlocked {
			position = queuePosition(rec, records)
		}
		info := RunInfo{
			RunID: rec.RunID, Tag: rec.Tag, PID: rec.PID, StartedAt: runStartedAt(rec), Identity: rec.StartedAt,
			Status: status, Command: rec.Command, SlotGroup: rec.SlotGroup,
			QueuePosition: position, WaitReason: rec.WaitReason, ElapsedMS: elapsed, StallMS: rec.StallMS,
			ExitCode: rec.ExitCode, Signal: rec.Signal, DurationMS: rec.DurationMS,
			Error: rec.Error, CostKey: rec.CostKey, CostClass: rec.CostClass, Priority: rec.Priority, MemoryBytes: rec.MemoryBytes, CPUsMin: rec.CPUsMin, CPUsMax: rec.CPUsMax,
			DiskBytes: rec.DiskBytes, GrantedCPUs: rec.GrantedCPUs, PeakRSSBytes: rec.PeakRSSBytes, AverageCPU: rec.AverageCPU,
			MeasurementPartial: rec.MeasurementPartial, LimitsAdvisory: rec.LimitsAdvisory, OverBudget: rec.OverBudget, ResourceNotice: hostResourceNoticeNow,
			Paused: status == StatusPaused, PausedMS: rec.PausedMS,
		}
		if status == StatusPaused {
			info.WaitReason = gov.Reason
		}
		if live && identity == rec.StartedAt && status != StatusQueued && status != StatusBlocked {
			tree, treeErr := ProcessTreeWithHistory(rec.PID, rec.RunID, rec.Observed)
			info.Tree = tree
			if treeErr != nil {
				observationErr := fmt.Errorf("unverified/failed observation: %w", treeErr)
				if info.Error == "" {
					info.Error = observationErr.Error()
				} else {
					info.Error = errors.Join(errors.New(info.Error), observationErr).Error()
				}
			}
		}
		out.Runs = append(out.Runs, info)
	}
	return out
}

func queuePosition(candidate record, records []record) int {
	position := 1
	for _, rec := range records {
		if rec.SlotGroup == candidate.SlotGroup && rec.QueueOrder < candidate.QueueOrder &&
			(rec.Status == StatusQueued || rec.Status == StatusBlocked) {
			position++
		}
	}
	return position
}

func ProcessTree(root int, runID string) ([]Process, error) {
	return processTreeWithSource(osProcessSource{}, root, runID)
}

func processTreeWithSource(source processSource, root int, runID string) ([]Process, error) {
	all, err := source.Snapshot(runID)
	return processTreeFromSnapshot(all, root), wrapSnapshotError(err)
}

func wrapSnapshotError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("process table snapshot: %w", err)
}

func processTreeFromSnapshot(all []Process, root int) []Process {
	children := make(map[int][]int)
	byPID := make(map[int]Process, len(all))
	for _, process := range all {
		children[process.PPID] = append(children[process.PPID], process.PID)
		byPID[process.PID] = process
	}
	seen := map[int]bool{root: true}
	queue := []int{root}
	for len(queue) > 0 {
		pid := queue[0]
		queue = queue[1:]
		for _, child := range children[pid] {
			if !seen[child] {
				seen[child] = true
				queue = append(queue, child)
			}
		}
	}
	for _, process := range all {
		if process.runMarker {
			seen[process.PID] = true
		}
	}
	out := make([]Process, 0, len(seen))
	for pid := range seen {
		if pid == root {
			continue
		}
		if process, ok := byPID[pid]; ok {
			process.Unverified = false
			out = append(out, process)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PID < out[j].PID })
	return out
}

func ProcessTreeWithHistory(root int, runID string, observed []Process) ([]Process, error) {
	return processTreeWithHistorySource(osProcessSource{}, root, runID, observed)
}

func processTreeWithHistorySource(source processSource, root int, runID string, observed []Process) ([]Process, error) {
	snapshot, err := source.Snapshot(runID)
	snapshotErr := wrapSnapshotError(err)
	byPID := make(map[int]Process)
	tree := processTreeFromSnapshot(snapshot, root)
	for _, process := range tree {
		byPID[process.PID] = process
	}
	currentProcesses := make(map[int]Process)
	for _, process := range snapshot {
		currentProcesses[process.PID] = process
	}
	for _, previous := range observed {
		identity, alive, err := source.Identity(previous.PID)
		if err != nil {
			if processGone(err) {
				continue
			}
			previous.Unverified = true
			byPID[previous.PID] = previous
			snapshotErr = errors.Join(snapshotErr, fmt.Errorf("inspect observed process %d: %w", previous.PID, err))
			continue
		}
		if !alive || identity != previous.Identity {
			continue
		}
		current, ok := currentProcesses[previous.PID]
		if ok && current.Identity != previous.Identity {
			continue
		}
		if !ok {
			current = previous
		}
		current.Unverified = false
		byPID[current.PID] = current
	}
	out := make([]Process, 0, len(byPID))
	for _, process := range byPID {
		out = append(out, process)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PID < out[j].PID })
	return out, snapshotErr
}

func ProcessLeaves(root int, runID string) ([]Process, error) {
	processes, err := ProcessTree(root, runID)
	return processLeaves(processes), err
}

func ProcessLeavesWithHistory(root int, runID string, observed []Process) ([]Process, error) {
	processes, err := ProcessTreeWithHistory(root, runID, observed)
	return processLeaves(processes), err
}

func processLeaves(processes []Process) []Process {
	parents := make(map[int]bool, len(processes))
	for _, process := range processes {
		parents[process.PPID] = true
	}
	leaves := make([]Process, 0, len(processes))
	for _, process := range processes {
		if !parents[process.PID] {
			leaves = append(leaves, process)
		}
	}
	return leaves
}

func cleanupTreeWithSource(root int, runID string, observed []Process, source processSource) treeCleanup {
	result := treeCleanup{Reaped: make([]Process, 0), Unverified: make([]Process, 0), Remaining: make([]Process, 0)}
	var failures []error
	seenFailures := make(map[string]bool)
	recordFailure := func(err error) {
		if err != nil && !seenFailures[err.Error()] {
			seenFailures[err.Error()] = true
			failures = append(failures, err)
		}
	}
	const cleanupBound = 3 * time.Second
	const cleanupPoll = 20 * time.Millisecond
	const termGrace = 250 * time.Millisecond
	deadline := time.Now().Add(cleanupBound)
	targets := make(map[string]Process, len(observed))
	for _, process := range observed {
		targets[processObservationKey(process)] = process
	}
	observedTree := append([]Process(nil), observed...)
	termSent := make(map[string]time.Time)
	killSent := make(map[string]bool)
	emptySnapshots := 0
	var lastSnapshotErr error
	snapshotFailureRecorded := false
	for time.Now().Before(deadline) {
		tree, snapshotErr := processTreeWithHistorySource(source, root, runID, observedTree)
		if snapshotErr != nil {
			if !snapshotFailureRecorded {
				recordFailure(fmt.Errorf("unverified/failed observation: %w", snapshotErr))
				snapshotFailureRecorded = true
			}
			lastSnapshotErr = snapshotErr
		} else {
			lastSnapshotErr = nil
		}
		if snapshotErr != nil && len(tree) == 0 && len(targets) == 0 {
			break
		}
		if len(tree) == 0 && snapshotErr == nil {
			emptySnapshots++
		} else {
			emptySnapshots = 0
		}
		for _, process := range tree {
			key := processObservationKey(process)
			targets[key] = process
			if !process.Unverified && !containsProcess(observedTree, process) {
				observedTree = append(observedTree, process)
			}
		}
		allGone := true
		verifiedLive := false
		unverifiedFound := false
		for key, process := range targets {
			if process.Unverified {
				allGone = false
				unverifiedFound = true
				continue
			}
			identity, alive, identityErr := source.Identity(process.PID)
			if identityErr != nil {
				allGone = false
				unverifiedFound = true
				recordFailure(fmt.Errorf("observe process %d during teardown: %w", process.PID, identityErr))
				process.Unverified = true
				targets[key] = process
				continue
			}
			if !alive || identity != process.Identity {
				continue
			}
			allGone = false
			verifiedLive = true
			if firstSignal, signaled := termSent[key]; !signaled {
				if signalErr := source.Signal(process.PID, syscall.SIGTERM); signalErr != nil && !errors.Is(signalErr, syscall.ESRCH) {
					recordFailure(fmt.Errorf("send SIGTERM to process %d: %w", process.PID, signalErr))
				}
				termSent[key] = time.Now()
			} else if time.Since(firstSignal) >= termGrace && !killSent[key] {
				if signalErr := source.Signal(process.PID, syscall.SIGKILL); signalErr != nil && !errors.Is(signalErr, syscall.ESRCH) {
					recordFailure(fmt.Errorf("send SIGKILL to process %d: %w", process.PID, signalErr))
				}
				killSent[key] = true
			}
		}
		if allGone && emptySnapshots >= 2 && lastSnapshotErr == nil {
			break
		}
		if unverifiedFound && !verifiedLive {
			break
		}
		time.Sleep(cleanupPoll)
	}
	for _, process := range targets {
		if process.Unverified {
			result.Unverified = append(result.Unverified, process)
			result.Remaining = append(result.Remaining, process)
			recordFailure(fmt.Errorf("unverified process %d prevents verified tree cleanup", process.PID))
			continue
		}
		identity, alive, identityErr := source.Identity(process.PID)
		if identityErr != nil {
			recordFailure(fmt.Errorf("verify process %d after teardown: %w", process.PID, identityErr))
			process.Unverified = true
			result.Remaining = append(result.Remaining, process)
			continue
		}
		if alive && identity == process.Identity {
			result.Remaining = append(result.Remaining, process)
		} else {
			result.Reaped = append(result.Reaped, process)
		}
	}
	if len(result.Remaining) > 0 && !time.Now().Before(deadline) {
		recordFailure(errors.New("tree cleanup deadline expired with owned processes remaining"))
	}
	result.err = errors.Join(failures...)
	return result
}

func containsProcess(processes []Process, candidate Process) bool {
	for _, process := range processes {
		if processObservationKey(process) == processObservationKey(candidate) {
			return true
		}
	}
	return false
}

func sortedProcesses(processes map[int]Process) []Process {
	out := make([]Process, 0, len(processes))
	for _, process := range processes {
		out = append(out, process)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PID < out[j].PID })
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
		if strings.HasPrefix(item, stateEnv+"=") || strings.HasPrefix(item, readyEnv+"=") || strings.HasPrefix(item, keeperDispatchEnv+"=") || strings.HasPrefix(item, runEnv+"=") || strings.HasPrefix(item, resourceStateEnv+"=") || strings.HasPrefix(item, "KB_RUNNER_CPUS=") || strings.HasPrefix(item, "KB_RUNNER_MEM_BYTES=") {
			continue
		}
		out = append(out, item)
	}
	return out
}

func environmentFor(cfg Config, parent []string) []string {
	var env []string
	if cfg.CleanEnv {
		env = make([]string, 0, len(cfg.Env))
	} else {
		env = filteredEnv(parent)
	}
	for _, item := range cfg.Env {
		if strings.HasPrefix(item, keeperDispatchEnv+"=") {
			continue
		}
		env = append(env, item)
	}
	return env
}

func decodeReady(r *os.File) (record, error) {
	data := make([]byte, 0, 512)
	chunk := make([]byte, 4096)
	for {
		n, readErr := r.Read(chunk)
		if n > 0 {
			data = append(data, chunk[:n]...)
		}
		if readErr != nil {
			if errors.Is(readErr, os.ErrDeadlineExceeded) {
				var drainErr error
				data, drainErr = drainReadyPipe(r, data)
				if drainErr != nil {
					return record{}, errors.Join(fmt.Errorf("proc: drain expired readiness pipe: %w", drainErr), io.ErrUnexpectedEOF)
				}
			}
			line := data
			if newline := bytes.IndexByte(data, '\n'); newline >= 0 {
				line = data[:newline]
			}
			if len(line) == 0 && errors.Is(readErr, io.EOF) {
				return record{}, io.EOF
			}
			if bytes.IndexByte(data, '\n') < 0 {
				return record{}, errors.Join(io.ErrUnexpectedEOF, readErr)
			}
			return decodeReadyLine(line)
		}
		if newline := bytes.IndexByte(data, '\n'); newline >= 0 {
			return decodeReadyLine(data[:newline])
		}
	}
}

func decodeReadyLine(data []byte) (record, error) {
	var ready readyMessage
	if err := json.Unmarshal(data, &ready); err != nil {
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

func drainReadyPipe(r *os.File, data []byte) ([]byte, error) {
	fd := int(r.Fd())
	if err := syscall.SetNonblock(fd, true); err != nil {
		return data, err
	}
	chunk := make([]byte, 4096)
	for {
		n, err := syscall.Read(fd, chunk)
		if n > 0 {
			data = append(data, chunk[:n]...)
		}
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		if errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EWOULDBLOCK) || (err == nil && n == 0) {
			return data, nil
		}
		if err != nil {
			return data, err
		}
	}
}

func writeRecord(rec record) error {
	return withStateLock(rec.StateDir, func() error {
		queued, err := readRecordLocked(rec.StateDir, rec.RunID)
		if err == nil {
			rec.SlotGroup, rec.Slots, rec.QueueOrder = queued.SlotGroup, queued.Slots, queued.QueueOrder
			rec.EnqueuedAt, rec.WaitGate, rec.HoldGate = queued.EnqueuedAt, queued.WaitGate, queued.HoldGate
			rec.CostKey, rec.CostClass, rec.MemoryBytes, rec.CPUsMin, rec.CPUsMax, rec.DiskBytes = queued.CostKey, queued.CostClass, queued.MemoryBytes, queued.CPUsMin, queued.CPUsMax, queued.DiskBytes
			rec.GrantedCPUs, rec.ResourceNotice = queued.GrantedCPUs, queued.ResourceNotice
		} else if !os.IsNotExist(err) {
			return err
		}
		if rec.Status == "" {
			rec.Status = StatusStarting
		}
		return writeRecordAtomic(rec)
	})
}

func eventFor(rec record, kind EventType) Event {
	return Event{Schema: EventSchema, At: time.Now().UTC(), Type: kind, Status: rec.Status, RunID: rec.RunID, Tag: rec.Tag, KeeperPID: rec.PID, Command: commandText(rec.Command), CostClass: rec.CostClass, GovernorDecision: "normal", MemoryBytes: rec.MemoryBytes, CPUsMin: rec.CPUsMin, CPUsMax: rec.CPUsMax, DiskBytes: rec.DiskBytes, GrantedCPUs: rec.GrantedCPUs, PeakRSSBytes: rec.PeakRSSBytes, AverageCPU: rec.AverageCPU, MeasurementPartial: rec.MeasurementPartial, LimitsAdvisory: rec.LimitsAdvisory, OverBudget: rec.OverBudget, ResourceNotice: rec.ResourceNotice}
}

func exitStatus(code *int, signal, failure string) RunStatus {
	if failure != "" {
		return StatusFailed
	}
	if signal != "" {
		return StatusDead
	}
	if code == nil || *code != 0 {
		return StatusFailed
	}
	return StatusCompleted
}

func runStartedAt(rec record) *time.Time {
	if rec.StartedAtTime.IsZero() {
		return nil
	}
	value := rec.StartedAtTime.UTC()
	return &value
}

func commandText(command []string) string { return strings.Join(command, " ") }

func configureCommand(cmd *exec.Cmd, runID, stateDir, scratch string) error {
	cmd.Env = append(filteredEnv(cmd.Env), runEnv+"="+runID, resourceStateEnv+"="+stateDir, "KB_RUNNER_SCRATCH="+scratch, "TMPDIR="+scratch, "TMP="+scratch, "TEMP="+scratch)
	return child.Configure(cmd, child.ProcessGroup)
}
