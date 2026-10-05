package v2cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	clikitcommand "github.com/kb-labs/clikit/command"
	"github.com/kb-labs/clikit/result"
	"github.com/spf13/cobra"
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

// case: S-025.1, S-025.2, S-203.2, S-203.4
func TestUnhealthyDoctorReportUsesNormalClikitSuccessResult(t *testing.T) {
	data := []byte(`{"ok":false,"platformChannel":"stable","platformVersion":"3.2.0","findings":[{"code":"KB_INSTALL_CONFIG_REQUIRED","component":"@kb-labs/gateway","path":"/gateway/access/mode","hint":"Set the value in the generated config."}],"installation":{"ok":false,"checks":[{"id":"config","ok":false,"detail":"required config is missing","hint":"Run kb-create doctor --fix."}]}}`)
	payload, err := doctorResultPayload(data)
	if err != nil {
		t.Fatal(err)
	}
	if payload["operation"] != "doctor" || payload["status"] != "unhealthy" {
		t.Fatalf("doctor result = %#v, want operation doctor and unhealthy status", payload)
	}
	findings, ok := payload["findings"].([]any)
	if !ok || len(findings) != 1 {
		t.Fatalf("doctor findings are not structured: %#v", payload["findings"])
	}
	installation, ok := payload["installation"].(map[string]any)
	if !ok || installation["ok"] != false {
		t.Fatalf("doctor installation report is not structured: %#v", payload["installation"])
	}
	summary := operationHumanSummary("doctor", payload)
	for _, want := range []string{"KB_INSTALL_CONFIG_REQUIRED", "@kb-labs/gateway", "/gateway/access/mode", "Set the value", "required config is missing", "Run kb-create doctor --fix"} {
		if !strings.Contains(summary, want) {
			t.Errorf("human summary %q does not show report item %q", summary, want)
		}
	}
	root := &cobra.Command{Use: "kb-create", RunE: func(*cobra.Command, []string) error { return result.Return(payload) }}
	root.PersistentFlags().Bool("json", false, "")
	var stdout, stderr bytes.Buffer
	if code := clikitcommand.Execute(root, []string{"--json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("clikit exit = %d for a successful diagnosis; stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var envelope struct {
		OK   bool           `json:"ok"`
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatalf("clikit result is invalid JSON: %v; %s", err, stdout.String())
	}
	if !envelope.OK || envelope.Data["status"] != "unhealthy" || envelope.Data["operation"] != "doctor" {
		t.Fatalf("clikit result = %s", stdout.String())
	}
}
