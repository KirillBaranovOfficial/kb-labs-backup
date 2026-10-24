//go:build darwin || linux

package proc

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"sync/atomic"
	"syscall"
	"time"
)

type activityWriter struct {
	file     *os.File
	terminal *os.File
	activity *atomic.Int64
}

func (writer activityWriter) Write(data []byte) (int, error) {
	writer.activity.Store(time.Now().UnixNano())
	if err := writeOutput(writer.file, data); err != nil {
		return 0, err
	}
	if writer.terminal != nil {
		if err := writeOutput(writer.terminal, data); err != nil {
			return 0, err
		}
	}
	return len(data), nil
}

func runKeeper(cfg keeperConfig) int {
	ready := os.NewFile(3, "proc-ready")
	if ready == nil {
		return 2
	}
	defer func() {
		if ready != nil {
			if err := ready.Close(); err != nil {
				fmt.Fprintln(os.Stderr, "proc: close readiness pipe:", err)
			}
		}
	}()
	parentGone, monitorErr := monitorParent(cfg.ParentPID, cfg.ParentIdentity)
	if monitorErr != nil {
		writeReadyError(ready, monitorErr)
		return 1
	}
	if err := prepareKeeperPlatform(); err != nil {
		writeReadyError(ready, err)
		return 1
	}
	if err := os.MkdirAll(filepath.Join(cfg.StateDir, "runs"), 0o700); err != nil {
		writeReadyError(ready, err)
		return 1
	}
	scratch, err := runScratchPath(cfg.RunID)
	if err != nil {
		writeReadyError(ready, err)
		return 1
	}
	runDirectory := filepath.Join(cfg.StateDir, "runs", cfg.RunID)
	runDirectoryIdentity, runDirectoryOwned, err := createOwnedDirectory(runDirectory, unixScratchDirectoryOps{})
	if err != nil {
		var cleanupErr error
		if runDirectoryOwned {
			cleanupErr = removeOwnedPathRelative(cfg.StateDir, runDirectory, runDirectoryIdentity, "", nil)
		}
		writeReadyError(ready, errors.Join(fmt.Errorf("create run directory: %w", err), cleanupErr))
		return 1
	}
	scratchIdentity, scratchOwned, err := createOwnedDirectory(scratch, unixScratchDirectoryOps{})
	if err != nil {
		cleanupErr := removeOwnedPathRelative(cfg.StateDir, runDirectory, runDirectoryIdentity, "", nil)
		if scratchOwned {
			cleanupErr = errors.Join(cleanupErr, removeScratchPathRelative(cfg.StateDir, scratch, scratchIdentity, nil))
		}
		writeReadyError(ready, errors.Join(fmt.Errorf("create run scratch directory: %w", err), cleanupErr))
		return 1
	}
	if err := inspectCreatedScratch(cfg.StateDir, scratch, scratchIdentity); err != nil {
		cleanupErr := removeOwnedPathRelative(cfg.StateDir, runDirectory, runDirectoryIdentity, "", nil)
		writeReadyError(ready, errors.Join(err, cleanupErr))
		return 1
	}
	cfg.Keep = resolveKeepPaths(scratch, cfg.Keep)
	rec := record{RunID: cfg.RunID, Tag: cfg.Tag, PID: os.Getpid(), Command: cfg.Command, StateDir: cfg.StateDir, Status: StatusStarting, Scratch: scratch, ScratchDevice: scratchIdentity.Device, ScratchInode: scratchIdentity.Inode, Keep: append([]string(nil), cfg.Keep...)}
	startedAt, ok, identityErr := ProcessIdentity(os.Getpid())
	if identityErr != nil {
		return keeperError(rec, "inspect keeper process identity", identityErr)
	}
	if !ok {
		writeReadyError(ready, errors.New("cannot determine keeper process start identity"))
		return keeperError(rec, "inspect keeper process identity", errors.New("cannot determine keeper process start identity"))
	}
	rec.StartedAt = startedAt
	rec.LatestTag = cfg.LatestTag
	if err := writeRecord(rec); err != nil {
		writeReadyError(ready, err)
		return keeperError(rec, "record run", err)
	}
	if err := claimGate(cfg.StateDir, cfg.RunID, cfg.HoldGate, rec.PID, rec.StartedAt); err != nil {
		writeReadyError(ready, err)
		return keeperError(rec, "claim held gate", err)
	}
	stdout, err := openOutput(cfg.StdoutPath)
	if err != nil {
		writeReadyError(ready, err)
		return keeperError(rec, "open stdout", err)
	}
	defer func() {
		if err := stdout.Close(); err != nil {
			fmt.Fprintln(os.Stderr, "proc: close stdout log:", err)
		}
	}()
	stderr, err := openOutput(cfg.StderrPath)
	if err != nil {
		writeReadyError(ready, err)
		return keeperError(rec, "open stderr", err)
	}
	defer func() {
		if err := stderr.Close(); err != nil {
			fmt.Fprintln(os.Stderr, "proc: close stderr log:", err)
		}
	}()

	cmd := exec.Command(cfg.Command[0], cfg.Command[1:]...)
	cmd.Dir = cfg.Dir
	cmd.Env = environmentFor(cfg.Config, os.Environ())
	configureCommand(cmd, cfg.RunID, cfg.StateDir, scratch)
	if cfg.GrantedCPUs > 0 || cfg.MemoryBytes > 0 {
		cmd.Env = append(filteredEnv(cmd.Env), fmt.Sprintf("KB_RUNNER_CPUS=%d", cfg.GrantedCPUs), fmt.Sprintf("KB_RUNNER_MEM_BYTES=%d", cfg.MemoryBytes))
	}
	if cfg.StdinPath != "" {
		stdin, err := os.Open(cfg.StdinPath)
		if err != nil {
			writeReadyError(ready, fmt.Errorf("open command stdin %s: %w", cfg.StdinPath, err))
			return keeperError(rec, "open command stdin", err)
		}
		defer func() {
			if err := stdin.Close(); err != nil {
				fmt.Fprintln(os.Stderr, "proc: close command stdin:", err)
			}
		}()
		cmd.Stdin = stdin
	}
	stdoutPipe, stdoutWriter, err := os.Pipe()
	if err != nil {
		writeReadyError(ready, err)
		return keeperError(rec, "create stdout pipe", err)
	}
	defer stdoutPipe.Close()
	defer stdoutWriter.Close()
	stderrPipe, stderrWriter, err := os.Pipe()
	if err != nil {
		writeReadyError(ready, err)
		return keeperError(rec, "create stderr pipe", err)
	}
	defer stderrPipe.Close()
	defer stderrWriter.Close()
	var lastOutput atomic.Int64
	rec.StartedAtTime = time.Now().UTC()
	lastOutput.Store(rec.StartedAtTime.UnixNano())
	cmd.Stdout = stdoutWriter
	cmd.Stderr = stderrWriter
	limitsAdvisory, cleanupLimits, startErr := startWithRunLimits(cfg.Config, cfg.RunID, cmd)
	defer cleanupLimits()
	if startErr != nil {
		writeReadyError(ready, startErr)
		return keeperError(rec, "start command", startErr)
	}
	commandIdentity, commandAlive, commandIdentityErr := ProcessIdentity(cmd.Process.Pid)
	if commandIdentityErr != nil {
		if killErr := cmd.Process.Kill(); killErr != nil {
			fmt.Fprintln(os.Stderr, "proc: stop command after identity observation failure:", killErr)
		}
		if waitErr := cmd.Wait(); waitErr != nil {
			fmt.Fprintln(os.Stderr, "proc: wait after identity observation failure:", waitErr)
		}
		writeReadyError(ready, fmt.Errorf("observe started command PID %d: %w", cmd.Process.Pid, commandIdentityErr))
		return keeperError(rec, "observe started command", commandIdentityErr)
	}
	if commandAlive {
		rec.Observed = append(rec.Observed, Process{
			PID: cmd.Process.Pid, PPID: os.Getpid(), Command: filepath.Base(cfg.Command[0]),
			Identity: commandIdentity,
		})
	}
	if err := errors.Join(stdoutWriter.Close(), stderrWriter.Close()); err != nil {
		if killErr := cmd.Process.Kill(); killErr != nil {
			fmt.Fprintln(os.Stderr, "proc: stop command after output pipe close failure:", killErr)
		}
		if waitErr := cmd.Wait(); waitErr != nil {
			fmt.Fprintln(os.Stderr, "proc: wait after output pipe close failure:", waitErr)
		}
		writeReadyError(ready, err)
		return keeperError(rec, "close command output pipes", err)
	}
	rec.Status = StatusRunning
	rec.LimitsAdvisory = limitsAdvisory
	rec.LastOutputAt = rec.StartedAtTime
	rec.StallMS = cfg.Stall.Milliseconds()
	if err := updateRecord(cfg.StateDir, rec.RunID, func(stored *record) error {
		rec.SlotGroup, rec.Slots, rec.QueueOrder = stored.SlotGroup, stored.Slots, stored.QueueOrder
		rec.EnqueuedAt, rec.WaitGate, rec.HoldGate = stored.EnqueuedAt, stored.WaitGate, stored.HoldGate
		rec.CostKey, rec.MemoryBytes, rec.CPUsMin, rec.CPUsMax, rec.DiskBytes = stored.CostKey, stored.MemoryBytes, stored.CPUsMin, stored.CPUsMax, stored.DiskBytes
		rec.GrantedCPUs, rec.ResourceNotice = stored.GrantedCPUs, stored.ResourceNotice
		*stored = rec
		return nil
	}); err != nil {
		writeReadyError(ready, err)
		return keeperError(rec, "record command start", err)
	}
	if err := appendEvent(cfg.StateDir, eventFor(rec, EventStart)); err != nil {
		writeReadyError(ready, err)
		return keeperError(rec, "record command start", err)
	}
	if err := json.NewEncoder(ready).Encode(readyMessage{Record: rec}); err != nil {
		if killErr := cmd.Process.Kill(); killErr != nil {
			fmt.Fprintln(os.Stderr, "proc: stop command after readiness failure:", killErr)
		}
		if waitErr := cmd.Wait(); waitErr != nil {
			fmt.Fprintln(os.Stderr, "proc: wait for command after readiness failure:", waitErr)
		}
		return keeperError(rec, "signal readiness", err)
	}
	if err := ready.Close(); err != nil {
		if killErr := cmd.Process.Kill(); killErr != nil {
			fmt.Fprintln(os.Stderr, "proc: stop command after readiness close failure:", killErr)
		}
		if waitErr := cmd.Wait(); waitErr != nil {
			fmt.Fprintln(os.Stderr, "proc: wait for command after readiness close failure:", waitErr)
		}
		ready = nil
		return keeperError(rec, "close readiness", err)
	}
	ready = nil
	var initialSampleError error
	if ancestrySampleInterval > 0 {
		rec.Observed, initialSampleError = sampleDescendants(rec, rec.Observed)
		if initialSampleError != nil {
			fmt.Fprintln(os.Stderr, "proc: sample initial descendant tree:", initialSampleError)
		}
		if err := saveProgress(rec, lastOutput.Load()); err != nil {
			fmt.Fprintln(os.Stderr, "proc: save initial descendant sample:", err)
		}
	}
	copyDone := make(chan error, 2)
	go func() {
		copyDone <- copyOutput(stdoutPipe, activityWriter{file: stdout, terminal: os.Stdout, activity: &lastOutput})
	}()
	go func() {
		copyDone <- copyOutput(stderrPipe, activityWriter{file: stderr, terminal: os.Stderr, activity: &lastOutput})
	}()
	waitDone := make(chan error, 1)
	go func() { waitDone <- cmd.Wait() }()
	stall := time.NewTicker(100 * time.Millisecond)
	defer stall.Stop()
	metricStarted := time.Now()
	var previousCPU, cpuUsed float64
	var metricSamples int
	var metricsPartial bool
	var waitErr error
	var commandExitedAt time.Time
	var commandFinished bool
	stallReported := false
	var parentMonitorError string
	var cleanupError error = initialSampleError
	cleanup := treeCleanup{Reaped: []Process{}, Unverified: []Process{}}
	var eventError error
	var outputError error
	readersRemaining := 2
	for !commandFinished {
		select {
		case waitErr = <-waitDone:
			commandExitedAt = time.Now().UTC()
			commandFinished = true
		case copyErr := <-copyDone:
			readersRemaining--
			if copyErr != nil {
				outputError = errors.Join(outputError, copyErr)
				result := killTreeWithHistory(rec.PID, rec.RunID, rec.Observed)
				cleanup = mergeCleanup(cleanup, result)
				cleanupError = errors.Join(cleanupError, result.err)
				waitErr = <-waitDone
				commandExitedAt = time.Now().UTC()
				commandFinished = true
			}
		case parentErr := <-parentGone:
			if parentErr != nil {
				parentMonitorError = parentErr.Error()
			}
			result := killTreeWithHistory(rec.PID, rec.RunID, rec.Observed)
			cleanup = mergeCleanup(cleanup, result)
			cleanupError = errors.Join(cleanupError, result.err)
			waitErr = <-waitDone
			commandExitedAt = time.Now().UTC()
			commandFinished = true
		case <-stall.C:
			if ancestrySampleInterval > 0 {
				observed, observeErr := sampleDescendants(rec, rec.Observed)
				if observeErr != nil {
					cleanupError = errors.Join(cleanupError, observeErr)
					fmt.Fprintln(os.Stderr, "proc: sample descendant tree:", observeErr)
				}
				rec.Observed = observed
			}
			if err := saveProgress(rec, lastOutput.Load()); err != nil {
				fmt.Fprintln(os.Stderr, "proc: update run progress:", err)
				cleanupError = errors.Join(cleanupError, err)
			}
			rss, cpu, partial, metricErr := sampleRunMetrics(rec)
			if metricErr != nil {
				partial = true
				fmt.Fprintln(os.Stderr, "proc: sample resource use:", metricErr)
			}
			metricsPartial = metricsPartial || partial
			if rss > rec.PeakRSSBytes {
				rec.PeakRSSBytes = rss
			}
			if metricSamples == 0 {
				cpuUsed = cpu
			} else if cpu >= previousCPU {
				cpuUsed += cpu - previousCPU
			}
			previousCPU = cpu
			metricSamples++
			if seconds := time.Since(metricStarted).Seconds(); seconds > 0 {
				rec.AverageCPU = cpuUsed / seconds
			}
			rec.MeasurementPartial = metricsPartial
			emitted, observationErr := recordResourceObservation(cfg.StateDir, rec.RunID, rss, rec.AverageCPU, rec.MeasurementPartial)
			if observationErr != nil {
				eventError = errors.Join(eventError, observationErr)
			}
			rec.OverBudget = rec.OverBudget || emitted
			if !stallReported && time.Since(time.Unix(0, lastOutput.Load())) >= cfg.Stall {
				e := eventFor(rec, EventStall)
				leaves, leafErr := ProcessLeavesWithHistory(rec.PID, rec.RunID, rec.Observed)
				e.Leaves = leaves
				if leafErr != nil {
					e.Error = "unverified/failed observation: " + leafErr.Error()
					cleanupError = errors.Join(cleanupError, leafErr)
				}
				if err := appendEvent(cfg.StateDir, e); err != nil {
					fmt.Fprintln(os.Stderr, "proc: append stall event:", err)
					eventError = errors.Join(eventError, err)
				}
				stallReported = true
			}
		}
	}
	if metricSamples == 0 {
		rec.MeasurementPartial = true
	}
	result := killTreeWithHistory(rec.PID, rec.RunID, rec.Observed)
	cleanup = mergeCleanup(cleanup, result)
	cleanupError = errors.Join(cleanupError, result.err)
	for readersRemaining > 0 {
		if copyErr := <-copyDone; copyErr != nil {
			outputError = errors.Join(outputError, copyErr)
		}
		readersRemaining--
	}
	code, signal, exitError := platformExit(waitErr)
	exitEvent := eventFor(rec, EventExit)
	exitEvent.At = commandExitedAt
	exitEvent.DurationMS = commandExitedAt.Sub(rec.StartedAtTime).Milliseconds()
	exitEvent.ExitCode = code
	exitEvent.Signal = signal
	exitEvent.Error = exitError
	exitEvent.Unverified = append(exitEvent.Unverified, cleanup.Unverified...)
	if exitEvent.Error == "" {
		exitEvent.Error = parentMonitorError
	}
	if outputError != nil {
		if exitEvent.Error == "" {
			exitEvent.Error = outputError.Error()
		} else {
			exitEvent.Error = errors.Join(errors.New(exitEvent.Error), outputError).Error()
		}
	}
	if cleanupError != nil {
		if exitEvent.Error == "" {
			exitEvent.Error = cleanupError.Error()
		} else {
			exitEvent.Error = errors.Join(errors.New(exitEvent.Error), cleanupError).Error()
		}
	}
	if eventError != nil {
		if exitEvent.Error == "" {
			exitEvent.Error = eventError.Error()
		} else {
			exitEvent.Error = errors.Join(errors.New(exitEvent.Error), eventError).Error()
		}
	}
	if err := appendEvent(cfg.StateDir, exitEvent); err != nil {
		fmt.Fprintln(os.Stderr, "proc: append exit event:", err)
		eventError = errors.Join(eventError, err)
	}
	if reapErr := reapAdopted(); reapErr != nil {
		cleanupError = errors.Join(cleanupError, reapErr)
	}
	reapEvent := eventFor(rec, EventTeardown)
	reapEvent.DurationMS = exitEvent.DurationMS
	reapEvent.Reaped = cleanup.Reaped
	reapEvent.Unverified = cleanup.Unverified
	reapEvent.Remaining = cleanup.Remaining
	if cleanupError != nil {
		reapEvent.Error = cleanupError.Error()
	}
	reapEvent.Resources = cleanupResources(cfg.StateDir, cfg.RunID)
	if resourceErr := resourceOutcomesError(reapEvent.Resources); resourceErr != nil {
		cleanupError = errors.Join(cleanupError, resourceErr)
		if reapEvent.Error == "" {
			reapEvent.Error = cleanupError.Error()
		} else {
			reapEvent.Error = errors.Join(errors.New(reapEvent.Error), resourceErr).Error()
		}
	}
	scratchOutcome, scratchErr := cleanupScratchOutcome(cfg.StateDir, scratch, fileIdentity{Device: rec.ScratchDevice, Inode: rec.ScratchInode}, cfg.Keep)
	if scratch != "" {
		reapEvent.Resources = append(reapEvent.Resources, scratchOutcome)
	}
	if scratchErr != nil {
		err := scratchErr
		cleanupError = errors.Join(cleanupError, fmt.Errorf("remove run scratch: %w", err))
		if dirtyErr := markRunDirty(cfg.StateDir, cfg.RunID, "scratch cleanup failed: "+err.Error()); dirtyErr != nil {
			cleanupError = errors.Join(cleanupError, dirtyErr)
		}
		if reapEvent.Error == "" {
			reapEvent.Error = cleanupError.Error()
		} else {
			reapEvent.Error = errors.Join(errors.New(reapEvent.Error), cleanupError).Error()
		}
	}
	if eventError != nil {
		if reapEvent.Error == "" {
			reapEvent.Error = eventError.Error()
		} else {
			reapEvent.Error = errors.Join(errors.New(reapEvent.Error), eventError).Error()
		}
	}
	if err := appendEvent(cfg.StateDir, reapEvent); err != nil {
		fmt.Fprintln(os.Stderr, "proc: append teardown event:", err)
		eventError = errors.Join(eventError, err)
	}
	if err := releaseGate(cfg.StateDir, cfg.RunID, cfg.HoldGate); err != nil {
		fmt.Fprintln(os.Stderr, "proc: release held gate:", err)
		cleanupError = errors.Join(cleanupError, err)
	}
	if err := completeRecord(cfg.StateDir, cfg.RunID, code, signal, time.Duration(exitEvent.DurationMS)*time.Millisecond, rec); err != nil {
		fmt.Fprintln(os.Stderr, "proc: record completed run:", err)
		cleanupError = errors.Join(cleanupError, err)
	}
	if code != nil && *code != 0 {
		return *code
	}
	if signal != "" {
		return 128 + signalNumber(signal)
	}
	if exitError != "" {
		return 1
	}
	if cleanupError != nil {
		return 1
	}
	if eventError != nil {
		return 1
	}
	return 0
}

