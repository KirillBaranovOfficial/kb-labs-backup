package host

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/kb-labs/clikit/diag"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kb-labs/create/v2/contracts"
	"github.com/kb-labs/create/v2/diagnostics"
)

// pidRecord is the host.pid file. It is the only cross-process handle on the
// host: stop and status from another launcher invocation rely on it.
type pidRecord struct {
	PID          int       `json:"pid"`
	PlatformRoot string    `json:"platformRoot"`
	StartedAt    time.Time `json:"startedAt"`
	HealthURL    string    `json:"healthUrl"`
	Command      string    `json:"command"`
}

// ActiveHostRecord identifies a live host process in the per-user launcher state.
type ActiveHostRecord struct {
	PID          int
	PlatformRoot string
}

// ActiveHost reads the live host record without deciding whether it belongs to
// a caller's requested platform root.
func ActiveHost(state State) (ActiveHostRecord, bool) {
	data, err := os.ReadFile(state.HostPID())
	if err != nil {
		return ActiveHostRecord{}, false
	}
	var record pidRecord
	if json.Unmarshal(data, &record) != nil || record.PID <= 0 {
		return ActiveHostRecord{}, false
	}
	if !processAlive(record.PID) {
		_ = os.Remove(state.HostPID())
		return ActiveHostRecord{}, false
	}
	return ActiveHostRecord{PID: record.PID, PlatformRoot: record.PlatformRoot}, true
}

// ProcessRunner spawns the host command directly in its own process group,
// records a pid file and log under the state home, polls the health URL, and
// stops it with a graceful terminate followed by a kill after the grace period.
type ProcessRunner struct {
	Spec           Spec
	State          State
	PlatformRoot   string
	InstallationID string
	Operation      string
	// HTTP and PollInterval exist for tests; zero values use sane defaults.
	HTTP         *http.Client
	PollInterval time.Duration
	// SecretEnv supplies the secret environment variables of the host process
	// (the platform's private store). It is read on every Start.
	SecretEnv func() (map[string]string, error)

	mu      sync.Mutex
	pid     int
	exited  chan struct{}
	waitErr error
	stderr  *tailBuffer
	secrets []string
}

const startupStderrLimit = 8 * 1024
const startupStderrLines = 40

var (
	secretAssignment = regexp.MustCompile(`(?i)(\b[A-Z0-9_]*(PASSWORD|TOKEN|SECRET|API[_-]?KEY|PRIVATE[_-]?KEY)[A-Z0-9_]*\s*=\s*)[^\s]+`)
	secretJSONField  = regexp.MustCompile(`(?i)(["']?(password|token|secret|apiKey|key)["']?\s*:\s*)("[^"\\]*(\\.[^"\\]*)*"|'[^']*'|[^,}\]\s]+)`)
)

type tailBuffer struct {
	mu    sync.Mutex
	limit int
	data  []byte
}

func (b *tailBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.data = append(b.data, data...)
	if len(b.data) > b.limit {
		b.data = append([]byte(nil), b.data[len(b.data)-b.limit:]...)
	}
	return len(data), nil
}

