package diag

import (
	"fmt"
	"strings"
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
	if hint == "" || hint == entry.Hint {
		hint = entry.Hint
		if d.CorrelationID == "" && entry.HintWithoutCorrelationID != "" {
			hint = entry.HintWithoutCorrelationID
		}
	}
	message := d.Message
	if message == "" {
		message = entry.Message
	}
	templateValues := make(map[string]string, len(d.Meta)+2)
	for key, value := range d.Meta {
		templateValues[key] = fmt.Sprint(value)
	}
	if d.Binary != "" {
		templateValues["binary"] = d.Binary
	}
	if d.CorrelationID != "" {
		templateValues["correlationId"] = d.CorrelationID
	}
	message, err := renderTemplate(message, templateValues)
	if err != nil {
		return Envelope{}, err
	}
	if err := validateCatalogTemplateParameters(entry, hint, templateValues); err != nil {
		return Envelope{}, err
	}
	hint, err = renderTemplate(hint, templateValues)
	if err != nil {
		return Envelope{}, err
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

// validateCatalogTemplateParameters checks the generated code-specific parameter contract.
func validateCatalogTemplateParameters(entry CatalogEntry, template string, values map[string]string) error {
	for _, name := range entry.TemplateParameters {
		if strings.Contains(template, "{"+name+"}") {
			if _, ok := values[name]; !ok {
				if entry.RuntimeTemplateParameters[name] == "optional" {
					continue
				}
				return fmt.Errorf("unrendered template parameter: %s", name)
			}
		}
	}
	return nil
}

// renderTemplate resolves the selected catalog template and rejects every unresolved parameter.
func renderTemplate(template string, values map[string]string) (string, error) {
	var rendered strings.Builder
	for len(template) > 0 {
		opening := strings.IndexByte(template, '{')
		if opening < 0 {
			rendered.WriteString(template)
			break
		}
		rendered.WriteString(template[:opening])
		template = template[opening+1:]
		closing := strings.IndexByte(template, '}')
		if closing < 0 {
			return "", fmt.Errorf("unclosed template parameter in %q", template)
		}
		key := template[:closing]
		value, ok := values[key]
		if !ok {
			return "", fmt.Errorf("unrendered template parameter: %s", key)
		}
		rendered.WriteString(value)
		template = template[closing+1:]
	}
	return rendered.String(), nil
}
