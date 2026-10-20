package doctor

import (
	"errors"
	"testing"
)

func TestRepairPlanWarnsForOptionalCredentialAndErrorsForRequiredConfig(t *testing.T) {
	findings := Diagnose([]Manifest{{ID: "plugin", Requirements: []Requirement{{Path: "/plugin/mode", Required: true, Default: []byte(`"safe"`)}, {ID: "plugin.token", Path: "/plugin/token", Secret: true, Optional: true, Required: true, Action: "set TOKEN to enable the optional feature", Hint: "set token"}, {Path: "/platform/required", Required: true, Hint: "configure platform"}}}}, map[string]bool{})
	plan := PlanRepair(findings)
	if len(plan.SafeDefaults) != 1 || len(plan.RequiredInput) != 1 || plan.RequiredInput[0].Code != "KB_INSTALL_CONFIG_REQUIRED" {
		t.Fatalf("plan = %#v", plan)
	}
	var optionalCredential, requiredConfig Finding
	for _, finding := range findings {
		if finding.Code == CodeAdapterCredentialsMissing {
			optionalCredential = finding
		} else if finding.Path == "/platform/required" {
			requiredConfig = finding
		}
	}
	if optionalCredential.Severity != SeverityWarning || optionalCredential.Action != "set TOKEN to enable the optional feature" {
		t.Fatalf("optional credential finding = %#v", optionalCredential)
	}
	if requiredConfig.Severity != SeverityError || requiredConfig.Code != "KB_INSTALL_CONFIG_REQUIRED" {
		t.Fatalf("required config finding = %#v", requiredConfig)
	}
	called := 0
	if err := ApplySafe(plan, func(Finding) error { called++; return nil }); err != nil || called != 1 {
		t.Fatalf("err/called = %v / %d", err, called)
	}
}

func TestApplySafeStopsAtFirstFailure(t *testing.T) {
	plan := RepairPlan{SafeDefaults: []Finding{{Path: "/one"}, {Path: "/two"}}}
	called := 0
	err := ApplySafe(plan, func(Finding) error { called++; return errors.New("write failed") })
	if err == nil || called != 1 {
		t.Fatalf("err/called = %v / %d", err, called)
	}
}
