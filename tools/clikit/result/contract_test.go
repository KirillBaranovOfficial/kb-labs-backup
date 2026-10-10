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

// case: S-203.1
func TestDecodeSuccessResultRequiresTheSharedEnvelope(t *testing.T) {
	var payload struct {
		Status string `json:"status"`
	}
	if err := DecodeSuccessResult([]byte(`{"ok":true,"data":{"status":"healthy"}}`), &payload); err != nil {
		t.Fatalf("DecodeSuccessResult(valid envelope): %v", err)
	}
	if payload.Status != "healthy" {
		t.Fatalf("payload status = %q, want healthy", payload.Status)
	}
	for _, body := range []string{
		`{"status":"healthy"}`,
		`{"ok":false,"error":{}}`,
	} {
		if err := DecodeSuccessResult([]byte(body), &payload); err == nil {
			t.Errorf("DecodeSuccessResult(%s) succeeded, want envelope error", body)
		}
	}
}

func TestDecodeResultReturnsCataloguedFailureEnvelope(t *testing.T) {
	body := []byte(`{"ok":false,"error":{"code":"KB_AUTH_FORBIDDEN","area":"auth","stage":"run","severity":"error","retryable":false,"message":"Gateway auth health is unavailable.","hint":"Run kb-create doctor --platform-root <root>.","correlationId":"corr-auth-health"}}`)
	var payload struct{ Status string `json:"status"` }
	failure, err := DecodeResult(body, &payload)
	if err != nil {
		t.Fatalf("DecodeResult(failure): %v", err)
	}
	if failure == nil || failure.Code != "KB_AUTH_FORBIDDEN" || failure.Message != "Gateway auth health is unavailable." || failure.Hint != "Run kb-create doctor --platform-root <root>." {
		t.Fatalf("failure = %#v, want catalogued Gateway diagnostic", failure)
	}
}

func TestDecodeResultRejectsMalformedFailureEnvelope(t *testing.T) {
	var payload struct{}
	if failure, err := DecodeResult([]byte(`{"ok":false,"error":{"code":"KB_NOT_CATALOGUED","message":"bad","hint":"retry"}}`), &payload); err == nil || failure != nil {
		t.Fatalf("DecodeResult(malformed failure) = (%#v, %v), want validation error", failure, err)
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
