package toolchain

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"time"

	"github.com/kb-labs/clikit/diag"
)

// case: S-260.4
func TestNodePolicyRejectsOlderAndNewerMajorsWithCatalogDiagnostic(t *testing.T) {
	if err := ValidateNode(fmt.Sprintf("v%d.18.0", SupportedNodeMajor)); err != nil {
		t.Fatal(err)
	}
	for _, major := range []int{SupportedNodeMajor - 4, SupportedNodeMajor + 2} {
		version := fmt.Sprintf("v%d.0.0", major)
		err := ValidateNode(version)
		if err == nil {
			t.Fatalf("%s must be rejected", version)
		}
		var failure *diag.Diag
		if !errors.As(err, &failure) {
			t.Fatalf("%s returned %T, want catalogued diagnostic", version, err)
		}
		envelope, err := diag.ToEnvelope(failure)
		if err != nil {
			t.Fatal(err)
		}
		entry, ok := diag.LookupCatalog("KB_NODE_VERSION_UNSUPPORTED")
		if !ok {
			t.Fatal("Node version diagnostic is absent from the catalog")
		}
		if envelope.Code != entry.Code || envelope.Details["requiredMajor"] != fmt.Sprint(SupportedNodeMajor) || envelope.Details["actualVersion"] != fmt.Sprintf("%d.0.0", major) || len(envelope.Details) != 2 {
			t.Fatalf("unexpected diagnostic envelope or undeclared details: %#v", envelope)
		}
		values := map[string]string{"requiredMajor": fmt.Sprint(SupportedNodeMajor), "actualVersion": fmt.Sprintf("%d.0.0", major)}
		expected, err := diag.FromCatalog(entry.Code, values)
		if err != nil {
			t.Fatal(err)
		}
		expectedEnvelope, err := diag.ToEnvelope(expected)
		if err != nil {
			t.Fatal(err)
		}
		if envelope.Message != expectedEnvelope.Message || envelope.Hint != expectedEnvelope.Hint {
			t.Fatalf("message or hint differs from the contract-checked catalog entry: %#v", envelope)
		}
	}
}

// case: S-261.6
func TestExternalToolStallReturnsCataloguedDiagnostic(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture requires POSIX")
	}
	tool := filepath.Join(t.TempDir(), "never-exits")
	if err := os.WriteFile(tool, []byte("#!/bin/sh\nprintf 'stalled-tool-output\\n' >&2\nexec sleep 30\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	_, err := Run(context.Background(), Invocation{Tool: "fixture", Executable: tool, Args: []string{"--wait"}, Operation: OperationVersionProbe})
	if err == nil {
		t.Fatal("stalled process returned success")
	}
	var failure *diag.Diag
	if !errors.As(err, &failure) || failure.Code != "KB_TOOLCHAIN_EXTERNAL_TIMEOUT" {
		t.Fatalf("stall failure = %#v", err)
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("stall bound took %s", elapsed)
	}
	for key, want := range map[string]string{"tool": "fixture", "argsSummary": "--wait", "stderrTail": "stalled-tool-output", "nextCommand": "'" + tool + "' '--wait'"} {
		if got := fmt.Sprint(failure.Meta[key]); !strings.Contains(got, want) {
			t.Errorf("timeout detail %s = %q, want %q", key, got, want)
		}
	}
}

// case: S-261.6
func TestExternalToolFailureKeepsExitCodeAndStderrTail(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture requires POSIX")
	}
	tool := filepath.Join(t.TempDir(), "failing-tool")
	if err := os.WriteFile(tool, []byte("#!/bin/sh\nprintf 'external-tool-stderr-tail\\n' >&2\nexit 47\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	_, err := Run(context.Background(), Invocation{Tool: "fixture", Executable: tool, Operation: OperationVersionProbe})
	var failure *CommandError
	if !errors.As(err, &failure) {
		t.Fatalf("external failure = %v, want CommandError", err)
	}
	if failure.ExitCode != 47 || failure.StderrTail != "external-tool-stderr-tail" {
		t.Fatalf("command failure = %#v", failure)
	}
}

// case: S-261.6
func TestPnpmResolutionUsesCapturedOuterCorepackCacheAndFailsWithoutDownload(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	_, _, err := resolvePnpmAt(cache)
	var failure *diag.Diag
	if !errors.As(err, &failure) || failure.Code != "KB_TOOLCHAIN_PNPM_UNAVAILABLE" {
		t.Fatalf("missing cached pnpm = %v", err)
	}
	if failure.Hint == "" || !strings.Contains(failure.Hint, "corepack install") {
		t.Fatalf("missing exact cache remediation: %q", failure.Hint)
	}
	script := filepath.Join(cache, "v1", "pnpm", SupportedPnpm, "bin", "pnpm.cjs")
	if err := os.MkdirAll(filepath.Dir(script), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(script, []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	_, got, err := resolvePnpmAt(cache)
	if err != nil || got != script {
		t.Fatalf("cached pnpm = %q, %v; want %q", got, err, script)
	}
}

// case: S-260.4
func TestNodeRemediationRendersTheCatalogHint(t *testing.T) {
	entry, ok := diag.LookupCatalog("KB_NODE_VERSION_UNSUPPORTED")
	if !ok {
		t.Fatal("Node version diagnostic is absent from the catalog")
	}
	expected, err := diag.FromCatalog(entry.Code, map[string]string{
		"requiredMajor": fmt.Sprint(SupportedNodeMajor), "actualVersion": "unknown",
	})
	if err != nil {
		t.Fatal(err)
	}
	expectedEnvelope, err := diag.ToEnvelope(expected)
	if err != nil {
		t.Fatal(err)
	}
	want := expectedEnvelope.Hint
	if got := NodeRemediation(); got != want {
		t.Fatalf("NodeRemediation() = %q; want catalog hint %q", got, want)
	}
}
