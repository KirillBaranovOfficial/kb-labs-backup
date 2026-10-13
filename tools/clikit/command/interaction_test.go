package command

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/kb-labs/clikit/diag"
	"github.com/kb-labs/clikit/result"
	"github.com/spf13/cobra"
)

// case: S-050.3
func TestConfirmWritesPromptBeforeReadingAnswer(t *testing.T) {
	var terminal bytes.Buffer
	command := &cobra.Command{Use: "remove"}
	command.SetIn(promptCheckingReader{output: &terminal, want: "Remove installation? [y/N] ", answer: strings.NewReader("n\n")})
	command.SetOut(&terminal)
	interaction := newInteraction(command, &terminal, true, false)

	confirmed, failure := interaction.Confirm("Remove installation? [y/N] ", "rerun `kb-create uninstall --yes`")
	if confirmed || failure == nil || failure.Code != "KB_INSTALL_OPERATION_CANCELLED" || diag.ExitCode(failure) != diag.ExitCode(diag.New("KB_INSTALL_OPERATION_CANCELLED", "declined")) {
		t.Fatalf("Confirm() = (%v, %v), want catalogued cancellation", confirmed, failure)
	}
	if got := terminal.String(); got != "Remove installation? [y/N] " {
		t.Fatalf("prompt output = %q, want immediate prompt", got)
	}
}

type promptCheckingReader struct {
	output *bytes.Buffer
	want   string
	answer io.Reader
}

func (r promptCheckingReader) Read(content []byte) (int, error) {
	if got := r.output.String(); got != r.want {
		return 0, fmt.Errorf("input was read before prompt was visible: %q", got)
	}
	return r.answer.Read(content)
}

func TestConfirmRefusesMachineAndNonTerminalInput(t *testing.T) {
	for _, test := range []struct {
		name     string
		machine  bool
		terminal bool
	}{
		{name: "machine", machine: true},
		{name: "non-terminal"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var terminal bytes.Buffer
			command := &cobra.Command{Use: "remove"}
			command.SetIn(strings.NewReader("y\n"))
			interaction := &Interaction{in: command.InOrStdin(), out: &terminal, terminal: test.terminal, machine: test.machine}
			confirmed, failure := interaction.Confirm("Remove? ", "rerun `kb-create uninstall --yes`")
			if confirmed || failure == nil || failure.Code != "KB_INSTALL_CONFIRMATION_REQUIRED" || diag.ExitCode(failure) != diag.ExitCode(diag.New("KB_INSTALL_CONFIRMATION_REQUIRED", "required")) {
				t.Fatalf("Confirm() = (%v, %v), want catalogued input refusal", confirmed, failure)
			}
			if terminal.Len() != 0 {
				t.Fatalf("refusal wrote an interactive prompt: %q", terminal.String())
			}
		})
	}
}

func TestAskRefusesMachineAndNonTerminalInput(t *testing.T) {
	for _, test := range []struct {
		name     string
		machine  bool
		terminal bool
	}{
		{name: "machine", machine: true},
		{name: "non-terminal"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var terminal bytes.Buffer
			interaction := &Interaction{in: strings.NewReader("answer\n"), out: &terminal, terminal: test.terminal, machine: test.machine}
			answer, failure := interaction.Ask("Enter value: ", "rerun `kb-create install --project-root PATH`")
			if answer != "" || failure == nil || failure.Code != "KB_RUNTIME_INPUT_INVALID" || diag.ExitCode(failure) != diag.ExitCode(diag.New("KB_INSTALL_CONFIRMATION_REQUIRED", "required")) {
				t.Fatalf("Ask() = (%q, %v), want catalogued input refusal", answer, failure)
			}
			if terminal.Len() != 0 {
				t.Fatalf("refusal wrote an interactive prompt: %q", terminal.String())
			}
		})
	}
}

func TestAskWritesPromptBeforeReadingAnswer(t *testing.T) {
	var terminal bytes.Buffer
	interaction := &Interaction{out: &terminal, terminal: true}
	interaction.in = promptCheckingReader{output: &terminal, want: "Enter value: ", answer: strings.NewReader("  chosen value  \n")}
	answer, failure := interaction.Ask("Enter value: ", "rerun `kb-create install --input request.json`")
	if failure != nil || answer != "chosen value" {
		t.Fatalf("Ask() = (%q, %v), want trimmed answer", answer, failure)
	}
}

func TestAskRetainsBufferedAnswersAcrossPrompts(t *testing.T) {
	var terminal bytes.Buffer
	interaction := &Interaction{in: strings.NewReader("first\nsecond\n"), out: &terminal, terminal: true}
	for _, expected := range []string{"first", "second"} {
		answer, failure := interaction.Ask("Value: ", "rerun `kb-create install --input request.json`")
		if failure != nil || answer != expected {
			t.Fatalf("Ask() = (%q, %v), want %q", answer, failure, expected)
		}
	}
}

func TestProgressWriterStreamsHumanOutputOnly(t *testing.T) {
	var terminal bytes.Buffer
	human := &Interaction{out: &terminal, terminal: true}
	if _, err := io.WriteString(human.ProgressWriter(), "phase started\n"); err != nil {
		t.Fatal(err)
	}
	if got := terminal.String(); got != "phase started\n" {
		t.Fatalf("human progress = %q", got)
	}
	terminal.Reset()
	machine := &Interaction{out: &terminal, terminal: true, machine: true}
	if _, err := io.WriteString(machine.ProgressWriter(), "phase started\n"); err != nil {
		t.Fatal(err)
	}
	if terminal.Len() != 0 {
		t.Fatalf("machine progress leaked to terminal: %q", terminal.String())
	}
}

// case: S-187.1
func TestExecuteDoesNotRenderResultAgainAfterLiveHumanProgress(t *testing.T) {
	var terminal, stderr bytes.Buffer
	root := &cobra.Command{Use: "root"}
	root.AddCommand(&cobra.Command{Use: "run", RunE: func(command *cobra.Command, _ []string) error {
		if _, err := io.WriteString(FromCommand(command).ProgressWriter(), "prompt visible\n"); err != nil {
			return err
		}
		return result.Return(map[string]string{"ok": "data"})
	}})
	if code := Execute(root, []string{"run"}, &terminal, &stderr); code != diag.SuccessStatus() {
		t.Fatalf("Execute() = %d; stderr=%s", code, stderr.String())
	}
	if got := terminal.String(); got != "prompt visible\n" {
		t.Fatalf("human output = %q, want live progress exactly once", got)
	}
}
