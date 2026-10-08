//go:build !windows

package v2cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kb-labs/create/v2/doctor"
)

func TestDoctorTextExplainsUnboundPersistentAdapter(t *testing.T) {
	const hint = "From a project using this platform, run `kb adapter bind analytics @kb-labs/adapters-analytics-sqlite`."
	data := renderDoctorPlatformChecks([]doctor.Check{{
		ID:             "adapter-analytics",
		Slot:           "analytics",
		Mode:           "inmemory",
		Implementation: "InMemory",
		Detail:         "analytics is using InMemory although @kb-labs/adapters-analytics-sqlite is bound in the install plan.",
		Hint:           hint,
	}})
	for _, expected := range []string{"Adapter analytics: mode=inmemory implementation=InMemory", "Problem: analytics is using InMemory", hint} {
		if !strings.Contains(data, expected) {
			t.Errorf("doctor text output omits %q: %s", expected, data)
		}
	}
}

func TestDoctorHandlerReturnsTypedFindingsAndNextAction(t *testing.T) {
	inputPath := filepath.Join(t.TempDir(), "doctor-input.json")
	input := map[string]any{
		"manifests": []any{map[string]any{"id": "@kb/plugin", "requirements": []any{map[string]any{
			"path": "/plugin/token", "required": true, "hint": "Set PLUGIN_TOKEN and rerun doctor.", "action": "set PLUGIN_TOKEN",
		}}}},
		"configured": map[string]bool{},
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(inputPath, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	value, failure, code, _ := executeForTest(t, operationTestSetup{Operation: "doctor", DoctorInput: inputPath, JSON: true})
	if code != 0 || failure != nil {
		t.Fatalf("doctor exit = %d, failure = %#v", code, failure)
	}
	response, ok := value.(doctorCommandResult)
	if !ok || len(response.Findings) != 1 {
		t.Fatalf("doctor output is not a typed report: %#v", value)
	}
	finding := response.Findings[0]
	if finding.Code == "" || finding.Component != "@kb/plugin" || finding.Path != "/plugin/token" ||
		finding.Action != "set PLUGIN_TOKEN" || finding.Hint != "Set PLUGIN_TOKEN and rerun doctor." {
		t.Fatalf("doctor finding is missing its typed action: %#v", finding)
	}
}
