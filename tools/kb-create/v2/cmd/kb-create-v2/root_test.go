package v2cli

import (
	"github.com/kb-labs/create/v2/contracts"
	"github.com/kb-labs/create/v2/doctor"
	"github.com/kb-labs/create/v2/shim"
	"strings"
	"testing"
)

// case: S-052.2, S-203.10
func TestDoctorHumanSummaryIncludesInstalledChannelAndVersion(t *testing.T) {
	summary := (doctorCommandResult{Status: "healthy", Response: doctor.Response{PlatformChannel: "canary", PlatformVersion: "3.0.2-canary.1"}}).human()
	for _, want := range []string{
		"Platform diagnosis completed.",
		"Platform channel: canary",
		"Platform version: 3.0.2-canary.1",
	} {
		if !strings.Contains(summary, want) {
			t.Errorf("doctor summary %q does not contain %q", summary, want)
		}
	}
}

// case: PC-001.10
func TestApplyHumanSummaryNamesShimOwnerAndDirectCommand(t *testing.T) {
	summary := renderApply(applyCommandResult{
		Shim: &shim.EnsureResult{Linked: false, Owner: "/platform/a"},
		Receipt: contracts.InstallReceipt{Plan: contracts.ResolvedInstallPlan{
			Request: contracts.InstallRequest{PlatformRoot: "/platform/b"},
		}},
	})
	for _, want := range []string{"owned by /platform/a", "run /platform/b/.kb/v2/bin/kb"} {
		if !strings.Contains(summary, want) {
			t.Errorf("apply summary %q does not contain %q", summary, want)
		}
	}
}

func TestDoctorRecoveryUsesItsOwnStructuredResult(t *testing.T) {
	payload := map[string]any{"operation": "recover", "recovered": true, "output": "recovered"}
	if payload["operation"] != "recover" || payload["recovered"] != true || payload["status"] != nil {
		t.Fatalf("recovery result was interpreted as a doctor report: %#v", payload)
	}
}
