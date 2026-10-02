package result

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	"github.com/kb-labs/clikit/diag"
)

// ValidateFixture checks the shared fixtures using the same envelope shape
// that Render emits. The shared JSON Schema remains the canonical contract.
func TestSharedFixturesAndSchema(t *testing.T) {
	for _, path := range []string{
		"../../../contracts/ux-result/fixtures/valid/success.json",
		"../../../contracts/ux-result/fixtures/valid/failure.json",
	} {
		if err := ValidateFixture(path); err != nil {
			t.Errorf("fixture %s: %v", path, err)
		}
	}
	var schema struct {
		OneOf []json.RawMessage `json:"oneOf"`
	}
	b, err := os.ReadFile("../../../contracts/ux-result/machine-result.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &schema); err != nil {
		t.Fatal(err)
	}
	if len(schema.OneOf) != 2 {
		t.Fatalf("shared schema has %d result branches, want success and failure", len(schema.OneOf))
	}
}

// case: S-203.1, S-203.2
func TestRenderResultsMatchSharedContract(t *testing.T) {
	var success bytes.Buffer
	Render(&success, Success("done", nil), ModeJSON)
	if err := ValidateMachineResult(success.Bytes()); err != nil {
		t.Errorf("rendered success: %v", err)
	}
	var failure, human bytes.Buffer
	RenderDiag(&failure, &human, diag.New("KB_RUNTIME_UNEXPECTED", "The operation failed.", diag.WithCorrelationID("corr-test")), ModeJSON)
	if err := ValidateMachineResult(failure.Bytes()); err != nil {
		t.Errorf("rendered failure: %v", err)
	}
}
