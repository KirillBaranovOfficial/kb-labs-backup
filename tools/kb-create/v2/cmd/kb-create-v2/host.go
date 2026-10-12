package v2cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/kb-labs/clikit/diag"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/kb-labs/create/v2/contracts"
	"github.com/kb-labs/create/v2/control"
	"github.com/kb-labs/create/v2/host"
	"github.com/kb-labs/create/v2/supervisor"
	"github.com/kb-labs/create/v2/versionroot"
)

// hostCommand carries everything a host lifecycle command needs; nothing is
// read from package state.
type hostCommand struct {
	PlatformRoot        string
	Foreground          bool
	Executable          string // launcher binary re-executed by `start` to run `supervise`
	Version             string
	Err                 io.Writer
	identityReader      func(int) (string, error)
	identityWaitTimeout time.Duration
}

type hostCommandResult map[string]any

func executeHostStart(ctx context.Context, command hostCommand) (hostCommandResult, *diag.Diag) {
	return command.run(ctx, "start", command.start)
}

func executeHostStop(ctx context.Context, command hostCommand) (hostCommandResult, *diag.Diag) {
	return command.run(ctx, "stop", command.stop)
}

func executeHostRestart(ctx context.Context, command hostCommand) (hostCommandResult, *diag.Diag) {
	return command.run(ctx, "restart", command.restart)
}

func executeHostSupervise(ctx context.Context, command hostCommand) (hostCommandResult, *diag.Diag) {
	return command.run(ctx, "start", command.supervise)
}

// setup resolves the installation state and the runner of the installation.
func (c hostCommand) setup(runnerOperation string) (*host.ProcessRunner, host.State, *diag.Diag) {
	if c.PlatformRoot == "" {
		return nil, host.State{}, contracts.NewDiag(contracts.CodeInputRequired, "--platform-root is required", "pass the V2 platform root that owns the active receipt", nil)
	}
	layout, layoutErr := versionroot.ForPlatformPath(c.PlatformRoot)
	if runnerOperation == "stop" {
		runner, runnerErr := host.NewRecordedRunner(c.PlatformRoot)
		if runnerErr != nil {
			return nil, host.State{}, asDiag(runnerErr)
		}
		return runner, runner.State, nil
	}
	if layoutErr == nil {
		if runnerOperation == "restart" {
			layoutErr = layout.ValidateStructure()
		} else {
			layoutErr = layout.Validate()
		}
	}
	if layoutErr != nil {
		return nil, host.State{}, unsupportedPlatformLayout(c.PlatformRoot, layoutErr)
	}
	runner, err := host.NewRunner(c.PlatformRoot)
	if err != nil {
		return nil, host.State{}, asDiag(err)
	}
	return runner, runner.State, nil
}

func (c hostCommand) run(ctx context.Context, runnerOperation string, action func(context.Context, *host.ProcessRunner, host.State) (hostCommandResult, *diag.Diag)) (hostCommandResult, *diag.Diag) {
	runner, state, failure := c.setup(runnerOperation)
	if failure != nil {
		failure.ProcessExitCode = diag.ExitInput
		return nil, failure
	}
	runner.Operation = runnerOperation
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	return action(ctx, runner, state)
}