func sampleRunMetrics(rec record) (uint64, float64, bool, error) {
	return sampleRunMetricsWithSource(rec, osProcessSource{})
}

func sampleRunMetricsWithSource(rec record, source processSource) (uint64, float64, bool, error) {
	tree, err := processTreeWithHistorySource(source, rec.PID, rec.RunID, rec.Observed)
	var rss uint64
	var cpu float64
	partial := false
	if len(tree) == 0 {
		partial = true
	}
	for _, process := range tree {
		if process.Unverified {
			partial = true
			continue
		}
		if !process.MetricsAvailable {
			partial = true
			continue
		}
		rss += process.RSSBytes
		cpu += process.CPUSeconds
	}
	return rss, cpu, partial || err != nil, err
}

func copyOutput(src io.Reader, dst activityWriter) error {
	if _, err := io.Copy(dst, src); err != nil {
		return fmt.Errorf("copy command output: %w", err)
	}
	return nil
}

func writeReadyError(ready *os.File, cause error) {
	if err := json.NewEncoder(ready).Encode(readyMessage{Error: cause.Error()}); err != nil {
		fmt.Fprintln(os.Stderr, "proc: report keeper startup failure:", err)
	}
}

func writeOutput(output *os.File, data []byte) error {
	written, err := output.Write(data)
	if err != nil {
		return err
	}
	if written != len(data) {
		return io.ErrShortWrite
	}
	return nil
}

