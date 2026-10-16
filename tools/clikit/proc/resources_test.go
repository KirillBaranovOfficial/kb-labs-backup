//go:build darwin || linux

package proc

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// case: S-237.43
func TestRegisterAndCleanupRunOwnedPath(t *testing.T) {
	state := t.TempDir()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(state, "runs"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(state, "cleanup-roots.json"), []byte("["+strconv.Quote(root)+"]"), 0o600); err != nil {
		t.Fatal(err)
	}
	run := record{RunID: "path-run", StateDir: state, Status: StatusRunning}
	if err := writeRecordAtomic(run); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "created")
	if err := CreateOwnedPath(state, run.RunID, path, OwnedDirectory); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "created-file")
	if err := CreateOwnedPath(state, run.RunID, file, OwnedFile); err != nil {
		t.Fatal(err)
	}
	entries, err := readResourceLedger(state, run.RunID)
	if err != nil || len(entries) != 2 || entries[0].ownerToken == "" || entries[1].ownerToken == "" {
		t.Fatalf("recorded path owner token = %+v, %v", entries, err)
	}
	outcomes := cleanupResources(state, run.RunID)
	if len(outcomes) != 2 || outcomes[0].Outcome != ResourceRemoved || outcomes[1].Outcome != ResourceRemoved {
		t.Fatalf("cleanup outcome = %+v", outcomes)
	}
	for _, created := range []string{path, file} {
		if _, err := os.Lstat(created); !os.IsNotExist(err) {
			t.Fatalf("registered path remains: %s: %v", created, err)
		}
	}
}

