package diag

import (
	"strings"
	"testing"
)

// case: S-203.2
func TestToEnvelopeRendersHintTemplateValues(t *testing.T) {
	d := New("KB_RUNTIME_UNEXPECTED", "unexpected",
		WithCorrelationID("corr-template"),
		WithHint("Inspect {correlationId}; missing value: {missingValue}."),
	)
	envelope, err := ToEnvelope(d)
	if err != nil {
		t.Fatal(err)
	}
	const want = "Inspect corr-template; missing value: unknown."
	if envelope.Hint != want {
		t.Fatalf("hint = %q, want %q", envelope.Hint, want)
	}
	if strings.ContainsAny(envelope.Hint, "{}") {
		t.Fatalf("hint retained template syntax: %q", envelope.Hint)
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
