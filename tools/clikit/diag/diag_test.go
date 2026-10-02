package diag

import (
	"bytes"
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
