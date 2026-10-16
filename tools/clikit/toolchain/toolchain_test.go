package toolchain

import (
	"errors"
	"fmt"
	"testing"

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
