package command

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/kb-labs/clikit/diag"
	"github.com/kb-labs/clikit/result"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type streamWriterKey struct{}

type captureBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *captureBuffer) Write(content []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(content)
}

func (b *captureBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

var outputWriter struct {
	sync.RWMutex
	writer io.Writer
}

// SetOutputWriter installs the result boundary's capture writer.
func SetOutputWriter(writer io.Writer) func() {
	outputWriter.Lock()
	previous := outputWriter.writer
	outputWriter.writer = writer
	outputWriter.Unlock()
	return func() {
		outputWriter.Lock()
		outputWriter.writer = previous
		outputWriter.Unlock()
	}
}

// OutputWriter returns the active invocation capture writer.
func OutputWriter() io.Writer {
	outputWriter.RLock()
	writer := outputWriter.writer
	outputWriter.RUnlock()
	if writer == nil {
		return io.Discard
	}
	return writer
}

// Printf records command text for the shared renderer.
func Printf(format string, args ...any) {
	_, _ = fmt.Fprintf(OutputWriter(), format, args...)
}

// Println records command text for the shared renderer.
func Println(args ...any) {
	_, _ = fmt.Fprintln(OutputWriter(), args...)
}

// StreamWriter returns the stdout writer supplied by the top-level dispatcher.
func StreamWriter(command *cobra.Command) io.Writer {
	if command != nil && command.Context() != nil {
		if writer, ok := command.Context().Value(streamWriterKey{}).(io.Writer); ok {
			return writer
		}
	}
	if command == nil {
		return io.Discard
	}
	return command.OutOrStdout()
}

// Execute runs a Cobra tree and renders its outcome exactly once.
func Execute(root *cobra.Command, args []string, stdout, stderr io.Writer) int {
	InstallInputBoundary(root)
	if err := resetFlags(root); err != nil {
		return result.RenderDiag(stdout, stderr, diag.FromError(err), modeForArgs(args))
	}
	captured := &captureBuffer{}
	restoreOutput := SetOutputWriter(captured)
	defer restoreOutput()
	root.SetArgs(args)
	root.SetOut(captured)
	root.SetErr(io.Discard)
	root.SilenceUsage = true
	root.SilenceErrors = true
	baseContext := root.Context()
	if baseContext == nil {
		baseContext = context.Background()
	}
	input := root.InOrStdin()
	interaction := &Interaction{in: input, out: stdout, err: stderr, terminal: isTerminalInput(input), machine: modeForArgs(args) != result.ModeHuman}
	baseContext = context.WithValue(baseContext, streamWriterKey{}, stdout)
	root.SetContext(context.WithValue(baseContext, interactionKey{}, interaction))

	_, err := root.ExecuteC()
	mode := modeForArgs(args)
	if exitCode, ok := result.StreamExitCode(err); ok {
		return exitCode
	}
	if data, ok := result.ReturnedData(err); ok {
		human := captured.String()
		if mode == result.ModeHuman && strings.TrimSpace(human) == "" {
			if interaction.hasStreamed() {
				return diag.ExitOK
			}
			encoded, marshalErr := json.MarshalIndent(data, "", "  ")
			if marshalErr != nil {
				return result.RenderDiag(stdout, stderr, diag.FromError(marshalErr), mode)
			}
			human = string(encoded)
		}
		result.Render(stdout, result.Success(human, data), mode)
		return diag.ExitOK
	}
	if err != nil {
		diagnostic := diag.FromError(err)
		return result.RenderDiag(stdout, stderr, diagnostic, mode)
	}
	human := strings.TrimSpace(captured.String())
	var data any
	if human != "" {
		data = human
	}
	result.Render(stdout, result.Success(human, data), mode)
	return diag.ExitOK
}

func modeForArgs(args []string) result.Mode {
	var jsonMode, agentMode bool
	output := ""
	for index, arg := range args {
		switch arg {
		case "--json":
			jsonMode = true
		case "--agent":
			agentMode = true
		case "--output":
			if index+1 < len(args) {
				output = args[index+1]
			}
		default:
			if strings.HasPrefix(arg, "--output=") {
				output = strings.TrimPrefix(arg, "--output=")
			}
		}
	}
	return result.ResolveMode(jsonMode, agentMode, output)
}

func resetFlags(root *cobra.Command) error {
	var visit func(*cobra.Command)
	var resetErr error
	visit = func(command *cobra.Command) {
		command.Flags().VisitAll(func(flag *pflag.Flag) {
			if err := resetFlag(flag); err != nil && resetErr == nil {
				resetErr = err
			}
		})
		command.PersistentFlags().VisitAll(func(flag *pflag.Flag) {
			if err := resetFlag(flag); err != nil && resetErr == nil {
				resetErr = err
			}
		})
		for _, child := range command.Commands() {
			visit(child)
		}
	}
	visit(root)
	return resetErr
}

func resetFlag(flag *pflag.Flag) error {
	if values, ok := flag.Value.(pflag.SliceValue); ok {
		defaultValue := strings.TrimSuffix(strings.TrimPrefix(flag.DefValue, "["), "]")
		var defaults []string
		if defaultValue == "" {
			defaults = []string{}
		} else {
			reader := csv.NewReader(strings.NewReader(defaultValue))
			var err error
			defaults, err = reader.Read()
			if err != nil {
				return fmt.Errorf("reset slice flag %q to its default: %w", flag.Name, err)
			}
		}
		if err := values.Replace(defaults); err != nil {
			return fmt.Errorf("reset slice flag %q to its default: %w", flag.Name, err)
		}
	} else {
		if err := flag.Value.Set(flag.DefValue); err != nil {
			return fmt.Errorf("reset flag %q to its default: %w", flag.Name, err)
		}
	}
	flag.Changed = false
	return nil
}
