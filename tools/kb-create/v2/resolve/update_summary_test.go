package resolve

import (
	"testing"

	"github.com/kb-labs/create/v2/catalog"
	"github.com/kb-labs/create/v2/contracts"
)

// case: S-023.2
func TestUpdateSummaryComparesArtifactsAndIncludesInterveningReleaseNotes(t *testing.T) {
	active := contracts.InstallReceipt{Plan: contracts.ResolvedInstallPlan{
		ServiceGraph: contracts.ServiceGraph{PlatformVersion: "2.0.0"},
		Artifacts: []contracts.Artifact{
			{ID: "platform", Kind: "platform", Package: "@kb/platform", Version: "2.0.0", SizeBytes: 20},
			{ID: "runtime", Kind: "core", Package: "@kb/runtime", Version: "2.0.0", SizeBytes: 30},
			{ID: "retired", Kind: "platform-member", Package: "@kb/retired", Version: "1.0.0"},
		},
	}}
	target := contracts.ResolvedInstallPlan{
		ServiceGraph: contracts.ServiceGraph{PlatformVersion: "2.0.2"},
		Artifacts: []contracts.Artifact{
			{ID: "platform", Kind: "platform", Package: "@kb/platform", Version: "2.0.2", SizeBytes: 25},
			{ID: "runtime", Kind: "core", Package: "@kb/runtime", Version: "2.0.1", SizeBytes: 35},
			{ID: "new", Kind: "platform-member", Package: "@kb/new", Version: "1.0.0", SizeBytes: 40},
		},
	}
	source := catalog.Catalog{Platforms: []catalog.PlatformBundle{
		{ID: "platform", Version: "2.0.1", ReleaseNotes: "Release 2.0.1 notes."},
		{ID: "platform", Version: "2.0.2", ReleaseNotes: "Release 2.0.2 notes."},
		{ID: "platform", Version: "2.1.0", ReleaseNotes: "Later release notes."},
	}}

	summary, err := SummarizeUpdate(active, target, source)
	if err != nil {
		t.Fatal(err)
	}
	if summary.DownloadBytes != 100 {
		t.Fatalf("downloadBytes = %d, want 100", summary.DownloadBytes)
	}
	if len(summary.Components) != 4 {
		t.Fatalf("components = %#v", summary.Components)
	}
	versions := map[string][2]string{}
	for _, component := range summary.Components {
		versions[component.Package] = [2]string{component.FromVersion, component.ToVersion}
	}
	if versions["@kb/platform"] != [2]string{"2.0.0", "2.0.2"} ||
		versions["@kb/runtime"] != [2]string{"2.0.0", "2.0.1"} ||
		versions["@kb/new"] != [2]string{"", "1.0.0"} ||
		versions["@kb/retired"] != [2]string{"1.0.0", ""} {
		t.Fatalf("component versions = %#v", versions)
	}
	if len(summary.AddedPackages) != 1 || summary.AddedPackages[0] != "@kb/new" ||
		len(summary.RemovedPackages) != 1 || summary.RemovedPackages[0] != "@kb/retired" {
		t.Fatalf("package delta = added %#v, removed %#v", summary.AddedPackages, summary.RemovedPackages)
	}
	if len(summary.ReleaseNotes) != 2 || summary.ReleaseNotes[0].Version != "2.0.1" || summary.ReleaseNotes[1].Version != "2.0.2" {
		t.Fatalf("intervening release notes = %#v", summary.ReleaseNotes)
	}
}
