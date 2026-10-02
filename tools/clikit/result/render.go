package result

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/kb-labs/clikit/diag"
)

// Render writes a successful CommandOutput to w in the given mode.
//
//   - ModeHuman: the pre-rendered Human string.
//   - ModeJSON:  the shared {ok,data} machine envelope.
//   - ModeAgent: the compact Agent payload, falling back to JSON.
func Render(w io.Writer, out CommandOutput, mode Mode) {
	switch mode {
	case ModeJSON:
		writeJSON(w, jsonEnvelope(out))
	case ModeAgent:
		writeJSON(w, jsonEnvelope(out))
	default:
		fmt.Fprintln(w, out.Human)
	}
}

// RenderDiag writes a failure (a *Diag) to w in the given mode and returns the
// process exit code. Human mode prints message + reason + hint lines; machine
// modes print the {ok:false, error:{…}} envelope (agent mode omits meta).
func RenderDiag(w io.Writer, hw io.Writer, d *diag.Diag, mode Mode) int {
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
		warnings := make([]map[string]any, 0, len(out.Warnings))
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

func errorEnvelope(d *diag.Diag) map[string]any {
	if d == nil {
		return nil
	}
	d = diag.FromError(d)
	catalog, ok := diag.LookupCatalog(d.Code)
	if !ok {
		d = diag.FromError(fmt.Errorf("normalized diagnostic code %q has no catalog entry", d.Code))
		catalog, _ = diag.LookupCatalog(d.Code)
	}
	p := map[string]any{
		"code":      catalog.Code,
		"area":      catalog.Area,
		"stage":     catalog.Stage,
		"severity":  catalog.Severity,
		"retryable": catalog.Retryable,
		"message":   d.Message,
		"hint":      catalog.Hint,
	}
	if d.Reason != "" {
		p["cause"] = d.Reason
	}
	if d.CorrelationID != "" {
		p["correlationId"] = d.CorrelationID
	}
	if len(d.Meta) > 0 {
		details := make(map[string]string, len(d.Meta))
		for key, value := range d.Meta {
			details[key] = fmt.Sprint(value)
		}
		p["details"] = details
	}
	return p
}

// FailureEnvelope returns the shared machine error envelope for result streams.
func FailureEnvelope(d *diag.Diag) map[string]any {
	return map[string]any{"ok": false, "error": errorEnvelope(d)}
}

func writeJSON(w io.Writer, v any) {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}
