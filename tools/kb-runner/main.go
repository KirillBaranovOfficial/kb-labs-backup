//go:build darwin || linux

package main

import (
	"context"
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
		return 2, errors.New("usage: kb-runner <run|gate|ps|wait|events|proc> [flags]")
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
	case "proc":
		return procCommand(args[1:], stdout)
	case "quota":
		return quotaCommand(args[1:], stdout, stderr)
	default:
		return 2, fmt.Errorf("kb-runner: unknown command %q; next: kb-runner --help", args[0])
	}
}

func procCommand(args []string, output io.Writer) (int, error) {
	if len(args) == 2 && args[0] == "inventory" && args[1] == "--json" {
		entries, err := proc.ProcessInventory()
		if err != nil {
			return 1, fmt.Errorf("kb-runner proc inventory: %w\nnext: run `kb-runner proc inventory --json` from a session allowed to inspect the host process table", err)
		}
		if err := json.NewEncoder(output).Encode(entries); err != nil {
			return 1, fmt.Errorf("kb-runner proc inventory: write JSON: %w", err)
		}
		return 0, nil
	}
	if len(args) != 2 || args[0] != "identity" {
		return 2, errors.New("kb-runner proc: usage: kb-runner proc identity <pid> or proc inventory --json; next: run `kb-runner proc identity <pid>` with a live PID or `kb-runner proc inventory --json`")
	}
	pid, err := strconv.Atoi(args[1])
	if err != nil || pid <= 0 {
		return 2, fmt.Errorf("kb-runner proc identity: PID %q must be a positive decimal process ID; next: run `kb-runner proc identity <pid>` with a live PID", args[1])
	}
	identity, alive, err := proc.ProcessIdentity(pid)
	if err != nil {
		return 1, fmt.Errorf("kb-runner proc identity: cannot inspect PID %d: %w\nnext: run `kb-runner proc identity %d` as the process owner", pid, err, pid)
	}
	if !alive {
		return 3, fmt.Errorf("kb-runner proc identity: PID %d is not live\nnext: pass a currently live PID to `kb-runner proc identity <pid>`", pid)
	}
	if _, err := fmt.Fprintln(output, identity); err != nil {
		return 1, fmt.Errorf("kb-runner proc identity: write identity for PID %d: %w", pid, err)
	}
	return 0, nil
}