func (c hostCommand) start(ctx context.Context, process *host.ProcessRunner, state host.State) (hostCommandResult, *diag.Diag) {
	if c.Foreground {
		// Direct host: no control channel, the host is a child of this process
		// and stops when the launcher is interrupted.
		if err := host.RunForeground(ctx, process); err != nil {
			return nil, asDiag(err)
		}
		return nil, nil
	}
	status, statusErr := process.Status(ctx)
	if statusErr != nil {
		return nil, startStatusDiagnostic(statusErr)
	}
	if status.State != host.StateStopped {
		return nil, contracts.NewDiag("KB_HOST_ALREADY_RUNNING", "The KB Labs host is already running.", "Use \"kb-create status\" or \"kb-create restart\".", nil)
	}
	if record, alive := activeLauncher(state); alive {
		return nil, supervisor.Refusal(record, process.InstallRoot)
	}
	if pid, alive := supervisor.LivePID(state, process.InstallRoot); alive {
		e := contracts.NewDiag("KB_HOST_ALREADY_RUNNING", "The KB Labs host is already running.", "Use \"kb-create status\" or \"kb-create restart\".", nil)
		e.Meta = map[string]any{"supervisorPid": fmt.Sprint(pid)}
		return nil, e
	}
	info, err := c.spawnSupervisor(ctx, process, state)
	if err != nil {
		return nil, asDiag(err)
	}
	return hostCommandResult(map[string]any{"ok": true, "operation": "start", "host": info}), nil
}

func startStatusDiagnostic(err error) *diag.Diag {
	return asDiag(err)
}

