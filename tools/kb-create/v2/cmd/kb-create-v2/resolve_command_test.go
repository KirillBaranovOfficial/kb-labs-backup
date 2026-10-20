package v2cli

import (
	"testing"

	"github.com/kb-labs/clikit/diag"
	"github.com/kb-labs/create/v2/contracts"
)

// case: PC-001.9
func TestGatewayAddressClaimFailureCarriesCatalogPortValue(t *testing.T) {
	failure, err := gatewayAddressClaimConflict("http://127.0.0.1:48100", "/installed/platform", "plan")
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := diag.ToEnvelope(failure)
	if err != nil {
		t.Fatal(err)
	}
	if envelope.Code != contracts.CodePortInUse || envelope.Details["port"] != "48100" {
		t.Fatalf("wire diagnostic = %+v, want KB_INSTALL_PORT_IN_USE with details.port=48100", envelope)
	}
}
