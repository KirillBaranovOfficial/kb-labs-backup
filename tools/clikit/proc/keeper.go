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
	"sync/atomic"
	"syscall"
	"time"
)

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
	startedAt, ok := ProcessIdentity(os.Getpid())
	if !ok {
		writeReadyError(ready, errors.New("cannot determine keeper process start identity"))
		return 1
	}
	rec := record{RunID: cfg.RunID, Tag: cfg.Tag, PID: os.Getpid(), StartedAt: startedAt, Command: cfg.Command, StateDir: cfg.StateDir}
	if err := writeRecord(rec); err != nil {
		writeReadyError(ready, err)
		return 1
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
	cmd.Env = append(filteredEnv(os.Environ()), cfg.Env...)
	configureCommand(cmd, cfg.RunID)
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		writeReadyError(ready, err)
		return keeperError(rec, "create stdout pipe", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		writeReadyError(ready, err)
		return keeperError(rec, "create stderr pipe", err)
	}
	if err := cmd.Start(); err != nil {
		writeReadyError(ready, err)
		return keeperError(rec, "start command", err)
	}
	if err := appendEvent(cfg.StateDir, eventFor(rec, "start")); err != nil {
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
	var lastOutput atomic.Int64
	lastOutput.Store(time.Now().UnixNano())
	copyDone := make(chan error, 2)
	go func() { copyDone <- copyOutput(stdoutPipe, stdout, os.Stdout, &lastOutput) }()
	go func() { copyDone <- copyOutput(stderrPipe, stderr, os.Stderr, &lastOutput) }()
	waitDone := make(chan error, 1)
	go func() { waitDone <- cmd.Wait() }()
	stall := time.NewTicker(100 * time.Millisecond)
	defer stall.Stop()
	var waitErr error
	var commandExitedAt time.Time
	var commandFinished bool
	stallReported := false
	var parentMonitorError string
	var outputError error
	var cleanupError error
	var eventError error
	readersRemaining := 2
	var copyNotifications <-chan error = copyDone
	for !commandFinished {
		select {
		case waitErr = <-waitDone:
			commandExitedAt = time.Now().UTC()
			commandFinished = true
		case copyErr := <-copyNotifications:
			readersRemaining--
			if copyErr != nil {
				outputError = errors.Join(outputError, copyErr)
				cleanupError = errors.Join(cleanupError, killTree(rec.PID, rec.RunID))
				waitErr = <-waitDone
				commandExitedAt = time.Now().UTC()
				commandFinished = true
			}
			if readersRemaining == 0 {
				copyNotifications = nil
			}
		case parentErr := <-parentGone:
			if parentErr != nil {
				parentMonitorError = parentErr.Error()
			}
			cleanupError = errors.Join(cleanupError, killTree(rec.PID, rec.RunID))
			waitErr = <-waitDone
			commandExitedAt = time.Now().UTC()
			commandFinished = true
		case <-stall.C:
			if !stallReported && time.Since(time.Unix(0, lastOutput.Load())) >= cfg.Stall {
				e := eventFor(rec, "stall")
				e.Leaves = ProcessLeaves(rec.PID, rec.RunID)
				if err := appendEvent(cfg.StateDir, e); err != nil {
					fmt.Fprintln(os.Stderr, "proc: append stall event:", err)
					eventError = errors.Join(eventError, err)
				}
				stallReported = true
			}
		}
	}
	cleanupError = errors.Join(cleanupError, killTree(rec.PID, rec.RunID))
	for readersRemaining > 0 {
		if copyErr := <-copyDone; copyErr != nil {
			outputError = errors.Join(outputError, copyErr)
		}
		readersRemaining--
	}
	code, signal, exitError := platformExit(waitErr)
	exitEvent := eventFor(rec, "exit")
	exitEvent.At = commandExitedAt
	exitEvent.ExitCode = code
	exitEvent.Signal = signal
	exitEvent.Error = exitError
	if outputError != nil {
		if exitEvent.Error == "" {
			exitEvent.Error = outputError.Error()
		} else {
			exitEvent.Error = errors.Join(errors.New(exitEvent.Error), outputError).Error()
		}
	}
	if exitEvent.Error == "" {
		exitEvent.Error = parentMonitorError
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
	reapEvent := eventFor(rec, "reap")
	if cleanupError != nil {
		reapEvent.Error = cleanupError.Error()
	}
	if eventError != nil {
		if reapEvent.Error == "" {
			reapEvent.Error = eventError.Error()
		} else {
			reapEvent.Error = errors.Join(errors.New(reapEvent.Error), eventError).Error()
		}
	}
	if err := appendEvent(cfg.StateDir, reapEvent); err != nil {
		fmt.Fprintln(os.Stderr, "proc: append reap event:", err)
		eventError = errors.Join(eventError, err)
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

func copyOutput(src io.Reader, dst, terminal *os.File, activity *atomic.Int64) error {
	buffer := make([]byte, 32*1024)
	for {
		n, err := src.Read(buffer)
		if n > 0 {
			activity.Store(time.Now().UnixNano())
			if err := writeOutput(dst, buffer[:n]); err != nil {
				return fmt.Errorf("write output file: %w", err)
			}
			if terminal != nil {
				if err := writeOutput(terminal, buffer[:n]); err != nil {
					return fmt.Errorf("write terminal output: %w", err)
				}
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("read command output: %w", err)
		}
	}
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
	e := eventFor(rec, "exit")
	e.Error = err.Error()
	if cleanupErr := killTree(rec.PID, rec.RunID); cleanupErr != nil {
		e.Error = errors.Join(err, cleanupErr).Error()
		fmt.Fprintln(os.Stderr, "proc: reap command tree:", cleanupErr)
	}
	if appendErr := appendEvent(rec.StateDir, e); appendErr != nil {
		fmt.Fprintln(os.Stderr, "proc: record command failure:", appendErr)
	}
	reapEvent := eventFor(rec, "reap")
	reapEvent.Error = e.Error
	if appendErr := appendEvent(rec.StateDir, reapEvent); appendErr != nil {
		fmt.Fprintln(os.Stderr, "proc: record reap:", appendErr)
	}
	return 1
}

func signalKeeper(pid int, identity string) error {
	if pid <= 0 {
		return errors.New("proc: invalid keeper pid")
	}
	if current, ok := ProcessIdentity(pid); !ok || current != identity {
		return nil
	}
	return syscall.Kill(pid, syscall.SIGTERM)
}