// spawnSupervisor re-executes this binary as `supervise`, detached, and waits
// until the host is healthy or the supervisor gave up. The supervisor reports
// its startup failure as a JSON envelope on stdout, captured in supervisor.log.
func (c hostCommand) spawnSupervisor(ctx context.Context, process *host.ProcessRunner, state host.State) (host.Info, error) {
	if err := state.Ensure(); err != nil {
		return host.Info{}, contracts.NewDiag("KB_HOST_STATE_DIR_UNWRITABLE", "Cannot write to the state directory "+state.Dir+".", "Fix the directory permissions or free disk space.", err)
	}
	executable := c.Executable
	if executable == "" {
		var err error
		if executable, err = os.Executable(); err != nil {
			return host.Info{}, host.StartFailure(state, err, map[string]string{"nextCommand": contracts.ShellCommand("kb-create", "start", "--platform-root", c.PlatformRoot)})
		}
	}
	logFile, err := os.OpenFile(state.SupervisorLog(), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return host.Info{}, contracts.NewDiag("KB_HOST_STATE_DIR_UNWRITABLE", "Cannot write to the state directory "+state.Dir+".", "Fix the directory permissions or free disk space.", err)
	}
	defer logFile.Close()
	cmd := exec.Command(executable, "supervise", "--json", "--platform-root", c.PlatformRoot)
	identityToken, err := host.NewProcessIdentityToken()
	if err != nil {
		return host.Info{}, contracts.NewDiag(contracts.CodeHostStateDirUnwritable, "The supervisor process identity could not be created.", "Check that system randomness is available, then run `"+contracts.ShellCommand("kb-create", "start", "--platform-root", c.PlatformRoot)+"`.", err)
	}
	cmd.Env = host.ProcessEnvironmentWithIdentity(os.Environ(), identityToken)
	cmd.Stdout, cmd.Stderr = logFile, logFile
	host.ConfigureDetached(cmd)
	if err := cmd.Start(); err != nil {
		return host.Info{}, host.StartFailure(state, err, map[string]string{"supervisorLog": state.SupervisorLog(), "nextCommand": contracts.ShellCommand("kb-create", "start", "--platform-root", c.PlatformRoot)})
	}
	exited := make(chan struct{})
	waitErr := make(chan error, 1)
	go func() { waitErr <- cmd.Wait(); close(exited) }()
	started := time.Now()
	waitTimeout := c.identityWaitTimeout
	if waitTimeout <= 0 {
		waitTimeout = time.Second
	}
	identityStatus := host.WaitForProcessIdentityWithReader(cmd.Process.Pid, identityToken, exited, waitTimeout, c.identityReader)
	switch identityStatus {
	case host.ProcessIdentityMatch:
	case host.ProcessIdentityMismatch:
		select {
		case <-exited:
			return host.Info{}, supervisorFailure(state, <-waitErr, process.Spec.HealthURL, time.Since(started), c.PlatformRoot)
		default:
		}
		killErr := host.TerminateAndReapLaunchedProcess(cmd.Process, exited)
		if killErr != nil && !errors.Is(killErr, os.ErrProcessDone) {
			return host.Info{}, host.StartFailure(state, fmt.Errorf("started supervisor process identity did not match its launch token, and the child could not be stopped: %w", killErr), map[string]string{"supervisorLog": state.SupervisorLog(), "nextCommand": contracts.ShellCommand("kb-create", "start", "--platform-root", c.PlatformRoot)})
		}
		if errors.Is(killErr, os.ErrProcessDone) {
			return host.Info{}, supervisorFailure(state, <-waitErr, process.Spec.HealthURL, time.Since(started), c.PlatformRoot)
		}
		return host.Info{}, host.StartFailure(state, errors.New("started supervisor process identity does not match the token assigned by this launcher"), map[string]string{"supervisorLog": state.SupervisorLog(), "nextCommand": contracts.ShellCommand("kb-create", "start", "--platform-root", c.PlatformRoot)})
	case host.ProcessIdentityUndetermined:
		select {
		case <-exited:
			return host.Info{}, supervisorFailure(state, <-waitErr, process.Spec.HealthURL, time.Since(started), c.PlatformRoot)
		default:
			if !host.ProcessAlive(cmd.Process.Pid) {
				<-exited
				return host.Info{}, supervisorFailure(state, <-waitErr, process.Spec.HealthURL, time.Since(started), c.PlatformRoot)
			}
			killErr := host.TerminateAndReapLaunchedProcess(cmd.Process, exited)
			if errors.Is(killErr, os.ErrProcessDone) {
				_ = os.Remove(state.SupervisorPID())
				return host.Info{}, supervisorFailure(state, <-waitErr, process.Spec.HealthURL, time.Since(started), c.PlatformRoot)
			}
			if killErr == nil {
				_ = os.Remove(state.SupervisorPID())
				waited := time.Since(started).Round(time.Millisecond)
				failure := host.StartFailure(state, fmt.Errorf("supervisor process identity remained undetermined after %s; the started process was terminated", waited), map[string]string{"supervisorLog": state.SupervisorLog(), "waited": waited.String(), "nextCommand": contracts.ShellCommand("kb-create", "start", "--platform-root", c.PlatformRoot)})
				return host.Info{}, failure
			}
			_ = os.Remove(state.SupervisorPID())
			waited := time.Since(started).Round(time.Millisecond)
			return host.Info{}, host.StartFailure(state, fmt.Errorf("supervisor process identity remained undetermined and the started process could not be stopped: %w", killErr), map[string]string{"supervisorLog": state.SupervisorLog(), "waited": waited.String(), "nextCommand": contracts.ShellCommand("kb-create", "start", "--platform-root", c.PlatformRoot)})
		}
	default:
		return host.Info{}, host.StartFailure(state, errors.New("started supervisor process identity result is invalid"), map[string]string{"supervisorLog": state.SupervisorLog(), "nextCommand": contracts.ShellCommand("kb-create", "start", "--platform-root", c.PlatformRoot)})
	}
	deadline := time.After(process.Spec.ReadyTimeout + 10*time.Second)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if client, connectErr := control.Connect(state); connectErr == nil {
			if status, statusErr := client.Status(ctx); statusErr == nil && status.Host.State == host.StateRunning {
				return host.Info{Runner: process.Name(), PID: status.Host.PID, HealthURL: status.Host.HealthURL}, nil
			}
		}
		select {
		case <-exited:
			return host.Info{}, supervisorFailure(state, <-waitErr, process.Spec.HealthURL, time.Since(started), c.PlatformRoot)
		case <-deadline:
			host.KillProcessIfIdentity(cmd.Process.Pid, identityToken)
			return host.Info{}, host.StartFailure(state, errors.New("supervisor did not report a healthy host in time"), map[string]string{
				"supervisorLog": state.SupervisorLog(), "readinessURL": process.Spec.HealthURL,
				"waited":      time.Since(started).Round(time.Millisecond).String(),
				"nextCommand": contracts.ShellCommand("kb-create", "start", "--platform-root", c.PlatformRoot),
			})
		case <-ctx.Done():
			host.KillProcessIfIdentity(cmd.Process.Pid, identityToken)
			return host.Info{}, host.StartFailure(state, ctx.Err(), map[string]string{
				"supervisorLog": state.SupervisorLog(), "readinessURL": process.Spec.HealthURL,
				"waited":      time.Since(started).Round(time.Millisecond).String(),
				"nextCommand": contracts.ShellCommand("kb-create", "start", "--platform-root", c.PlatformRoot),
			})
		case <-ticker.C:
		}
	}
}