func (b *tailBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	lines := strings.Split(strings.TrimSpace(string(b.data)), "\n")
	if len(lines) > startupStderrLines {
		lines = lines[len(lines)-startupStderrLines:]
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func (r *ProcessRunner) Name() string { return "process" }

func (r *ProcessRunner) interval() time.Duration {
	if r.PollInterval > 0 {
		return r.PollInterval
	}
	return 100 * time.Millisecond
}

func (r *ProcessRunner) client() *http.Client {
	if r.HTTP != nil {
		return r.HTTP
	}
	return &http.Client{Timeout: time.Second}
}

// Start launches the host and returns once its health URL answers 2xx.
func (r *ProcessRunner) Start(ctx context.Context) (Info, error) {
	if err := r.State.Ensure(); err != nil {
		return Info{}, hostError("KB_HOST_STATE_DIR_UNWRITABLE", "Cannot write to the state directory "+r.State.Dir+".", "Fix the directory permissions or free disk space.", err, map[string]string{"path": r.State.Dir})
	}
	if record, ok := r.readPID(); ok {
		if processAlive(record.PID) {
			if record.PlatformRoot != r.PlatformRoot {
				return Info{}, RootConflict(record.PlatformRoot, record.PID)
			}
			return Info{}, hostError("KB_HOST_ALREADY_RUNNING", "The KB Labs host is already running.", "Use \"kb-create status\" or \"kb-create restart\".", nil, pidDetail(record.PID))
		}
		_ = os.Remove(r.State.HostPID())
	}
	address := r.Spec.HealthAddress()
	if conn, err := net.DialTimeout("tcp", address, 300*time.Millisecond); err == nil {
		_ = conn.Close()
		_, port, _ := net.SplitHostPort(address)
		owner := listenerOwner(port)
		return Info{}, portInUseError(port, address, "", owner, r.Operation, nil)
	}
	logFile, err := os.OpenFile(r.State.HostLog(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return Info{}, hostError("KB_HOST_STATE_DIR_UNWRITABLE", "Cannot write to the state directory "+r.State.Dir+".", "Fix the directory permissions or free disk space.", err, map[string]string{"path": r.State.Dir})
	}
	cmd := exec.Command(r.Spec.Command, r.Spec.Args...)
	cmd.Dir = r.Spec.WorkingDir
	env, err := r.environment()
	if err != nil {
		_ = logFile.Close()
		return Info{}, r.startFailed(err, nil)
	}
	cmd.Env = env
	r.stderr = &tailBuffer{limit: startupStderrLimit}
	cmd.Stdout, cmd.Stderr = logFile, io.MultiWriter(logFile, r.stderr)
	configureHostProcess(cmd)
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return Info{}, r.startFailed(err, nil)
	}
	exited := make(chan struct{})
	r.mu.Lock()
	r.pid, r.exited, r.waitErr = cmd.Process.Pid, exited, nil
	r.mu.Unlock()
	go func() {
		err := cmd.Wait()
		_ = logFile.Close()
		r.mu.Lock()
		r.waitErr = err
		r.mu.Unlock()
		close(exited)
	}()
	record := pidRecord{PID: cmd.Process.Pid, PlatformRoot: r.PlatformRoot, StartedAt: time.Now().UTC(), HealthURL: r.Spec.HealthURL, Command: r.Spec.Command}
	if err := writeJSONFile(r.State.HostPID(), record); err != nil {
		r.kill(cmd.Process.Pid)
		return Info{}, hostError("KB_HOST_STATE_DIR_UNWRITABLE", "Cannot write to the state directory "+r.State.Dir+".", "Fix the directory permissions or free disk space.", err, map[string]string{"path": r.State.Dir})
	}
	if err := r.waitReady(ctx, record.PID, exited); err != nil {
		return Info{}, err
	}
	return Info{Runner: r.Name(), PID: record.PID, HealthURL: r.Spec.HealthURL}, nil
}

// RunTool runs the platform's recovery tool to completion, in the host's
// working directory and environment, with the caller's terminal attached. It
// returns the tool's exit status; an error means the tool could not be run.
func (r *ProcessRunner) RunTool(ctx context.Context, tool Tool, extra []string, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	env, err := r.environment()
	if err != nil {
		return 0, err
	}
	cmd := exec.CommandContext(ctx, tool.Command, append(append([]string(nil), tool.Args...), extra...)...)
	cmd.Dir, cmd.Env = r.Spec.WorkingDir, env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, stderr
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return exit.ExitCode(), nil
		}
		return 0, err
	}
	return 0, nil
}

// environment layers the host's environment: the launcher's own, then the
// platform's secrets, then the spec's explicit values (the release wins).
func (r *ProcessRunner) environment() ([]string, error) {
	values := map[string]string{}
	if r.SecretEnv != nil {
		secretValues, err := r.SecretEnv()
		if err != nil {
			return nil, fmt.Errorf("read the platform secrets: %w", err)
		}
		for key, value := range secretValues {
			values[key] = value
		}
	}
	for key, value := range r.Spec.Env {
		values[key] = value
	}
	if r.InstallationID != "" {
		values["KB_INSTALLATION_ID"] = r.InstallationID
	}
	env := os.Environ()
	for key, value := range values {
		env = append(env, key+"="+value)
	}
	r.mu.Lock()
	r.secrets = r.secrets[:0]
	for _, value := range values {
		if value != "" {
			r.secrets = append(r.secrets, value)
		}
	}
	r.mu.Unlock()
	return env, nil
}