func openOutput(path string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	return os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
}

func keeperError(rec record, action string, err error) int {
	fmt.Fprintf(os.Stderr, "proc: %s: %v\n", action, err)
	e := eventFor(rec, EventExit)
	e.Error = err.Error()
	if cleanupErr := killTree(rec.PID, rec.RunID); cleanupErr != nil {
		e.Error = errors.Join(err, cleanupErr).Error()
		fmt.Fprintln(os.Stderr, "proc: reap command tree:", cleanupErr)
	}
	if appendErr := appendEvent(rec.StateDir, e); appendErr != nil {
		fmt.Fprintln(os.Stderr, "proc: record command failure:", appendErr)
	}
	reapEvent := eventFor(rec, EventTeardown)
	reapEvent.Error = e.Error
	reapEvent.Resources = cleanupResources(rec.StateDir, rec.RunID)
	scratch := rec.Scratch
	if scratch != "" {
		scratchOutcome, scratchErr := cleanupScratchOutcome(rec.StateDir, scratch, fileIdentity{Device: rec.ScratchDevice, Inode: rec.ScratchInode}, rec.Keep)
		reapEvent.Resources = append(reapEvent.Resources, scratchOutcome)
		if scratchErr != nil {
			if dirtyErr := markRunDirty(rec.StateDir, rec.RunID, "scratch cleanup failed: "+scratchErr.Error()); dirtyErr != nil {
				fmt.Fprintln(os.Stderr, "proc: mark scratch cleanup failure dirty:", dirtyErr)
			}
		}
	}
	if appendErr := appendEvent(rec.StateDir, reapEvent); appendErr != nil {
		fmt.Fprintln(os.Stderr, "proc: record teardown:", appendErr)
	}
	if releaseErr := releaseGate(rec.StateDir, rec.RunID, rec.HoldGate); releaseErr != nil {
		fmt.Fprintln(os.Stderr, "proc: release gate after keeper error:", releaseErr)
	}
	return 1
}

