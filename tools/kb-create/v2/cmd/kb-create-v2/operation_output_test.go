package v2cli

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	clikitcommand "github.com/kb-labs/clikit/command"
	"github.com/kb-labs/create/v2/doctor"
)

func TestEveryOperationUsesJSONAndHumanOutputBoundary(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("KB_HOME", filepath.Join(home, ".kb"))
	t.Setenv("KB_PLATFORM_ROOT", "")
	t.Setenv("KB_NET_OFFSET", "")

	registered := make(map[string]struct{}, len(operationDescriptors))
	for _, descriptor := range operationDescriptors {
		descriptor := descriptor
		registered[descriptor.Name] = struct{}{}
		t.Run(descriptor.Name, func(t *testing.T) {
			t.Run("json", func(t *testing.T) {
				var stdout, stderr bytes.Buffer
				code := clikitcommand.Execute(newRootCommand(), []string{descriptor.Name, "--json"}, &stdout, &stderr)
				if code < 0 || code > 2 {
					t.Fatalf("exit code = %d", code)
				}
				var envelope map[string]any
				if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
					for _, line := range bytes.Split(bytes.TrimSpace(stdout.Bytes()), []byte("\n")) {
						var record map[string]any
						if streamErr := json.Unmarshal(line, &record); streamErr != nil {
							t.Fatalf("JSON result and records are invalid: %v / %v; stdout=%q stderr=%q", err, streamErr, stdout.String(), stderr.String())
						}
						if _, ok := record["ok"]; !ok {
							t.Fatalf("JSON result record has no shared ok field: %s", line)
						}
					}
					return
				}
				if _, ok := envelope["ok"]; !ok {
					t.Fatalf("JSON result has no shared ok field: %s", stdout.String())
				}
			})

			t.Run("human", func(t *testing.T) {
				var stdout, stderr bytes.Buffer
				code := clikitcommand.Execute(newRootCommand(), []string{descriptor.Name}, &stdout, &stderr)
				if code < 0 || code > 2 {
					t.Fatalf("exit code = %d", code)
				}
				output := stdout.String() + stderr.String()
				if strings.TrimSpace(output) == "" {
					t.Fatal("human result is empty")
				}
				if json.Valid(bytes.TrimSpace(stdout.Bytes())) {
					t.Fatalf("human result fell back to a raw JSON document: %s", stdout.String())
				}
			})
		})
	}
	if len(registered) != len(operationDescriptors) {
		t.Fatalf("operation output coverage has %d entries for %d registered descriptors", len(registered), len(operationDescriptors))
	}
}

func TestOperationHumanRenderersMatchGoldenOutput(t *testing.T) {
	status := map[string]any{"installed": true, "running": false, "reachable": false,
		"installation": map[string]any{"platformChannel": "stable", "platformVersion": "1.2.3"}}
	diagnosis := doctorCommandResult{Status: "healthy", Response: doctor.Response{PlatformChannel: "stable", PlatformVersion: "1.2.3"}}
	goldens := map[string]struct {
		value any
		want  string
	}{
		"install":   {nil, "Platform installation completed."},
		"plan":      {planResult{}, "Install plan resolved."},
		"apply":     {applyCommandResult{}, "Platform installation completed."},
		"status":    {status, "Installed: true\nPlatform channel: stable\nPlatform version: 1.2.3\nRunning: false\nReachable: false"},
		"doctor":    {diagnosis, "Platform diagnosis completed.\nStatus: healthy\nPlatform channel: stable\nPlatform version: 1.2.3"},
		"start":     {nil, "KB Labs host started."},
		"stop":      {nil, "KB Labs host stopped."},
		"restart":   {nil, "KB Labs host restarted."},
		"update":    {nil, "Platform update completed."},
		"uninstall": {nil, "Platform uninstalled."},
		"rollback":  {nil, "Platform rollback completed."},
		"supervise": {nil, "KB Labs host supervisor started."},
		"version":   {nil, "kb-create " + buildVersion + " (commit " + buildCommit + ")"},
	}
	if len(goldens) != len(operationDescriptors) {
		t.Fatalf("golden count = %d, registered command count = %d", len(goldens), len(operationDescriptors))
	}
	for _, descriptor := range operationDescriptors {
		golden, ok := goldens[descriptor.Name]
		if !ok {
			t.Errorf("operation %q has no human renderer golden", descriptor.Name)
			continue
		}
		if got := descriptor.Human(golden.value); got != golden.want {
			t.Errorf("%s human output = %q, want %q", descriptor.Name, got, golden.want)
		}
	}
}
