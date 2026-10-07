package v2cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	clikitcommand "github.com/kb-labs/clikit/command"
	"github.com/kb-labs/clikit/diag"
	"github.com/kb-labs/clikit/result"
	"github.com/kb-labs/create/v2/catalog"
	"github.com/kb-labs/create/v2/contracts"
	"github.com/kb-labs/create/v2/runtime"
	"github.com/spf13/cobra"
)

// case: S-203.1, S-203.2, S-203.6, S-203.10, S-203.11, S-203.12
func TestRegisteredCommandsUseSharedResultBoundary(t *testing.T) {
	root := newRootCommand()
	commands := registeredCommands(root)
	if len(commands) == 0 {
		t.Fatal("kb-create command registry is empty")
	}

	doctor := findCommand(root, "kb-create doctor")
	if doctor == nil || doctor.RunE == nil {
		t.Fatal("doctor must be registered as the safe read-only boundary fixture")
	}
	original := doctor.RunE
	doctor.RunE = func(*cobra.Command, []string) error {
		return result.Return(map[string]any{"operation": "doctor", "ok": true})
	}
	var success bytes.Buffer
	if code := clikitcommand.Execute(root, []string{"doctor", "--json"}, &success, &bytes.Buffer{}); code != 0 {
		t.Fatalf("doctor fixture exit = %d", code)
	}
	if err := result.ValidateMachineResult(success.Bytes()); err != nil {
		t.Fatalf("doctor fixture envelope: %v; output=%s", err, success.String())
	}
	doctor.RunE = original
	delete(doctor.Annotations, "clikit.sharedResultBoundary")
	clikitcommand.InstallInputBoundary(root)

	for _, command := range commands {
		if !clikitcommand.BoundaryInstalled(command) {
			t.Errorf("registered command %q is not wrapped by the shared boundary", command.CommandPath())
			continue
		}
		path := strings.Fields(strings.TrimPrefix(command.CommandPath(), root.CommandPath()))
		var help bytes.Buffer
		if code := clikitcommand.Execute(root, append(path, "--help"), &help, &bytes.Buffer{}); code != 0 {
			t.Errorf("%s --help exit = %d", command.CommandPath(), code)
		}

		var invalid bytes.Buffer
		args := append(append([]string{}, path...), "--json", "--kb-contract-invalid")
		if code := clikitcommand.Execute(root, args, &invalid, &bytes.Buffer{}); code != 2 {
			t.Errorf("%s invalid flag exit = %d, want 2", command.CommandPath(), code)
		}
		if err := result.ValidateMachineResult(invalid.Bytes()); err != nil {
			t.Errorf("%s invalid flag envelope: %v; output=%s", command.CommandPath(), err, invalid.String())
			continue
		}
		var failure struct {
			OK    bool `json:"ok"`
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal(invalid.Bytes(), &failure); err != nil || failure.OK || failure.Error.Code != "KB_RUNTIME_INPUT_INVALID" {
			t.Errorf("%s invalid flag result = %s, want typed KB_RUNTIME_INPUT_INVALID", command.CommandPath(), invalid.String())
		}
	}
}

func registeredCommands(root *cobra.Command) []*cobra.Command {
	var commands []*cobra.Command
	var visit func(*cobra.Command)
	visit = func(parent *cobra.Command) {
		for _, child := range parent.Commands() {
			if child.Run != nil || child.RunE != nil {
				commands = append(commands, child)
			}
			visit(child)
		}
	}
	visit(root)
	return commands
}

func findCommand(root *cobra.Command, path string) *cobra.Command {
	for _, command := range registeredCommands(root) {
		if command.CommandPath() == path {
			return command
		}
	}
	return nil
}

// case: S-203.11, S-203.12
func TestDoctorFailuresUseSharedHumanAndJSONBoundary(t *testing.T) {
	for _, mode := range []string{"human", "json"} {
		t.Run(mode+" typed", func(t *testing.T) {
			root := newRootCommand()
			doctor := findCommand(root, "kb-create doctor")
			doctor.RunE = func(*cobra.Command, []string) error {
				return diag.New("KB_INSTALL_DOCTOR_INPUT_INVALID", "doctor fixture root is missing")
			}
			var stdout, stderr bytes.Buffer
			args := []string{"doctor"}
			if mode == "json" {
				args = append(args, "--json")
			}
			if code := clikitcommand.Execute(root, args, &stdout, &stderr); code != 1 {
				t.Fatalf("exit = %d, want 1; out=%s err=%s", code, stdout.String(), stderr.String())
			}
			if mode == "json" {
				if err := result.ValidateMachineResult(stdout.Bytes()); err != nil {
					t.Fatalf("result: %v; out=%s", err, stdout.String())
				}
			}
			visible := stdout.String() + stderr.String()
			if !strings.Contains(visible, "KB_INSTALL_DOCTOR_INPUT_INVALID") || mode == "human" && !strings.Contains(strings.ToLower(visible), "correlation") {
				t.Fatalf("failure lacks code/correlation: %s", visible)
			}
		})
		t.Run(mode+" unexpected", func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			root := newRootCommand()
			doctor := findCommand(root, "kb-create doctor")
			doctor.RunE = func(*cobra.Command, []string) error {
				return runSafely(func() error { return errors.New("raw Go error fixture") })
			}
			var stdout, stderr bytes.Buffer
			args := []string{"doctor"}
			if mode == "json" {
				args = append(args, "--json")
			}
			if code := clikitcommand.Execute(root, args, &stdout, &stderr); code != 1 {
				t.Fatalf("exit = %d, want 1", code)
			}
			visible := stdout.String() + stderr.String()
			if !strings.Contains(visible, "KB_RUNTIME_UNEXPECTED") || mode == "json" && !strings.Contains(visible, "correlationId") {
				t.Fatalf("unexpected failure lacks code/id: %s", visible)
			}
			if strings.Contains(visible, "raw Go error fixture") || strings.Contains(visible, "goroutine") {
				t.Fatalf("raw failure escaped to terminal: %s", visible)
			}
			correlationID := regexp.MustCompile(`[a-f0-9]{32}`).FindString(visible)
			if correlationID == "" {
				t.Fatalf("failure has no correlation ID: %s", visible)
			}
			log, err := os.ReadFile(filepath.Join(home, ".kb", "logs", "kb-create.jsonl"))
			if err != nil || !bytes.Contains(log, []byte(correlationID)) || !bytes.Contains(log, []byte(`"stack"`)) {
				t.Fatalf("structured diagnostics lack correlation/stack: %s (%v)", log, err)
			}
		})
	}
}

