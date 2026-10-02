package result

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/kb-labs/clikit/diag"
)

func TestSuccess_Shape(t *testing.T) {
	o := Success("done", map[string]any{"n": 1})
	if !o.Ok || o.Status != StatusSuccess || o.Human != "done" {
		t.Errorf("Success shape = %+v", o)
	}
}

// WithWarnings is exercised in production (reconcile_devservices) but was
// previously untested. It must: collect warnings, skip nils, and downgrade a
// success to "warning" — without touching a non-success status.
func TestWithWarnings_DowngradesSuccessToWarning(t *testing.T) {
	o := Success("ok", nil).WithWarnings(diag.New("ERR_DEP_PRUNED", "pruned dep"))
	if o.Status != StatusWarning {
		t.Errorf("status = %q, want warning", o.Status)
	}
	if len(o.Warnings) != 1 {
		t.Fatalf("warnings = %d, want 1", len(o.Warnings))
	}
}

func TestWithWarnings_SkipsNil(t *testing.T) {
	o := Success("ok", nil).WithWarnings(nil, diag.New("ERR_W", "w"), nil)
	if len(o.Warnings) != 1 {
		t.Errorf("warnings = %d, want 1 (nils skipped)", len(o.Warnings))
	}
}

func TestWithWarnings_NoWarningsKeepsSuccess(t *testing.T) {
	o := Success("ok", nil).WithWarnings()
	if o.Status != StatusSuccess {
		t.Errorf("status = %q, want unchanged success", o.Status)
	}
}

func TestWithWarnings_DoesNotDowngradeNonSuccess(t *testing.T) {
	// A command that already failed keeps its status even with warnings.
	o := CommandOutput{Ok: false, Status: StatusError}.WithWarnings(diag.New("ERR_W", "w"))
	if o.Status != StatusError {
		t.Errorf("status = %q, want error preserved", o.Status)
	}
}

// Warnings are no longer emitted as a parallel root-level protocol field.
func TestRender_JSONHasOnlySharedSuccessFields(t *testing.T) {
	o := Success("ok", nil).WithWarnings(
		diag.New("ERR_DEP_PRUNED", "pruned", diag.WithMeta(map[string]any{"dep": "redis"})),
	)
	var b bytes.Buffer
	Render(&b, o, ModeJSON)

	var env map[string]any
	if err := json.Unmarshal(b.Bytes(), &env); err != nil {
		t.Fatalf("not json: %v", err)
	}
	if len(env) != 3 || env["ok"] != true || env["meta"] == nil {
		t.Errorf("success envelope = %v, want ok, data, and warning metadata", env)
	}
}

// Agent mode must use the same protocol as JSON mode.
func TestRender_AgentUsesSharedEnvelope(t *testing.T) {
	o := Success("h", map[string]any{"full": true})
	o.Agent = map[string]any{"compact": "yes"}
	var b bytes.Buffer
	Render(&b, o, ModeAgent)

	if !strings.Contains(b.String(), `"data"`) || !strings.Contains(b.String(), `"full"`) {
		t.Errorf("agent output should use shared envelope, got %q", b.String())
	}
	if strings.Contains(b.String(), `"compact"`) {
		t.Errorf("agent mode must not create a parallel protocol, got %q", b.String())
	}
}

// Success with no value serializes data as null.
func TestRender_JSONIncludesNullData(t *testing.T) {
	var b bytes.Buffer
	Render(&b, Success("h", nil), ModeJSON)
	if !strings.Contains(b.String(), `"data": null`) {
		t.Errorf("envelope should include null data, got %q", b.String())
	}
}

// The error path must tolerate a nil *Diag (a typed-nil error reaching the
// renderer) — no panic, exit OK, empty render.
func TestRenderDiag_NilIsSafe(t *testing.T) {
	for _, mode := range []Mode{ModeHuman, ModeJSON, ModeAgent} {
		var out, herr bytes.Buffer
		if code := RenderDiag(&out, &herr, nil, mode); code != diag.ExitOK {
			t.Errorf("mode %d: nil diag exit = %d, want %d", mode, code, diag.ExitOK)
		}
	}
}

func TestRenderDiag_InvalidInputUsesExitTwo(t *testing.T) {
	d := diag.New("KB_RUNTIME_INPUT_INVALID", "The provided input is invalid.")
	var out, herr bytes.Buffer
	if code := RenderDiag(&out, &herr, d, ModeAgent); code != diag.ExitInput {
		t.Errorf("agent RenderDiag exit = %d, want %d", code, diag.ExitInput)
	}
}

// --output is normalized: surrounding whitespace and case must not matter.
func TestResolveMode_NormalizesOutputFlag(t *testing.T) {
	cases := []struct {
		out  string
		want Mode
	}{
		{"  JSON ", ModeJSON},
		{"Agent", ModeAgent},
		{"\tHUMAN\n", ModeHuman},
		{"garbage", ModeHuman}, // unknown → falls through to flag/default
	}
	for _, c := range cases {
		if got := ResolveMode(false, false, c.out); got != c.want {
			t.Errorf("ResolveMode(out=%q) = %d, want %d", c.out, got, c.want)
		}
	}
}
