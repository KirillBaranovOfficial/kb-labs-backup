//go:build darwin || linux

package proc

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const keeperRecordFailureTestEnv = "KB_CLIKIT_TEST_KEEPER_RECORD_FAILURE"

func TestKeeperScratchIsRemovedWhenInitialRecordWriteFails(t *testing.T) {
	if os.Getenv(keeperRecordFailureTestEnv) == "1" {
		os.Exit(KeeperMain())
	}

	state := t.TempDir()
	runID, err := newID()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(state, "runs"), 0o700); err != nil {
		t.Fatal(err)
	}
	queued := record{RunID: runID, Tag: "record-failure", StateDir: state, Status: StatusQueued}
	if err := writeRecordAtomic(queued); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(state, "runs", runID+".json.tmp"), 0o700); err != nil {
		t.Fatal(err)
	}
	scratch, err := runScratchPath(runID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(scratch); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(scratch); err != nil {
			t.Errorf("remove test scratch %s: %v", scratch, err)
		}
	})

	identity, ok, identityErr := ProcessIdentity(os.Getpid())
	if identityErr != nil {
		t.Fatal(identityErr)
	}
	if !ok {
		t.Fatal("cannot determine test process identity")
	}
	config, err := json.Marshal(keeperConfig{
		Config: Config{
			StateDir:   state,
			Tag:        "record-failure",
			Stall:      time.Hour,
			StdoutPath: filepath.Join(state, "stdout.log"),
			StderrPath: filepath.Join(state, "stderr.log"),
			Command:    []string{"/bin/true"},
		},
		RunID:          runID,
		ParentPID:      os.Getpid(),
		ParentIdentity: identity,
	})
	if err != nil {
		t.Fatal(err)
	}
	readReady, writeReady, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestKeeperScratchIsRemovedWhenInitialRecordWriteFails$")
	cmd.ExtraFiles = []*os.File{writeReady}
	cmd.Env = append(os.Environ(), keeperRecordFailureTestEnv+"=1", stateEnv+"="+base64.RawStdEncoding.EncodeToString(config), readyEnv+"=3")
	if err := cmd.Start(); err != nil {
		_ = readReady.Close()
		_ = writeReady.Close()
		t.Fatal(err)
	}
	waited := false
	t.Cleanup(func() {
		if waited || cmd.Process == nil {
			return
		}
		if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			t.Errorf("stop keeper record-failure helper: %v", err)
		}
		if err := cmd.Wait(); err != nil {
			t.Logf("reap keeper record-failure helper: %v", err)
		}
	})
	if err := writeReady.Close(); err != nil {
		t.Errorf("close keeper readiness writer: %v", err)
	}
	var ready readyMessage
	decodeErr := json.NewDecoder(readReady).Decode(&ready)
	closeErr := readReady.Close()
	waitErr := cmd.Wait()
	waited = true
	if decodeErr != nil {
		t.Fatalf("read keeper startup result: %v (wait: %v)", decodeErr, waitErr)
	}
	if ready.Error == "" {
		t.Fatal("keeper unexpectedly accepted obstructed initial record path")
	}
	if !strings.Contains(ready.Error, filepath.Join(state, "runs", runID+".json.tmp")) {
		t.Fatalf("keeper failed before the obstructed initial record write: %s", ready.Error)
	}
	if waitErr == nil {
		t.Fatalf("keeper exited successfully after record failure: %s", ready.Error)
	}
	if closeErr != nil {
		t.Errorf("close keeper readiness reader: %v", closeErr)
	}
	if _, err := os.Lstat(scratch); !os.IsNotExist(err) {
		t.Fatalf("scratch remains without a durable run record at %s: %v", scratch, err)
	}
}
