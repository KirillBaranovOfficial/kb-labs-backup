// Package stream renders shared JSONL command streams.
package stream

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/kb-labs/clikit/diag"
	"github.com/kb-labs/clikit/result"
)

// Writer emits contract records followed by exactly one terminal record.
type Writer struct {
	output   io.Writer
	finished bool
	result   bool
	exitCode int
	failure  *diag.Diag
}

// New creates a JSONL writer for one command invocation.
func New(output io.Writer) *Writer { return &Writer{output: output} }

// ExitCode returns the terminal status selected for this stream.
func (w *Writer) ExitCode() int { return w.exitCode }

// Event writes one successful event record.
func (w *Writer) Event(data any) error {
	if w.finished {
		return fmt.Errorf("cannot write an event after the stream terminal record")
	}
	if w.result {
		return fmt.Errorf("cannot write an event after the stream result record")
	}
	return json.NewEncoder(w.output).Encode(map[string]any{"ok": true, "data": data})
}

// Result writes the successful command result as a stream record.
func (w *Writer) Result(data any) error {
	if w.finished {
		return fmt.Errorf("cannot write a result after the stream terminal record")
	}
	if w.result {
		return fmt.Errorf("stream already has a result record")
	}
	record := map[string]any{
		"ok":   true,
		"data": data,
		"meta": map[string]any{"stream": map[string]any{"result": true}},
	}
	if err := json.NewEncoder(w.output).Encode(record); err != nil {
		return err
	}
	w.result = true
	return nil
}

// Fail writes one typed error record.
func (w *Writer) Fail(d *diag.Diag) error {
	if w.finished {
		return fmt.Errorf("cannot write an error after the stream terminal record")
	}
	if w.result {
		return fmt.Errorf("cannot write an error after the stream result record")
	}
	d = diag.FromError(d)
	w.failure = d
	if code := diag.ExitCode(d); code > w.exitCode {
		w.exitCode = code
	}
	return json.NewEncoder(w.output).Encode(result.FailureEnvelope(d))
}

// Finish writes the terminal success envelope and closes the stream.
func (w *Writer) Finish(exitCode int) error {
	if w.finished {
		return fmt.Errorf("stream already has a terminal record")
	}
	if exitCode != diag.ExitOK && exitCode != diag.ExitInput && exitCode != diag.ExitError {
		return fmt.Errorf("invalid stream exit code %d", exitCode)
	}
	if w.failure != nil {
		exitCode = diag.ExitCode(w.failure)
	} else if w.exitCode > exitCode {
		exitCode = w.exitCode
	}
	record := map[string]any{
		"ok":   true,
		"data": nil,
		"meta": map[string]any{"stream": map[string]any{"terminal": true, "exitCode": exitCode}},
	}
	if err := json.NewEncoder(w.output).Encode(record); err != nil {
		return err
	}
	w.exitCode = exitCode
	w.finished = true
	return nil
}
