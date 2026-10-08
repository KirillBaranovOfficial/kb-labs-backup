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
)

// hostCommand carries everything a host lifecycle command needs; nothing is
// read from package state.
type hostCommand struct {
	PlatformRoot string
	Foreground   bool
	Executable   string // launcher binary re-executed by `start` to run `supervise`
	Version      string
	Err          io.Writer
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

// setup resolves the per-user state home and the runner of the installation.
func (c hostCommand) setup() (*host.ProcessRunner, host.State, *diag.Diag) {
	if c.PlatformRoot == "" {
		return nil, host.State{}, contracts.NewDiag(contracts.CodeInputRequired, "--platform-root is required", "pass the V2 platform root that owns the active receipt", nil)
	}
	state, err := host.StateHome()
	if err != nil {
		return nil, host.State{}, asDiag(err)
	}
	runner, err := host.NewRunner(c.PlatformRoot, state)
	if err != nil {
		return nil, host.State{}, asDiag(err)
	}
	return runner, state, nil
}

func (c hostCommand) run(ctx context.Context, runnerOperation string, action func(context.Context, *host.ProcessRunner, host.State) (hostCommandResult, *diag.Diag)) (hostCommandResult, *diag.Diag) {
	runner, state, failure := c.setup()
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
		return nil, supervisor.Refusal(record, c.PlatformRoot)
	}
	if pid, alive := supervisor.LivePID(state, c.PlatformRoot); alive {
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
			return host.Info{}, host.StartFailure(state, err, map[string]string{"nextCommand": fmt.Sprintf("kb-create start --platform-root %q", c.PlatformRoot)})
		}
	}
	logFile, err := os.OpenFile(state.SupervisorLog(), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return host.Info{}, contracts.NewDiag("KB_HOST_STATE_DIR_UNWRITABLE", "Cannot write to the state directory "+state.Dir+".", "Fix the directory permissions or free disk space.", err)
	}
	defer logFile.Close()
	cmd := exec.Command(executable, "supervise", "--json", "--platform-root", c.PlatformRoot)
	cmd.Stdout, cmd.Stderr = logFile, logFile
	host.ConfigureDetached(cmd)
	if err := cmd.Start(); err != nil {
		return host.Info{}, host.StartFailure(state, err, map[string]string{"supervisorLog": state.SupervisorLog(), "nextCommand": fmt.Sprintf("kb-create start --platform-root %q", c.PlatformRoot)})
	}
	exited := make(chan struct{})
	waitErr := make(chan error, 1)
	go func() { waitErr <- cmd.Wait(); close(exited) }()
	started := time.Now()
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
			host.KillProcess(cmd.Process.Pid)
			return host.Info{}, host.StartFailure(state, errors.New("supervisor did not report a healthy host in time"), map[string]string{
				"supervisorLog": state.SupervisorLog(), "readinessURL": process.Spec.HealthURL,
				"waited":      time.Since(started).Round(time.Millisecond).String(),
				"nextCommand": fmt.Sprintf("kb-create start --platform-root %q", c.PlatformRoot),
			})
		case <-ctx.Done():
			host.KillProcess(cmd.Process.Pid)
			return host.Info{}, host.StartFailure(state, ctx.Err(), map[string]string{
				"supervisorLog": state.SupervisorLog(), "readinessURL": process.Spec.HealthURL,
				"waited":      time.Since(started).Round(time.Millisecond).String(),
				"nextCommand": fmt.Sprintf("kb-create start --platform-root %q", c.PlatformRoot),
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
		"nextCommand": fmt.Sprintf("kb-create start --platform-root %q", platformRoot),
	})
}

func (c hostCommand) supervise(ctx context.Context, runner *host.ProcessRunner, state host.State) (hostCommandResult, *diag.Diag) {
	if err := (&supervisor.Supervisor{Runner: runner, State: state, PlatformRoot: c.PlatformRoot, Version: c.Version}).Run(ctx); err != nil {
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
		platformRoot = process.PlatformRoot
	}
	if pid, alive := supervisor.LivePID(state, platformRoot); alive {
		host.TerminateProcess(pid)
		if !waitExit(pid, stopBudget(runner)) {
			host.KillProcess(pid)
			waitExit(pid, 5*time.Second)
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

func waitExit(pid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !host.ProcessAlive(pid) {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return !host.ProcessAlive(pid)
}

func (c hostCommand) restart(ctx context.Context, runner *host.ProcessRunner, state host.State) (hostCommandResult, *diag.Diag) {
	// A live supervisor owns the host: restart through its channel so its view
	// of the child stays consistent.
	if client, err := control.Connect(state); err == nil {
		response, restartErr := client.Restart(ctx)
		if restartErr != nil {
			return nil, asDiag(restartErr)
		}
		return hostCommandResult(map[string]any{"ok": true, "operation": "restart", "stopped": response.Stopped, "host": response.Host}), nil
	}
	stopped, err := runner.Stop(ctx)
	if err != nil {
		return nil, asDiag(err)
	}
	info, startErr := c.spawnSupervisor(ctx, runner, state)
	if startErr != nil {
		return nil, asDiag(startErr)
	}
	return hostCommandResult(map[string]any{"ok": true, "operation": "restart", "stopped": stopped, "host": info}), nil
}

// lifecycleHost is the supervised host as the swap operations see it. Both
// calls build their runner from the receipt as it is at that moment, so a
// Resume after an update starts the new version and after a restore the old.
type lifecycleHost struct {
	command hostCommand
	state   host.State
}

func (h lifecycleHost) Halt(ctx context.Context) (bool, error) {
	runner, err := host.NewRunner(h.command.PlatformRoot, h.state)
	if err != nil {
		return false, err
	}
	result, err := halt(ctx, runner, h.state)
	return result.WasRunning, err
}

func (h lifecycleHost) Resume(ctx context.Context) error {
	runner, err := host.NewRunner(h.command.PlatformRoot, h.state)
	if err != nil {
		return err
	}
	_, err = h.command.spawnSupervisor(ctx, runner, h.state)
	return err
}

func asDiag(err error) *diag.Diag {
	var typed *diag.Diag
	if errors.As(err, &typed) {
		return typed
	}
	return contracts.NewDiag("KB_RUNTIME_UNEXPECTED", "The launcher failed to process the request.", "Read the launcher log in the state directory.", err)
}
