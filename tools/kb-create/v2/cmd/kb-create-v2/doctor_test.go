//go:build !windows

package v2cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kb-labs/create/v2/contracts"
	"github.com/kb-labs/create/v2/doctor"
	"github.com/kb-labs/create/v2/shim"
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

func TestDoctorNamesForeignShimAndGivesOrderedCleanupAndRepairCommands(t *testing.T) {
	inst := newInstallation(t)
	link := shim.Path(inst.launcher)
	target := filepath.Join(t.TempDir(), "removed-owner", "current", ".kb", "v2", "bin", "kb")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	removeCommand := contracts.ShellCommand("rm", "--", link)
	repairCommand := contracts.ShellCommand("kb-create", "doctor", "--fix", "--platform-root", inst.root)
	expected := []string{link, target, "cannot be mapped to a validated installation", "considers it foreign and will not change it", removeCommand, repairCommand}
	assertOrdered := func(output string) {
		t.Helper()
		last := -1
		for _, fragment := range expected {
			index := strings.Index(output, fragment)
			if index <= last {
				t.Fatalf("doctor output does not explain the foreign shim and next commands in order; missing/out of order %q: %s", fragment, output)
			}
			last = index
		}
	}

	jsonResult, code, jsonOutput := inst.run("doctor", "--json")
	if code != 0 {
		t.Fatalf("JSON doctor failed: %d %s", code, jsonOutput)
	}
	findings, ok := jsonResult["findings"].([]any)
	if !ok {
		t.Fatalf("JSON doctor has no findings: %s", jsonOutput)
	}
	var shimFinding map[string]any
	for _, item := range findings {
		if finding, ok := item.(map[string]any); ok && finding["code"] == "KB_INSTALL_SHIM_DANGLING" {
			shimFinding = finding
		}
	}
	if shimFinding == nil || shimFinding["path"] != link {
		t.Fatalf("JSON doctor omitted the foreign shim path finding: %s", jsonOutput)
	}
	jsonHint, ok := shimFinding["hint"].(string)
	if !ok {
		t.Fatalf("JSON doctor finding has no hint: %#v", shimFinding)
	}
	assertOrdered(jsonHint)
	findingJSON, err := json.Marshal(shimFinding)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("foreign shim JSON finding: %s", findingJSON)

	_, code, humanOutput := inst.run("doctor")
	if code != 0 {
		t.Fatalf("human doctor failed: %d %s", code, humanOutput)
	}
	assertOrdered(humanOutput)
	t.Logf("foreign shim human output: %s", humanOutput)
	if got, err := os.Readlink(link); err != nil || got != target {
		t.Fatalf("doctor changed the foreign shim: got %q, error %v; want %q", got, err, target)
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