func TestNewDiagCauseIsVisibleAndRedactedAtDoctorCommandBoundary(t *testing.T) {
	for _, mode := range []string{"human", "json"} {
		t.Run(mode, func(t *testing.T) {
			root := newRootCommand()
			doctor := findCommand(root, "kb-create doctor")
			doctor.RunE = func(*cobra.Command, []string) error {
				return contracts.NewDiag("KB_INSTALL_DOCTOR_INPUT_INVALID", "doctor input could not be prepared", "fix the input and rerun doctor", errors.New("decode config: token=private-value"))
			}
			var stdout, stderr bytes.Buffer
			args := []string{"doctor"}
			if mode == "json" {
				args = append(args, "--json")
			}
			if code := clikitcommand.Execute(root, args, &stdout, &stderr); code != 1 {
				t.Fatalf("exit = %d, want 1; stdout=%s stderr=%s", code, stdout.String(), stderr.String())
			}
			visible := stdout.String() + stderr.String()
			if strings.Contains(visible, "private-value") || !strings.Contains(visible, "[REDACTED]") {
				t.Fatalf("diagnostic cause must be visible with secret material redacted: %s", visible)
			}
			if !strings.Contains(visible, "decode config") {
				t.Fatalf("diagnostic cause context missing: %s", visible)
			}
		})
	}
}

// case: S-203.11, PC-003.2
func TestDoctorMissingReceiptUsesSharedHumanAndJSONBoundary(t *testing.T) {
	for _, mode := range []string{"human", "json"} {
		t.Run(mode, func(t *testing.T) {
			root := newRootCommand()
			var stdout, stderr bytes.Buffer
			args := []string{"doctor", "--platform-root", filepath.Join(t.TempDir(), "missing-platform")}
			if mode == "json" {
				args = append(args, "--json")
			}
			if code := clikitcommand.Execute(root, args, &stdout, &stderr); code != 1 {
				t.Fatalf("exit = %d, want 1; stdout=%s stderr=%s", code, stdout.String(), stderr.String())
			}
			visible := stdout.String() + stderr.String()
			if mode == "json" {
				if err := result.ValidateMachineResult(stdout.Bytes()); err != nil {
					t.Fatalf("result: %v; stdout=%s", err, stdout.String())
				}
			}
			if !strings.Contains(visible, "KB_INSTALL_RECEIPT_UNAVAILABLE") || !strings.Contains(strings.ToLower(visible), "correlation") || !strings.Contains(visible, "Run `kb-create install`") {
				t.Fatalf("missing-receipt failure lacks code, correlation, or next step: %s", visible)
			}
		})
	}
}

