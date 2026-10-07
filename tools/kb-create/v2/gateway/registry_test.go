package gateway

import (
	"path/filepath"
	"testing"
)

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
