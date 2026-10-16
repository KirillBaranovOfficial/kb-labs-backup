package testfixtures

import (
	"encoding/json"
	"testing"

	"github.com/kb-labs/create/v2/artifacts"
	"github.com/kb-labs/create/v2/catalog"
	"github.com/kb-labs/create/v2/contracts"
)

func TestReleaseIndexJSONSealsWithPlatformSDKAndBinaryRelations(t *testing.T) {
	var source catalog.Catalog
	if err := json.Unmarshal(ReleaseIndexJSON("3.2.0", "3.0.0", "linux", "amd64"), &source); err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.Seal(source); err != nil {
		t.Fatalf("shared release-index fixture is invalid: %v", err)
	}
	for _, item := range []struct{ name, version string }{
		{source.Platforms[0].Package, source.Platforms[0].Version},
		{source.SDKs[0].Package, source.SDKs[0].Version},
	} {
		var artifactProvenance catalog.ArtifactProvenance
		for _, candidate := range source.Provenance {
			if candidate.Package == item.name && candidate.Version == item.version {
				artifactProvenance = candidate
				break
			}
		}
		if err := artifacts.ValidatePackageProvenance(contracts.Artifact{
			Package: item.name, Version: item.version, Provenance: artifactProvenance.ArtifactProvenance,
		}, "release-index-fixture"); err != nil {
			t.Fatalf("shared release-index fixture omitted sealed provenance for %s@%s: %v", item.name, item.version, err)
		}
	}
}
