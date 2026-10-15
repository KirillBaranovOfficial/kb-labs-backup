//go:build darwin || linux

package proc

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const pressureNormalDuration = 10 * time.Second

const pressurePSIThreshold = 10.0

const lowAvailableMemoryFraction = 10

const highLoadPerLogicalCPU = 1.5

type resourceUsage struct {
	MemoryBytes uint64
	MinCPUs     int
	DiskBytes   uint64
}

type resourceQuota struct {
	MemoryBytes uint64
	CPUs        int
	MinFree     uint64
}

type ResourceSnapshot struct {
	PhysicalMemory       uint64
	AvailableMemory      uint64
	MemoryAvailableKnown bool
	PressureAvailable    bool
	LoadAvailable        bool
	LogicalCPUs          int
	Pressure             string
	Load1                float64
	CPUPressure          float64
	Swap                 string
	Unavailable          map[string]string
	CgroupMemory         uint64
	CgroupCPUs           int
	CgroupCPUsKnown      bool
}

func resourceNotice(sample ResourceSnapshot) string {
	keys := make([]string, 0, len(sample.Unavailable))
	for key := range sample.Unavailable {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+" unavailable: "+sample.Unavailable[key])
	}
	return strings.Join(parts, "; ")
}

// ResourceSource supplies one host resource sample to admission. Start uses
// the current host reader; callers that coordinate several clients can inject
// the same source into every admission poll.
type ResourceSource interface{ Snapshot() ResourceSnapshot }

type quotaFile struct {
	MemoryBytes   *uint64 `json:"memory_bytes,omitempty"`
	MemoryPercent *int    `json:"memory_percent,omitempty"`
	CPUs          *int    `json:"cpus,omitempty"`
	MinFree       *uint64 `json:"min_free_bytes,omitempty"`
}

func effectiveQuota(stateDir string, source ResourceSource) (resourceQuota, error) {
	sample := source.Snapshot()
	quota := resourceQuota{MemoryBytes: sample.PhysicalMemory * 70 / 100, CPUs: max(1, sample.LogicalCPUs-2)}
	data, err := os.ReadFile(filepath.Join(stateDir, "quota.json"))
	if os.IsNotExist(err) {
		if sample.CgroupMemory > 0 && sample.CgroupMemory < quota.MemoryBytes {
			quota.MemoryBytes = sample.CgroupMemory
		}
		if sample.CgroupCPUsKnown && sample.CgroupCPUs < quota.CPUs {
			quota.CPUs = sample.CgroupCPUs
		}
		return quota, nil
	}
	if err != nil {
		return resourceQuota{}, err
	}
	var stored quotaFile
	if err := json.Unmarshal(data, &stored); err != nil {
		return resourceQuota{}, fmt.Errorf("decode quota.json: %w", err)
	}
	if stored.MemoryBytes != nil {
		quota.MemoryBytes = *stored.MemoryBytes
	}
	if stored.MemoryPercent != nil {
		quota.MemoryBytes = sample.PhysicalMemory * uint64(*stored.MemoryPercent) / 100
	}
	if stored.CPUs != nil {
		quota.CPUs = *stored.CPUs
	}
	if stored.MinFree != nil {
		quota.MinFree = *stored.MinFree
	}
	if sample.CgroupMemory > 0 && sample.CgroupMemory < quota.MemoryBytes {
		quota.MemoryBytes = sample.CgroupMemory
	}
	if sample.CgroupCPUsKnown && sample.CgroupCPUs < quota.CPUs {
		quota.CPUs = sample.CgroupCPUs
	}
	return quota, nil
}

func writeQuota(stateDir string, quota quotaFile) error {
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(quota)
	if err != nil {
		return err
	}
	tmp := filepath.Join(stateDir, "quota.json.tmp")
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, filepath.Join(stateDir, "quota.json")); err != nil {
		return fmt.Errorf("write quota.json: %w", err)
	}
	return nil
}

type QuotaStatus struct {
	MemoryBytes uint64
	CPUs        int
	MinFree     uint64
	MemorySet   bool
	CPUsSet     bool
	MinFreeSet  bool
	Resources   ResourceSnapshot
}

