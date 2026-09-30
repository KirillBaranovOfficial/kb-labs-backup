package preflight

import (
	"testing"

	"github.com/kb-labs/create/v2/contracts"
)

func TestValidateResourcesReportsAvailableMemoryAndMinimum(t *testing.T) {
	err := validateResources(MinimumAvailableMemory-1, MinimumFreeDisk)
	failure, ok := err.(*contracts.LauncherError)
	if !ok || failure.Code != contracts.CodeMemoryInsufficient || failure.Details["minimum"] != "1.20 GiB" {
		t.Fatalf("error = %#v", err)
	}
}

func TestValidateResourcesReportsFreeDiskAndMinimum(t *testing.T) {
	err := validateResources(MinimumAvailableMemory, MinimumFreeDisk-1)
	failure, ok := err.(*contracts.LauncherError)
	if !ok || failure.Code != "KB_INSTALL_NO_DISK_SPACE" || failure.Details["minimum"] != "2.00 GiB" {
		t.Fatalf("error = %#v", err)
	}
}

func TestValidateResourcesAcceptsInstallBudget(t *testing.T) {
	if err := validateResources(MinimumAvailableMemory, MinimumFreeDisk); err != nil {
		t.Fatalf("error = %v", err)
	}
}

func TestDarwinAvailableMemoryParsesAvailablePageCounts(t *testing.T) {
	output := "Mach Virtual Memory Statistics: (page size of 16384 bytes)\nPages free: 10.\nPages inactive: 20.\nPages speculative: 3.\nPages purgeable: 2.\nPages wired down: 40.\n"
	got, err := darwinAvailableMemory(output)
	want := uint64(35 * 16384)
	if err != nil || got != want {
		t.Fatalf("available memory = %d, %v; want %d", got, err, want)
	}
}
