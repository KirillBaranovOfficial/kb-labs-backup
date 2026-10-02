package result

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/kb-labs/clikit/diag"
)

// ValidateFixture checks the shared fixtures using the same envelope shape
// that Render emits. The shared JSON Schema remains the canonical contract.
func ValidateFixture(path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return ValidateMachineResult(b)
}

func ValidateMachineResult(b []byte) error {
	var env map[string]json.RawMessage
	if err := json.Unmarshal(b, &env); err != nil {
		return err
	}
	var ok bool
	if err := json.Unmarshal(env["ok"], &ok); err != nil {
		return fmt.Errorf("missing boolean ok: %w", err)
	}
	if ok {
		if len(env) < 2 || len(env) > 3 || env["data"] == nil {
			return fmt.Errorf("success must contain only ok, data, and optional meta")
		}
		if _, exists := env["error"]; exists {
			return fmt.Errorf("success contains error")
		}
		return nil
	}
	if len(env) != 2 || env["error"] == nil {
		return fmt.Errorf("failure must contain only ok and error")
	}
	var e map[string]json.RawMessage
	if err := json.Unmarshal(env["error"], &e); err != nil {
		return err
	}
	for _, key := range []string{"code", "message", "hint", "correlationId"} {
		if e[key] == nil {
			return fmt.Errorf("failure missing %s", key)
		}
	}
	var code string
	if err := json.Unmarshal(e["code"], &code); err != nil || !strings.HasPrefix(code, "KB_") {
		return fmt.Errorf("failure code must be a KB_* string")
	}
	return nil
}

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
