//go:build darwin

package proc

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
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

// case: S-237.21
func TestProcargs2ArgumentsPreserveOSArgumentBoundaries(t *testing.T) {
	want := []string{"/tmp/fixture", "unbalanced'quote", "", "first-line\nsecond-line", "one argument with spaces"}
	for padding := 0; padding <= 7; padding++ {
		t.Run("padding-"+strconv.Itoa(padding), func(t *testing.T) {
			data, environment := procargs2Fixture(want, padding, true)
			got, position, err := parseProcargs2(data)
			if err != nil {
				t.Fatalf("parse procargs2 argv: %v", err)
			}
			if !equalArguments(got, want) {
				t.Fatalf("procargs2 argv = %#v, want %#v", got, want)
			}
			if !bytes.Equal(data[position:], environment) {
				t.Fatalf("environment boundary at %d = %q, want %q", position, data[position:], environment)
			}
			if !procargs2HasEnvironment(data) {
				t.Fatal("parser did not detect environment after argv")
			}

			withoutEnvironment, _ := procargs2Fixture(want, padding, false)
			if procargs2HasEnvironment(withoutEnvironment) {
				t.Fatal("parser reported an environment when none followed argv")
			}
		})
	}
}

func procargs2Fixture(argv []string, padding int, includeEnvironment bool) ([]byte, []byte) {
	data := make([]byte, 4)
	binary.NativeEndian.PutUint32(data, uint32(len(argv)))
	data = append(data, []byte("/tmp/fixture")...)
	data = append(data, 0)
	data = append(data, bytes.Repeat([]byte{0}, padding)...)
	for _, argument := range argv {
		data = append(data, []byte(argument)...)
		data = append(data, 0)
	}
	var environment []byte
	if includeEnvironment {
		environment = []byte("X53_PROCARGS_FIXTURE=present\x00")
		data = append(data, environment...)
	}
	return data, environment
}

// Requires real kern.procargs2 process-table access; run outside the restricted sandbox.
// case: S-237.21
func TestDarwinProcargs2ReadsLiveChildArguments(t *testing.T) {
	if os.Getenv("X53_PROCARGS2_CHILD") == "1" {
		if _, err := io.Copy(io.Discard, os.Stdin); err != nil {
			t.Fatalf("wait for parent to release procargs2 child: %v", err)
		}
		return
	}

	childTest := "^TestDarwinProcargs2ReadsLiveChildArguments$"
	want := []string{os.Args[0], "-test.run=" + childTest, "--", "unbalanced'quote", "first-line\nsecond-line", "one argument with spaces", ""}
	child := exec.Command(want[0], want[1:]...)
	childInput, err := child.StdinPipe()
	if err != nil {
		t.Fatalf("create test-owned procargs2 child input: %v", err)
	}
	child.Env = append(os.Environ(), "X53_PROCARGS2_CHILD=1", "X53_PROCARGS2_ENV_BOUNDARY=visible")
	if err := child.Start(); err != nil {
		t.Fatalf("start test-owned procargs2 child: %v", err)
	}
	t.Cleanup(func() {
		if err := childInput.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
			t.Errorf("close test-owned procargs2 child input: %v", err)
		}
		if err := child.Wait(); err != nil {
			t.Errorf("wait for test-owned procargs2 child: %v", err)
		}
	})

	raw, err := unix.SysctlRaw("kern.procargs2", child.Process.Pid)
	if err != nil {
		t.Fatalf("read test-owned child kern.procargs2 bytes: %v", err)
	}
	got, environmentStart, err := parseProcargs2(raw)
	if err != nil {
		t.Fatalf("parse live child kern.procargs2 bytes: %v", err)
	}
	if !equalArguments(got, want) {
		t.Fatalf("live kern.procargs2 argv = %#v, want exact child argv %#v", got, want)
	}
	if !bytes.Contains(raw[environmentStart:], []byte("X53_PROCARGS2_ENV_BOUNDARY=visible")) {
		t.Fatalf("environment boundary %d does not contain the child's test environment", environmentStart)
	}
	if !procargs2HasEnvironment(raw) {
		t.Fatal("live procargs2 parser did not detect child environment")
	}
}

