package preflight

import (
	"fmt"

	"github.com/kb-labs/create/v2/contracts"
)

const (
	MinimumAvailableMemory uint64 = (6 << 30) / 5 // 1.20 GiB: 992.3 MiB measured peak plus 236.5 MiB headroom
	MinimumFreeDisk        uint64 = 2 << 30       // 4.5x the measured 446.7 MiB installed root
)

// CheckInstallResources checks the two resources needed before the launcher
// stages artifacts or starts pnpm. The memory floor includes headroom over the
// measured peak from the tuned exact-artifact installer.
func CheckInstallResources(platformRoot string) error {
	availableMemory, err := availableMemory()
	if err != nil {
		return fmt.Errorf("read available memory: %w", err)
	}
	freeDisk, err := availableDisk(platformRoot)
	if err != nil {
		return fmt.Errorf("read free disk space for platform root: %w", err)
	}
	return validateResources(availableMemory, freeDisk)
}

func validateResources(availableMemory, freeDisk uint64) error {
	if availableMemory < MinimumAvailableMemory {
		failure := contracts.NewDiag(contracts.CodeMemoryInsufficient,
			fmt.Sprintf("The platform install needs at least %s of available memory; %s is available.", formatMemoryBytes(MinimumAvailableMemory), formatMemoryBytes(availableMemory)),
			"Close memory-heavy applications or increase the machine memory, then run `kb-create install` again.", nil)
		failure.Meta = map[string]any{
			"minimum": formatMemoryBytes(MinimumAvailableMemory), "available": formatMemoryBytes(availableMemory),
			"minimumBytes": fmt.Sprintf("%d", MinimumAvailableMemory), "availableBytes": fmt.Sprintf("%d", availableMemory),
		}
		return failure
	}
	if freeDisk < MinimumFreeDisk {
		failure := contracts.NewDiag("KB_INSTALL_NO_DISK_SPACE",
			fmt.Sprintf("The platform install needs at least %s of free disk space; %s is available.", formatBytes(MinimumFreeDisk), formatBytes(freeDisk)),
			"Free disk space or choose another directory with `--platform-root`, then run `kb-create install` again.", nil)
		failure.Meta = map[string]any{"minimum": formatBytes(MinimumFreeDisk), "available": formatBytes(freeDisk)}
		return failure
	}
	return nil
}

func formatBytes(bytes uint64) string {
	return fmt.Sprintf("%.2f GiB", float64(bytes)/(1<<30))
}

func formatMemoryBytes(bytes uint64) string {
	return fmt.Sprintf("%.2f MiB (%d bytes)", float64(bytes)/(1<<20), bytes)
}