// StartFailure is the single diagnostic builder for failed launcher host starts.
func StartFailure(state State, cause error, evidence map[string]string) *diag.Diag {
	details := map[string]string{"log": state.HostLog(), "launcherLog": state.LauncherLog()}
	for key, value := range evidence {
		details[key] = redactHostOutput(value, nil)
	}
	if cause != nil {
		cause = errors.New(redactHostOutput(cause.Error(), nil))
	}
	if startup := startupEnvelope(details["stderrTail"]); startup != nil {
		details["hostStartupCode"] = startup.Error.Code
		reason := startup.Error.Code + ": " + startup.Error.Message
		if startup.Error.Cause != "" {
			reason += " (" + startup.Error.Cause + ")"
		}
		cause = errors.New(reason)
	}
	reason := "host startup failed"
	if cause != nil {
		reason = cause.Error()
	}
	nextCommand := details["nextCommand"]
	if nextCommand == "" {
		nextCommand = "kb-create start"
	}
	hint := fmt.Sprintf("Read the launcher log at %q and host log at %q, correct the reported startup failure, then run `%s`.", state.LauncherLog(), state.HostLog(), nextCommand)
	failure := hostError("KB_HOST_START_FAILED", "The host process failed while starting.", hint, cause, details)
	failure.Reason = reason
	failure.Cause = reason
	if err := appendLauncherFailure(state, failure); err != nil {
		failure.Meta["launcherLogError"] = err.Error()
	}
	return failure
}

func (r *ProcessRunner) startFailed(cause error, evidence map[string]string) *diag.Diag {
	if evidence == nil {
		evidence = map[string]string{}
	}
	if tail := r.stderrTail(); tail != "" {
		evidence["stderrTail"] = redactHostOutput(tail, r.secrets)
	}
	if r.Operation == "apply" {
		evidence["nextCommand"] = "kb-create apply"
	} else {
		root := r.PlatformRoot
		if root == "" {
			root = "<platform-root>"
		}
		evidence["nextCommand"] = fmt.Sprintf("kb-create start --platform-root %q", root)
	}
	return StartFailure(r.State, cause, evidence)
}

func (r *ProcessRunner) waitReady(ctx context.Context, pid int, exited <-chan struct{}) error {
	started := time.Now()
	deadline := time.NewTimer(r.Spec.ReadyTimeout)
	defer deadline.Stop()
	ticker := time.NewTicker(r.interval())
	defer ticker.Stop()
	lastProbe := readinessProbe{URL: r.Spec.HealthURL}
	for {
		lastProbe = r.probe(ctx)
		if lastProbe.Healthy {
			if err := r.verifyListenerOwner(pid); err != nil {
				r.kill(pid)
				return err
			}
			return nil
		}
		select {
		case <-exited:
			_ = os.Remove(r.State.HostPID())
			r.mu.Lock()
			cause := r.waitErr
			r.mu.Unlock()
			if cause == nil {
				cause = fmt.Errorf("process exited with status 0")
			}
			return r.startFailed(fmt.Errorf("host exited before becoming ready: %s", processExit(cause)), processExitDetails(cause))
		case <-deadline.C:
			r.kill(pid)
			return r.startFailed(fmt.Errorf("readiness check %s failed after %s", probeDescription(lastProbe), time.Since(started).Round(time.Millisecond)), readinessDetails(lastProbe, time.Since(started)))
		case <-ctx.Done():
			// A host that became healthy just as the caller cancelled is up;
			// the caller decides whether to stop it.
			if r.healthy(context.Background()) {
				if err := r.verifyListenerOwner(pid); err != nil {
					r.kill(pid)
					return err
				}
				return nil
			}
			r.kill(pid)
			return r.startFailed(ctx.Err(), readinessDetails(lastProbe, time.Since(started)))
		case <-ticker.C:
			lastProbe = r.probe(ctx)
			if lastProbe.Healthy {
				if err := r.verifyListenerOwner(pid); err != nil {
					r.kill(pid)
					return err
				}
				return nil
			}
		}
	}
}

