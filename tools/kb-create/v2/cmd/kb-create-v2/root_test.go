package v2cli

import (
	"strings"
	"testing"
)

// case: S-052.2, S-203.10
func TestDoctorHumanSummaryIncludesInstalledChannelAndVersion(t *testing.T) {
	summary := operationHumanSummary("doctor", map[string]any{
		"platformChannel": "canary",
		"platformVersion": "3.0.2-canary.1",
	})
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
