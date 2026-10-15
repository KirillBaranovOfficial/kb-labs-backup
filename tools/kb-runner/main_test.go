package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

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
	command := exec.Command(runner, "run", "--tag", "ps-check", "--state", state, "--stall", "1h", "--stdout", filepath.Join(state, "out"), "--stderr", filepath.Join(state, "err"), "--", "/bin/sleep", "30")
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	finished := false
	waited := false
	t.Cleanup(func() {
		if !finished && command.Process != nil {
			_ = command.Process.Kill()
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				output, err := exec.Command(runner, "ps", "--state", state).CombinedOutput()
				if err == nil && strings.Contains(string(output), "DEAD") {
					break
				}
				time.Sleep(20 * time.Millisecond)
			}
		}
		if !waited {
			if err := command.Wait(); err != nil && !finished {
				t.Logf("wait for test runner cleanup: %v", err)
			}
			waited = true
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
	_ = command.Process.Kill()
	_ = command.Wait()
	waited = true
	finished = true
}