// case: S-203.7, S-203.13
func TestJSONInstallProgressEndsWithTerminalRecord(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	index := filepath.Join(t.TempDir(), "release-index.json")
	release, err := catalog.Seal(catalog.Catalog{
		Channels:      map[contracts.Channel]string{contracts.ChannelStable: "2.0.0"},
		Compatibility: testCompatibility("2.0.0"),
		Platforms:     []catalog.PlatformBundle{{ID: "platform", Version: "2.0.0", Package: "@kb/platform", Tarball: "https://example.test/platform.tgz", SHA256: "platform", Host: testHostOnFreePort(t), Profiles: map[string]contracts.ServiceGraph{"default": {PlatformVersion: "2.0.0"}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(release)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(index, data, 0o600); err != nil {
		t.Fatal(err)
	}
	platformRoot := filepath.Join(t.TempDir(), "platform")
	var stdout bytes.Buffer
	cmd := &cobra.Command{Use: "kb-create install"}
	cmd.SetOut(&stdout)
	inv := invocation{Operation: "install", Index: index, JSON: true, Direct: directRequest{PlatformRoot: platformRoot}, RuntimeDeps: &runtime.Dependencies{Artifacts: installArtifacts{root: platformRoot}, Host: installHost{}}, NodePreflight: func() error { return nil }}
	err = runInvocation(cmd, inv, "install")
	if code, ok := result.StreamExitCode(err); !ok || code != 0 {
		t.Fatalf("stream result = %v, %v; out=%s", code, ok, stdout.String())
	}
	lines := bytes.Split(bytes.TrimSpace(stdout.Bytes()), []byte("\n"))
	if len(lines) < 2 {
		t.Fatalf("expected progress and terminal records, got %s", stdout.String())
	}
	for _, line := range lines {
		var record map[string]any
		if err := json.Unmarshal(line, &record); err != nil {
			t.Fatalf("invalid JSONL record %q: %v", line, err)
		}
		if _, ok := record["ok"]; !ok {
			t.Fatalf("record is outside the shared stream schema: %s", line)
		}
	}
	var terminal struct {
		OK   bool `json:"ok"`
		Data any  `json:"data"`
		Meta struct {
			Stream struct {
				Terminal bool `json:"terminal"`
				ExitCode int  `json:"exitCode"`
			} `json:"stream"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(lines[len(lines)-1], &terminal); err != nil {
		t.Fatal(err)
	}
	if !terminal.OK || terminal.Data != nil || !terminal.Meta.Stream.Terminal || terminal.Meta.Stream.ExitCode != 0 {
		t.Fatalf("terminal record = %s", lines[len(lines)-1])
	}
}

// case: S-203.7, S-203.13
func TestJSONInstallFailureEndsWithTerminalRecord(t *testing.T) {
	var stdout bytes.Buffer
	cmd := &cobra.Command{Use: "kb-create install"}
	cmd.SetOut(&stdout)
	err := runInvocation(cmd, invocation{Operation: "install", JSON: true}, "install")
	if code, ok := result.StreamExitCode(err); !ok || code != 2 {
		t.Fatalf("stream result = %v, %v; out=%s", code, ok, stdout.String())
	}
	lines := bytes.Split(bytes.TrimSpace(stdout.Bytes()), []byte("\n"))
	if len(lines) != 2 {
		t.Fatalf("expected failure and terminal records, got %s", stdout.String())
	}
	var failure struct {
		OK    bool `json:"ok"`
		Error struct {
			Code          string `json:"code"`
			CorrelationID string `json:"correlationId"`
		} `json:"error"`
	}
	if err := json.Unmarshal(lines[0], &failure); err != nil {
		t.Fatal(err)
	}
	if failure.OK || failure.Error.Code != "KB_INSTALL_INPUT_REQUIRED" || failure.Error.CorrelationID == "" {
		t.Fatalf("typed stream failure = %s", lines[0])
	}
	var terminal struct {
		Meta struct {
			Stream struct {
				Terminal bool `json:"terminal"`
				ExitCode int  `json:"exitCode"`
			} `json:"stream"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(lines[1], &terminal); err != nil {
		t.Fatal(err)
	}
	if !terminal.Meta.Stream.Terminal || terminal.Meta.Stream.ExitCode != 2 {
		t.Fatalf("terminal record = %s", lines[1])
	}
}
