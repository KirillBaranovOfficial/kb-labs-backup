//go:build darwin && cgo

package proc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// case: S-237.1
func TestDetachedDarwinShellAndSleepExposeMarkerOrUnverified(t *testing.T) {
	dir := t.TempDir()
	helper := filepath.Join(dir, "proc-helper")
	_, source, _, ok := runtimeCaller(0)
	if !ok {
		t.Fatal("locate proc package")
	}
	build := exec.Command("go", "build", "-o", helper, "./testhelper")
	build.Dir = filepath.Dir(source)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build proc helper: %v\n%s", err, output)
	}
	state := filepath.Join(dir, "state")
	pidFile := filepath.Join(dir, "pids")
	var pids []int
	run, err := Start(context.Background(), Config{
		HelperPath: helper, StateDir: state, Tag: "darwin-procargs", Stall: 250 * time.Millisecond,
		StdoutPath: filepath.Join(dir, "stdout"), StderrPath: filepath.Join(dir, "stderr"),
		Command: []string{helper, "probe-detached", pidFile, "4"},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = run.Kill()
		_ = run.Wait()
		for _, pid := range pids {
			if _, alive := processIdentityForTest(t, pid); alive {
				if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil {
					t.Logf("cleanup test process group %d: %v", pid, err)
				}
			}
		}
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			alive := false
			for _, pid := range pids {
				if _, ok := processIdentityForTest(t, pid); ok {
					alive = true
				}
			}
			if !alive {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Logf("test-owned detached process identities still exist after bounded cleanup: %v", pids)
	})
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		data, readErr := os.ReadFile(pidFile)
		if readErr == nil {
			lines := strings.Fields(string(data))
			if len(lines) == 2 {
				for _, line := range lines {
					pid, parseErr := strconv.Atoi(line)
					if parseErr != nil {
						t.Fatal(parseErr)
					}
					pids = append(pids, pid)
				}
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(pids) != 2 {
		t.Fatalf("detached shell and sleep PIDs were not recorded: %v", pids)
	}
	runner := filepath.Join(dir, "kb-runner")
	buildRunner := exec.Command("go", "build", "-o", runner, ".")
	buildRunner.Dir = filepath.Join(filepath.Dir(source), "..", "..", "kb-runner")
	if output, err := buildRunner.CombinedOutput(); err != nil {
		t.Fatalf("build kb-runner: %v\n%s", err, output)
	}
	for _, pid := range pids {
		raw, queryErr := unix.SysctlRaw("kern.procargs2", pid)
		markerFound := queryErr == nil && bytes.Contains(raw, []byte(runEnv+"="+run.RunID))
		environmentFound := queryErr == nil && procargs2HasEnvironment(raw)
		t.Logf("pid=%d kern.procargs2_error=%v environment=%t marker=%t", pid, queryErr, environmentFound, markerFound)
		tree, treeErr := ProcessTree(run.PID, run.RunID)
		if treeErr != nil {
			t.Logf("process table had an unrelated observation gap while sampling target: %v", treeErr)
		}
		var found *Process
		for i := range tree {
			if tree[i].PID == pid {
				found = &tree[i]
				break
			}
		}
		if found == nil {
			t.Fatalf("detached same-user pid %d was omitted from tree", pid)
		}
		if queryErr == nil && environmentFound && !markerFound {
			t.Fatalf("kern.procargs2 returned an environment without inherited run marker for pid %d: %s", pid, fmt.Sprint(tree))
		}
		if (queryErr != nil || !environmentFound) && !found.Unverified {
			t.Fatalf("pid %d with unavailable environment was not marked unverified: %+v", pid, *found)
		}
		if !strings.Contains(found.Command, "sh") && !strings.Contains(found.Command, "sleep") {
			t.Fatalf("unexpected detached process command for pid %d: %+v", pid, *found)
		}
		if markerFound && found.Unverified {
			t.Fatalf("pid %d exposes the marker but was classified unverified: %+v", pid, *found)
		}
		output, err := exec.Command(runner, "ps", "--state", state).CombinedOutput()
		if err != nil {
			t.Fatalf("kb-runner ps failed: %v\n%s", err, output)
		}
		listed := false
		for _, line := range strings.Split(string(output), "\n") {
			if strings.Contains(line, strconv.Itoa(pid)) && strings.Contains(line, "unverified") {
				listed = true
			}
		}
		if !listed {
			t.Fatalf("kb-runner ps did not label detached pid %d unverified: %s", pid, output)
		}
	}
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		data, readErr := os.ReadFile(filepath.Join(state, "events.jsonl"))
		if readErr == nil {
			for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
				var event Event
				if json.Unmarshal([]byte(line), &event) != nil || event.Type != "stall" {
					continue
				}
				foundUnverifiedLeaf := false
				for _, leaf := range event.Leaves {
					if leaf.Unverified && strings.Contains(leaf.Command, "sleep") {
						foundUnverifiedLeaf = true
					}
				}
				if !foundUnverifiedLeaf {
					t.Fatalf("stall event omitted unverified detached sleep leaves: %+v", event.Leaves)
				}
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("no stall event reported the detached unverified processes")
}

func processIdentityForTest(t *testing.T, pid int) (string, bool) {
	t.Helper()
	identity, alive, err := ProcessIdentity(pid)
	if err != nil {
		t.Errorf("inspect process %d identity: %v", pid, err)
		return "", false
	}
	return identity, alive
}

func runtimeCaller(skip int) (uintptr, string, int, bool) { return runtime.Caller(skip + 1) }
