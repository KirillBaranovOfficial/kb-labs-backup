package orchestrator

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/kb-labs/clikit/diag"
	"github.com/kb-labs/env/internal/config"
	"github.com/kb-labs/env/internal/env"
)

func TestInstallUsesV2RequestAndParsesTypedStreamFailure(t *testing.T) {
	root := t.TempDir()
	l := env.Layout{
		Home: filepath.Join(root, "home"), Root: filepath.Join(root, "current"),
		Platform: filepath.Join(root, "current", "platform"), Project: filepath.Join(root, "current", "project"),
		State: filepath.Join(root, "current", "state"), Logs: filepath.Join(root, "current", "logs"),
	}
	if err := os.MkdirAll(l.Home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(l.Logs, 0o755); err != nil {
		t.Fatal(err)
	}
	argsFile := filepath.Join(root, "args")
	fake := filepath.Join(root, "kb-create")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$KB_CREATE_ARGS_FILE\"\nprintf '%s\\n' '{\"ok\":false,\"error\":{\"code\":\"KB_INSTALL_PORT_IN_USE\",\"area\":\"install\",\"stage\":\"preflight\",\"severity\":\"error\",\"retryable\":false,\"message\":\"Port is in use.\",\"hint\":\"Stop the process.\",\"correlationId\":\"stream-test-id\"}}'\nprintf '%s\\n' '{\"ok\":true,\"data\":null,\"meta\":{\"stream\":{\"terminal\":true,\"exitCode\":1}}}'\nexit 1\n"
	if err := os.WriteFile(fake, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KB_CREATE_ARGS_FILE", argsFile)
	err := Install(fake, l, config.Profile{Plugins: []string{"mind", "marketplace"}}, "http://registry.example.test")
	typed, ok := err.(*diag.Diag)
	if !ok || typed.Code != "KB_INSTALL_PORT_IN_USE" || typed.CorrelationID != "stream-test-id" {
		log, _ := os.ReadFile(filepath.Join(l.Logs, "install.log"))
		t.Fatalf("Install error = %#v, want correlated KB_INSTALL_PORT_IN_USE; log=%s", err, log)
	}
	data, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Fields(string(data))
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"install", "--json", "--index", filepath.Join(home, ".local", "share", "kb-create", "release-index.json"), "--request-platform-root", l.Platform, "--project-root", l.Project, "--service-profile", "default", "--plugins", "mind,marketplace", "--registry", "http://registry.example.test"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("kb-create args = %q, want %q", got, want)
	}
}