// case: S-237.44
func TestDefaultTempCleanupRootHandlesSystemAlias(t *testing.T) {
	state := t.TempDir()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(state, "runs"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeRecordAtomic(record{RunID: "default-temp-run", StateDir: state, Status: StatusRunning}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "created")
	if err := CreateOwnedPath(state, "default-temp-run", path, OwnedFile); err != nil {
		t.Fatal(err)
	}
	outcomes := cleanupResources(state, "default-temp-run")
	if len(outcomes) != 1 || outcomes[0].Outcome != ResourceRemoved {
		t.Fatalf("default temporary root cleanup = %+v", outcomes)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("owned path remains under default temporary root: %v", err)
	}
}

// case: S-237.44
func TestCreateOwnedPathRefusesRecreatedPathWithDifferentOwnerNonce(t *testing.T) {
	state := t.TempDir()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(state, "runs"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(state, "cleanup-roots.json"), []byte("[\""+root+"\"]"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeRecordAtomic(record{RunID: "nonce-run", StateDir: state, Status: StatusRunning}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "replace")
	if err := CreateOwnedPath(state, "nonce-run", path, OwnedFile); err != nil {
		t.Fatal(err)
	}
	entries, err := readResourceLedger(state, "nonce-run")
	if err != nil || len(entries) != 1 || !strings.HasPrefix(entries[0].ownerToken, "nonce-run:") {
		t.Fatalf("ownership token ledger = %+v, %v", entries, err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("replacement"), 0o600); err != nil {
		t.Fatal(err)
	}
	outcome := cleanupResource(state, entries[0])
	if outcome.Outcome != ResourceRefused {
		t.Fatalf("recreated path cleanup = %+v", outcome)
	}
	contents, err := os.ReadFile(path)
	if err != nil || string(contents) != "replacement" {
		t.Fatalf("recreated path changed by cleanup: %q, %v", contents, err)
	}
}

// case: S-237.44
func TestCleanupRequiresOwnerNonceWhenDeviceAndInodeMatch(t *testing.T) {
	state := t.TempDir()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(state, "runs"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(state, "cleanup-roots.json"), []byte("[\""+root+"\"]"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeRecordAtomic(record{RunID: "nonce-match-run", StateDir: state, Status: StatusRunning}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "replace")
	if err := CreateOwnedPath(state, "nonce-match-run", path, OwnedFile); err != nil {
		t.Fatal(err)
	}
	entries, err := readResourceLedger(state, "nonce-match-run")
	if err != nil || len(entries) != 1 {
		t.Fatalf("ownership ledger = %+v, %v", entries, err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("replacement"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	replacement, err := resourceIdentity(path, info)
	if err != nil {
		t.Fatal(err)
	}
	resource := entries[0]
	resource.Device, resource.Inode = replacement.Device, replacement.Inode
	outcome := cleanupResource(state, resource)
	if outcome.Outcome != ResourceRefused || !strings.Contains(outcome.Error, "owner") {
		t.Fatalf("same device/inode with a different owner nonce = %+v", outcome)
	}
	if _, err := os.Lstat(path); err != nil {
		t.Fatalf("replacement path was removed: %v", err)
	}
}

// case: S-237.44
func TestCreateOwnedPathRefusesUnsupportedUserXattr(t *testing.T) {
	state := t.TempDir()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(state, "runs"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(state, "cleanup-roots.json"), []byte("[\""+root+"\"]"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeRecordAtomic(record{RunID: "unsupported-xattr-run", StateDir: state, Status: StatusRunning}); err != nil {
		t.Fatal(err)
	}
	original := setPathOwnerXattr
	setPathOwnerXattr = func(string, []byte) error { return unix.EOPNOTSUPP }
	t.Cleanup(func() { setPathOwnerXattr = original })
	path := filepath.Join(root, "unsupported")
	err := CreateOwnedPath(state, "unsupported-xattr-run", path, OwnedFile)
	if err == nil || !strings.Contains(err.Error(), "filesystem") || !strings.Contains(err.Error(), "xattr") {
		t.Fatalf("unsupported xattr registration error = %v", err)
	}
	var unsupported *PathXattrUnsupportedError
	if !errors.As(err, &unsupported) || unsupported.Filesystem == "" {
		t.Fatalf("unsupported xattr error omitted filesystem identity: %#v", err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("unsupported xattr left a path behind: %v", err)
	}
}

// case: S-237.45
func TestComposeCleanupRunsDownThenRemovesLabeledContainersAndNetworks(t *testing.T) {
	var composeCalls, dockerCalls [][]string
	originalCompose, originalDocker := composeCommand, dockerCommand
	composeCommand = func(_ context.Context, prefix []string, args ...string) ([]byte, error) {
		composeCalls = append(composeCalls, append(append([]string(nil), prefix...), args...))
		return nil, nil
	}
	dockerCommand = func(_ context.Context, args ...string) ([]byte, error) {
		dockerCalls = append(dockerCalls, append([]string(nil), args...))
		joined := strings.Join(args, " ")
		switch {
		case strings.HasPrefix(joined, "ps -aq "):
			return []byte("container-id\n"), nil
		case strings.Contains(joined, "network ls -q"):
			return []byte("network-id\n"), nil
		case strings.Contains(joined, "volume ls -q"):
			return []byte("volume-id\n"), nil
		default:
			return nil, nil
		}
	}
	t.Cleanup(func() { composeCommand = originalCompose; dockerCommand = originalDocker })
	outcome := cleanupResource(t.TempDir(), Resource{Kind: ResourceCompose, ID: "throwaway-project", Files: []string{"compose.yml"}, ComposePrefix: []string{"docker-compose"}})
	if outcome.Outcome != ResourceRemoved {
		t.Fatalf("compose cleanup = %+v", outcome)
	}
	wantDown := []string{"docker-compose", "-p", "throwaway-project", "-f", "compose.yml", "down", "-v", "--remove-orphans", "--timeout", "10"}
	if len(composeCalls) != 1 || !reflect.DeepEqual(composeCalls[0], wantDown) {
		t.Fatalf("cleanup Compose command = %#v", composeCalls)
	}
	if !containsArgs(dockerCalls, "ps", "-aq", "--filter", "label=com.docker.compose.project=throwaway-project") || !containsArgs(dockerCalls, "network", "ls", "-q", "--filter", "label=com.docker.compose.project=throwaway-project") || !containsArgs(dockerCalls, "volume", "ls", "-q", "--filter", "label=com.docker.compose.project=throwaway-project") || !containsArgs(dockerCalls, "rm", "-f", "container-id") || !containsArgs(dockerCalls, "network", "rm", "network-id") || !containsArgs(dockerCalls, "volume", "rm", "volume-id") {
		t.Fatalf("cleanup omitted label sweep by id: %#v", dockerCalls)
	}
}

// case: S-237.45
func TestRegisterComposePersistsCallerPrefix(t *testing.T) {
	state := t.TempDir()
	if err := os.MkdirAll(filepath.Join(state, "runs"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeRecordAtomic(record{RunID: "compose-run", StateDir: state, Status: StatusRunning}); err != nil {
		t.Fatal(err)
	}
	var gotPrefix []string
	original := composeCommand
	composeCommand = func(_ context.Context, prefix []string, args ...string) ([]byte, error) {
		gotPrefix = append([]string(nil), prefix...)
		if !reflect.DeepEqual(args, []string{"-p", "project-name", "-f", "compose.yaml", "config", "--format", "json"}) {
			t.Fatalf("compose preflight args = %v", args)
		}
		return []byte(`{"volumes":{}}`), nil
	}
	t.Cleanup(func() { composeCommand = original })
	resource := Resource{Kind: ResourceCompose, ID: "project-name", Files: []string{"compose.yaml"}, ComposePrefix: []string{"docker-compose"}}
	if err := RegisterResource(state, "compose-run", resource); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotPrefix, resource.ComposePrefix) {
		t.Fatalf("compose preflight prefix = %v", gotPrefix)
	}
	entries, err := readResourceLedger(state, "compose-run")
	if err != nil || len(entries) != 1 || !reflect.DeepEqual(entries[0].ComposePrefix, resource.ComposePrefix) {
		t.Fatalf("stored Compose prefix = %+v, %v", entries, err)
	}
}

// case: S-237.45
func TestComposeCleanupSweepsLabelsAfterDownFailure(t *testing.T) {
	var calls [][]string
	originalCompose, originalDocker := composeCommand, dockerCommand
	composeCommand = func(context.Context, []string, ...string) ([]byte, error) {
		return []byte("compose down failed"), errors.New("compose exited 1")
	}
	dockerCommand = func(_ context.Context, args ...string) ([]byte, error) {
		calls = append(calls, append([]string(nil), args...))
		return nil, nil
	}
	t.Cleanup(func() { composeCommand = originalCompose; dockerCommand = originalDocker })
	resource := Resource{Kind: ResourceCompose, ID: "failed-down-project", Files: []string{"compose.yml"}, ComposePrefix: []string{"docker-compose"}}
	outcome := cleanupResource(t.TempDir(), resource)
	if outcome.Outcome != ResourceFailed {
		t.Fatalf("failed down outcome = %+v", outcome)
	}
	if len(outcome.Steps) != 2 || outcome.Steps[0].Name != "down" || outcome.Steps[0].Outcome != ResourceFailed || outcome.Steps[1].Name != "label-sweep" || outcome.Steps[1].Outcome != ResourceRemoved {
		t.Fatalf("Compose cleanup step outcomes = %+v", outcome.Steps)
	}
	for _, kind := range []string{"ps", "network", "volume"} {
		found := false
		for _, args := range calls {
			if len(args) >= 2 && args[0] == kind {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("label sweep for %s did not run after down failure: %v", kind, calls)
		}
	}
}

// case: S-237.49
func TestSlowComposeDownUsesStopTimeoutMarginAndReportsSweep(t *testing.T) {
	started := time.Now()
	var deadline time.Time
	downFinished := false
	var calls [][]string
	originalCompose, originalDocker := composeCommand, dockerCommand
	composeCommand = func(ctx context.Context, _ []string, _ ...string) ([]byte, error) {
		var ok bool
		deadline, ok = ctx.Deadline()
		if !ok {
			t.Fatal("Compose down has no cleanup deadline")
		}
		<-ctx.Done()
		downFinished = true
		return []byte("container still stopping"), ctx.Err()
	}
	dockerCommand = func(_ context.Context, args ...string) ([]byte, error) {
		if !downFinished {
			t.Fatalf("label sweep started before down finished: %v", args)
		}
		calls = append(calls, append([]string(nil), args...))
		return nil, nil
	}
	t.Cleanup(func() { composeCommand = originalCompose; dockerCommand = originalDocker })
	resource := Resource{Kind: ResourceCompose, ID: "slow-down-project", Files: []string{"compose.yml"}, ComposePrefix: []string{"docker-compose"}}
	outcome := cleanupResource(t.TempDir(), resource)
	if deadline.IsZero() {
		t.Fatal("Compose down did not run")
	}
	want := composeCleanupBound()
	remaining := deadline.Sub(started)
	if want <= composeStopTimeout || remaining < want-100*time.Millisecond || remaining > want+100*time.Millisecond {
		t.Fatalf("Compose deadline = %s, stop=%s cleanup-bound=%s", remaining, composeStopTimeout, want)
	}
	if outcome.Outcome != ResourceFailed || len(outcome.Steps) != 2 || outcome.Steps[0].Name != "down" || outcome.Steps[0].Outcome != ResourceFailed || outcome.Steps[1].Name != "label-sweep" || outcome.Steps[1].Outcome != ResourceRemoved {
		t.Fatalf("slow Compose cleanup outcomes = %+v", outcome)
	}
	if len(calls) != 3 {
		t.Fatalf("label sweep calls after stalled down = %v", calls)
	}
}

func containsArgs(calls [][]string, expected ...string) bool {
	for _, args := range calls {
		if reflect.DeepEqual(args, expected) {
			return true
		}
	}
	return false
}

// case: S-237.46
func TestRegisterRefusesPreexistingDockerVolume(t *testing.T) {
	state := t.TempDir()
	if err := os.MkdirAll(filepath.Join(state, "runs"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeRecordAtomic(record{RunID: "volume-run", StateDir: state, StartedAtTime: time.Now(), Status: StatusRunning}); err != nil {
		t.Fatal(err)
	}
	original := dockerCommand
	dockerCommand = func(_ context.Context, args ...string) ([]byte, error) {
		if !reflect.DeepEqual(args, []string{"volume", "ls", "-q", "--filter", "name=shared-volume"}) {
			t.Fatalf("unexpected Docker inspect: %v", args)
		}
		return []byte("shared-volume\n"), nil
	}
	t.Cleanup(func() { dockerCommand = original })
	err := RegisterResource(state, "volume-run", Resource{Kind: ResourceVolume, ID: "shared-volume"})
	if err == nil || !strings.Contains(err.Error(), "pre-existing") {
		t.Fatalf("pre-existing volume registration error = %v", err)
	}
}

// case: S-237.48
func TestFailedResourceCleanupMarksDirtyAndGCRetriesEntry(t *testing.T) {
	state := t.TempDir()
	if err := os.MkdirAll(filepath.Join(state, "runs"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeRecordAtomic(record{RunID: "dirty-run", StateDir: state, StartedAtTime: time.Now(), Status: StatusRunning}); err != nil {
		t.Fatal(err)
	}
	if err := RegisterResource(state, "dirty-run", Resource{Kind: ResourceContainer, ID: "container-id"}); err != nil {
		t.Fatal(err)
	}
	fail := true
	original := dockerCommand
	dockerCommand = func(_ context.Context, args ...string) ([]byte, error) {
		if fail {
			return []byte("no such host"), errors.New("docker exited 1")
		}
		if len(args) > 0 && args[0] == "ps" {
			return []byte("container-id\n"), nil
		}
		return nil, nil
	}
	t.Cleanup(func() { dockerCommand = original })
	outcomes := cleanupResources(state, "dirty-run")
	if len(outcomes) != 1 || outcomes[0].Outcome != ResourceFailed {
		t.Fatalf("first cleanup outcome = %+v", outcomes)
	}
	stored, err := readRecordLockedForTest(state, "dirty-run")
	if err != nil || stored.Status != StatusDirty {
		t.Fatalf("failed cleanup state = %+v, %v", stored, err)
	}
	leftovers, err := Leftovers(state)
	if err != nil || len(leftovers) != 1 || leftovers[0].Outcome != ResourceFailed {
		t.Fatalf("dirty leftovers = %+v, %v", leftovers, err)
	}
	fail = false
	outcomes, err = GarbageCollect(state)
	if err != nil || len(outcomes) != 1 || outcomes[0].Outcome != ResourceRemoved {
		t.Fatalf("gc outcome = %+v, %v", outcomes, err)
	}
	stored, err = readRecordLockedForTest(state, "dirty-run")
	if err != nil || stored.Status != StatusDead {
		t.Fatalf("cleaned run state = %+v, %v", stored, err)
	}
	leftovers, err = Leftovers(state)
	if err != nil || len(leftovers) != 0 {
		t.Fatalf("cleaned leftovers = %+v, %v", leftovers, err)
	}
}

// case: S-237.48
func TestWaitReturnsForLostRunWithFailedResourceCleanup(t *testing.T) {
	state := t.TempDir()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(state, "runs"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(state, "cleanup-roots.json"), []byte("["+strconv.Quote(root)+"]"), 0o600); err != nil {
		t.Fatal(err)
	}
	run := record{RunID: "lost-resource", Tag: "lost-resource", StateDir: state, PID: 2147483647, StartedAt: "stale", Status: StatusRunning}
	if err := writeRecordAtomic(run); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "owned")
	if err := CreateOwnedPath(state, run.RunID, path, OwnedFile); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("replacement"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := recordObservedKeeperLoss(state, run.RunID); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	got, err := WaitTag(ctx, state, run.Tag)
	if err != nil {
		t.Fatalf("wait for lost run with failed resource cleanup: %v", err)
	}
	if got.Status != StatusDirty || !strings.Contains(got.Error, path) {
		t.Fatalf("wait result = status %q error %q, want dirty cleanup failure for %q", got.Status, got.Error, path)
	}
}

// case: S-237.48
func TestWaitReturnsForLostRunWithFailedScratchCleanup(t *testing.T) {
	state := t.TempDir()
	if err := os.MkdirAll(filepath.Join(state, "runs", "lost-scratch", "scratch"), 0o700); err != nil {
		t.Fatal(err)
	}
	scratch := filepath.Join(state, "runs", "lost-scratch", "scratch")
	info, err := os.Lstat(scratch)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := resourceIdentity(scratch, info)
	if err != nil {
		t.Fatal(err)
	}
	run := record{RunID: "lost-scratch", Tag: "lost-scratch", StateDir: state, PID: 2147483647, StartedAt: "stale", Status: StatusRunning,
		Scratch: scratch, ScratchDevice: identity.Device, ScratchInode: identity.Inode}
	if err := writeRecordAtomic(run); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(scratch); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), scratch); err != nil {
		t.Fatal(err)
	}
	if err := recordObservedKeeperLoss(state, run.RunID); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	got, err := WaitTag(ctx, state, run.Tag)
	if err != nil {
		t.Fatalf("wait for lost run with failed scratch cleanup: %v", err)
	}
	if got.Status != StatusDirty || !strings.Contains(got.Error, scratch) {
		t.Fatalf("wait result = status %q error %q, want dirty scratch cleanup failure for %q", got.Status, got.Error, scratch)
	}
}

// case: S-237.48
func TestLedgerPersistenceFailureLeavesDirtyPendingEntryForGC(t *testing.T) {
	state := t.TempDir()
	if err := os.MkdirAll(filepath.Join(state, "runs"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeRecordAtomic(record{RunID: "ledger-write-run", StateDir: state, StartedAtTime: time.Now(), Status: StatusRunning}); err != nil {
		t.Fatal(err)
	}
	if err := RegisterResource(state, "ledger-write-run", Resource{Kind: ResourceContainer, ID: "container-id"}); err != nil {
		t.Fatal(err)
	}
	originalDocker := dockerCommand
	dockerCommand = func(_ context.Context, args ...string) ([]byte, error) {
		if len(args) > 0 && args[0] == "ps" {
			return []byte("container-id\n"), nil
		}
		return nil, nil
	}
	t.Cleanup(func() { dockerCommand = originalDocker })

	originalWrite := writeResourceLedgerFile
	writeFailed := false
	writeResourceLedgerFile = func(path string, data []byte, mode os.FileMode) error {
		if !writeFailed {
			writeFailed = true
			return errors.New("injected ledger persistence failure")
		}
		return originalWrite(path, data, mode)
	}
	t.Cleanup(func() { writeResourceLedgerFile = originalWrite })

	outcomes := cleanupResources(state, "ledger-write-run")
	if len(outcomes) != 1 || outcomes[0].Outcome != ResourceRemoved || !strings.Contains(outcomes[0].Error, "injected ledger persistence failure") {
		t.Fatalf("processed cleanup outcome = %+v", outcomes)
	}
	if err := completeRecord(state, "ledger-write-run", nil, "", time.Second, record{}); err != nil {
		t.Fatal(err)
	}
	stored, err := readRecordLockedForTest(state, "ledger-write-run")
	if err != nil || stored.Status != StatusDirty {
		t.Fatalf("run status after ledger failure = %s, %v", stored.Status, err)
	}
	leftovers, err := Leftovers(state)
	if err != nil || len(leftovers) != 1 || leftovers[0].Outcome != ResourcePending {
		t.Fatalf("pending leftovers = %+v, %v", leftovers, err)
	}

	outcomes, err = GarbageCollect(state)
	if err != nil || len(outcomes) != 1 || outcomes[0].Outcome != ResourceRemoved {
		t.Fatalf("gc retry outcomes = %+v, %v", outcomes, err)
	}
	stored, err = readRecordLockedForTest(state, "ledger-write-run")
	if err != nil || stored.Status != StatusDead {
		t.Fatalf("run status after gc = %s, %v", stored.Status, err)
	}
}

// case: S-237.48
func TestDockerTransportErrorContainingNoSuchIsFailedAndDirty(t *testing.T) {
	state := t.TempDir()
	if err := os.MkdirAll(filepath.Join(state, "runs"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeRecordAtomic(record{RunID: "docker-transport-run", StateDir: state, StartedAtTime: time.Now(), Status: StatusRunning}); err != nil {
		t.Fatal(err)
	}
	if err := RegisterResource(state, "docker-transport-run", Resource{Kind: ResourceContainer, ID: "container-id"}); err != nil {
		t.Fatal(err)
	}
	original := dockerCommand
	dockerCommand = func(context.Context, ...string) ([]byte, error) {
		return []byte("dial tcp: no such host"), errors.New("docker exited 1")
	}
	t.Cleanup(func() { dockerCommand = original })
	outcomes := cleanupResources(state, "docker-transport-run")
	if len(outcomes) != 1 || outcomes[0].Outcome != ResourceFailed {
		t.Fatalf("transport failure outcome = %+v", outcomes)
	}
	stored, err := readRecordLockedForTest(state, "docker-transport-run")
	if err != nil || stored.Status != StatusDirty {
		t.Fatalf("transport failure run status = %s, %v", stored.Status, err)
	}
}

// case: S-237.44
func TestCleanupOwnedDirectoryRemovesSymlinkWithoutFollowingItsTarget(t *testing.T) {
	state := t.TempDir()
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(state, "runs"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(state, "cleanup-roots.json"), []byte("["+strconv.Quote(root)+"]"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeRecordAtomic(record{RunID: "nested-link-run", StateDir: state, Status: StatusRunning}); err != nil {
		t.Fatal(err)
	}
	owned := filepath.Join(root, "owned")
	if err := CreateOwnedPath(state, "nested-link-run", owned, OwnedDirectory); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(outside, "sentinel")
	if err := os.WriteFile(sentinel, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(owned, "outside-link")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	entries, err := readResourceLedger(state, "nested-link-run")
	if err != nil || len(entries) != 1 {
		t.Fatalf("owned ledger = %+v, %v", entries, err)
	}
	outcome := cleanupResource(state, entries[0])
	if outcome.Outcome != ResourceRemoved {
		t.Fatalf("owned directory cleanup = %+v", outcome)
	}
	if _, err := os.Lstat(owned); !os.IsNotExist(err) {
		t.Fatalf("owned directory remains: %v", err)
	}
	if data, err := os.ReadFile(sentinel); err != nil || string(data) != "keep" {
		t.Fatalf("cleanup followed nested symlink: data=%q err=%v", data, err)
	}
}

// case: S-237.49
func TestHangingDockerCleanupIsStalledAndBounded(t *testing.T) {
	state := t.TempDir()
	if err := os.MkdirAll(filepath.Join(state, "runs"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeRecordAtomic(record{RunID: "hang-run", StateDir: state, StartedAtTime: time.Now(), Status: StatusRunning}); err != nil {
		t.Fatal(err)
	}
	if err := RegisterResource(state, "hang-run", Resource{Kind: ResourceContainer, ID: "hanging-container"}); err != nil {
		t.Fatal(err)
	}
	original := dockerCommand
	dockerCommand = func(ctx context.Context, args ...string) ([]byte, error) {
		if len(args) > 0 && args[0] == "ps" {
			return []byte("hanging-container\n"), nil
		}
		<-ctx.Done()
		return nil, ctx.Err()
	}
	t.Cleanup(func() { dockerCommand = original })
	started := time.Now()
	outcomes := cleanupResources(state, "hang-run")
	if time.Since(started) > composeCleanupBound()+2*time.Second {
		t.Fatalf("Docker cleanup exceeded its %s bound: %s", composeCleanupBound(), time.Since(started))
	}
	if len(outcomes) != 1 || outcomes[0].Outcome != ResourceFailed || !strings.Contains(outcomes[0].Error, "stalled Docker cleanup") {
		t.Fatalf("bounded cleanup outcome = %+v", outcomes)
	}
	var events []Event
	data, err := os.ReadFile(filepath.Join(state, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var event Event
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatal(err)
		}
		events = append(events, event)
	}
	if len(events) != 1 || events[0].Type != EventStall {
		t.Fatalf("hanging cleanup stall events = %+v", events)
	}
}

func readRecordLockedForTest(stateDir, runID string) (record, error) {
	var result record
	err := withStateLock(stateDir, func() error {
		var err error
		result, err = readRecordLocked(stateDir, runID)
		return err
	})
	return result, err
}

// case: S-237.44
func TestCreateOwnedPathRefusesOutsideRootPreexistingSymlinkAndInodeSwap(t *testing.T) {
	state := t.TempDir()
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(state, "runs"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(state, "cleanup-roots.json"), []byte("[\""+root+"\"]"), 0o600); err != nil {
		t.Fatal(err)
	}
	outsidePath := filepath.Join(outside, "outside")
	if err := os.WriteFile(outsidePath, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	preexisting := filepath.Join(root, "preexisting")
	if err := os.WriteFile(preexisting, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	run := record{RunID: "unsafe-run", StateDir: state, StartedAtTime: time.Now(), Status: StatusRunning}
	if err := writeRecordAtomic(run); err != nil {
		t.Fatal(err)
	}
	if err := CreateOwnedPath(state, run.RunID, outsidePath, OwnedFile); err == nil {
		t.Fatal("outside cleanup root was accepted")
	}
	if err := CreateOwnedPath(state, run.RunID, preexisting, OwnedFile); err == nil {
		t.Fatal("pre-existing path was accepted")
	}
	target := filepath.Join(root, "target")
	if err := os.WriteFile(target, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	symlink := filepath.Join(root, "link")
	if err := os.Symlink(target, symlink); err != nil {
		t.Fatal(err)
	}
	if err := CreateOwnedPath(state, run.RunID, symlink, OwnedFile); err == nil {
		t.Fatal("symlink was accepted")
	}
	path := filepath.Join(root, "swap")
	if err := CreateOwnedPath(state, run.RunID, path, OwnedFile); err != nil {
		t.Fatal(err)
	}
	linkSwap := filepath.Join(root, "link-swap")
	if err := CreateOwnedPath(state, run.RunID, linkSwap, OwnedFile); err != nil {
		t.Fatal(err)
	}
	linkTarget := filepath.Join(root, "must-survive")
	if err := os.WriteFile(linkTarget, []byte("target"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(linkSwap); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(linkTarget, linkSwap); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("two"), 0o600); err != nil {
		t.Fatal(err)
	}
	outcomes := cleanupResources(state, run.RunID)
	if len(outcomes) != 2 || outcomes[0].Outcome != ResourceRefused || outcomes[1].Outcome != ResourceRefused {
		t.Fatalf("inode swap outcome = %+v", outcomes)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("swapped path was removed: %v", err)
	}
	if content, err := os.ReadFile(linkTarget); err != nil || string(content) != "target" {
		t.Fatalf("symlink target was followed: %q, %v", content, err)
	}
}

// case: S-237.44
func TestCreateOwnedPathRefusesExistingPathAndSymlink(t *testing.T) {
	state := t.TempDir()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(state, "runs"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(state, "cleanup-roots.json"), []byte("[\""+root+"\"]"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeRecordAtomic(record{RunID: "exclusive-run", StateDir: state, Status: StatusRunning}); err != nil {
		t.Fatal(err)
	}
	preexisting := filepath.Join(root, "preexisting")
	if err := os.WriteFile(preexisting, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CreateOwnedPath(state, "exclusive-run", preexisting, OwnedFile); err == nil {
		t.Fatal("exclusive create accepted a pre-existing path")
	}
	target := filepath.Join(root, "target")
	if err := os.WriteFile(target, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	symlink := filepath.Join(root, "symlink")
	if err := os.Symlink(target, symlink); err != nil {
		t.Fatal(err)
	}
	if err := CreateOwnedPath(state, "exclusive-run", symlink, OwnedFile); err == nil {
		t.Fatal("exclusive create accepted a symlink")
	}
	if content, err := os.ReadFile(target); err != nil || string(content) != "keep" {
		t.Fatalf("target changed through symlink: %q, %v", content, err)
	}
	outside := t.TempDir()
	parentLink := filepath.Join(root, "parent-link")
	if err := os.Symlink(outside, parentLink); err != nil {
		t.Fatal(err)
	}
	if err := CreateOwnedPath(state, "exclusive-run", filepath.Join(parentLink, "new-file"), OwnedFile); err == nil {
		t.Fatal("exclusive create followed a symlinked parent")
	}
	if _, err := os.Lstat(filepath.Join(outside, "new-file")); !os.IsNotExist(err) {
		t.Fatalf("symlink parent created outside cleanup root: %v", err)
	}
}

// case: S-237.44
func TestCleanupOwnedPathRefusesSymlinkedParentPresentAtCleanup(t *testing.T) {
	state := t.TempDir()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(state, "runs"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(state, "cleanup-roots.json"), []byte("[\""+root+"\"]"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeRecordAtomic(record{RunID: "parent-swap-run", StateDir: state, Status: StatusRunning}); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(root, "parent")
	if err := os.Mkdir(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	owned := filepath.Join(parent, "owned")
	if err := CreateOwnedPath(state, "parent-swap-run", owned, OwnedFile); err != nil {
		t.Fatal(err)
	}
	relocated := filepath.Join(root, "relocated")
	if err := os.Rename(parent, relocated); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(relocated, parent); err != nil {
		t.Fatal(err)
	}
	outcomes := cleanupResources(state, "parent-swap-run")
	if len(outcomes) != 1 || outcomes[0].Outcome != ResourceRefused || !strings.Contains(outcomes[0].Error, "parent") {
		t.Fatalf("symlinked parent cleanup = %+v", outcomes)
	}
	if _, err := os.Lstat(filepath.Join(relocated, "owned")); err != nil {
		t.Fatalf("cleanup followed symlinked parent and removed the owned path: %v", err)
	}
}
