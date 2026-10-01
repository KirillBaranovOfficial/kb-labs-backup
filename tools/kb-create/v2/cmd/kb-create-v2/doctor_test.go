//go:build !windows

package v2cli

import (
	"os"
	"strings"
	"testing"

	"github.com/kb-labs/create/v2/doctor"
)

func TestDoctorTextExplainsUnboundPersistentAdapter(t *testing.T) {
	output, err := os.CreateTemp(t.TempDir(), "doctor-text")
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()

	const hint = "From a project using this platform, run `kb adapter bind analytics @kb-labs/adapters-analytics-sqlite`."
	writeDoctorPlatformChecks(output, []doctor.Check{{
		ID:             "adapter-analytics",
		Slot:           "analytics",
		Mode:           "inmemory",
		Implementation: "InMemory",
		Detail:         "analytics is using InMemory although @kb-labs/adapters-analytics-sqlite is bound in the install plan.",
		Hint:           hint,
	}})
	if err := output.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(output.Name())
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"Adapter analytics: mode=inmemory implementation=InMemory", "Problem: analytics is using InMemory", hint} {
		if !strings.Contains(string(data), expected) {
			t.Errorf("doctor text output omits %q: %s", expected, data)
		}
	}
}
