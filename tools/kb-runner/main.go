//go:build darwin || linux

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/kb-labs/clikit/proc"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "__keeper" {
		os.Exit(proc.KeeperMain())
	}
	code, err := execute(os.Args[1:], os.Stdout, os.Stderr)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
	if code != 0 {
		os.Exit(code)
	}
}

func execute(args []string, stdout, stderr io.Writer) (int, error) {
	if len(args) == 0 {
		return 2, errors.New("usage: kb-runner <run|ps|events> [flags]")
	}
	switch args[0] {
	case "run":
		return runCommand(args[1:], stdout, stderr)
	case "ps":
		return psCommand(args[1:], stdout)
	case "events":
		return eventsCommand(args[1:], stdout)
	default:
		return 2, fmt.Errorf("kb-runner: unknown command %q; next: kb-runner --help", args[0])
	}
}

func runCommand(args []string, stdout, stderr io.Writer) (int, error) {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	tag := fs.String("tag", "", "run label")
	state := fs.String("state", "", "directory for run records and events")
	stall := fs.Duration("stall", 0, "report no-output intervals at this duration")
	stdoutPath := fs.String("stdout", "", "tee stdout to this file")
	stderrPath := fs.String("stderr", "", "tee stderr to this file")
	if err := fs.Parse(args); err != nil {
		return 2, err
	}
	command := fs.Args()
	if len(command) > 0 && command[0] == "--" {
		command = command[1:]
	}
	if len(command) == 0 {
		return 2, errors.New("kb-runner run: provide a command after --; next: kb-runner run --help")
	}
	if *tag == "" || *state == "" || *stall <= 0 || *stdoutPath == "" || *stderrPath == "" {
		return 2, errors.New("kb-runner run: --tag, --state, positive --stall, --stdout, and --stderr are required; next: kb-runner run --help")
	}
	helper, err := os.Executable()
	if err != nil {
		return 1, fmt.Errorf("kb-runner run: locate keeper executable: %w", err)
	}
	run, err := proc.Start(context.Background(), proc.Config{HelperPath: helper, StateDir: *state, Tag: *tag, Stall: *stall, StdoutPath: *stdoutPath, StderrPath: *stderrPath, Command: command})
	if err != nil {
		return 1, fmt.Errorf("kb-runner run: %w\nnext: inspect state directory access with ls -ld %s, then retry: %s", err, shellQuote(*state), shellJoin(os.Args))
	}
	if err := run.Wait(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			code := exitErr.ExitCode()
			if code == 0 {
				return 0, nil
			}
			return code, fmt.Errorf("kb-runner: supervised command exited with status %d; inspect its tree and lifecycle at %s\nnext: kb-runner ps --state %s", code, filepath.Join(*state, "events.jsonl"), shellQuote(*state))
		}
		return 1, fmt.Errorf("kb-runner: keeper wait failed: %w\nnext: inspect run state with kb-runner ps --state %s", err, shellQuote(*state))
	}
	if _, err := fmt.Fprintf(stdout, "run %s completed\n", *tag); err != nil {
		return 1, fmt.Errorf("kb-runner: write completion output: %w", err)
	}
	return 0, nil
}

func psCommand(args []string, output io.Writer) (int, error) {
	fs := flag.NewFlagSet("ps", flag.ContinueOnError)
	state := fs.String("state", "", "directory containing run records")
	if err := fs.Parse(args); err != nil {
		return 2, err
	}
	if *state == "" {
		return 2, errors.New("kb-runner ps: --state is required; next: kb-runner ps --help")
	}
	listing := proc.List(*state)
	if listing.Error != nil {
		return 1, fmt.Errorf("kb-runner ps: read run state: %w\nnext: check directory access with ls -ld %s", listing.Error, shellQuote(*state))
	}
	if len(listing.Runs) == 0 {
		if _, err := fmt.Fprintln(output, "No supervised runs."); err != nil {
			return 1, fmt.Errorf("kb-runner ps: write output: %w", err)
		}
		return 0, nil
	}
	if _, err := fmt.Fprintln(output, "TAG\tPID\tSTATUS\tCOMMAND"); err != nil {
		return 1, fmt.Errorf("kb-runner ps: write output: %w", err)
	}
	for _, run := range listing.Runs {
		if _, err := fmt.Fprintf(output, "%s\t%d\t%s\t%s\n", run.Tag, run.PID, strings.ToUpper(string(run.Status)), strings.Join(run.Command, " ")); err != nil {
			return 1, fmt.Errorf("kb-runner ps: write output: %w", err)
		}
		for _, child := range run.Tree {
			status := ""
			if child.Unverified {
				status = "\tunverified"
			}
			if _, err := fmt.Fprintf(output, "  └─ %d\t%s\t elapsed=%s%s\n", child.PID, child.Command, time.Duration(child.ElapsedMS)*time.Millisecond, status); err != nil {
				return 1, fmt.Errorf("kb-runner ps: write output: %w", err)
			}
		}
	}
	return 0, nil
}

func eventsCommand(args []string, output io.Writer) (int, error) {
	fs := flag.NewFlagSet("events", flag.ContinueOnError)
	state := fs.String("state", "", "directory containing events.jsonl")
	follow := fs.Bool("follow", false, "continue streaming appended events")
	if err := fs.Parse(args); err != nil {
		return 2, err
	}
	if *state == "" {
		return 2, errors.New("kb-runner events: --state is required; next: kb-runner events --help")
	}
	ctx := context.Background()
	if *follow {
		var stop context.CancelFunc
		ctx, stop = signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
		defer stop()
	}
	if err := proc.Events(ctx, *state, *follow, output); err != nil && !errors.Is(err, context.Canceled) {
		return 1, fmt.Errorf("kb-runner events: %w\nnext: check event file access with ls -l %s", err, shellQuote(filepath.Join(*state, "events.jsonl")))
	}
	return 0, nil
}

func shellJoin(args []string) string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = shellQuote(arg)
	}
	return strings.Join(quoted, " ")
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
