//go:build !windows

package command

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

// case: S-050.3
func TestInteractionPromptIsVisibleBeforePTYAnswer(t *testing.T) {
	command := exec.Command("script", "-q", "/dev/null", os.Args[0], "-test.run=^TestInteractionPTYChild$")
	command.Env = append(os.Environ(), "CLIKIT_INTERACTION_PTY_CHILD=1")
	input, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatalf("start PTY test helper: %v", err)
	}
	t.Cleanup(func() {
		_ = input.Close()
		if command.Process != nil {
			_ = command.Process.Kill()
		}
	})

	bytes := make(chan byte, 256)
	readErrors := make(chan error, 1)
	go func() {
		for {
			var content [1]byte
			_, readErr := output.Read(content[:])
			if readErr != nil {
				readErrors <- readErr
				return
			}
			bytes <- content[0]
		}
	}()

	var transcript strings.Builder
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	for !strings.Contains(transcript.String(), "PTY-CONFIRM> ") {
		select {
		case content := <-bytes:
			transcript.WriteByte(content)
		case readErr := <-readErrors:
			t.Fatalf("PTY closed before prompt: %v; output=%q", readErr, transcript.String())
		case <-timer.C:
			t.Fatalf("prompt did not reach PTY before answer; output=%q", transcript.String())
		}
	}
	if _, err := io.WriteString(input, "y\n"); err != nil {
		t.Fatal(err)
	}
	if err := command.Wait(); err != nil {
		t.Fatalf("PTY child failed: %v; output=%q", err, transcript.String())
	}
}

func TestInteractionPTYChild(t *testing.T) {
	if os.Getenv("CLIKIT_INTERACTION_PTY_CHILD") != "1" {
		return
	}
	root := &cobra.Command{Use: "root"}
	root.AddCommand(&cobra.Command{Use: "run", RunE: func(command *cobra.Command, _ []string) error {
		_, failure := FromCommand(command).Confirm("PTY-CONFIRM> ", "rerun `kb-create uninstall --yes`")
		if failure != nil {
			return failure
		}
		_, err := fmt.Fprintln(FromCommand(command).ProgressWriter(), "PTY-CONFIRMED")
		return err
	}})
	if code := Execute(root, []string{"run"}, os.Stdout, os.Stderr); code.Code() != 0 {
		t.Fatalf("Execute() = %d", code)
	}
}
