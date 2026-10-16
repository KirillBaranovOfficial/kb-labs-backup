package diag

import (
	"strings"
	"testing"
)

// case: S-203.2
func TestToEnvelopeRejectsUnrenderedHintTemplateParameters(t *testing.T) {
	d := New("KB_RUNTIME_UNEXPECTED", "unexpected",
		WithCorrelationID("corr-template"),
		WithHint("Inspect {correlationId}; missing value: {missingValue}."),
	)
	_, err := ToEnvelope(d)
	if err == nil || !strings.Contains(err.Error(), "unrendered template parameter: missingValue") {
		t.Fatalf("ToEnvelope error = %v, want missing template parameter defect", err)
	}
}

// case: S-203.2
func TestToEnvelopeUsesCatalogHintWithoutCorrelationID(t *testing.T) {
	entry, ok := LookupCatalog("KB_RUNTIME_UNEXPECTED")
	if !ok {
		t.Fatal("unexpected runtime catalog entry is missing")
	}
	d := New(entry.Code, entry.Message)
	envelope, err := ToEnvelope(d)
	if err != nil {
		t.Fatal(err)
	}
	if envelope.Hint != entry.HintWithoutCorrelationID {
		t.Fatalf("hint = %q, want catalog's no-correlation hint %q", envelope.Hint, entry.HintWithoutCorrelationID)
	}
	if strings.ContainsAny(envelope.Hint, "{}") {
		t.Fatalf("hint retained template syntax: %q", envelope.Hint)
	}
}

// case: S-203.6
func TestToEnvelopeRendersBinaryTemplateFromInvocation(t *testing.T) {
	d := New("KB_RUNTIME_COMMAND_FAILED", "The command could not complete.", WithBinary("kb-devkit"))
	envelope, err := ToEnvelope(d)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(envelope.Hint, "kb-devkit --help") {
		t.Fatalf("hint = %q, want the invoking binary's help command", envelope.Hint)
	}
	if strings.ContainsAny(envelope.Hint, "{}") {
		t.Fatalf("hint retained template syntax: %q", envelope.Hint)
	}
}

// case: S-203.6
func TestToEnvelopeRequiresGeneratedUnknownCommandTemplateParameter(t *testing.T) {
	entry, ok := LookupCatalog("KB_RUNTIME_UNKNOWN_COMMAND")
	if !ok {
		t.Fatal("unknown-command catalog entry is missing")
	}
	if len(entry.TemplateParameters) != 1 || entry.TemplateParameters[0] != "binary" {
		t.Fatalf("generated template parameters = %v, want [binary]", entry.TemplateParameters)
	}
	d := New(entry.Code, entry.Message)
	if _, err := ToEnvelope(d); err == nil || !strings.Contains(err.Error(), "unrendered template parameter: binary") {
		t.Fatalf("ToEnvelope error = %v, want missing generated binary parameter defect", err)
	}
}
