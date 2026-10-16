package result

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/kb-labs/clikit/diag"
)

// Render writes a successful CommandOutput to w in the given mode.
//
//   - ModeHuman: the pre-rendered Human string.
//   - ModeJSON:  the shared {ok,data} machine envelope.
//   - ModeAgent: the compact Agent payload, falling back to JSON.
func Render(w io.Writer, out CommandOutput, mode Mode) error {
	switch mode {
	case ModeJSON:
		return writeJSON(w, jsonEnvelope(out))
	case ModeAgent:
		return writeJSON(w, jsonEnvelope(out))
	default:
		_, err := fmt.Fprintln(w, out.Human)
		return err
	}
}

// RenderDiag writes a failure (a *Diag) to w in the given mode and returns the
// process exit code. Human mode prints message + reason + hint lines; machine
// modes print the {ok:false, error:{…}} envelope (agent mode omits meta).
func RenderDiag(w io.Writer, hw io.Writer, d *diag.Diag, mode Mode) diag.ExitStatus {
	switch mode {
	case ModeJSON:
		writeJSON(w, FailureEnvelope(d))
	case ModeAgent:
		writeJSON(w, FailureEnvelope(d))
	default:
		writeHumanDiag(hw, d)
	}
	return diag.ExitCode(d)
}

// jsonEnvelope is the machine shape for a CommandOutput.
func jsonEnvelope(out CommandOutput) map[string]any {
	env := map[string]any{
		"ok":   out.Ok,
		"data": out.JSON,
	}
	if len(out.Warnings) > 0 {
		warnings := make([]diag.Envelope, 0, len(out.Warnings))
		for _, warning := range out.Warnings {
			if warning != nil {
				warnings = append(warnings, errorEnvelope(warning))
			}
		}
		if len(warnings) > 0 {
			env["meta"] = map[string]any{"warnings": warnings}
		}
	}
	return env
}

func errorEnvelope(d *diag.Diag) diag.Envelope {
	if d == nil {
		return diag.Envelope{}
	}
	d = diag.FromError(d)
	envelope, err := diag.ToEnvelope(d)
	if err != nil {
		var violation *diag.Diag
		if errors.As(err, &violation) {
			envelope, _ = diag.ToEnvelope(violation)
		} else {
			envelope, _ = diag.ToEnvelope(diag.NewContractViolation(d.Code, "serialization"))
		}
	}
	return envelope
}

// FailureEnvelope returns the shared machine error envelope for result streams.
func FailureEnvelope(d *diag.Diag) map[string]any {
	envelope := map[string]any{"ok": false, "error": errorEnvelope(d)}
	if d != nil && d.Data != nil {
		envelope["data"] = d.Data
	}
	return envelope
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
