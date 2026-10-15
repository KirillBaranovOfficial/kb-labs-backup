package proc

import (
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestParseProcStatUsesFinalCommDelimiter(t *testing.T) {
	data := "42 (name ) (with ) delimiters) S 7 8 9 10 11 12 13 14 15 16 17 18 19 20 21 22 23 24 987654\n"
	got, err := parseProcStat(data)
	if err != nil {
		t.Fatal(err)
	}
	if got.PID != 42 || got.Comm != "name ) (with ) delimiters" || got.State != 'S' || got.PPID != 7 || got.StartTicks != 987654 || got.Identity == "" {
		t.Fatalf("parsed stat = %+v", got)
	}
}

func TestParseProcBootTimeAndStartTicks(t *testing.T) {
	boot, err := parseProcBootTime("cpu 1 2 3 4\nbtime 1700000000\nprocesses 9\n")
	if err != nil {
		t.Fatal(err)
	}
	got := procStartTime(boot, 1250, 250)
	want := time.Unix(1700000005, 0)
	if !got.Equal(want) {
		t.Fatalf("start time = %s, want %s", got, want)
	}
}

// case: S-237.3
func TestRealChildSnapshotHasStableIdentityAndWallStart(t *testing.T) {
	before := time.Now()
	cmd := exec.Command("/bin/sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	deadline := time.Now().Add(3 * time.Second)
	var snapshots []Process
	var err error
	for time.Now().Before(deadline) {
		snapshots, err = ProcessTree(os.Getpid(), "")
		if processByPID(snapshots, cmd.Process.Pid) != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	first := processByPID(snapshots, cmd.Process.Pid)
	if first == nil {
		t.Fatalf("child snapshot = %+v", snapshots)
	}
	if err != nil {
		t.Logf("snapshot retained a partial table and reported an unrelated observation gap: %v", err)
	}
	if first.Identity == "" || first.ElapsedMS < 0 || first.StartedAt.Before(before.Add(-time.Second)) || first.StartedAt.After(time.Now().Add(time.Second)) {
		t.Fatalf("invalid child identity/time: %+v, started after %s", *first, before)
	}
	time.Sleep(20 * time.Millisecond)
	again, err := ProcessTree(os.Getpid(), "")
	second := processByPID(again, cmd.Process.Pid)
	if second == nil || second.Identity != first.Identity {
		t.Fatalf("identity changed across snapshots: first=%+v second=%+v error=%v", *first, again, err)
	}
	if err != nil {
		t.Logf("second snapshot retained the child and reported an unrelated observation gap: %v", err)
	}
}

func processByPID(processes []Process, pid int) *Process {
	for i := range processes {
		if processes[i].PID == pid {
			return &processes[i]
		}
	}
	return nil
}
