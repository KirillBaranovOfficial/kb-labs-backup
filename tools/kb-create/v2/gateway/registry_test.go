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
	root := t.TempDir()
	rootA, _, _ := installedRootSpellings(t, filepath.Join(root, "a"))
	rootB, _, _ := installedRootSpellings(t, filepath.Join(root, "b"))
	registry := Registry{Path: filepath.Join(t.TempDir(), "launcher", "gateways.json")}
	if err := registry.Register(rootA, "http://127.0.0.1:4000"); err != nil {
		t.Fatal(err)
	}
	allocated, err := registry.Allocate(rootB, "http://127.0.0.1:4000", "")
	if err != nil || allocated != "http://127.0.0.1:4100" {
		t.Fatalf("allocated = %q, error = %v", allocated, err)
	}
	if err := registry.Register(rootB, allocated); err != nil {
		t.Fatal(err)
	}
	stable, err := registry.Allocate(rootB, "http://127.0.0.1:4000", "")
	if err != nil || stable != allocated {
		t.Fatalf("existing root allocation = %q, error = %v", stable, err)
	}
}

func TestAllocateHonorsExplicitAddress(t *testing.T) {
	root := t.TempDir()
	rootA, _, _ := installedRootSpellings(t, filepath.Join(root, "a"))
	rootB, _, _ := installedRootSpellings(t, filepath.Join(root, "b"))
	registry := Registry{Path: filepath.Join(t.TempDir(), "launcher", "gateways.json")}
	if err := registry.Register(rootA, "http://127.0.0.1:4000"); err != nil {
		t.Fatal(err)
	}
	allocated, err := registry.Allocate(rootB, "http://127.0.0.1:4000", "http://127.0.0.1:4200")
	if err != nil || allocated != "http://127.0.0.1:4200" {
		t.Fatalf("explicit Gateway = %q, error = %v", allocated, err)
	}
}

func TestRemoveReleasesAddressForNextRoot(t *testing.T) {
	root := t.TempDir()
	rootA, _, _ := installedRootSpellings(t, filepath.Join(root, "a"))
	rootB, _, _ := installedRootSpellings(t, filepath.Join(root, "b"))
	registry := Registry{Path: filepath.Join(t.TempDir(), "launcher", "gateways.json")}
	if err := registry.Register(rootA, "http://127.0.0.1:4000"); err != nil {
		t.Fatal(err)
	}
	if err := registry.Remove(rootA); err != nil {
		t.Fatal(err)
	}
	allocated, err := registry.Allocate(rootB, "http://127.0.0.1:4000", "")
	if err != nil || allocated != "http://127.0.0.1:4000" {
		t.Fatalf("allocated after remove = %q, error = %v", allocated, err)
	}
}

func TestStoppedRootKeepsItsGatewayAllocationUntilUninstall(t *testing.T) {
	base := t.TempDir()
	rootA, _, _ := installedRootSpellings(t, filepath.Join(base, "a"))
	rootB, _, _ := installedRootSpellings(t, filepath.Join(base, "b"))
	rootC, _, _ := installedRootSpellings(t, filepath.Join(base, "c"))
	registry := Registry{Path: filepath.Join(t.TempDir(), "launcher", "gateways.json")}
	if err := registry.RegisterMany([]Root{
		{PlatformRoot: rootA, URL: "http://127.0.0.1:4000"},
		{PlatformRoot: rootB, URL: "http://127.0.0.1:4100"},
	}); err != nil {
		t.Fatal(err)
	}

	stoppedRoot, err := registry.Allocate(rootB, "http://127.0.0.1:4000", "")
	if err != nil || stoppedRoot != "http://127.0.0.1:4100" {
		t.Fatalf("stopped root allocation = %q, error = %v; want its recorded address", stoppedRoot, err)
	}
	nextRoot, err := registry.Allocate(rootC, "http://127.0.0.1:4000", "")
	if err != nil || nextRoot != "http://127.0.0.1:4200" {
		t.Fatalf("next root allocation = %q, error = %v; want the next offset after the stopped root", nextRoot, err)
	}
}

func TestSameInstallationRejectsUnvalidatablePathsIncludingEqualSpellings(t *testing.T) {
	if SameInstallation("/missing/one", "/missing/one") {
		t.Fatal("equal unvalidatable paths compared as an installation identity")
	}
	if SameInstallation("/missing/one", "/missing/two") {
		t.Fatal("different unvalidatable paths compared as one installation")
	}
}

func TestUnvalidatablePathCannotClaimItsFormerGatewayRow(t *testing.T) {
	registry := Registry{Path: filepath.Join(t.TempDir(), "gateway-roots.json")}
	platformRoot := filepath.Join(t.TempDir(), "platform")
	installRoot, _, _ := installedRootSpellings(t, platformRoot)
	if err := registry.Register(installRoot, "http://127.0.0.1:4100"); err != nil {
		t.Fatal(err)
	}
	layout, err := versionroot.ForPlatformPath(installRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := layout.RemoveOwned(); err != nil {
		t.Fatal(err)
	}
	if SameInstallation(installRoot, installRoot) {
		t.Fatal("unvalidatable path matched its stale Gateway row by path spelling")
	}
	allocated, err := registry.Allocate(installRoot, "http://127.0.0.1:4000", "")
	if err != nil || allocated != "http://127.0.0.1:4000" {
		t.Fatalf("allocation for unvalidatable former root = %q, %v; want an independent allocation", allocated, err)
	}
	claimant, claimed, err := registry.Claimant(installRoot, "http://127.0.0.1:4100")
	if err != nil || !claimed || claimant.URL != "http://127.0.0.1:4100" {
		t.Fatalf("stale row claimant = %#v, %v, %v; want the recorded row to retain its address claim", claimant, claimed, err)
	}
}

func TestUninstallRemovesGatewayRowByCapturedInstallationIdentity(t *testing.T) {
	base := t.TempDir()
	installRoot, _, _ := installedRootSpellings(t, filepath.Join(base, "platform"))
	layout, err := versionroot.ForPlatformPath(installRoot)
	if err != nil {
		t.Fatal(err)
	}
	installationID, err := versionroot.InstallationIDForPath(installRoot)
	if err != nil {
		t.Fatal(err)
	}
	registry := Registry{Path: filepath.Join(base, "launcher", "gateways.json")}
	if err := registry.Register(installRoot, "http://127.0.0.1:4000"); err != nil {
		t.Fatal(err)
	}
	if err := layout.RemoveOwned(); err != nil {
		t.Fatal(err)
	}
	if SameInstallation(installRoot, installRoot) || SameInstallation(installRoot, filepath.Join(base, "other")) {
		t.Fatal("removed installation still compared as a validated identity")
	}
	if err := registry.RemoveInstallation(installationID); err != nil {
		t.Fatal(err)
	}
	roots, err := registry.Read()
	if err != nil || len(roots) != 0 {
		t.Fatalf("registry after uninstall = %#v, %v; want own row removed by captured identity", roots, err)
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
