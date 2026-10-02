package v2cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kb-labs/create/v2/catalog"
	"github.com/kb-labs/create/v2/contracts"
	"github.com/kb-labs/create/v2/installed"
	"github.com/kb-labs/create/v2/runtime"
)

func TestRequestWithGeneratedSecretsAddsOnlyDeclaredGeneratedSecretInputs(t *testing.T) {
	request := contracts.InstallRequest{Schema: contracts.RequestSchema, PlatformRoot: t.TempDir()}
	artifacts := []contracts.Artifact{{Config: []contracts.ConfigRequirement{
		{ID: "gateway.jwtSecret", Secret: true, Generate: true},
		{ID: "gateway.bootstrap.password", Secret: true},
	}}}
	got, changed, err := requestWithGeneratedSecrets(request, artifacts)
	if err != nil || !changed || len(got.SecretInputs) != 1 || got.SecretInputs[0] != "gateway.jwtSecret" {
		t.Fatalf("request/changed/error = %#v / %v / %v", got, changed, err)
	}
	got, changed, err = requestWithGeneratedSecrets(got, artifacts)
	if err != nil || changed || len(got.SecretInputs) != 1 {
		t.Fatalf("second request/changed/error = %#v / %v / %v", got, changed, err)
	}
}

type installArtifacts struct{ root string }

