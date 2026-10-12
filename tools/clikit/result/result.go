// Package result is the dual human/machine output envelope for the KB Labs Go
// launcher tools. It mirrors the platform's CommandOutput
// (shared/cli-ui/src/command-result.ts): a single command outcome that can be
// rendered as pretty human text, full JSON, or a compact agent payload.
package result

import (
	"errors"

	"github.com/kb-labs/clikit/diag"
)

// Status mirrors the platform CommandOutput status union.
type Status string

const (
	StatusSuccess   Status = "success"
	StatusError     Status = "error"
	StatusWarning   Status = "warning"
	StatusInfo      Status = "info"
	StatusFailed    Status = "failed"
	StatusCancelled Status = "cancelled"
	StatusSkipped   Status = "skipped"
)

// Mode selects how a CommandOutput / Diag is rendered.
type Mode int

const (
	ModeHuman Mode = iota
	ModeJSON
	ModeAgent
)

// CommandOutput is one command's outcome in all renderable forms.
//
//   - Human is the pre-rendered pretty string (built with the ui package).
//   - JSON is the full machine payload (any JSON-marshalable value).
//   - Agent is an optional compact payload for agents; when nil, agent mode
//     falls back to JSON.
type CommandOutput struct {
	Ok       bool         `json:"ok"`
	Status   Status       `json:"status"`
	Human    string       `json:"-"`
	JSON     any          `json:"json,omitempty"`
	Agent    any          `json:"agent,omitempty"`
	Warnings []*diag.Diag `json:"warnings,omitempty"`
}

// Returned marks a handler value for the shared command boundary to render.
type Returned struct {
	Data any
	Emit func(func() error) *diag.Diag
}

func (r *Returned) Error() string { return "command result" }

// Return hands a value to the shared command boundary without writing it.
func Return(data any) error { return &Returned{Data: data} }

// ReturnWithEmitter lets a command wrap the shared result write with work that
// must happen only after the result has been written successfully.
func ReturnWithEmitter(data any, emit func(func() error) *diag.Diag) error {
	return &Returned{Data: data, Emit: emit}
}

// ReturnedData extracts a handler value without exposing it as an error.
func ReturnedData(err error) (any, bool) {
	var returned *Returned
	if errors.As(err, &returned) && returned != nil {
		return returned.Data, true
	}
	return nil, false
}

// EmitReturned writes a returned result through its command-owned emitter, or
// directly when the result has no post-write behavior.
func EmitReturned(err error, write func() error) *diag.Diag {
	var returned *Returned
	if errors.As(err, &returned) && returned != nil && returned.Emit != nil {
		return returned.Emit(write)
	}
	if writeErr := write(); writeErr != nil {
		return diag.New("KB_RUNTIME_UNEXPECTED", "The command result could not be written.",
			diag.WithCauseAndReason("the output writer returned an error: "+writeErr.Error()),
			diag.WithHint("Restore a writable output destination, then rerun the command."))
	}
	return nil
}

// Streamed marks a command whose shared stream writer emitted its own terminal
// record. The dispatcher must not append a second result.
type Streamed struct {
	ExitCode int
}

func (s *Streamed) Error() string { return "stream complete" }

func StreamComplete(exitCode ...int) error {
	code := diag.ExitOK
	if len(exitCode) > 0 {
		code = exitCode[0]
	}
	return &Streamed{ExitCode: code}
}

func IsStreamComplete(err error) bool {
	var streamed *Streamed
	return errors.As(err, &streamed)
}

func StreamExitCode(err error) (int, bool) {
	var streamed *Streamed
	if errors.As(err, &streamed) && streamed != nil {
		return streamed.ExitCode, true
	}
	return diag.ExitOK, false
}

// Success builds a successful CommandOutput.
func Success(human string, jsonPayload any) CommandOutput {
	return CommandOutput{Ok: true, Status: StatusSuccess, Human: human, JSON: jsonPayload}
}

// WithWarnings attaches non-fatal diagnostics and downgrades the status to
// warning when the command otherwise succeeded.
func (o CommandOutput) WithWarnings(ws ...*diag.Diag) CommandOutput {
	for _, w := range ws {
		if w != nil {
			o.Warnings = append(o.Warnings, w)
		}
	}
	if o.Ok && len(o.Warnings) > 0 && o.Status == StatusSuccess {
		o.Status = StatusWarning
	}
	return o
}
