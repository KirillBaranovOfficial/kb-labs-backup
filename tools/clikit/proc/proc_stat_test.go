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
	for time.Now().Before(deadline) {
		snapshots = ProcessTree(os.Getpid(), "")
		if len(snapshots) == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(snapshots) != 1 {
		t.Fatalf("child snapshot = %+v", snapshots)
	}
	first := snapshots[0]
	if first.Identity == "" || first.ElapsedMS < 0 || first.StartedAt.Before(before.Add(-time.Second)) || first.StartedAt.After(time.Now().Add(time.Second)) {
		t.Fatalf("invalid child identity/time: %+v, started after %s", first, before)
	}
	time.Sleep(20 * time.Millisecond)
	again := ProcessTree(os.Getpid(), "")
	if len(again) != 1 || again[0].Identity != first.Identity {
		t.Fatalf("identity changed across snapshots: first=%+v second=%+v", first, again)
	}
}
