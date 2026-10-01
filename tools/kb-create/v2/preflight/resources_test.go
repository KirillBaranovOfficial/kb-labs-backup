package preflight

import (
	"fmt"
	"strings"
	"testing"

	"github.com/kb-labs/create/v2/contracts"
)

func TestValidateResourcesReportsAvailableMemoryAndMinimum(t *testing.T) {
	available := MinimumAvailableMemory - 8*(1<<20)
	err := validateResources(available, MinimumFreeDisk)
	failure, ok := err.(*contracts.LauncherError)
	if !ok || failure.Code != contracts.CodeMemoryInsufficient ||
		failure.Details["minimum"] != "1228.80 MiB (1288490188 bytes)" || failure.Details["available"] != "1220.80 MiB (1280101580 bytes)" ||
		failure.Details["minimumBytes"] != "1288490188" || failure.Details["availableBytes"] != "1280101580" {
		t.Fatalf("error = %#v", err)
	}
	if failure.Message == "" || !strings.Contains(failure.Message, "1228.80 MiB (1288490188 bytes)") || !strings.Contains(failure.Message, "1220.80 MiB (1280101580 bytes)") ||
		!strings.Contains(failure.Hint, "Close memory-heavy applications") || !strings.Contains(failure.Hint, "kb-create install") {
		t.Fatalf("message/hint = %q / %q", failure.Message, failure.Hint)
	}
}

func TestValidateResourcesComparesAvailableMemoryAtBytePrecision(t *testing.T) {
	err := validateResources(MinimumAvailableMemory-1, MinimumFreeDisk)
	failure, ok := err.(*contracts.LauncherError)
	if !ok || failure.Code != contracts.CodeMemoryInsufficient || failure.Details["availableBytes"] != "1288490187" {
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

func TestDarwinAvailableMemoryUsesReportedPageSize(t *testing.T) {
	for _, pageSize := range []uint64{4096, 16384} {
		t.Run(fmt.Sprintf("%d-byte-pages", pageSize), func(t *testing.T) {
			output := fmt.Sprintf("Mach Virtual Memory Statistics: (page size of %d bytes)\nPages free: 10.\nPages inactive: 20.\nPages speculative: 3.\nPages purgeable: 2.\nPages wired down: 40.\n", pageSize)
			got, err := darwinAvailableMemory(output)
			want := 35 * pageSize
			if err != nil || got != want {
				t.Fatalf("available memory = %d, %v; want %d", got, err, want)
			}
		})
	}
}
