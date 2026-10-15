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
	"strconv"
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
		return 2, errors.New("usage: kb-runner <run|gate|ps|wait|events|own|gc> [flags]")
	}
	switch args[0] {
	case "run":
		return runCommand(args[1:], stdout, stderr)
	case "ps":
		return psCommand(args[1:], stdout)
	case "wait":
		return waitCommand(args[1:], stdout, stderr)
	case "gate":
		return gateCommand(args[1:], stdout, stderr)
	case "events":
		return eventsCommand(args[1:], stdout)
	case "own":
		return ownCommand(args[1:], stdout)
	case "gc":
		return gcCommand(args[1:], stdout)
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
	stdinPath := fs.String("stdin", "", "read command stdin from this file")
	slotGroup := fs.String("slot-group", "", "serialize starts within this capacity group")
	slots := fs.Int("slots", 0, "maximum live runs allowed in the slot group")
	waitGate := fs.String("wait-gate", "", "wait for this named gate to be released")
	holdGate := fs.String("hold-gate", "", "hold this named gate for the command tree lifetime")
	minFree := fs.String("min-free", "", "minimum free bytes on the state filesystem")
	var envVars stringListFlag
	fs.Var(&envVars, "env", "add environment entry KEY=VALUE (repeatable)")
	var keepPaths pathListFlag
	fs.Var(&keepPaths, "keep", "keep this output path after run cleanup (repeatable)")
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
	var minFreeBytes uint64
	if *minFree != "" {
		var err error
		minFreeBytes, err = parseSize(*minFree)
		if err != nil {
			return 2, fmt.Errorf("kb-runner run: invalid --min-free: %w; next: pass an integer byte count, for example --min-free 12000000000", err)
		}
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	helper, err := os.Executable()
	if err != nil {
		return 1, fmt.Errorf("kb-runner run: locate keeper executable: %w", err)
	}
	keepPaths = append(keepPaths, *stdoutPath, *stderrPath)
	run, err := proc.Start(ctx, proc.Config{HelperPath: helper, StateDir: *state, Tag: *tag, Stall: *stall, StdoutPath: *stdoutPath, StderrPath: *stderrPath, StdinPath: *stdinPath, SlotGroup: *slotGroup, Slots: *slots, WaitGate: *waitGate, HoldGate: *holdGate, MinFree: minFreeBytes, Env: envVars, Keep: keepPaths, Command: command})
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

type stringListFlag []string

func (values *stringListFlag) String() string { return strings.Join(*values, ",") }

func (values *stringListFlag) Set(value string) error {
	key, _, ok := strings.Cut(value, "=")
	if !ok || key == "" {
		return fmt.Errorf("environment entry %q must be KEY=VALUE", value)
	}
	*values = append(*values, value)
	return nil
}

type pathListFlag []string

func (values *pathListFlag) String() string { return strings.Join(*values, ",") }
func (values *pathListFlag) Set(value string) error {
	if value == "" {
		return errors.New("kept path cannot be empty")
	}
	*values = append(*values, value)
	return nil
}

func psCommand(args []string, output io.Writer) (int, error) {
	fs := flag.NewFlagSet("ps", flag.ContinueOnError)
	state := fs.String("state", "", "directory containing run records")
	leftovers := fs.Bool("leftovers", false, "list every uncleaned owned resource")
	if err := fs.Parse(args); err != nil {
		return 2, err
	}
	if *state == "" {
		return 2, errors.New("kb-runner ps: --state is required; next: kb-runner ps --help")
	}
	if *leftovers {
		entries, err := proc.Leftovers(*state)
		if err != nil {
			return 1, fmt.Errorf("kb-runner ps: list leftovers: %w\nnext: check state access with ls -ld %s", err, shellQuote(*state))
		}
		if len(entries) == 0 {
			_, err := fmt.Fprintln(output, "No resource leftovers.")
			return 0, err
		}
		for _, entry := range entries {
			if _, err := fmt.Fprintf(output, "%s\t%s\t%s\t%s\n", entry.Resource.Kind, entry.Resource.ID, entry.Outcome, entry.Error); err != nil {
				return 1, err
			}
		}
		return 0, nil
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
	if _, err := fmt.Fprintln(output, "TAG\tPID\tSTATUS\tWAIT\tELAPSED\tTREE\tCOMMAND"); err != nil {
		return 1, fmt.Errorf("kb-runner ps: write output: %w", err)
	}
	for _, run := range listing.Runs {
		wait := run.WaitReason
		if run.Status == proc.StatusLost {
			wait = "lost: " + run.Error
		} else if run.Error != "" {
			wait = "observation: " + run.Error
		}
		if run.QueuePosition > 0 {
			wait = fmt.Sprintf("position=%d %s", run.QueuePosition, wait)
		}
		if _, err := fmt.Fprintf(output, "%s\t%d\t%s\t%s\telapsed=%dms\ttree=%d\t%s\n", run.Tag, run.PID, strings.ToUpper(string(run.Status)), wait, run.ElapsedMS, len(run.Tree), strings.Join(run.Command, " ")); err != nil {
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
	filter := fs.String("filter", "", "comma-separated event types to emit")
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
	filters, err := proc.ParseEventFilter(*filter)
	if err != nil {
		return 2, fmt.Errorf("kb-runner events: %w; next: use --filter start,exit,stall,queued,blocked,teardown", err)
	}
	if err := proc.Events(ctx, *state, *follow, filters, output); err != nil && !errors.Is(err, context.Canceled) {
		return 1, fmt.Errorf("kb-runner events: %w\nnext: check event file access with ls -l %s", err, shellQuote(filepath.Join(*state, "events.jsonl")))
	}
	return 0, nil
}

func gateCommand(args []string, stdout, stderr io.Writer) (int, error) {
	if len(args) < 2 || args[0] != "hold" || args[1] == "" {
		return 2, errors.New("kb-runner gate: usage: kb-runner gate hold <name> [run flags] -- <command>; next: kb-runner gate --help")
	}
	name := args[1]
	runArgs := append([]string(nil), args[2:]...)
	runArgs = append([]string{"--hold-gate", name}, runArgs...)
	return runCommand(runArgs, stdout, stderr)
}

func waitCommand(args []string, stdout, stderr io.Writer) (int, error) {
	fs := flag.NewFlagSet("wait", flag.ContinueOnError)
	fs.SetOutput(stderr)
	tag := fs.String("tag", "", "run tag to await")
	state := fs.String("state", "", "directory for run records")
	if err := fs.Parse(args); err != nil {
		return 2, err
	}
	if *tag == "" || *state == "" {
		return 2, errors.New("kb-runner wait: --tag and --state are required; next: kb-runner wait --help")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	run, err := proc.WaitTag(ctx, *state, *tag)
	if err != nil {
		return 1, fmt.Errorf("kb-runner wait: %w; next: kb-runner ps --state %s", err, shellQuote(*state))
	}
	if run.Status == proc.StatusLost {
		return 1, fmt.Errorf("kb-runner wait: run %s was lost: %s; next: kb-runner ps --state %s", run.Tag, run.Error, shellQuote(*state))
	}
	if _, err := fmt.Fprintf(stdout, "run %s exited after %dms\n", run.Tag, run.DurationMS); err != nil {
		return 1, fmt.Errorf("kb-runner wait: write output: %w", err)
	}
	if run.Signal != "" {
		number, _ := strconv.Atoi(run.Signal)
		return 128 + number, nil
	}
	if run.ExitCode != nil {
		return *run.ExitCode, nil
	}
	return 1, errors.New("kb-runner wait: run ended without an exit code or signal")
}

func parseSize(value string) (uint64, error) {
	return strconv.ParseUint(value, 10, 64)
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

func ownCommand(args []string, output io.Writer) (int, error) {
	if len(args) < 2 {
		return 2, errors.New("kb-runner own: usage: own path --create dir|file PATH or own docker-compose NAME -f FILE... -- COMPOSE-ARGV...; next: call own from a command started by kb-runner run")
	}
	runID := os.Getenv("KBLABS_CLIPROC_RUN")
	state := os.Getenv("KBLABS_CLIPROC_STATE")
	if runID == "" || state == "" {
		return 2, errors.New("kb-runner own: no active run marker; run this command inside kb-runner run so the resource has a cleanup owner\nnext: kb-runner run --help")
	}
	resource := proc.Resource{Kind: proc.ResourceKind(args[0])}
	var ownErr error
	switch resource.Kind {
	case proc.ResourcePath:
		if len(args) != 4 || args[1] != "--create" {
			return 2, errors.New("kb-runner own path: usage: own path --create dir|file PATH; next: create external paths through this exclusive ownership command")
		}
		ownErr = proc.CreateOwnedPath(state, runID, args[3], proc.OwnedPathType(args[2]))
		resource.ID = args[3]
	case proc.ResourceCompose:
		if len(args) < 5 {
			return 2, errors.New("kb-runner own docker-compose: usage: own docker-compose NAME -f FILE... -- COMPOSE-ARGV...; next: pass the same Compose prefix used to create the project")
		}
		resource.ID = args[1]
		separator := -1
		for i := 2; i < len(args); {
			if args[i] == "--" {
				separator = i
				break
			}
			if args[i] != "-f" || i+1 >= len(args) {
				return 2, fmt.Errorf("kb-runner own docker-compose: expected -f FILE before --, got %q", args[i])
			}
			resource.Files = append(resource.Files, args[i+1])
			i += 2
		}
		if separator < 0 || separator+1 >= len(args) {
			return 2, errors.New("kb-runner own docker-compose: compose argv prefix is required after --")
		}
		resource.ComposePrefix = append([]string(nil), args[separator+1:]...)
		ownErr = proc.RegisterResource(state, runID, resource)
	default:
		if len(args) != 2 {
			return 2, errors.New("kb-runner own: resource kind takes one id")
		}
		resource.ID = args[1]
		ownErr = proc.RegisterResource(state, runID, resource)
	}
	if ownErr != nil {
		return 1, ownFailure(args, state, resource, ownErr)
	}
	_, err := fmt.Fprintf(output, "owned %s %s\n", resource.Kind, resource.ID)
	return 0, err
}

func ownFailure(args []string, state string, resource proc.Resource, ownErr error) error {
	var unsupported *proc.PathXattrUnsupportedError
	if errors.As(ownErr, &unsupported) {
		invocation := shellJoin(append([]string{"kb-runner", "own"}, args...))
		return fmt.Errorf("kb-runner own: %w\nnext: inspect the mount with `df -P %s`, choose a mount that supports user xattrs, then rerun: `%s`", ownErr, shellQuote(unsupported.Path), invocation)
	}
	return fmt.Errorf("kb-runner own: %w\nnext: %s; correct the resource or cleanup root, then retry", ownErr, ownershipNextCommand(state, resource))
}

func ownershipNextCommand(state string, resource proc.Resource) string {
	switch resource.Kind {
	case proc.ResourcePath:
		return "ls -ld " + shellQuote(resource.ID) + " " + shellQuote(filepath.Join(state, "cleanup-roots.json"))
	case proc.ResourceCompose:
		args := append([]string(nil), resource.ComposePrefix...)
		args = append(args, "-p", resource.ID)
		for _, file := range resource.Files {
			args = append(args, "-f", file)
		}
		args = append(args, "config", "--format", "json")
		return shellJoin(args)
	case proc.ResourceContainer:
		return "docker inspect " + shellQuote(resource.ID)
	case proc.ResourceNetwork:
		return "docker network inspect " + shellQuote(resource.ID)
	case proc.ResourceVolume:
		return "docker volume inspect " + shellQuote(resource.ID)
	default:
		return "kb-runner own --help"
	}
}

func gcCommand(args []string, output io.Writer) (int, error) {
	fs := flag.NewFlagSet("gc", flag.ContinueOnError)
	state := fs.String("state", "", "directory containing run records")
	if err := fs.Parse(args); err != nil {
		return 2, err
	}
	if *state == "" {
		return 2, errors.New("kb-runner gc: --state is required; next: kb-runner gc --help")
	}
	outcomes, err := proc.GarbageCollect(*state)
	if err != nil {
		return 1, fmt.Errorf("kb-runner gc: cleanup failed: %w\nnext: inspect remaining entries with kb-runner ps --state %s --leftovers", err, shellQuote(*state))
	}
	for _, outcome := range outcomes {
		if _, err := fmt.Fprintf(output, "%s\t%s\t%s", outcome.Resource.Kind, outcome.Resource.ID, outcome.Outcome); err != nil {
			return 1, err
		}
		if outcome.Error != "" {
			if _, err := fmt.Fprintf(output, "\t%s", outcome.Error); err != nil {
				return 1, err
			}
		}
		if _, err := fmt.Fprintln(output); err != nil {
			return 1, err
		}
	}
	return 0, nil
}
