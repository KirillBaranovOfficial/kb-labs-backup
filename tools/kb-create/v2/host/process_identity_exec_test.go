//go:build !windows

package host

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// case: PC-001.7
func TestProcessIdentitySurvivesExecTransition(t *testing.T) {
	source, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	temp := t.TempDir()
	before := filepath.Join(temp, "before-exec")
	after := filepath.Join(temp, "after-exec")
	binary, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{before, after} {
		if err := os.WriteFile(target, binary, 0o700); err != nil {
			t.Fatal(err)
		}
	}

	token, err := NewProcessIdentityToken()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(before, "-test.run=^TestProcessIdentityExecHelper$")
	command.Env = ProcessEnvironmentWithIdentity(append(os.Environ(), "KB_PROCESS_IDENTITY_EXEC_PHASE=before", "KB_PROCESS_IDENTITY_EXEC_TARGET="+after), token)
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	finished := false
	t.Cleanup(func() {
		_ = stdin.Close()
		if !finished {
			_ = command.Process.Kill()
		}
		_ = command.Wait()
	})
	reader := bufio.NewReader(stdout)
	if line, err := reader.ReadString('\n'); err != nil || line != "before\n" {
		t.Fatalf("pre-exec barrier = %q, %v", line, err)
	}
	identity, err := ProcessIdentityForPID(command.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprintln(stdin, "exec"); err != nil {
		t.Fatal(err)
	}
	if line, err := reader.ReadString('\n'); err != nil || line != "after\n" {
		t.Fatalf("post-exec barrier = %q, %v", line, err)
	}
	if got := ProcessIdentityOf(command.Process.Pid, identity); got != ProcessIdentityMatch {
		t.Fatal("the same live PID no longer matches its per-start token after exec")
	}
	if err := stdin.Close(); err != nil {
		t.Fatal(err)
	}
	if err := command.Wait(); err != nil {
		t.Fatal(err)
	}
	finished = true
}

func TestProcessIdentityExecHelper(t *testing.T) {
	switch os.Getenv("KB_PROCESS_IDENTITY_EXEC_PHASE") {
	case "before":
		_, _ = fmt.Fprintln(os.Stdout, "before")
		var command string
		if _, err := fmt.Fscan(os.Stdin, &command); err != nil || command != "exec" {
			os.Exit(2)
		}
		target := os.Getenv("KB_PROCESS_IDENTITY_EXEC_TARGET")
		args := []string{target, "-test.run=^TestProcessIdentityExecHelper$"}
		environment := make([]string, 0, len(os.Environ()))
		for _, value := range os.Environ() {
			if strings.HasPrefix(value, "KB_PROCESS_IDENTITY_EXEC_PHASE=") {
				value = "KB_PROCESS_IDENTITY_EXEC_PHASE=after"
			}
			environment = append(environment, value)
		}
		if err := syscall.Exec(target, args, environment); err != nil {
			_, _ = fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
	case "after":
		_, _ = fmt.Fprintln(os.Stdout, "after")
		_, _ = io.Copy(io.Discard, os.Stdin)
		os.Exit(0)
	}
}
