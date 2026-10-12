package gateway

import (
	"path/filepath"
	"testing"

	"github.com/kb-labs/create/v2/contracts"
	"github.com/kb-labs/create/v2/receipt"
	"github.com/kb-labs/create/v2/versionroot"
)

func installedRootSpellings(t *testing.T, root string) (string, string, string) {
	t.Helper()
	layout, err := versionroot.New(root)
	if err != nil {
		t.Fatal(err)
	}
	stage, err := layout.StageInitial()
	if err != nil {
		t.Fatal(err)
	}
	installationID, err := versionroot.InstallationIDForPath(stage)
	if err != nil {
		t.Fatal(err)
	}
	if err := receipt.Write(stage, contracts.InstallReceipt{Schema: contracts.ReceiptSchema, ID: "gateway-test", InstallationID: installationID,
		Plan: contracts.ResolvedInstallPlan{ServiceGraph: contracts.ServiceGraph{PlatformVersion: "1.0.0"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := layout.ActivateInitial(stage, "1.0.0", "gateway-test"); err != nil {
		t.Fatal(err)
	}
	if err := layout.Validate(); err != nil {
		t.Fatal(err)
	}
	active, err := layout.Active()
	if err != nil {
		t.Fatal(err)
	}
	return layout.InstallRoot(), layout.PlatformRoot(), active
}

func TestAllocateUsesNextHundredPortOffsetAndKeepsRootAddress(t *testing.T) {
	registry := Registry{Path: filepath.Join(t.TempDir(), "launcher", "gateways.json")}
	if err := registry.Register("/platform/a", "http://127.0.0.1:4000"); err != nil {
		t.Fatal(err)
	}
	allocated, err := registry.Allocate("/platform/b", "http://127.0.0.1:4000", "")
	if err != nil || allocated != "http://127.0.0.1:4100" {
		t.Fatalf("allocated = %q, error = %v", allocated, err)
	}
	if err := registry.Register("/platform/b", allocated); err != nil {
		t.Fatal(err)
	}
	stable, err := registry.Allocate("/platform/b", "http://127.0.0.1:4000", "")
	if err != nil || stable != allocated {
		t.Fatalf("existing root allocation = %q, error = %v", stable, err)
	}
}

func TestAllocateHonorsExplicitAddress(t *testing.T) {
	registry := Registry{Path: filepath.Join(t.TempDir(), "launcher", "gateways.json")}
	if err := registry.Register("/platform/a", "http://127.0.0.1:4000"); err != nil {
		t.Fatal(err)
	}
	allocated, err := registry.Allocate("/platform/b", "http://127.0.0.1:4000", "http://127.0.0.1:4200")
	if err != nil || allocated != "http://127.0.0.1:4200" {
		t.Fatalf("explicit Gateway = %q, error = %v", allocated, err)
	}
}

func TestRemoveReleasesAddressForNextRoot(t *testing.T) {
	registry := Registry{Path: filepath.Join(t.TempDir(), "launcher", "gateways.json")}
	if err := registry.Register("/platform/a", "http://127.0.0.1:4000"); err != nil {
		t.Fatal(err)
	}
	if err := registry.Remove("/platform/a"); err != nil {
		t.Fatal(err)
	}
	allocated, err := registry.Allocate("/platform/b", "http://127.0.0.1:4000", "")
	if err != nil || allocated != "http://127.0.0.1:4000" {
		t.Fatalf("allocated after remove = %q, error = %v", allocated, err)
	}
}

func TestStoppedRootKeepsItsGatewayAllocationUntilUninstall(t *testing.T) {
	registry := Registry{Path: filepath.Join(t.TempDir(), "launcher", "gateways.json")}
	if err := registry.RegisterMany([]Root{
		{PlatformRoot: "/platform/a", URL: "http://127.0.0.1:4000"},
		{PlatformRoot: "/platform/b", URL: "http://127.0.0.1:4100"},
	}); err != nil {
		t.Fatal(err)
	}

	stoppedRoot, err := registry.Allocate("/platform/b", "http://127.0.0.1:4000", "")
	if err != nil || stoppedRoot != "http://127.0.0.1:4100" {
		t.Fatalf("stopped root allocation = %q, error = %v; want its recorded address", stoppedRoot, err)
	}
	nextRoot, err := registry.Allocate("/platform/c", "http://127.0.0.1:4000", "")
	if err != nil || nextRoot != "http://127.0.0.1:4200" {
		t.Fatalf("next root allocation = %q, error = %v; want the next offset after the stopped root", nextRoot, err)
	}
}

func TestRegistryUsesInstallationIdentityAcrossRootSpellings(t *testing.T) {
	root := t.TempDir()
	installRoot, currentRoot, versionRoot := installedRootSpellings(t, filepath.Join(root, "platform"))
	registry := Registry{Path: filepath.Join(root, "launcher", "gateways.json")}
	if err := registry.Register(currentRoot, "http://127.0.0.1:4000"); err != nil {
		t.Fatal(err)
	}
	for _, spelling := range []string{installRoot, currentRoot, versionRoot} {
		allocated, err := registry.Allocate(spelling, "http://127.0.0.1:4000", "")
		if err != nil || allocated != "http://127.0.0.1:4000" {
			t.Fatalf("Allocate(%q) = %q, %v; want the existing installation address", spelling, allocated, err)
		}
		if _, claimed, err := registry.Claimant(spelling, allocated); err != nil || claimed {
			t.Fatalf("Claimant(%q) = claimed %t, error %v; installation cannot claim its own address", spelling, claimed, err)
		}
	}
	if err := registry.Register(versionRoot, "http://127.0.0.1:4100"); err != nil {
		t.Fatal(err)
	}
	roots, err := registry.Read()
	if err != nil || len(roots) != 1 || roots[0].URL != "http://127.0.0.1:4100" {
		t.Fatalf("registry after alias update = %#v, %v; want one updated installation", roots, err)
	}
	if err := registry.Remove(installRoot); err != nil {
		t.Fatal(err)
	}
	roots, err = registry.Read()
	if err != nil || len(roots) != 0 {
		t.Fatalf("registry after install-root removal = %#v, %v; want empty", roots, err)
	}
}
