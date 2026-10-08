package v2cli

import (
	"errors"
	"testing"

	"github.com/kb-labs/clikit/diag"
)

func TestTypedOperationDiagnosticRetainsCanonicalFields(t *testing.T) {
	const hint = "Send exactly one of an exact version or a channel (stable, canary, experimental)."
	original := diag.New("KB_RUNTIME_INPUT_INVALID", "The update request is not valid.",
		diag.WithHint(hint), diag.WithCause("exactly one of version and channel is required"),
		diag.WithCorrelationID("corr-operation"), diag.WithMeta(map[string]any{"field": "channel"}))
	got, ok := runSafely(func() error { return original }).(*diag.Diag)
	if !ok || got.Code != original.Code || got.Hint != hint || got.Cause != original.Cause ||
		got.CorrelationID != original.CorrelationID || got.Meta["field"] != "channel" {
		t.Fatalf("typed operation diagnostic lost fields: %#v", got)
	}
}

func TestUncataloguedOperationDiagnosticBecomesContractError(t *testing.T) {
	unknown := diag.New("KB_PRIVATE_OPERATION_CODE", "operation failed")
	err := runSafely(func() error { return unknown })
	var got *diag.Diag
	if !errors.As(err, &got) || got == nil || got.Code != "KB_RUNTIME_UNEXPECTED" {
		t.Fatalf("uncatalogued operation diagnostic = %#v, want KB_RUNTIME_UNEXPECTED", err)
	}
}