func sampleDescendants(rec record, previous []Process) ([]Process, error) {
	observed := make(map[string]Process, len(previous))
	for _, process := range previous {
		identity, alive, err := inspectIdentity(process.PID)
		if err != nil {
			return nil, fmt.Errorf("inspect previously observed process %d: %w", process.PID, err)
		}
		if alive && identity == process.Identity {
			observed[processObservationKey(process)] = process
		}
	}
	tree, err := ProcessTree(rec.PID, rec.RunID)
	for _, process := range tree {
		if !process.Unverified {
			observed[processObservationKey(process)] = process
		}
	}
	out := make([]Process, 0, len(observed))
	for _, process := range observed {
		out = append(out, process)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].PID == out[j].PID {
			return out[i].Identity < out[j].Identity
		}
		return out[i].PID < out[j].PID
	})
	return out, err
}

func processObservationKey(process Process) string {
	return fmt.Sprintf("%d:%s", process.PID, process.Identity)
}

func saveProgress(rec record, lastOutput int64) error {
	last := time.Unix(0, lastOutput).UTC()
	status := StatusRunning
	if rec.StallMS > 0 && time.Since(last) >= time.Duration(rec.StallMS)*time.Millisecond {
		status = StatusStalled
	}
	return updateRecord(rec.StateDir, rec.RunID, func(stored *record) error {
		stored.Status = status
		stored.LastOutputAt = last
		stored.Observed = rec.Observed
		stored.PeakRSSBytes, stored.AverageCPU = rec.PeakRSSBytes, rec.AverageCPU
		stored.MeasurementPartial, stored.OverBudget = rec.MeasurementPartial, rec.OverBudget
		return nil
	})
}

func mergeCleanup(left, right treeCleanup) treeCleanup {
	appendUnique := func(dst, src []Process) []Process {
		seen := make(map[string]bool, len(dst)+len(src))
		for _, process := range dst {
			seen[processObservationKey(process)] = true
		}
		for _, process := range src {
			key := processObservationKey(process)
			if !seen[key] {
				seen[key] = true
				dst = append(dst, process)
			}
		}
		return dst
	}
	left.Reaped = appendUnique(left.Reaped, right.Reaped)
	left.Unverified = appendUnique(left.Unverified, right.Unverified)
	left.Remaining = appendUnique(left.Remaining, right.Remaining)
	left.err = errors.Join(left.err, right.err)
	return left
}

func signalKeeper(pid int, identity string) error {
	if pid <= 0 {
		return errors.New("proc: invalid keeper pid")
	}
	current, alive, err := inspectIdentity(pid)
	if err != nil {
		return err
	}
	if !alive || current != identity {
		return nil
	}
	return syscall.Kill(pid, syscall.SIGTERM)
}
