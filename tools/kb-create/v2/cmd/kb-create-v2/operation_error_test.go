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

func TestOperationResultPreservesCataloguedCodeWhenEnvelopeHasExtraFields(t *testing.T) {
	legacy := []byte(`{"ok":false,"error":{"code":"KB_RUNTIME_INPUT_INVALID","message":"old shape","reason":"private","hint":"retry"}}`)
	var got *diag.Diag
	if err := errorFromOperationResult(legacy, diag.ExitError); !errors.As(err, &got) || got.Code != "KB_RUNTIME_INPUT_INVALID" || got.Message != "old shape" {
		t.Fatalf("catalogued operation diagnostic was not preserved: %#v", err)
	}
}

func TestOperationResultPreservesExitInputForCataloguedDiagnostic(t *testing.T) {
	original := diag.New("KB_INSTALL_CONFIRMATION_REQUIRED", "This destructive operation requires explicit confirmation.",
		diag.WithHint("rerun with --yes"))
	data, err := json.Marshal(map[string]any{"ok": false, "error": original})
	if err != nil {
		t.Fatal(err)
	}
	parsed := errorFromOperationResult(data, diag.ExitInput)
	var got *diag.Diag
	if !errors.As(parsed, &got) || got.Code != original.Code || diag.ExitCode(got) != diag.ExitInput {
		t.Fatalf("operation diagnostic = %#v, exit = %d; want catalogued code and exit %d", parsed, diag.ExitCode(got), diag.ExitInput)
	}
}

// case: PC-001.9
func TestOperationResultPreservesCataloguedInputCodesAndMapsUnknownCodes(t *testing.T) {
	for _, code := range []string{"KB_INSTALL_PORT_IN_USE", "KB_INSTALL_INPUT_REQUIRED"} {
		t.Run(code, func(t *testing.T) {
			original := diag.New(code, "operation validation failed", diag.WithHint("fix the input"), diag.WithMeta(map[string]any{"field": "x"}))
			data, err := json.Marshal(map[string]any{"ok": false, "error": original})
			if err != nil {
				t.Fatal(err)
			}
			parsed := errorFromOperationResult(data, diag.ExitInput)
			var got *diag.Diag
			if !errors.As(parsed, &got) || got.Code != code || got.Meta["field"] != "x" || got.Hint != original.Hint {
				t.Fatalf("catalogued input diagnostic = %#v, want preserved %#v", parsed, original)
			}
		})
	}

	data := []byte(`{"ok":false,"error":{"code":"KB_PRIVATE_OPERATION_CODE","message":"operation validation failed","hint":"fix the input","meta":{"field":"x"}}}`)
	parsed := errorFromOperationResult(data, diag.ExitInput)
	var got *diag.Diag
	if !errors.As(parsed, &got) || got.Code != "KB_RUNTIME_INPUT_INVALID" || got.Meta["field"] != "x" || got.Hint != "fix the input" {
		t.Fatalf("uncatalogued input diagnostic = %#v, want generic wrapper", parsed)
	}
}
