package diag

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

func TestNewUsesPlatformCodeAndHint(t *testing.T) {
	d := New("KB_RUNTIME_INPUT_INVALID", "The provided input is invalid.")
	if d.Code != "KB_RUNTIME_INPUT_INVALID" || d.Hint == "" {
		t.Fatalf("diagnostic = %+v, want platform code and actionable hint", d)
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

func TestPlatformCodeMapsLegacyCodesToCatalog(t *testing.T) {
	d := New("ERR_CONFIG_NOT_FOUND", "configuration missing")
	if d.Code != "ERR_CONFIG_NOT_FOUND" {
		t.Fatalf("diagnostic code = %q, want original typed code for internal consumers", d.Code)
	}
	if code := PlatformCode(d.Code); code != "KB_RUNTIME_UNEXPECTED" {
		t.Fatalf("platform code = %q, want KB_RUNTIME_UNEXPECTED", code)
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