// supervisorFailure recovers the envelope the supervisor printed before exiting.
func supervisorFailure(state host.State, exit error, readinessURL string, waited time.Duration, platformRoot string) error {
	file, err := os.Open(state.SupervisorLog())
	if err == nil {
		defer file.Close()
		data, _ := io.ReadAll(file)
		var parsed struct {
			Error *diag.Diag `json:"error"`
		}
		for offset := 0; offset < len(data); offset++ {
			if data[offset] != '{' {
				continue
			}
			if json.Unmarshal(bytes.TrimSpace(data[offset:]), &parsed) == nil && parsed.Error != nil {
				return parsed.Error
			}
		}
	}
	return host.StartFailure(state, fmt.Errorf("supervisor exited before host readiness: %v", exit), map[string]string{
		"supervisorLog": state.SupervisorLog(), "supervisorExit": fmt.Sprint(exit),
		"readinessURL": readinessURL, "waited": waited.Round(time.Millisecond).String(),
		"nextCommand": contracts.ShellCommand("kb-create", "start", "--platform-root", platformRoot),
	})
}

func (c hostCommand) supervise(ctx context.Context, runner *host.ProcessRunner, state host.State) (hostCommandResult, *diag.Diag) {
	if err := (&supervisor.Supervisor{Runner: runner, State: state, PlatformRoot: runner.InstallRoot, Version: c.Version}).Run(ctx); err != nil {
		return nil, asDiag(err)
	}
	return nil, nil
}

func (c hostCommand) stop(ctx context.Context, runner *host.ProcessRunner, state host.State) (hostCommandResult, *diag.Diag) {
	result, err := halt(ctx, runner, state)
	if err != nil {
		return nil, asDiag(err)
	}
	return hostCommandResult(map[string]any{"ok": true, "operation": "stop", "stopped": result}), nil
}

// halt stops the supervisor (which stops the host gracefully and removes the
// control channel) and then whatever it left behind, or a foreground host.
func halt(ctx context.Context, runner host.Runner, state host.State) (host.StopResult, error) {
	before, _ := runner.Status(ctx)
	platformRoot := ""
	if process, ok := runner.(*host.ProcessRunner); ok {
		platformRoot = process.InstallRoot
	}
	if record, identityStatus, stale := supervisor.StaleRecord(state); stale {
		if identityStatus == host.ProcessIdentityUndetermined && record.InstallationID != "" {
			return host.StopResult{Runner: runner.Name()}, host.UndeterminedProcessIdentityFailure(state.SupervisorPID(), "supervisor", record.PID, platformRoot, host.GatewayURLForState(state))
		}
		return host.StopResult{Runner: runner.Name()}, host.StaleProcessIdentityFailure(state.SupervisorPID(), "supervisor", record.PID, platformRoot, host.GatewayURLForState(state))
	}
	installationID, identityErr := versionroot.InstallationIDForPath(platformRoot)
	if record, alive := supervisor.ReadRecord(state); alive && identityErr == nil && record.InstallationID == installationID {
		if host.TerminateProcessIfIdentity(record.PID, record.ProcessIdentity) && !waitExit(record.PID, record.ProcessIdentity, stopBudget(runner)) {
			host.KillProcessIfIdentity(record.PID, record.ProcessIdentity)
			waitExit(record.PID, record.ProcessIdentity, 5*time.Second)
		}
	}
	result, err := runner.Stop(ctx)
	if err != nil {
		return result, err
	}
	result.WasRunning = result.WasRunning || before.State != host.StateStopped
	if result.WasRunning && !result.Forced {
		result.Graceful = true
	}
	return result, nil
}