func (a installArtifacts) Install(items []contracts.Artifact) error {
	return a.InstallWithProgress(items, nil)
}
func (a installArtifacts) InstallWithProgress(items []contracts.Artifact, progress func(string, bool)) error {
	if progress != nil {
		progress("Download and verify artifacts", true)
		progress("Download and verify artifacts", false)
		progress("Install packages", true)
	}
	bin := filepath.Join(a.root, "node_modules", "@kb-labs", "cli-bin", "dist")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		if progress != nil {
			progress("Install packages", false)
		}
		return err
	}
	err := os.WriteFile(filepath.Join(bin, "bin.js"), []byte("export {};\n"), 0o644)
	// Like a real install, every artifact package lands with its package.json;
	// the platform lock hashes it for discovery's integrity check.
	for _, item := range items {
		if err != nil || item.Package == "" {
			continue
		}
		dir := filepath.Join(a.root, "node_modules", filepath.FromSlash(item.Package))
		if err = os.MkdirAll(dir, 0o755); err == nil {
			err = os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"name":"`+item.Package+`","version":"`+item.Version+`"}`), 0o644)
		}
		if err == nil {
			err = installed.Write(a.root, item)
		}
	}
	if progress != nil {
		progress("Install packages", false)
	}
	return err
}
func (installArtifacts) Restore([]contracts.Artifact) error { return nil }

type installHost struct{}

func (installHost) Probe(context.Context, contracts.HostSpec) error { return nil }

func installOutput(t *testing.T) (*os.File, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "screen.txt")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	return file, path
}

func TestInstallRunsInteractiveCommandWithDefaultsAndProgress(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	root := filepath.Join(home, "kb-platform")
	index := filepath.Join(home, "release-index.json")
	release, err := catalog.Seal(catalog.Catalog{
		Channels:      map[contracts.Channel]string{contracts.ChannelStable: "2.0.0"},
		Compatibility: testCompatibility("2.0.0"),
		Platforms:     []catalog.PlatformBundle{{ID: "platform", Version: "2.0.0", Package: "@kb/platform", Tarball: "https://example.test/platform.tgz", SHA256: "platform", Host: &contracts.HostSpec{Command: "kb-host", HealthURL: fmt.Sprintf("http://127.0.0.1:%d/health", port)}, Profiles: map[string]contracts.ServiceGraph{"default": {PlatformVersion: "2.0.0"}}, Config: []catalog.ConfigRequirement{{ID: "gateway.access.mode", Path: "/gateway/access/mode", Default: `"local"`}}}},
		Plugins:       []catalog.Component{{ID: "commit", Version: "1.0.0", Package: "@kb/commit", Tarball: "https://example.test/commit.tgz", SHA256: "commit"}},
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
	launcher := filepath.Join(home, "bin", "kb-create")
	if err := os.MkdirAll(filepath.Dir(launcher), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(launcher, []byte("launcher"), 0o755); err != nil {
		t.Fatal(err)
	}
	output, outputPath := installOutput(t)
	var screen bytes.Buffer
	code := (invocation{Operation: "install", Index: index, Direct: directRequest{PlatformRoot: root}, Executable: launcher, InstallInput: strings.NewReader(strings.Repeat("\n", 12)), InstallOutput: &screen, RuntimeDeps: &runtime.Dependencies{Artifacts: installArtifacts{root: root}, Host: installHost{}}, NodePreflight: func() error { return nil }}).run(output)
	if err := output.Close(); err != nil {
		t.Fatal(err)
	}
	if code != 0 {
		t.Fatalf("exit code %d, screen:\n%s", code, screen.String())
	}
	for _, expected := range []string{"Resolve (", "Download and verify artifacts (", "Install packages (", "Render config (", "Start host (", "Verify installation (", "Create kb shim (", "KB Labs is installed", "kb project add <path>"} {
		if !strings.Contains(screen.String(), expected) {
			t.Errorf("screen missing %q:\n%s", expected, screen.String())
		}
	}
	if strings.Contains(screen.String(), `{"ok"`) || strings.Contains(screen.String(), "goroutine ") || strings.Contains(screen.String(), "panic:") {
		t.Fatalf("human screen leaked JSON or stack trace:\n%s", screen.String())
	}
	if _, err := os.Stat(outputPath); err != nil {
		t.Fatal(err)
	}
	if target, err := os.Readlink(filepath.Join(home, "bin", "kb")); err != nil || target != filepath.Join(root, ".kb", "v2", "bin", "kb") {
		t.Fatalf("kb shim target/error = %q / %v", target, err)
	}
}

func TestRegisterProjectInvokesPlainProjectAdd(t *testing.T) {
	home := t.TempDir()
	bin := filepath.Join(home, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	kb := filepath.Join(bin, "kb")
	argsFile := filepath.Join(home, "args")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$CAPTURE_ARGS\"\nprintf '{\"ok\":true,\"project\":{\"id\":\"prj_test123\",\"path\":\"%s\"}}' \"$PROJECT_PATH\"\n"
	if err := os.WriteFile(kb, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CAPTURE_ARGS", argsFile)
	projectPath := filepath.Join(home, "project")
	t.Setenv("PROJECT_PATH", projectPath)
	if err := os.Mkdir(projectPath, 0o755); err != nil {
		t.Fatal(err)
	}

	projectID, err := (invocation{Executable: filepath.Join(bin, "kb-create")}).registerProject(projectPath)
	if err != nil || projectID != "prj_test123" {
		t.Fatalf("projectID/error = %q / %v", projectID, err)
	}
	args, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	want := "project\nadd\n" + projectPath + "\n--json\n"
	if string(args) != want {
		t.Fatalf("kb args = %q, want %q", args, want)
	}
}

func TestInstallRegistersSelectedProjectAndReportsBothResults(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	platformRoot := filepath.Join(home, "kb-platform")
	projectRoot := filepath.Join(home, "work", "project")
	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	canonicalProjectRoot, err := filepath.EvalSymlinks(projectRoot)
	if err != nil {
		t.Fatal(err)
	}
	projectRoot = canonicalProjectRoot
	index := filepath.Join(home, "release-index.json")
	release, err := catalog.Seal(catalog.Catalog{
		Channels:      map[contracts.Channel]string{contracts.ChannelStable: "2.0.0"},
		Compatibility: testCompatibility("2.0.0"),
		Platforms:     []catalog.PlatformBundle{{ID: "platform", Version: "2.0.0", Package: "@kb/platform", Tarball: "https://example.test/platform.tgz", SHA256: "platform", Host: testHost(), Profiles: map[string]contracts.ServiceGraph{"default": {PlatformVersion: "2.0.0"}}, Config: []catalog.ConfigRequirement{{ID: "gateway.access.mode", Path: "/gateway/access/mode", Default: `"local"`}}}},
		Plugins:       []catalog.Component{{ID: "commit", Version: "1.0.0", Package: "@kb/commit", Tarball: "https://example.test/commit.tgz", SHA256: "commit"}},
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
	launcher := filepath.Join(home, "bin", "kb-create")
	if err := os.MkdirAll(filepath.Dir(launcher), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(launcher, []byte("launcher"), 0o755); err != nil {
		t.Fatal(err)
	}
	var registered string
	var screen bytes.Buffer
	output, _ := installOutput(t)
	code := (invocation{
		Operation: "install", Index: index, Direct: directRequest{PlatformRoot: platformRoot, ProjectRoot: projectRoot},
		Executable: launcher, InstallInput: strings.NewReader(strings.Repeat("\n", 12)), InstallOutput: &screen,
		RuntimeDeps:      &runtime.Dependencies{Artifacts: installArtifacts{root: platformRoot}, Host: installHost{}},
		NodePreflight:    func() error { return nil },
		ProjectRegistrar: func(path string) (string, error) { registered = path; return "prj_test123", nil },
	}).run(output)
	if err := output.Close(); err != nil {
		t.Fatal(err)
	}
	if code != 0 || registered != projectRoot {
		t.Fatalf("code=%d registered=%q screen:\n%s", code, registered, screen.String())
	}
	for _, expected := range []string{projectRoot, "prj_test123", "Verification: platform installed", "kb project show prj_test123"} {
		if !strings.Contains(screen.String(), expected) {
			t.Errorf("screen missing %q:\n%s", expected, screen.String())
		}
	}
	if _, err := os.Stat(filepath.Join(projectRoot, ".kb")); !os.IsNotExist(err) {
		t.Fatalf("installer created project files: %v", err)
	}
}

func TestInstallEarlyFailuresUseDiagRendering(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	output, path := installOutput(t)
	code := (invocation{Operation: "install", Index: filepath.Join(t.TempDir(), "missing.json"), NodePreflight: func() error { return nil }}).run(output)
	if err := output.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if code != 2 || !strings.Contains(string(data), "Install failed [KB_INSTALL_RELEASE_INDEX_INVALID]") || !strings.Contains(string(data), "Next:") {
		t.Fatalf("code/output = %d / %s", code, data)
	}
}

func TestInstallChecksNodeBeforeQuestions(t *testing.T) {
	output, path := installOutput(t)
	var screen bytes.Buffer
	code := (invocation{Operation: "install", InstallInput: strings.NewReader("\n"), InstallOutput: &screen, NodePreflight: func() error { return errors.New("node missing") }}).run(output)
	if err := output.Close(); err != nil {
		t.Fatal(err)
	}
	if code != 2 || !strings.Contains(screen.String(), "Install failed [KB_INSTALL_NODE_VERSION]") || strings.Contains(screen.String(), "Platform channel") || !strings.Contains(screen.String(), "Next:") {
		t.Fatalf("code/output = %d / %s", code, screen.String())
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}

func TestApplyReportsNodeVersionCodeBeforeResolution(t *testing.T) {
	output, path := installOutput(t)
	code := (invocation{Operation: "apply", NodePreflight: func() error { return errors.New("Node.js v22.11.0 is unsupported") }}).run(output)
	if err := output.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if code != 2 || !strings.Contains(string(data), "KB_INSTALL_NODE_VERSION") || !strings.Contains(string(data), "Install Node.js 24") || strings.Contains(string(data), "KB_INSTALL_INPUT_REQUIRED") {
		t.Fatalf("code/output = %d / %s", code, data)
	}
}

func TestHumanProgressUsesElapsedPhaseEvents(t *testing.T) {
	output, path := installOutput(t)
	p := &humanProgress{out: output, started: map[string]time.Time{}}
	p.started["Install packages"] = time.Now().Add(-time.Millisecond)
	p.Emit(runtime.ProgressEvent{Phase: "Install packages"})
	if err := output.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "Install packages") || !strings.Contains(string(data), "ms)") {
		t.Fatalf("progress output = %s", data)
	}
}

func testHost() *contracts.HostSpec {
	return &contracts.HostSpec{Command: "kb-host", HealthURL: "http://127.0.0.1:4000/health"}
}

func testCompatibility(version string) *catalog.CompatibilityMatrix {
	return &catalog.CompatibilityMatrix{
		Schema: catalog.CompatibilitySchema,
		Labels: []catalog.CompatibilityLabel{{
			ID: "platform@" + version, Kind: "platform", ArtifactID: "platform", Version: version,
			Status: "prepared", ValidatedBy: []string{"test"},
		}},
	}
}

func TestParseProvidersSupportsCommaSeparatedPreferences(t *testing.T) {
	got, err := parseProviders("storage=disk-io-storage, cache=state-broker")
	if err != nil || got["storage"] != "disk-io-storage" || got["cache"] != "state-broker" {
		t.Fatalf("providers/error = %#v / %v", got, err)
	}
	if _, err := parseProviders("storage=disk,storage=other"); err == nil {
		t.Fatal("expected duplicate capability to fail")
	}
}

func TestHelpListsRegisteredCommandsAndFlags(t *testing.T) {
	root := newRootCommand()
	var output bytes.Buffer
	root.SetOut(&output)
	if err := root.Help(); err != nil { t.Fatal(err) }
	text := output.String()
	for _, expected := range []string{"install", "Install KB Labs interactively", "--provider", "apply"} {
		if !strings.Contains(text, expected) { t.Fatalf("help is missing %q: %s", expected, text) }
	}
}
