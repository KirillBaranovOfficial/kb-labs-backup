//go:build darwin

package proc

import "testing"

func TestDarwinResourceReaderUsesHostSysctls(t *testing.T) {
	sample := (darwinResourceSource{}).Snapshot()
	if sample.PhysicalMemory == 0 || sample.LogicalCPUs < 1 {
		t.Fatalf("host sysctl sample lacks physical memory or CPU count: %+v", sample)
	}
	if sample.Pressure == "" && sample.Unavailable["kern.memorystatus_vm_pressure_level"] == "" {
		t.Fatalf("pressure is neither available nor explained: %+v", sample)
	}
}
