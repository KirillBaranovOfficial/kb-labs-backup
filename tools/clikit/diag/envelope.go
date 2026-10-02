package diag

import (
	"fmt"
)

// Action is a copy-ready recovery action in the platform error envelope.
type Action struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	Command string `json:"command,omitempty"`
}

// Envelope is the canonical platform error wire shape. Required catalog
// metadata is always populated from the generated platform catalog.
type Envelope struct {
	Code          string            `json:"code"`
	Area          string            `json:"area"`
	Stage         string            `json:"stage"`
	Severity      string            `json:"severity"`
	Retryable     bool              `json:"retryable"`
	Message       string            `json:"message"`
	Cause         string            `json:"cause,omitempty"`
	Hint          string            `json:"hint"`
	Actions       []Action          `json:"actions,omitempty"`
	Docs          string            `json:"docs,omitempty"`
	CorrelationID string            `json:"correlationId,omitempty"`
	Details       map[string]string `json:"details,omitempty"`
}

// ToEnvelope serializes a diagnostic with all required canonical metadata.
func ToEnvelope(d *Diag) (Envelope, error) {
	if d == nil {
		return Envelope{}, fmt.Errorf("cannot serialize a nil diagnostic")
	}
	entry, ok := LookupCatalog(d.Code)
	if !ok {
		return Envelope{}, fmt.Errorf("diagnostic code %q is absent from the platform catalog", d.Code)
	}
	hint := d.Hint
	if hint == "" {
		hint = entry.Hint
	}
	message := d.Message
	if message == "" {
		message = entry.Message
	}
	envelope := Envelope{
		Code: entry.Code, Area: entry.Area, Stage: entry.Stage,
		Severity: entry.Severity, Retryable: entry.Retryable,
		Message: message, Hint: hint, Cause: d.Cause,
		Actions: append([]Action(nil), d.Actions...), Docs: d.Docs,
		CorrelationID: d.CorrelationID,
	}
	if envelope.Cause == "" {
		envelope.Cause = d.Reason
	}
	if len(d.Meta) > 0 {
		envelope.Details = make(map[string]string, len(d.Meta))
		for key, value := range d.Meta {
			envelope.Details[key] = fmt.Sprint(value)
		}
	}
	return envelope, nil
}
