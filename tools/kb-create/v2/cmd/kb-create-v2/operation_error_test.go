package v2cli

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/kb-labs/clikit/diag"
)

func TestOperationResultReadsCanonicalDiagnosticWithoutDroppingFields(t *testing.T) {
	const hint = "Send exactly one of an exact version or a channel (stable, canary, experimental)."
	original := diag.New("KB_RUNTIME_INPUT_INVALID", "The update request is not valid.",
		diag.WithHint(hint), diag.WithCause("exactly one of version and channel is required"),
		diag.WithCorrelationID("corr-operation"), diag.WithMeta(map[string]any{"field": "channel"}))
	data, err := json.Marshal(map[string]any{"ok": false, "error": original})
	if err != nil {
		t.Fatal(err)
	}
	parsed := errorFromOperationResult(data, diag.ExitError)
	var got *diag.Diag
	if !errors.As(parsed, &got) {
		t.Fatalf("operation result error = %T, want *diag.Diag", parsed)
	}
	if got.Code != original.Code || got.Hint != hint || got.Cause != original.Cause || got.CorrelationID != original.CorrelationID || got.Meta["field"] != "channel" {
		t.Fatalf("operation diagnostic lost canonical fields: got %#v, want %#v", got, original)
	}

	inputError := errorFromOperationResult(data, diag.ExitInput)
	var inputDiag *diag.Diag
	if !errors.As(inputError, &inputDiag) || inputDiag.Code != "KB_RUNTIME_INPUT_INVALID" || inputDiag.Hint != hint || inputDiag.Cause != original.Cause {
		t.Fatalf("invalid input diagnostic = %#v", inputError)
	}
}

func TestOperationResultDoesNotAcceptPrivateDiagnosticJSON(t *testing.T) {
	legacy := []byte(`{"ok":false,"error":{"code":"KB_RUNTIME_INPUT_INVALID","message":"old shape","reason":"private","hint":"retry"}}`)
	if err := errorFromOperationResult(legacy, diag.ExitError); err == nil || err.Error() != "kb-create command failed unexpectedly" {
		t.Fatalf("private diagnostic JSON was accepted: %v", err)
	}
}
