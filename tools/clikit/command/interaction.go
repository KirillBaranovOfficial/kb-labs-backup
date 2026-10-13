package command

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
	"sync/atomic"

	"github.com/charmbracelet/x/term"
	"github.com/kb-labs/clikit/diag"
	"github.com/spf13/cobra"
)

type interactionKey struct{}

// Interaction carries the terminal streams and mode selected by Execute.
// Commands use it for prompts and live human progress while Execute retains
// ownership of the final result.
type Interaction struct {
	in       io.Reader
	reader   *bufio.Reader
	out      io.Writer
	err      io.Writer
	terminal bool
	machine  bool
	streamed atomic.Bool
}

func newInteraction(command *cobra.Command, out io.Writer, terminal, machine bool) *Interaction {
	return &Interaction{in: command.InOrStdin(), out: out, terminal: terminal, machine: machine}
}

// FromCommand returns the invocation interaction channel installed by Execute.
func FromCommand(command *cobra.Command) *Interaction {
	if command != nil && command.Context() != nil {
		if interaction, ok := command.Context().Value(interactionKey{}).(*Interaction); ok {
			return interaction
		}
	}
	return &Interaction{in: strings.NewReader(""), out: io.Discard}
}

// Confirm displays a terminal prompt and waits for a yes/no answer. A machine
// invocation or non-terminal input receives the catalogued explicit-confirmation
// refusal without emitting an interactive prompt.
func (i *Interaction) Confirm(prompt, hint string) (bool, *diag.Diag) {
	if !i.available() {
		return false, confirmationRequired(hint, fmt.Errorf("confirmation unavailable without an interactive terminal"))
	}
	answer, err := i.readLine(prompt)
	if err != nil {
		return false, confirmationRequired(hint, err)
	}
	confirmed := strings.EqualFold(answer, "y") || strings.EqualFold(answer, "yes")
	if !confirmed {
		failure := diag.New("KB_INSTALL_OPERATION_CANCELLED", "The operation was cancelled.", diag.WithHint("rerun the command and confirm when prompted"))
		return false, failure
	}
	return true, nil
}

// Ask displays a terminal prompt and returns the line entered by the user.
// Machine invocations and non-terminal input receive a catalogued refusal.
func (i *Interaction) Ask(prompt, hint string) (string, *diag.Diag) {
	if !i.available() {
		failure := diag.New("KB_RUNTIME_INPUT_INVALID", "Interactive input requires a terminal.", diag.WithHint(hint), diag.WithCause("input is unavailable without an interactive terminal"))
		return "", failure
	}
	answer, err := i.readLine(prompt)
	if err != nil {
		failure := diag.New("KB_RUNTIME_INPUT_INVALID", "The prompt could not be written.", diag.WithHint(hint), diag.WithCause(err.Error()))
		return "", failure
	}
	return answer, nil
}

func (i *Interaction) available() bool { return i != nil && !i.machine && i.terminal }

func (i *Interaction) readLine(prompt string) (string, error) {
	if _, err := io.WriteString(i.out, prompt); err != nil {
		return "", err
	}
	if i.reader == nil {
		i.reader = bufio.NewReader(i.in)
	}
	answer, err := i.reader.ReadString('\n')
	if err == io.EOF && len(answer) == 0 {
		return "", io.EOF
	}
	if err != nil && err != io.EOF {
		return "", err
	}
	return strings.TrimSpace(answer), nil
}

func confirmationRequired(hint string, cause error) *diag.Diag {
	failure := diag.New("KB_INSTALL_CONFIRMATION_REQUIRED", "This destructive operation requires explicit confirmation.", diag.WithHint(hint), diag.WithCause(cause.Error()))
	return failure
}

// Input supplies the command's input stream to an interactive frontend.
func (i *Interaction) Input() io.Reader {
	if i == nil || i.in == nil {
		return strings.NewReader("")
	}
	if i.reader != nil {
		return i.reader
	}
	return i.in
}

// ProgressWriter streams human progress to the terminal. Machine invocations
// receive a discard writer so the final result envelope remains the sole output.
func (i *Interaction) ProgressWriter() io.Writer {
	if i == nil || i.machine || i.out == nil {
		return io.Discard
	}
	return progressWriter{interaction: i}
}

type progressWriter struct{ interaction *Interaction }

func (w progressWriter) Write(content []byte) (int, error) {
	n, err := w.interaction.out.Write(content)
	if n > 0 {
		w.interaction.streamed.Store(true)
	}
	return n, err
}

func (i *Interaction) hasStreamed() bool { return i != nil && i.streamed.Load() }

// ErrorWriter returns the invocation's stderr channel for child processes.
func (i *Interaction) ErrorWriter() io.Writer {
	if i == nil || i.err == nil {
		return io.Discard
	}
	return i.err
}

func isTerminalInput(input io.Reader) bool {
	file, ok := input.(*os.File)
	return ok && term.IsTerminal(file.Fd())
}