func ShowQuota(stateDir string) (QuotaStatus, error) {
	var status QuotaStatus
	err := withStateLock(stateDir, func() error {
		source := systemResourceSource()
		sample := source.Snapshot()
		quota, err := effectiveQuota(stateDir, source)
		if err != nil {
			return err
		}
		status = QuotaStatus{MemoryBytes: quota.MemoryBytes, CPUs: quota.CPUs, MinFree: quota.MinFree, Resources: sample}
		data, readErr := os.ReadFile(filepath.Join(stateDir, "quota.json"))
		if readErr == nil {
			var stored quotaFile
			if err := json.Unmarshal(data, &stored); err != nil {
				return err
			}
			status.MemorySet = stored.MemoryBytes != nil || stored.MemoryPercent != nil
			status.CPUsSet, status.MinFreeSet = stored.CPUs != nil, stored.MinFree != nil
		} else if !os.IsNotExist(readErr) {
			return readErr
		}
		return nil
	})
	return status, err
}

func SetQuota(stateDir, memory, cpus, minFree string) error {
	if stateDir == "" {
		return fmt.Errorf("state directory is required")
	}
	return withStateLock(stateDir, func() error {
		var stored quotaFile
		data, err := os.ReadFile(filepath.Join(stateDir, "quota.json"))
		if err == nil {
			if err := json.Unmarshal(data, &stored); err != nil {
				return err
			}
		} else if !os.IsNotExist(err) {
			return err
		}
		if memory != "" {
			if strings.HasSuffix(memory, "%") {
				n, err := strconv.Atoi(strings.TrimSuffix(memory, "%"))
				if err != nil || n < 1 || n > 100 {
					return fmt.Errorf("memory percent must be 1..100")
				}
				stored.MemoryPercent, stored.MemoryBytes = &n, nil
			} else {
				value, err := ParseResourceSize(memory)
				if err != nil {
					return err
				}
				if value == 0 {
					return fmt.Errorf("memory quota must be positive")
				}
				stored.MemoryBytes, stored.MemoryPercent = &value, nil
			}
		}
		if cpus != "" {
			value, err := strconv.Atoi(cpus)
			if err != nil || value < 1 {
				return fmt.Errorf("CPU quota must be positive")
			}
			stored.CPUs = &value
		}
		if minFree != "" {
			value, err := ParseResourceSize(minFree)
			if err != nil {
				return err
			}
			stored.MinFree = &value
		}
		return writeQuota(stateDir, stored)
	})
}

type pressureState struct {
	ElevatedAt time.Time `json:"elevated_at,omitempty"`
	NormalAt   time.Time `json:"normal_at,omitempty"`
	Level      string    `json:"level,omitempty"`
}

func isElevatedPressure(sample ResourceSnapshot) bool {
	if sample.Pressure == "memory warn" || sample.Pressure == "memory critical" || sample.Pressure == "memory psi elevated" {
		return true
	}
	if sample.PhysicalMemory > 0 && sample.AvailableMemory < sample.PhysicalMemory*lowAvailableMemoryFraction/100 {
		return true
	}
	return sample.LogicalCPUs > 0 && sample.Load1 > highLoadPerLogicalCPU*float64(sample.LogicalCPUs)
}

func pressureWaitReason(sample ResourceSnapshot) string {
	switch {
	case sample.Pressure == "memory warn" || sample.Pressure == "memory critical" || sample.Pressure == "memory psi elevated":
		return "host pressure (" + sample.Pressure + ")"
	case sample.PhysicalMemory > 0 && sample.AvailableMemory < sample.PhysicalMemory*lowAvailableMemoryFraction/100:
		return "host pressure (available memory below 10% of physical)"
	case sample.LogicalCPUs > 0 && sample.Load1 > highLoadPerLogicalCPU*float64(sample.LogicalCPUs):
		return fmt.Sprintf("host pressure (load %.2f above %.1f per logical CPU)", sample.Load1, highLoadPerLogicalCPU)
	default:
		return "host pressure"
	}
}

func pressureSignalsAvailable(sample ResourceSnapshot) bool {
	return sample.PressureAvailable && sample.LoadAvailable && sample.MemoryAvailableKnown
}

