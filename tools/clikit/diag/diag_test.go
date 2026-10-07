package diag

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

func TestNewUsesCatalogHint(t *testing.T) {
	d := New("KB_RUNTIME_INPUT_INVALID", "The provided input is invalid.")
	entry, ok := LookupCatalog(d.Code)
	if !ok || d.Hint != entry.Hint {
		t.Fatalf("diagnostic = %+v, want the platform catalog hint", d)
	}
}

func TestWithHintSurvivesBoundaryNormalization(t *testing.T) {
	const want = "Use GET /v1/status, POST /v1/host/restart or POST /v1/update-request."
	d := FromErrorWithLogger(New("KB_RUNTIME_INPUT_INVALID", "Unknown control endpoint.", WithHint(want)), slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)))
	if d.Hint != want {
		t.Fatalf("normalized hint = %q, want the specific next step %q", d.Hint, want)
	}
}

func TestDiagnosticJSONUsesCanonicalEnvelopeAndAcceptsNoPrivateShape(t *testing.T) {
	const hint = "Use GET /v1/status, POST /v1/host/restart or POST /v1/update-request."
	original := New("KB_RUNTIME_INPUT_INVALID", "Unknown control endpoint.", WithHint(hint), WithCause("safe detail"), WithCorrelationID("corr-test"))
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"code", "area", "stage", "severity", "retryable", "message", "hint", "cause", "correlationId"} {
		if _, exists := fields[field]; !exists {
			t.Errorf("canonical field %q missing from %s", field, data)
		}
	}
	var decoded Diag
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("canonical envelope did not decode: %v", err)
	}
	if decoded.Hint != hint || decoded.Cause != "safe detail" || decoded.CorrelationID != "corr-test" {
		t.Fatalf("decoded diagnostic = %+v", decoded)
	}
	if err := json.Unmarshal([]byte(`{"code":"KB_RUNTIME_INPUT_INVALID","message":"old private shape","reason":"old","meta":{}}`), &decoded); err == nil {
		t.Fatal("private diagnostic JSON shape was accepted")
	}
}

func TestFromErrorLogsRawDetailsAndReturnsSafeCorrelatedDiag(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	d := FromErrorWithLogger(errors.New("secret internal failure"), logger)
	if d.Code != "KB_RUNTIME_UNEXPECTED" || d.CorrelationID == "" {
		t.Fatalf("diag = %+v, want unexpected code and correlation ID", d)
	}
	if strings.Contains(d.Message, "secret internal failure") {
		t.Fatalf("user-facing message leaked raw error: %q", d.Message)
	}
	for _, part := range []string{d.CorrelationID, "secret internal failure", "stack"} {
		if !strings.Contains(logs.String(), part) {
			t.Errorf("structured log missing %q: %s", part, logs.String())
		}
	}
}

func TestUncataloguedTypedFailureBecomesUnexpectedAtBoundary(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	d := FromErrorWithLogger(New("KB_RUNTIME_UNREGISTERED", "This code is missing from the catalog."), logger)
	if d.Code != "KB_RUNTIME_UNEXPECTED" || d.CorrelationID == "" {
		t.Fatalf("diag = %+v, want unexpected code and correlation ID", d)
	}
	if !strings.Contains(logs.String(), "correlationId") || !strings.Contains(logs.String(), "stack") {
		t.Fatalf("uncatalogued typed error was not logged with correlation and stack: %s", logs.String())
	}
}

func TestWrap_UnwrapChain(t *testing.T) {
	cause := errors.New("root cause")
	d := Wrap(cause, "KB_RUNTIME_UNEXPECTED", "The operation failed.", WithMeta(map[string]any{"k": "v"}))
	if !errors.Is(d, cause) {
		t.Error("errors.Is should find the wrapped cause")
	}
	if d.Meta["k"] != "v" {
		t.Errorf("meta = %v", d.Meta)
	}
}

func TestExitCodeUsesSharedTable(t *testing.T) {
	cases := []struct {
		name string
		d    *Diag
		want int
	}{
		{"nil", nil, ExitOK},
		{"invalid input", New("KB_RUNTIME_INPUT_INVALID", "invalid"), ExitInput},
		{"operation failure", New("KB_RUNTIME_UNEXPECTED", "failed"), ExitError},
	}
	for _, c := range cases {
		if got := ExitCode(c.d); got != c.want {
			t.Errorf("%s: ExitCode = %d, want %d", c.name, got, c.want)
		}
	}
}

func TestExitCodePreservesWrappedOperationStatus(t *testing.T) {
	d := New("KB_INSTALL_CONFIRMATION_REQUIRED", "Confirmation is required.", WithProcessExitCode(ExitInput))
	if got := ExitCode(d); got != ExitInput {
		t.Fatalf("ExitCode() = %d, want %d", got, ExitInput)
	}
}

func TestDiag_AsError(t *testing.T) {
	var err error = New("KB_RUNTIME_INPUT_INVALID", "invalid")
	var d *Diag
	if !errors.As(err, &d) {
		t.Fatal("errors.As should extract *Diag")
	}
	if d.Code != "KB_RUNTIME_INPUT_INVALID" {
		t.Errorf("code = %q", d.Code)
	}
}
