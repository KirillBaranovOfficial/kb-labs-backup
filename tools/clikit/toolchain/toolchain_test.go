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
		if envelope.Code != entry.Code || envelope.Details["requiredMajor"] != fmt.Sprint(SupportedNodeMajor) || envelope.Details["actualVersion"] != fmt.Sprintf("%d.0.0", major) {
			t.Fatalf("unexpected diagnostic envelope: %#v", envelope)
		}
		values := map[string]string{"requiredMajor": fmt.Sprint(SupportedNodeMajor), "actualVersion": fmt.Sprintf("%d.0.0", major)}
		if envelope.Message != diag.RenderTemplate(entry.Message, values) || envelope.Hint != diag.RenderTemplate(entry.Hint, values) {
			t.Fatalf("message or hint differs from the rendered catalog entry: %#v", envelope)
		}
	}
}

// case: S-260.4
func TestNodeRemediationRendersTheCatalogHint(t *testing.T) {
	entry, ok := diag.LookupCatalog("KB_NODE_VERSION_UNSUPPORTED")
	if !ok {
		t.Fatal("Node version diagnostic is absent from the catalog")
	}
	want := diag.RenderTemplate(entry.Hint, map[string]string{"requiredMajor": fmt.Sprint(SupportedNodeMajor)})
	if got := NodeRemediation(); got != want {
		t.Fatalf("NodeRemediation() = %q; want catalog hint %q", got, want)
	}
}