func (s *pressureState) blocks(elevated bool, now time.Time) bool {
	if elevated {
		s.ElevatedAt = now
		s.NormalAt = time.Time{}
		return true
	}
	if s.ElevatedAt.IsZero() {
		return false
	}
	if s.NormalAt.IsZero() {
		s.NormalAt = now
	}
	if now.Sub(s.NormalAt) < pressureNormalDuration {
		return true
	}
	s.ElevatedAt = time.Time{}
	s.NormalAt = time.Time{}
	return false
}

func resourceFits(active, candidate resourceUsage, quota resourceQuota) bool {
	if active.MemoryBytes == 0 && active.MinCPUs == 0 && active.DiskBytes == 0 {
		return true
	}
	if active.MemoryBytes > quota.MemoryBytes || active.MinCPUs > quota.CPUs {
		return candidate.MemoryBytes == 0 && candidate.MinCPUs == 0 && candidate.DiskBytes == 0
	}
	if candidate.MemoryBytes > quota.MemoryBytes-active.MemoryBytes {
		return false
	}
	return candidate.MinCPUs <= quota.CPUs-active.MinCPUs
}

func diskFits(free, reserved, need, minFree uint64) bool {
	return reserved <= free && need <= free-reserved && minFree <= free-reserved-need
}

func effectiveResourceUsage(declared, learned resourceUsage) resourceUsage {
	return resourceUsage{
		MemoryBytes: max(declared.MemoryBytes, learned.MemoryBytes),
		MinCPUs:     max(declared.MinCPUs, learned.MinCPUs),
		DiskBytes:   declared.DiskBytes,
	}
}

func memoryUnavailable(need, available uint64) bool { return need > available }

func FormatResourceBytes(value uint64) string {
	const gib = uint64(1024 * 1024 * 1024)
	const mib = uint64(1024 * 1024)
	if value >= gib {
		return fmt.Sprintf("%.1fG", float64(value)/float64(gib))
	}
	if value >= mib {
		return fmt.Sprintf("%.1fM", float64(value)/float64(mib))
	}
	return fmt.Sprintf("%dB", value)
}

type overBudgetState struct{ emitted bool }

func (s *overBudgetState) observe(rss, reserved uint64) bool {
	if !s.emitted && reserved > 0 && rss > reserved+reserved/4 {
		s.emitted = true
		return true
	}
	return false
}

func recordResourceObservation(stateDir, runID string, rss uint64, averageCPU float64, partial bool) (bool, error) {
	emitted := false
	err := withStateLock(stateDir, func() error {
		rec, err := readRecordLocked(stateDir, runID)
		if err != nil {
			return err
		}
		rec.PeakRSSBytes = max(rec.PeakRSSBytes, rss)
		rec.AverageCPU = averageCPU
		rec.MeasurementPartial = rec.MeasurementPartial || partial
		state := overBudgetState{emitted: rec.OverBudget}
		emitted = state.observe(rss, rec.MemoryBytes)
		if emitted {
			rec.OverBudget = true
			if err := appendEvent(stateDir, eventFor(rec, EventOverBudget)); err != nil {
				return err
			}
		}
		return writeRecordAtomic(rec)
	})
	return emitted, err
}

func ParseResourceSize(value string) (uint64, error) {
	value = strings.TrimSpace(strings.ToUpper(value))
	if value == "" {
		return 0, fmt.Errorf("size is empty")
	}
	multiplier := uint64(1)
	last := value[len(value)-1]
	switch last {
	case 'K', 'M', 'G', 'T':
		value = value[:len(value)-1]
		multiplier = 1
		for range strings.IndexByte("KMGT", last) + 1 {
			multiplier *= 1024
		}
		if strings.HasSuffix(value, "I") {
			value = strings.TrimSuffix(value, "I")
		}
		if strings.HasSuffix(value, "B") {
			value = strings.TrimSuffix(value, "B")
		}
	case 'B':
		value = value[:len(value)-1]
	}
	amount, err := strconv.ParseUint(value, 10, 64)
	if err != nil || amount > ^uint64(0)/multiplier {
		return 0, fmt.Errorf("invalid or overflowing size")
	}
	return amount * multiplier, nil
}
