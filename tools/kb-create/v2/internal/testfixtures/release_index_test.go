package testfixtures

import (
	"encoding/json"
	"testing"

	"github.com/kb-labs/create/v2/catalog"
)

func TestReleaseIndexJSONSealsWithPlatformSDKAndBinaryRelations(t *testing.T) {
	var source catalog.Catalog
	if err := json.Unmarshal(ReleaseIndexJSON("3.2.0", "3.0.0", "linux", "amd64"), &source); err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.Seal(source); err != nil {
		t.Fatalf("shared release-index fixture is invalid: %v", err)
	}
}