type readinessProbe struct {
	URL     string
	Status  int
	Error   string
	Healthy bool
}

func (r *ProcessRunner) probe(ctx context.Context) readinessProbe {
	result := readinessProbe{URL: r.Spec.HealthURL}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, r.Spec.HealthURL, nil)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	response, err := r.client().Do(request)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	defer response.Body.Close()
	result.Status = response.StatusCode
	result.Healthy = response.StatusCode >= 200 && response.StatusCode < 300
	return result
}

func readinessDetails(probe readinessProbe, waited time.Duration) map[string]string {
	details := map[string]string{"readinessURL": probe.URL, "waited": waited.Round(time.Millisecond).String()}
	if probe.Status != 0 {
		details["readinessStatus"] = strconv.Itoa(probe.Status)
	}
	if probe.Error != "" {
		details["readinessError"] = probe.Error
	}
	return details
}

func probeDescription(probe readinessProbe) string {
	if probe.Status != 0 {
		return fmt.Sprintf("%s returned HTTP %d", probe.URL, probe.Status)
	}
	if probe.Error != "" {
		return fmt.Sprintf("%s could not be reached: %s", probe.URL, probe.Error)
	}
	return probe.URL
}

func processExit(err error) string {
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		if code := exit.ExitCode(); code >= 0 {
			return fmt.Sprintf("exit code %d", code)
		}
		return exit.Error()
	}
	return err.Error()
}

func processExitDetails(err error) map[string]string {
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		if code := exit.ExitCode(); code >= 0 {
			return map[string]string{"exitCode": strconv.Itoa(code)}
		}
		return map[string]string{"signal": exit.Error()}
	}
	return map[string]string{"exitStatus": err.Error()}
}

type hostStartupEnvelope struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Cause   string `json:"cause"`
	} `json:"error"`
}

func startupEnvelope(stderr string) *hostStartupEnvelope {
	for _, line := range strings.Split(stderr, "\n") {
		var envelope hostStartupEnvelope
		if json.Unmarshal(bytes.TrimSpace([]byte(line)), &envelope) == nil && envelope.Error.Code != "" {
			if _, known := diag.LookupCatalog(envelope.Error.Code); known {
				return &envelope
			}
		}
	}
	return nil
}

func (r *ProcessRunner) stderrTail() string {
	if r.stderr == nil {
		return ""
	}
	return redactHostOutput(r.stderr.String(), r.secrets)
}

func redactHostOutput(value string, secrets []string) string {
	value = diagnostics.Redact(value, secrets)
	value = secretAssignment.ReplaceAllString(value, `${1}"[REDACTED]"`)
	return secretJSONField.ReplaceAllString(value, `${1}"[REDACTED]"`)
}