func stopBudget(runner host.Runner) time.Duration {
	if process, ok := runner.(*host.ProcessRunner); ok {
		return process.Spec.StopGrace + 5*time.Second
	}
	return 15 * time.Second
}

func waitExit(pid int, processIdentity string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		switch host.ProcessIdentityOf(pid, processIdentity) {
		case host.ProcessIdentityMatch:
		case host.ProcessIdentityMismatch:
			return true
		case host.ProcessIdentityUndetermined:
			if !host.ProcessAlive(pid) {
				return true
			}
		default:
			if !host.ProcessAlive(pid) {
				return true
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	switch host.ProcessIdentityOf(pid, processIdentity) {
	case host.ProcessIdentityMatch:
		return false
	case host.ProcessIdentityMismatch:
		return true
	case host.ProcessIdentityUndetermined:
		return !host.ProcessAlive(pid)
	default:
		return !host.ProcessAlive(pid)
	}
}

func (c hostCommand) restart(ctx context.Context, runner *host.ProcessRunner, state host.State) (hostCommandResult, *diag.Diag) {
	if record, identityStatus, stale := supervisor.StaleRecord(state); stale {
		if identityStatus == host.ProcessIdentityUndetermined && record.InstallationID != "" {
			return nil, host.UndeterminedProcessIdentityFailure(state.SupervisorPID(), "supervisor", record.PID, c.PlatformRoot, host.GatewayURLForState(state))
		}
		return nil, host.StaleProcessIdentityFailure(state.SupervisorPID(), "supervisor", record.PID, c.PlatformRoot, host.GatewayURLForState(state))
	}
	// A live supervisor owns the host: restart through its channel so its view
	// of the child stays consistent.
	if client, err := control.Connect(state); err == nil {
		response, restartErr := client.Restart(ctx)
		if restartErr != nil {
			return nil, asDiag(restartErr)
		}
		return hostCommandResult(map[string]any{"ok": true, "operation": "restart", "stopped": response.Stopped, "host": response.Host}), nil
	}
	outcome, restartErr := host.RestartWithRecovery(ctx, c.PlatformRoot, runner,
		func(ctx context.Context, current *host.ProcessRunner) (host.Info, error) {
			return c.spawnSupervisor(ctx, current, current.State)
		})
	if restartErr != nil {
		return nil, asDiag(restartErr)
	}
	return hostCommandResult(map[string]any{"ok": true, "operation": "restart", "stopped": outcome.Stopped, "host": outcome.Info}), nil
}

// lifecycleHost is the supervised host as the swap operations see it. Both
// calls build their runner from the receipt as it is at that moment, so a
// Resume after an update starts the new version and after a restore the old.
type lifecycleHost struct {
	command hostCommand
	state   host.State
}

func (h lifecycleHost) Halt(ctx context.Context) (bool, error) {
	runner, err := host.NewRunner(h.command.PlatformRoot)
	if err != nil {
		return false, err
	}
	result, err := halt(ctx, runner, runner.State)
	return result.WasRunning, err
}

func (h lifecycleHost) Resume(ctx context.Context) error {
	runner, err := host.NewRunner(h.command.PlatformRoot)
	if err != nil {
		return err
	}
	_, err = h.command.spawnSupervisor(ctx, runner, runner.State)
	return err
}

func asDiag(err error) *diag.Diag {
	var typed *diag.Diag
	if errors.As(err, &typed) {
		return typed
	}
	return contracts.NewDiag("KB_RUNTIME_UNEXPECTED", "The launcher failed to process the request.", "Read the launcher log in the state directory.", err)
}