func runCommand(args []string, stdout, stderr io.Writer) (int, error) {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	tag := fs.String("tag", "", "run label")
	latestTag := fs.String("latest-tag", "", "stable stream key used to resolve the newest run record")
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
	costKey := fs.String("cost-key", "", "key used to learn completed run resource costs")
	memory := fs.String("mem", "", "declared memory reservation, for example 2G")
	cpus := fs.String("cpus", "", "minimum or minimum..maximum CPU cores")
	disk := fs.String("disk", "", "declared disk growth reservation, for example 3G")
	var envVars stringListFlag
	fs.Var(&envVars, "env", "add environment entry KEY=VALUE (repeatable)")
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
	var err error
	if *minFree == "" {
		minFreeBytes = 0
	} else {
		minFreeBytes, err = strconv.ParseUint(*minFree, 10, 64)
	}
	if err != nil {
		return 2, fmt.Errorf("kb-runner run: invalid --min-free: %w; next: pass an integer byte count, for example --min-free 12000000000", err)
	}
	memBytes, err := proc.ParseResourceSize(*memory)
	if *memory == "" {
		memBytes = 0
		err = nil
	}
	if err != nil {
		return 2, fmt.Errorf("kb-runner run: invalid --mem: %w; next: pass a size such as --mem 2G", err)
	}
	diskBytes, err := proc.ParseResourceSize(*disk)
	if *disk == "" {
		diskBytes = 0
		err = nil
	}
	if err != nil {
		return 2, fmt.Errorf("kb-runner run: invalid --disk: %w; next: pass a size such as --disk 3G", err)
	}
	cpuMin, cpuMax, err := parseCPURange(*cpus)
	if err != nil {
		return 2, fmt.Errorf("kb-runner run: invalid --cpus: %w; next: pass --cpus N or --cpus MIN..MAX", err)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	helper, err := os.Executable()
	if err != nil {
		return 1, fmt.Errorf("kb-runner run: locate keeper executable: %w", err)
	}
	run, err := proc.Start(ctx, proc.Config{HelperPath: helper, StateDir: *state, Tag: *tag, LatestTag: *latestTag, Stall: *stall, StdoutPath: *stdoutPath, StderrPath: *stderrPath, StdinPath: *stdinPath, SlotGroup: *slotGroup, Slots: *slots, WaitGate: *waitGate, HoldGate: *holdGate, MinFree: minFreeBytes, CostKey: *costKey, MemoryBytes: memBytes, CPUsMin: cpuMin, CPUsMax: cpuMax, DiskBytes: diskBytes, Env: envVars, Command: command})
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

func parseCPURange(value string) (int, int, error) {
	if value == "" {
		return 0, 0, nil
	}
	minText, maxText, hasRange := strings.Cut(value, "..")
	minimum, err := strconv.Atoi(minText)
	if err != nil || minimum < 1 {
		return 0, 0, errors.New("CPU count must be positive")
	}
	maximum := minimum
	if hasRange {
		maximum, err = strconv.Atoi(maxText)
		if err != nil || maximum < minimum {
			return 0, 0, errors.New("CPU range maximum must be at least its minimum")
		}
	}
	return minimum, maximum, nil
}

func quotaCommand(args []string, stdout, stderr io.Writer) (int, error) {
	if len(args) == 0 {
		return 2, errors.New("kb-runner quota: usage: quota <show|set> --state DIR; next: kb-runner quota show --help")
	}
	action := args[0]
	fs := flag.NewFlagSet("quota "+action, flag.ContinueOnError)
	fs.SetOutput(stderr)
	state := fs.String("state", "", "state directory owning the quota")
	memory := fs.String("memory", "", "memory quota size or percentage")
	cpus := fs.String("cpus", "", "CPU quota")
	minFree := fs.String("min-free", "", "minimum free disk space")
	if err := fs.Parse(args[1:]); err != nil {
		return 2, err
	}
	if *state == "" {
		return 2, errors.New("kb-runner quota: --state is required; next: pass --state DIR")
	}
	switch action {
	case "set":
		if *memory == "" && *cpus == "" && *minFree == "" {
			return 2, errors.New("kb-runner quota set: provide --memory, --cpus, or --min-free; next: kb-runner quota set --help")
		}
		if err := proc.SetQuota(*state, *memory, *cpus, *minFree); err != nil {
			return 1, fmt.Errorf("kb-runner quota set: %w; next: check state directory access with ls -ld %s", err, shellQuote(*state))
		}
		return quotaShow(*state, stdout)
	case "show":
		return quotaShow(*state, stdout)
	default:
		return 2, fmt.Errorf("kb-runner quota: unknown action %q; next: kb-runner quota show --help", action)
	}
}

func quotaShow(state string, stdout io.Writer) (int, error) {
	quota, err := proc.ShowQuota(state)
	if err != nil {
		return 1, fmt.Errorf("kb-runner quota show: %w; next: check state directory access with ls -ld %s", err, shellQuote(state))
	}
	memSource, cpuSource, diskSource := "default", "default", "default"
	if quota.MemorySet {
		memSource = "set"
	}
	if quota.CPUsSet {
		cpuSource = "set"
	}
	if quota.MinFreeSet {
		diskSource = "set"
	}
	if _, err := fmt.Fprintf(stdout, "memory=%s (%s)\ncpus=%d (%s)\nmin-free=%s (%s)\n", proc.FormatResourceBytes(quota.MemoryBytes), memSource, quota.CPUs, cpuSource, proc.FormatResourceBytes(quota.MinFree), diskSource); err != nil {
		return 1, err
	}
	for signal, failure := range quota.Resources.Unavailable {
		if _, err := fmt.Fprintf(stdout, "signal %s unavailable: %s\n", signal, failure); err != nil {
			return 1, err
		}
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
	if _, err := fmt.Fprintln(output, "TAG\tPID\tSTATUS\tWAIT\tELAPSED\tTREE\tRESOURCES\tCOMMAND"); err != nil {
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
		if run.Status == proc.StatusBlocked && wait != "" {
			wait = "blocked: " + wait
		}
		resources := fmt.Sprintf("mem=%s cpus=%d..%d grant=%d disk=%s", proc.FormatResourceBytes(run.MemoryBytes), run.CPUsMin, run.CPUsMax, run.GrantedCPUs, proc.FormatResourceBytes(run.DiskBytes))
		if run.LimitsAdvisory != "" {
			resources += " limits advisory: " + run.LimitsAdvisory
		}
		if run.OverBudget {
			resources += " over-budget"
		}
		if run.ResourceNotice != "" {
			resources += " signals: " + run.ResourceNotice
		}
		if _, err := fmt.Fprintf(output, "%s\t%d\t%s\t%s\telapsed=%dms\ttree=%d\t%s\t%s\n", run.Tag, run.PID, strings.ToUpper(string(run.Status)), wait, run.ElapsedMS, len(run.Tree), resources, strings.Join(run.Command, " ")); err != nil {
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
	if len(args) >= 2 && args[0] == "status" && args[1] != "" {
		fs := flag.NewFlagSet("gate status", flag.ContinueOnError)
		fs.SetOutput(stderr)
		state := fs.String("state", "", "directory containing gate records")
		if err := fs.Parse(args[2:]); err != nil {
			return 2, err
		}
		if *state == "" {
			return 2, errors.New("kb-runner gate status: --state is required; next: kb-runner gate status --help")
		}
		gate, held, err := proc.GateStatus(*state, args[1])
		if err != nil {
			return 1, fmt.Errorf("kb-runner gate status: %w; next: kb-runner ps --state %s", err, shellQuote(*state))
		}
		if held {
			_, err = fmt.Fprintf(stdout, "held\t%s\tpid=%d\tidentity=%s\n", gate.Tag, gate.PID, gate.Identity)
		} else {
			_, err = fmt.Fprintln(stdout, "free")
		}
		if err != nil {
			return 1, fmt.Errorf("kb-runner gate status: write output: %w", err)
		}
		return 0, nil
	}
	if len(args) < 2 || args[0] != "hold" || args[1] == "" {
		return 2, errors.New("kb-runner gate: usage: kb-runner gate status <name> --state <dir> or gate hold <name> [run flags] -- <command>; next: kb-runner gate --help")
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
	latestTag := fs.String("latest-tag", "", "stable stream key whose newest run record to await")
	state := fs.String("state", "", "directory for run records")
	if err := fs.Parse(args); err != nil {
		return 2, err
	}
	if (*tag == "") == (*latestTag == "") || *state == "" {
		return 2, errors.New("kb-runner wait: provide exactly one of --tag or --latest-tag, and --state; next: kb-runner wait --help")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	var run proc.RunInfo
	var err error
	if *latestTag != "" {
		run, err = proc.WaitLatestTag(ctx, *state, *latestTag)
	} else {
		run, err = proc.WaitTag(ctx, *state, *tag)
	}
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
