//go:build darwin || linux

package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/kb-labs/clikit/proc"
)

func agentCommand(root runnerRoot, args []string, out, errOut io.Writer) (int, error) {
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		_, err := fmt.Fprintf(out, "Usage: %s agent start --stream <name> --vendor codex|claude --workdir <path> --prompt-file <path> [phase commands]\n", root.Name())
		return 0, err
	}
	if len(args) == 0 || args[0] != "start" {
		return 2, fmt.Errorf("%s agent: expected start; next: %s agent --help", root.Name(), root.InvocationCommand())
	}
	return agentStartCommand(root, args[1:], out, errOut)
}

func agentStartCommand(root runnerRoot, args []string, out, errOut io.Writer) (int, error) {
	fs := flag.NewFlagSet("agent start", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	state := fs.String("state", defaultRunnerState(), "durable runner state directory")
	stream := fs.String("stream", "", "stable stream name")
	vendor := fs.String("vendor", "codex", "agent provider")
	model := fs.String("model", "", "provider model")
	workdir := fs.String("workdir", "", "agent working directory")
	promptFile := fs.String("prompt-file", "", "prompt or resume message file")
	session := fs.String("session", "", "resume this vendor session")
	effort := fs.String("effort", "high", "reasoning effort")
	message := fs.String("message", "", "final message output path")
	setup := fs.String("worktree-command", "", "optional worktree preparation command")
	install := fs.String("install-command", "", "dependency installation command")
	build := fs.String("build-command", "", "workspace build command")
	jobID := fs.String("job-id", "", "internal persisted job identifier")
	recoveryAttempt := fs.Int("recovery-attempt", 0, "internal bounded provider recovery attempt")
	slots := fs.Int("slots", 5, "maximum concurrent jobs in slot group")
	slotGroup := fs.String("slot-group", "", "vendor capacity group")
	waitGate := fs.String("wait-gate", "", "wait for this shared batch gate")
	holdGate := fs.String("hold-gate", "", "hold this batch gate while the job runs")
	minFree := fs.String("min-free", "", "minimum free disk bytes required for admission")
	stall := fs.Duration("stall", 30*time.Minute, "fail the job after this no-output interval")
	fiveHour := fs.Float64("five-hour-threshold", 95, "quota admission threshold")
	week := fs.Float64("week-threshold", 98, "weekly quota admission threshold")
	providerFailureThreshold := fs.Int("provider-failure-threshold", 3, "consecutive provider network errors before pausing")
	var writable pathListFlag
	fs.Var(&writable, "add-dir", "additional writable directory (repeatable)")
	if err := fs.Parse(args); err != nil {
		return 2, err
	}
	if *stream == "" || *workdir == "" || *promptFile == "" {
		return 2, fmt.Errorf("%s agent start: --stream, --workdir, and --prompt-file are required; next: %s agent --help", root.Name(), root.InvocationCommand())
	}
	if *slotGroup == "" {
		*slotGroup = *vendor
	}
	if *jobID == "" {
		id, err := newJobID(*stream)
		if err != nil {
			return 1, err
		}
		store := newJobStore(*state)
		finalPath := *message
		if finalPath == "" {
			finalPath = filepath.Join(*state, "jobs", id+".last.md")
		}
		if _, err := store.Create(Job{ID: id, Stream: *stream, Vendor: *vendor, Model: *model, State: JobQueued, Phase: PhaseWorktree, SlotGroup: *slotGroup, SlotCeiling: *slots, MessagePath: finalPath}); err != nil {
			return 1, err
		}
		minFreeBytes := uint64(0)
		if *minFree != "" {
			minFreeBytes, err = strconv.ParseUint(*minFree, 10, 64)
			if err != nil {
				return failAgentJob(store, id, PhaseAdmission, ReasonSetup, root, finalPath, fmt.Errorf("invalid --min-free bytes: %w", err))
			}
		}
		helper, err := os.Executable()
		if err != nil {
			return failAgentJob(store, id, PhaseWorktree, ReasonSetup, root, finalPath, fmt.Errorf("locate agent process owner: %w", err))
		}
		argsForWorker := append([]string{"__agent_worker", "--job-id", id}, args...)
		stdoutPath := filepath.Join(*state, "jobs", id+".stdout.log")
		stderrPath := filepath.Join(*state, "jobs", id+".stderr.log")
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		run, err := proc.Start(ctx, proc.Config{HelperPath: helper, StateDir: *state, Tag: "agent:" + id, LatestTag: "agent:" + *stream, Stall: *stall, StdoutPath: stdoutPath, StderrPath: stderrPath, SlotGroup: *slotGroup, Slots: *slots, WaitGate: *waitGate, HoldGate: *holdGate, MinFree: minFreeBytes, Keep: []string{stdoutPath, stderrPath}, Command: append([]string{helper}, argsForWorker...)})
		if err != nil {
			return failAgentJob(store, id, PhaseAdmission, ReasonSetup, root, finalPath, fmt.Errorf("agent process admission failed: %w", err))
		}
		waitErr := run.Wait()
		if job, getErr := store.Get(id); getErr == nil && job.Final == nil {
			terminalState, reason := JobFailed, ReasonVendorError
			if ctx.Err() != nil {
				terminalState, reason = JobCancelled, ReasonNone
			} else if waitErr != nil {
				var exitErr *exec.ExitError
				if errors.As(waitErr, &exitErr) && exitErr.ExitCode() == 137 {
					terminalState, reason = JobStalled, ReasonStall
				} else {
					terminalState, reason = JobLost, ReasonProcessLost
				}
			}
			if err := store.Terminate(id, JobTerminal{State: terminalState, Phase: job.Phase, Reason: reason, Next: root.InvocationCommand() + " status --json", MessagePath: finalPath, SessionID: job.SessionID}); err != nil {
				return 1, fmt.Errorf("agent process ended but terminal job record could not be persisted: %w", err)
			}
		}
		if waitErr != nil {
			return 1, fmt.Errorf("agent job %s failed under process owner: %s; next: %s status --json", id, exitSummary(waitErr), root.InvocationCommand())
		}
		_, err = fmt.Fprintf(out, "job %s completed\n", id)
		return 0, err
	}
	adapter, err := newAdapter(*vendor)
	if err != nil {
		return 2, err
	}
	prompt, err := os.ReadFile(*promptFile)
	if err != nil {
		return 1, fmt.Errorf("agent start: read prompt: %w", err)
	}
	id := *jobID
	store := newJobStore(*state)
	logs := filepath.Join(*state, "jobs", id+".jsonl")
	finalPath := *message
	if finalPath == "" {
		finalPath = filepath.Join(*state, "jobs", id+".last.md")
	}
	phases := []struct {
		phase   JobPhase
		command string
	}{{PhaseWorktree, *setup}, {PhaseInstall, *install}, {PhaseBuild, *build}}
	for _, phase := range phases {
		if *recoveryAttempt > 0 {
			break
		}
		if phase.command == "" {
			continue
		}
		if err = store.Update(id, func(j *Job) error { j.State = JobRunning; j.Phase = phase.phase; return nil }); err != nil {
			return 1, err
		}
		cmd := exec.Command("/bin/sh", "-c", phase.command)
		cmd.Dir = *workdir
		output, e := cmd.CombinedOutput()
		if logErr := appendJobEvent(logs, map[string]any{"phase": phase.phase, "output": string(output), "time": time.Now().UTC()}); logErr != nil {
			return failAgentJob(store, id, phase.phase, ReasonSetup, root, finalPath, fmt.Errorf("record phase output: %w", logErr))
		}
		if e != nil {
			reason := ReasonSetup
			return failAgentJob(store, id, phase.phase, reason, root, finalPath, fmt.Errorf("agent job %s failed in %s: %s", id, phase.phase, exitSummary(e)))
		}
	}
	if *recoveryAttempt == 0 {
		if err = store.Update(id, func(j *Job) error { j.State = JobRunning; j.Phase = PhaseAdmission; return nil }); err != nil {
			return 1, err
		}
		limitCtx, limitCancel := context.WithTimeout(context.Background(), 10*time.Second)
		limits, limitErr := adapter.Limits(limitCtx)
		limitCancel()
		if limitErr == nil {
			if err := waitForQuotaAdmission(context.Background(), limits, adapter, store, id, *fiveHour, *week); err != nil {
				return failAgentJob(store, id, PhaseAdmission, ReasonQuota, root, finalPath, err)
			}
		}
	}
	if err = store.Update(id, func(j *Job) error { j.State = JobRunning; j.Phase = PhaseRun; return nil }); err != nil {
		return 1, err
	}
	if err = store.Update(id, func(job *Job) error {
		job.Attempts = append(job.Attempts, ProviderAttempt{Number: *recoveryAttempt + 1, StartedAt: time.Now().UTC(), SessionID: *session, Outcome: "running"})
		return nil
	}); err != nil {
		return failAgentJob(store, id, PhaseRun, ReasonSetup, root, finalPath, fmt.Errorf("record provider attempt: %w", err))
	}
	req := AgentRequest{Prompt: string(prompt), Workdir: *workdir, Effort: *effort, Model: *model, SessionID: *session, WritableDirs: writable, FinalPath: finalPath}
	var cmd *exec.Cmd
	if *session != "" {
		cmd, err = adapter.Resume(context.Background(), req)
	} else {
		cmd, err = adapter.Start(context.Background(), req)
	}
	if err != nil {
		return failAgentJob(store, id, PhaseRun, ReasonVendorError, root, finalPath, fmt.Errorf("agent job %s could not start provider: %v", id, err))
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return failAgentJob(store, id, PhaseRun, ReasonVendorError, root, finalPath, err)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err = cmd.Start(); err != nil {
		return failAgentJob(store, id, PhaseRun, ReasonVendorError, root, finalPath, err)
	}
	health := newHealthTracker(*providerFailureThreshold)
	var final strings.Builder
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	var scanErr error
	for scanner.Scan() {
		line := append([]byte(nil), scanner.Bytes()...)
		event, e := health.Observe(*vendor, line)
		if e != nil {
			scanErr = e
			break
		}
		if logErr := appendJobEvent(logs, event); logErr != nil {
			scanErr = logErr
			break
		}
		if updateErr := store.Update(id, func(j *Job) error {
			j.LastActivity = time.Now().UTC()
			if event.SessionID != "" {
				j.SessionID = event.SessionID
			}
			if event.Kind == EventUsage {
				j.Tokens = event.Tokens
			}
			return nil
		}); updateErr != nil {
			scanErr = updateErr
			break
		}
		if event.Kind == EventFinal {
			if event.FinalMessage != "" {
				final.WriteString(event.FinalMessage)
			}
		}
		if event.Kind == EventProviderUnreachable || event.Kind == EventRateLimited || event.Kind == EventAuthExpired {
			if updateErr := store.Update(id, func(j *Job) error {
				j.State = JobPaused
				if event.Kind == EventRateLimited {
					j.Reason = ReasonRateLimited
				} else if event.Kind == EventProviderUnreachable {
					j.Reason = ReasonProviderUnreachable
				} else {
					j.Reason = ReasonAuthExpired
				}
				return nil
			}); updateErr != nil {
				scanErr = updateErr
				break
			}
		}
	}
	if e := scanner.Err(); e != nil {
		scanErr = e
	}
	if scanErr != nil {
		killErr := cmd.Process.Kill()
		if killErr != nil && !errors.Is(killErr, os.ErrProcessDone) {
			scanErr = errors.Join(scanErr, fmt.Errorf("stop provider after stream failure: %w", killErr))
		}
	}
	waitErr := cmd.Wait()
	if scanErr != nil {
		waitErr = scanErr
	}
	completedJob, getErr := store.Get(id)
	if getErr != nil {
		return 1, getErr
	}
	if waitErr != nil && completedJob.State == JobPaused && *recoveryAttempt < 3 && completedJob.SessionID != "" {
		nextAttempt := *recoveryAttempt + 1
		if updateErr := store.Update(id, func(job *Job) error {
			last := &job.Attempts[len(job.Attempts)-1]
			last.FinishedAt = time.Now().UTC()
			last.SessionID = completedJob.SessionID
			last.Reason = job.Reason
			last.Outcome = "paused"
			return nil
		}); updateErr != nil {
			return failAgentJob(store, id, PhaseRun, ReasonSetup, root, finalPath, fmt.Errorf("record paused provider attempt: %w", updateErr))
		}
		if err := waitForProviderRecovery(context.Background(), adapter, completedJob.Reason, nextAttempt, *fiveHour, *week); err != nil {
			return failAgentJob(store, id, PhaseRun, completedJob.Reason, root, finalPath, err)
		}
		retryArgs := append([]string(nil), args...)
		retryArgs = append(retryArgs, "--session", completedJob.SessionID, "--recovery-attempt", strconv.Itoa(nextAttempt))
		return agentStartCommand(root, retryArgs, out, errOut)
	}
	if waitErr == nil {
		if updateErr := store.Update(id, func(job *Job) error {
			last := &job.Attempts[len(job.Attempts)-1]
			last.FinishedAt = time.Now().UTC()
			last.SessionID = completedJob.SessionID
			last.Outcome = "ok"
			return nil
		}); updateErr != nil {
			return failAgentJob(store, id, PhaseRun, ReasonSetup, root, finalPath, fmt.Errorf("record completed provider attempt: %w", updateErr))
		}
	}
	if waitErr == nil {
		if e := os.MkdirAll(filepath.Dir(finalPath), 0700); e != nil {
			return failAgentJob(store, id, PhaseRun, ReasonSetup, root, finalPath, e)
		}
		if _, statErr := os.Stat(finalPath); errors.Is(statErr, os.ErrNotExist) {
			if e := os.WriteFile(finalPath, []byte(final.String()), 0600); e != nil {
				return failAgentJob(store, id, PhaseRun, ReasonSetup, root, finalPath, e)
			}
		} else if statErr != nil {
			return failAgentJob(store, id, PhaseRun, ReasonSetup, root, finalPath, statErr)
		}
		if completedJob.State == JobPaused {
			err = store.Terminate(id, JobTerminal{State: JobFailed, Phase: PhaseRun, Reason: completedJob.Reason, Next: root.InvocationCommand() + " status --json", MessagePath: finalPath, SessionID: completedJob.SessionID})
		} else {
			err = store.Terminate(id, JobTerminal{State: JobOK, Phase: PhaseRun, Next: root.InvocationCommand() + " status", MessagePath: finalPath, SessionID: completedJob.SessionID})
		}
	} else {
		state := JobFailed
		reason := ReasonVendorError
		if errors.Is(waitErr, context.DeadlineExceeded) {
			state = JobStalled
			reason = ReasonStall
		}
		if strings.Contains(strings.ToLower(stderr.String()), "rate limit") {
			reason = ReasonRateLimited
		}
		err = store.Terminate(id, JobTerminal{State: state, Phase: PhaseRun, Reason: reason, Next: root.InvocationCommand() + " status", MessagePath: finalPath, SessionID: completedJob.SessionID})
	}
	if err != nil {
		return 1, err
	}
	if waitErr != nil {
		return 1, fmt.Errorf("agent job %s ended in %s: %s; next: %s status", id, PhaseRun, exitSummary(waitErr), root.InvocationCommand())
	}
	_, err = fmt.Fprintf(out, "job %s ok\n", id)
	return 0, err
}

func failAgentJob(store *jobStore, id string, phase JobPhase, reason JobReason, root runnerRoot, messagePath string, cause error) (int, error) {
	next := root.InvocationCommand() + " status --state " + shellQuote(filepath.Dir(store.dir)) + " --json"
	terminalErr := store.Terminate(id, JobTerminal{State: JobFailed, Phase: phase, Reason: reason, Next: next, MessagePath: messagePath})
	if terminalErr != nil {
		return 1, fmt.Errorf("%v; also could not persist terminal job record: %w; next: %s", cause, terminalErr, next)
	}
	return 1, fmt.Errorf("%v; next: %s", cause, next)
}

func waitForQuotaAdmission(ctx context.Context, limits []VendorLimit, provider limitsProvider, store *jobStore, id string, fiveHour, week float64) error {
	for quotaBlocked(limits, fiveHour, week) {
		if err := store.Update(id, func(job *Job) error {
			job.State = JobQuotaWait
			job.Phase = PhaseAdmission
			job.Reason = ReasonQuota
			return nil
		}); err != nil {
			return fmt.Errorf("persist quota-wait state: %w", err)
		}
		reset := nextBlockedReset(limits, fiveHour, week)
		if reset == nil {
			return nil
		}
		delay := time.Until(*reset)
		if delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
		probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		refreshed, err := provider.Limits(probeCtx)
		cancel()
		if err != nil {
			return nil
		}
		limits = refreshed
	}
	return store.Update(id, func(job *Job) error {
		job.State = JobRunning
		job.Phase = PhaseAdmission
		job.Reason = ReasonNone
		return nil
	})
}

func nextBlockedReset(limits []VendorLimit, fiveHour, week float64) *time.Time {
	var next *time.Time
	for _, limit := range limits {
		blocked := (limit.Window == "5h" && limit.UsedPercent >= fiveHour) || (limit.Window == "week" && limit.UsedPercent >= week)
		if !blocked || limit.Confidence != "vendor" || limit.ResetsAt == nil {
			continue
		}
		if next == nil || limit.ResetsAt.Before(*next) {
			value := *limit.ResetsAt
			next = &value
		}
	}
	return next
}

func waitForProviderRecovery(ctx context.Context, adapter AgentAdapter, reason JobReason, attempt int, fiveHour, week float64) error {
	delay := time.Duration(1<<min(attempt-1, 5)) * time.Second
	if reason == ReasonRateLimited {
		limits, err := adapter.Limits(ctx)
		if err == nil {
			if reset := nextBlockedReset(limits, fiveHour, week); reset != nil && time.Until(*reset) > delay {
				delay = time.Until(*reset)
			}
		}
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return nil
		}
	}
	for probe := 0; probe < 3; probe++ {
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		limits, err := adapter.Limits(ctx)
		if err == nil {
			for _, limit := range limits {
				if limit.Confidence == "vendor" {
					return nil
				}
			}
		}
		delay = min(delay*2, 30*time.Second)
	}
	return errors.New("provider recovery probes remained unavailable after three attempts")
}

func newJobID(stream string) (string, error) {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	safe := strings.NewReplacer("/", "-", "\\", "-", " ", "-").Replace(stream)
	return fmt.Sprintf("%s-%s", safe, hex.EncodeToString(b[:])), nil
}
func appendJobEvent(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	encodeErr := json.NewEncoder(f).Encode(value)
	closeErr := f.Close()
	if encodeErr != nil {
		return encodeErr
	}
	return closeErr
}
func exitSummary(err error) string {
	var e *exec.ExitError
	if errors.As(err, &e) {
		return fmt.Sprintf("provider command exited with code %d", e.ExitCode())
	}
	return "provider command failed"
}