func equalArguments(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range got {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}

// case: S-237.13
func TestDarwinCommandNameStopsAtNULInKinfoRecord(t *testing.T) {
	var info unix.KinfoProc
	copy(info.Proc.P_comm[:], []byte{'s', 'l', 'e', 'e', 'p', 0, ' ', 'e', 'l', 'p', 'e', 'r'})
	if got := darwinCommandName(info.Proc.P_comm[:]); got != "sleep" {
		t.Fatalf("kinfo P_comm decoded as %q, want the bytes before its first NUL", got)
	}
}

func TestDarwinCommandNameParsesLiveKinfoRecord(t *testing.T) {
	info, err := unix.SysctlKinfoProc("kern.proc.pid", os.Getpid())
	if err != nil {
		t.Fatalf("read own kern.proc record: %v", err)
	}
	comm := info.Proc.P_comm[:]
	end := bytes.IndexByte(comm, 0)
	if end < 0 {
		t.Fatalf("own kinfo P_comm has no NUL terminator: %q", comm)
	}
	if got, want := darwinCommandName(comm), string(comm[:end]); got != want {
		t.Fatalf("live kinfo P_comm decoded as %q, want %q", got, want)
	}
}

// case: S-237.13
func TestSupervisedRunObservesWholeDarwinTableWithForeignUIDs(t *testing.T) {
	truePath, err := exec.LookPath("true")
	if err != nil {
		t.Fatal(err)
	}
	infos, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		t.Fatalf("enumerate basic kern.proc.all table: %v", err)
	}
	foreign := false
	for _, info := range infos {
		if info.Eproc.Ucred.Uid != uint32(unix.Getuid()) {
			foreign = true
			break
		}
	}
	if !foreign {
		t.Fatal("macOS process table did not include the protected foreign-uid processes required by this regression")
	}
	dir, err := os.MkdirTemp("", "x53-darwin-process-table-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	_, source, _, ok := runtimeCaller(0)
	if !ok {
		t.Fatal("locate proc package")
	}
	runner := filepath.Join(dir, "kb-runner")
	buildRunner := exec.Command("go", "build", "-o", runner, ".")
	buildRunner.Dir = filepath.Join(filepath.Dir(source), "..", "..", "kb-runner")
	if output, err := buildRunner.CombinedOutput(); err != nil {
		t.Fatalf("build kb-runner: %v\n%s", err, output)
	}
	state := filepath.Join(dir, "state")
	run, err := Start(context.Background(), Config{
		HelperPath: runner, StateDir: state, Tag: "foreign-process-table", Stall: time.Hour,
		StdoutPath: filepath.Join(state, "stdout"), StderrPath: filepath.Join(state, "stderr"),
		Command: []string{truePath},
	})
	if err != nil {
		t.Fatalf("start with protected foreign-uid processes present: %v", err)
	}
	if err := run.Wait(); err != nil {
		t.Fatalf("ordinary supervised command failed on unrelated foreign-uid process: %v", err)
	}
}

// case: S-237.1
func TestDetachedDarwinMembershipRequiresHistoryOrMarker(t *testing.T) {
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
	identities := make(map[int]string)
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
			identity, exists := processIdentityForTest(t, pid)
			if exists && identity == identities[pid] {
				if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil {
					t.Logf("cleanup test-owned process group %d: %v", pid, err)
				}
			}
		}
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			alive := false
			for _, pid := range pids {
				if identity, exists := processIdentityForTest(t, pid); exists && identity == identities[pid] {
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
					identity, alive := processIdentityForTest(t, pid)
					if !alive {
						t.Fatalf("detached fixture PID %d exited before classification", pid)
					}
					pids = append(pids, pid)
					identities[pid] = identity
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
		listing := List(state)
		if listing.Error != nil {
			t.Fatalf("kb-runner process listing failed: %v", listing.Error)
		}
		var found *Process
		for _, listedRun := range listing.Runs {
			if listedRun.RunID != run.RunID {
				continue
			}
			if listedRun.Error != "" {
				t.Fatalf("process tree observation failed while checking PID %d: %s", pid, listedRun.Error)
			}
			for i := range listedRun.Tree {
				if listedRun.Tree[i].PID == pid {
					process := listedRun.Tree[i]
					found = &process
				}
			}
		}
		if markerFound && found == nil {
			t.Fatalf("readable inherited marker for PID %d was not enough to include it", pid)
		}
		if found != nil {
			if found.Unverified {
				t.Fatalf("owned detached PID %d remained unverified despite marker or sampled history: %+v", pid, *found)
			}
			identity, alive := processIdentityForTest(t, pid)
			if !alive || identity != found.Identity {
				t.Fatalf("listed detached PID %d lacks current matching identity: %+v alive=%t identity=%s", pid, *found, alive, identity)
			}
		}
		output, err := exec.Command(runner, "ps", "--state", state).CombinedOutput()
		if err != nil {
			t.Fatalf("kb-runner ps failed: %v\n%s", err, output)
		}
		listedInPS := false
		for _, line := range strings.Split(string(output), "\n") {
			if strings.Contains(line, "\t"+strconv.Itoa(pid)+"\t") || strings.Contains(line, "─ "+strconv.Itoa(pid)+" ") {
				listedInPS = true
				if strings.Contains(line, "unverified") {
					t.Fatalf("ps marked proven marker/history PID %d unverified: %s", pid, line)
				}
			}
		}
		if listedInPS != (found != nil) {
			t.Fatalf("ps membership for PID %d = %t, tree membership = %t; output: %s", pid, listedInPS, found != nil, output)
		}
	}
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
