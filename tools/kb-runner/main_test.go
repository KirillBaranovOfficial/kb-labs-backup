package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/kb-labs/clikit/proc"
)

// case: S-237.15
func TestQuotaShowSetUsesStateDirectoryValues(t *testing.T) {
	state := t.TempDir()
	var output bytes.Buffer
	if code, err := execute([]string{"quota", "show", "--state", state}, &output, &output); code != 0 || err != nil {
		t.Fatalf("quota show default: code=%d err=%v output=%s", code, err, output.String())
	}
	if !strings.Contains(output.String(), "memory=") || !strings.Contains(output.String(), "(default)") {
		t.Fatalf("default quota output = %s", output.String())
	}
	output.Reset()
	if code, err := execute([]string{"quota", "set", "--state", state, "--memory", "12G", "--cpus", "4", "--min-free", "2G"}, &output, &output); code != 0 || err != nil {
		t.Fatalf("quota set: code=%d err=%v output=%s", code, err, output.String())
	}
	for _, want := range []string{"memory=12.0G (set)", "cpus=4 (set)", "min-free=2.0G (set)"} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("quota output missing %q: %s", want, output.String())
		}
	}
}

// case: S-237.22
func TestElasticCPUGrantReachesChildEnvironmentAndPS(t *testing.T) {
	state := t.TempDir()
	if err := proc.SetQuota(state, "8G", "2", ""); err != nil {
		t.Fatal(err)
	}
	runner := filepath.Join(state, "kb-runner")
	build := exec.Command("go", "build", "-o", runner, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build kb-runner: %v\n%s", err, output)
	}
	stdout := filepath.Join(state, "child.out")
	command := exec.Command(runner, "run", "--tag", "elastic", "--state", state, "--stall", "1h", "--stdout", stdout, "--stderr", filepath.Join(state, "child.err"), "--cost-key", "elastic", "--mem", "1M", "--cpus", "1..2", "--", "/bin/sh", "-c", "printf '%s %s' \"$KB_RUNNER_CPUS\" \"$KB_RUNNER_MEM_BYTES\"")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("run elastic command: %v\n%s", err, output)
	}
	data, err := os.ReadFile(stdout)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "2 1048576" {
		t.Fatalf("resource env = %q", data)
	}
	var ps bytes.Buffer
	if code, err := execute([]string{"ps", "--state", state}, &ps, &ps); code != 0 || err != nil {
		t.Fatalf("ps: code=%d err=%v output=%s", code, err, ps.String())
	}
	if !strings.Contains(ps.String(), "grant=2") || !strings.Contains(ps.String(), "mem=1.0M") {
		t.Fatalf("ps omitted resource grant: %s", ps.String())
	}
}

// case: S-237.12
func TestPSIncludesEachRunTreeAndElapsed(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("process supervision supports Linux and macOS")
	}
	state := t.TempDir()
	runner := filepath.Join(state, "kb-runner")
	build := exec.Command("go", "build", "-o", runner, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build kb-runner: %v\n%s", err, output)
	}
	childPIDPath := filepath.Join(state, "child.pid")
	command := exec.Command(runner, "run", "--tag", "ps-check", "--state", state, "--stall", "1h", "--stdout", filepath.Join(state, "out"), "--stderr", filepath.Join(state, "err"), "--", "/bin/sh", "-c", `echo $$ > "$1"; exec /bin/sleep 30`, "sh", childPIDPath)
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	finished := false
	waited := false
	t.Cleanup(func() {
		if !finished && command.Process != nil {
			if data, err := os.ReadFile(childPIDPath); err == nil {
				if pid, parseErr := strconv.Atoi(strings.TrimSpace(string(data))); parseErr == nil {
					_ = syscall.Kill(pid, syscall.SIGKILL)
				}
			}
			_ = command.Process.Signal(syscall.SIGTERM)
		}
		if !waited {
			if err := command.Wait(); err != nil && !finished {
				t.Logf("wait for test runner cleanup: %v", err)
			}
			waited = true
		}
		if !finished && !waitForTeardownEvent(state, 5*time.Second) {
			t.Log("runner teardown event was not recorded before temporary state cleanup")
		}
	})
	deadline := time.Now().Add(8 * time.Second)
	var listing []byte
	for time.Now().Before(deadline) {
		listing, _ = exec.Command(runner, "ps", "--state", state).CombinedOutput()
		if strings.Contains(string(listing), "ps-check\t") && strings.Contains(string(listing), "RUNNING") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	var runRow, childRow string
	for _, line := range strings.Split(string(listing), "\n") {
		if strings.HasPrefix(line, "ps-check\t") {
			runRow = line
		} else if strings.HasPrefix(line, "  └─ ") {
			childRow = line
		}
	}
	fields := strings.Split(runRow, "\t")
	if len(fields) < 7 || !strings.HasPrefix(fields[4], "elapsed=") || !strings.HasPrefix(fields[5], "tree=") {
		t.Fatalf("ps run row omitted its elapsed or tree fields: %s", listing)
	}
	treeCount, err := strconv.Atoi(strings.TrimPrefix(fields[5], "tree="))
	if err != nil || treeCount == 0 {
		t.Fatalf("ps run row omitted a nonempty tree inventory: %s", listing)
	}
	childCount := 0
	childHasSleep := false
	for _, line := range strings.Split(string(listing), "\n") {
		if strings.HasPrefix(line, "  └─ ") {
			childCount++
			childHasSleep = childHasSleep || strings.Contains(line, "sleep")
			if !strings.Contains(line, " elapsed=") {
				t.Fatalf("ps child row omitted elapsed: %s", line)
			}
		}
	}
	if childCount != treeCount || !childHasSleep || childRow == "" {
		t.Fatalf("ps child row omitted command or elapsed field: %s", listing)
	}
	_ = command.Process.Signal(syscall.SIGTERM)
	_ = command.Wait()
	waited = true
	if !waitForTeardownEvent(state, 5*time.Second) {
		t.Fatal("runner teardown event was not recorded")
	}
	finished = true
}

func waitForTeardownEvent(state string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		file, err := os.Open(filepath.Join(state, "events.jsonl"))
		if err == nil {
			var event proc.Event
			scanner := bufio.NewScanner(file)
			for scanner.Scan() {
				if json.Unmarshal(scanner.Bytes(), &event) == nil && event.Type == proc.EventTeardown {
					_ = file.Close()
					return true
				}
			}
			_ = file.Close()
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}