func appendLauncherFailure(state State, failure *diag.Diag) error {
	file, err := os.OpenFile(state.LauncherLog(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	entry := map[string]any{"time": time.Now().UTC(), "event": "host.start.failed", "code": failure.Code,
		"cause": failure.Cause, "details": failure.Meta}
	encodeErr := json.NewEncoder(file).Encode(entry)
	closeErr := file.Close()
	return errors.Join(encodeErr, closeErr)
}

type portOwner struct{ PID, Command string }

func portInUseError(port, address, purpose string, owner portOwner, operation string, cause error) *diag.Diag {
	if operation == "" {
		operation = "start"
	}
	message := "Port " + port + " is already in use."
	if purpose != "" {
		message = "Port " + port + " required by " + purpose + " is already in use."
	}
	hint := "Stop the process using port " + port + ", then rerun `kb-create " + operation + "`."
	if owner.PID != "" && owner.Command != "" {
		hint = "Stop process " + owner.PID + " (" + owner.Command + ") using port " + port + ", then rerun `kb-create " + operation + "`."
	}
	meta := map[string]string{"port": port, "pid": owner.PID, "command": owner.Command, "operation": operation}
	if address != "" {
		meta["address"] = address
	}
	if purpose != "" {
		meta["purpose"] = purpose
	}
	return hostError(contracts.CodePortInUse, message, hint, cause, meta)
}

// RootConflict reports that the per-user launcher's live host serves another
// platform root. A host recorded without a root predates root ownership, so the
// hint names its pid instead of a stop command that could not match it.
func RootConflict(platformRoot string, pid int) *diag.Diag {
	pidText := strconv.Itoa(pid)
	meta := map[string]string{"platformRoot": platformRoot, "pid": pidText}
	if platformRoot == "" {
		return hostError("KB_HOST_ALREADY_RUNNING", "A KB Labs host started by an earlier launcher is already running (pid "+pidText+").", "Stop process "+pidText+", then rerun the command.", nil, meta)
	}
	return hostError("KB_HOST_ALREADY_RUNNING", "The KB Labs host for platform root "+platformRoot+" is already running (pid "+pidText+").", "Stop that platform first with `kb-create stop --platform-root "+platformRoot+"`.", nil, meta)
}

func listenerOwner(port string) portOwner {
	output, err := exec.Command("lsof", "-nP", "-iTCP:"+port, "-sTCP:LISTEN", "-Fp", "-Fc").Output()
	if err != nil {
		return portOwner{}
	}
	var owner portOwner
	for _, line := range strings.Split(string(output), "\n") {
		if len(line) < 2 {
			continue
		}
		switch line[0] {
		case 'p':
			owner.PID = line[1:]
		case 'c':
			owner.Command = line[1:]
		}
	}
	return owner
}

// CheckPorts reports conflicts in the resolved service graph before package
// downloads or installation change the platform root.
func CheckPorts(plan contracts.ResolvedInstallPlan, operation string) error {
	ports := map[string]string{}
	if plan.Host != nil {
		if parsed, err := url.Parse(plan.Host.HealthURL); err == nil {
			if _, port, err := net.SplitHostPort(parsed.Host); err == nil {
				ports[port] = plan.Host.HealthURL
			}
		}
	}
	for _, service := range plan.ServiceGraph.Services {
		if service.Port > 0 {
			ports[strconv.Itoa(service.Port)] = service.ID
		}
	}
	for port, purpose := range ports {
		listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", port))
		if err == nil {
			_ = listener.Close()
			continue
		}
		owner := listenerOwner(port)
		return portInUseError(port, "", purpose, owner, operation, err)
	}
	return nil
}

func (r *ProcessRunner) verifyListenerOwner(pid int) error {
	_, port, err := net.SplitHostPort(r.Spec.HealthAddress())
	if err != nil {
		return r.startFailed(err, map[string]string{"readinessURL": r.Spec.HealthURL})
	}
	owner := listenerOwner(port)
	if owner.PID != strconv.Itoa(pid) {
		cause := fmt.Errorf("readiness endpoint %s is served by pid %s (%s), expected host pid %d", r.Spec.HealthURL, owner.PID, owner.Command, pid)
		return r.startFailed(cause, map[string]string{"readinessURL": r.Spec.HealthURL, "expectedPid": strconv.Itoa(pid), "pid": owner.PID, "command": owner.Command})
	}
	return nil
}

// kill force-terminates a host that failed to become ready and clears its pid file.
func (r *ProcessRunner) kill(pid int) {
	killProcessGroup(pid)
	r.waitGone(pid, 5*time.Second)
	_ = os.Remove(r.State.HostPID())
}

func (r *ProcessRunner) healthy(ctx context.Context) bool {
	return r.probe(ctx).Healthy
}

// Stop terminates the recorded host: graceful first, kill after the grace
// period. Stopping a host that is not running succeeds with WasRunning=false.
func (r *ProcessRunner) Stop(ctx context.Context) (StopResult, error) {
	result := StopResult{Runner: r.Name()}
	record, ok := r.readPID()
	if ok && processAlive(record.PID) && record.PlatformRoot != r.PlatformRoot {
		return result, nil
	}
	if !ok || !processAlive(record.PID) {
		_ = os.Remove(r.State.HostPID())
		return result, nil
	}
	result.WasRunning, result.PID = true, record.PID
	terminateProcessGroup(record.PID)
	if r.waitGoneCtx(ctx, record.PID, r.Spec.StopGrace) {
		result.Graceful = true
		_ = os.Remove(r.State.HostPID())
		return result, nil
	}
	killProcessGroup(record.PID)
	result.Forced = true
	if !r.waitGoneCtx(ctx, record.PID, 5*time.Second) {
		return result, hostError("KB_HOST_STOP_FAILED", fmt.Sprintf("The host process %d did not stop.", record.PID), "Run \"kb-create stop\" again, or end the process manually and remove the stale pid file.", nil, pidDetail(record.PID))
	}
	_ = os.Remove(r.State.HostPID())
	return result, nil
}

func (r *ProcessRunner) waitGone(pid int, timeout time.Duration) bool {
	return r.waitGoneCtx(context.Background(), pid, timeout)
}

// waitGoneCtx waits for pid to disappear. When this runner spawned the process
// its own Wait goroutine reaps it, so the exit channel is authoritative;
// otherwise the pid is polled (the owner of the child reaps it).
func (r *ProcessRunner) waitGoneCtx(ctx context.Context, pid int, timeout time.Duration) bool {
	r.mu.Lock()
	var exited chan struct{}
	if r.pid == pid {
		exited = r.exited
	}
	r.mu.Unlock()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		if exited != nil {
			select {
			case <-exited:
				return true
			default:
			}
		} else if !processAlive(pid) {
			return true
		}
		select {
		case <-deadline.C:
			return false
		case <-ctx.Done():
			return false
		case <-ticker.C:
		}
	}
}

// Status reports the recorded process and its health. A pid file that points
// at a dead process is stale: it is removed and reported as stopped.
func (r *ProcessRunner) Status(ctx context.Context) (Status, error) {
	status := Status{Runner: r.Name(), State: StateStopped, HealthURL: r.Spec.HealthURL}
	record, ok := r.readPID()
	if !ok {
		return status, nil
	}
	if record.PlatformRoot != r.PlatformRoot {
		return status, nil
	}
	if !processAlive(record.PID) {
		_ = os.Remove(r.State.HostPID())
		status.StalePID = true
		return status, nil
	}
	started := record.StartedAt
	status.PID, status.StartedAt = record.PID, &started
	status.Healthy = r.healthy(ctx)
	status.State = StateUnhealthy
	if status.Healthy {
		status.State = StateRunning
	}
	return status, nil
}

// Wait blocks until the process this runner started exits. It is meant for
// foreground supervision and returns immediately when nothing was started here.
func (r *ProcessRunner) Wait(ctx context.Context) error {
	r.mu.Lock()
	exited := r.exited
	r.mu.Unlock()
	if exited == nil {
		return nil
	}
	select {
	case <-exited:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *ProcessRunner) readPID() (pidRecord, bool) {
	data, err := os.ReadFile(r.State.HostPID())
	if err != nil {
		return pidRecord{}, false
	}
	var record pidRecord
	if err := json.Unmarshal(data, &record); err != nil || record.PID <= 0 {
		// A corrupt pid file cannot identify a process; treat it as stale.
		_ = os.Remove(r.State.HostPID())
		return pidRecord{}, false
	}
	return record, true
}

// RunForeground starts the host, blocks until ctx is cancelled or the host
// exits, then stops it. It is the direct-host mode of `kb-create start`.
func RunForeground(ctx context.Context, runner *ProcessRunner) error {
	if _, err := runner.Start(ctx); err != nil {
		return err
	}
	_ = runner.Wait(ctx)
	if ctx.Err() == nil {
		// The host exited by itself; nothing left to stop.
		return nil
	}
	_, err := runner.Stop(context.Background())
	return err
}

func writeJSONFile(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	temp := path + ".tmp"
	if err := os.WriteFile(temp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(temp, path)
}
