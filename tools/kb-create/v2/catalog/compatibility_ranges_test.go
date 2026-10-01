package catalog

import (
	"testing"

	"github.com/kb-labs/create/v2/contracts"
)

func TestSealRejectsUnsupportedCompatibilityRanges(t *testing.T) {
	source := Catalog{
		Channels:  map[contracts.Channel]string{contracts.ChannelStable: "3.0.0"},
		Platforms: []PlatformBundle{{ID: "platform", Version: "3.0.0", Package: "@kb/platform", SHA256: "platform", Tarball: "platform.tgz", SDKRange: "^3", Host: &contracts.HostSpec{Command: "kb-host", HealthURL: "http://127.0.0.1:4000/health"}, Profiles: map[string]contracts.ServiceGraph{"default": {}}}},
	}
	if _, err := Seal(source); err == nil {
		t.Fatal("Seal accepted an unsupported range")
	}
}
