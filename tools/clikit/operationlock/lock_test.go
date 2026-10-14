package operationlock

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/kb-labs/clikit/diag"
)

const helperMode = "CLIKIT_OPERATIONLOCK_HELPER"

func TestLockProcessHelper(t *testing.T) {
	if os.Getenv(helperMode) == "" {
		return
	}
	path := os.Getenv("CLIKIT_OPERATIONLOCK_PATH")
	mode := os.Getenv(helperMode)
	if mode == "wave" {
		fmt.Println("READY")
		_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
	}
	lease, err := Acquire(path, "test-operation")
	if err != nil {
		if holder, ok := Holder(path); ok {
			fmt.Printf("REFUSED %d\n", holder)
		} else {
			fmt.Println("REFUSED unknown")
		}
		os.Exit(0)
	}
	defer lease.Release()
	if mode == "wave" {
		counter := os.Getenv("CLIKIT_OPERATIONLOCK_COUNTER")
		active := 0
		if data, readErr := os.ReadFile(counter); readErr == nil {
			active, _ = strconv.Atoi(strings.TrimSpace(string(data)))
		}
		active++
		_ = os.WriteFile(counter, []byte(strconv.Itoa(active)), 0o600)
		fmt.Printf("HELD %d\n", active)
		_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
		active--
		_ = os.WriteFile(counter, []byte(strconv.Itoa(active)), 0o600)
		return
	}
	fmt.Printf("HELD %d\n", os.Getpid())
	_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
}

func TestContendingProcessesHaveOneKernelLockHolder(t *testing.T) {
	root := t.TempDir()
	path := Path(root)
	const rounds, contenders = 12, 12
	for round := 0; round < rounds; round++ {
		counter := filepath.Join(root, fmt.Sprintf("counter-%d", round))
		children := make([]*exec.Cmd, contenders)
		inputs := make([]io.WriteCloser, contenders)
		readers := make([]*bufio.Reader, contenders)
		for i := range children {
			command, input, reader := startHelper(t, path, "wave", counter)
			children[i], inputs[i], readers[i] = command, input, reader
			if line := readLine(t, reader); line != "READY" {
				t.Fatalf("helper readiness = %q", line)
			}
		}
		for _, input := range inputs {
			_, _ = input.Write([]byte("go\n"))
		}
		held := 0
		var maxActive int
		for _, reader := range readers {
			line := readLine(t, reader)
			fields := strings.Fields(line)
			if len(fields) == 2 && fields[0] == "HELD" {
				held++
				active, _ := strconv.Atoi(fields[1])
				if active > maxActive {
					maxActive = active
				}
			} else if len(fields) != 2 || fields[0] != "REFUSED" {
				t.Fatalf("helper result = %q", line)
			}
		}
		for _, input := range inputs {
			_, _ = input.Write([]byte("release\n"))
			_ = input.Close()
		}
		for _, child := range children {
			if err := child.Wait(); err != nil {
				t.Fatalf("wait for lock helper: %v", err)
			}
		}
		if held != 1 || maxActive != 1 {
			t.Fatalf("round %d: holders=%d max active=%d; want exactly one", round, held, maxActive)
		}
	}
}

func TestKilledHolderReleasesKernelLockAndLeavesPath(t *testing.T) {
	path := Path(t.TempDir())
	child, _, reader := startHelper(t, path, "hold", "")
	line := readLine(t, reader)
	if !strings.HasPrefix(line, "HELD ") {
		t.Fatalf("holder result = %q", line)
	}
	if err := child.Process.Kill(); err != nil {
		t.Fatal("kill lock holder")
	}
	_ = child.Wait()
	lease, err := Acquire(path, "after-kill")
	if err != nil {
		t.Fatalf("kernel did not release the killed holder: %v", err)
	}
	lease.Release()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("persistent lock path missing after release: %v", err)
	}
}

func TestExistingLockFileContentsNeverControlExclusivity(t *testing.T) {
	for _, content := range []string{"", "garbage", strconv.Itoa(os.Getpid()) + " unrelated\n"} {
		t.Run(fmt.Sprintf("bytes-%d", len(content)), func(t *testing.T) {
			path := Path(t.TempDir())
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			lease, err := Acquire(path, "replace-description")
			if err != nil {
				t.Fatalf("stale content blocked kernel acquisition: %v", err)
			}
			lease.Release()
			if _, err := os.Stat(path); err != nil {
				t.Fatalf("release unlinked stable inode: %v", err)
			}
		})
	}
}

func TestContenderErrorNamesDescriptiveHolder(t *testing.T) {
	path := Path(t.TempDir())
	child, input, reader := startHelper(t, path, "hold", "")
	line := readLine(t, reader)
	fields := strings.Fields(line)
	if len(fields) != 2 || fields[0] != "HELD" {
		t.Fatalf("holder handshake = %q", line)
	}
	lease, err := Acquire(path, "contender")
	if lease != nil || !errors.Is(err, ErrInProgress) {
		t.Fatalf("contender lease/error = %v / %v", lease, err)
	}
	var failure *diag.Diag
	if !errors.As(err, &failure) || failure.Code != "KB_INSTALL_OPERATION_IN_PROGRESS" || !strings.Contains(failure.Message, fields[1]) {
		t.Fatalf("contender refusal does not name holder: %v", err)
	}
	_, _ = input.Write([]byte("release\n"))
	_ = input.Close()
	if err := child.Wait(); err != nil {
		t.Fatal(err)
	}
}

func startHelper(t *testing.T, path, mode, counter string) (*exec.Cmd, io.WriteCloser, *bufio.Reader) {
	t.Helper()
	command := exec.Command(os.Args[0], "-test.run=^TestLockProcessHelper$")
	command.Env = append(os.Environ(), helperMode+"="+mode, "CLIKIT_OPERATIONLOCK_PATH="+path,
		"CLIKIT_OPERATIONLOCK_COUNTER="+counter)
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	input, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if command.Process != nil {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
		_ = input.Close()
	})
	return command, input, bufio.NewReader(stdout)
}

func readLine(t *testing.T, reader *bufio.Reader) string {
	t.Helper()
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("read helper handshake: %v", err)
	}
	return strings.TrimSpace(line)
}
