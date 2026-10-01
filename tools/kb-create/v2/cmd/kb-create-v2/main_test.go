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
	"github.com/kb-labs/create/v2/runtime"
	"github.com/kb-labs/create/v2/secrets"
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
	for _, expected := range []string{"Resolve (", "Download and verify artifacts (", "Install packages (", "Render config (", "Start host (", "Verify installation (", "Create kb shim (", "KB Labs is installed", "cd ~/any-project && kb commit"} {
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

func TestInstallEarlyFailuresUseLauncherErrorRendering(t *testing.T) {
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

func TestHelpListsCommandsAndOnlyRelevantProviderFlag(t *testing.T) {
	root := filepath.Join(t.TempDir(), "help.txt")
	file, err := os.Create(root)
	if err != nil {
		t.Fatal(err)
	}
	printHelp(file)
	printCommandHelp(file, "apply")
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(root)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, expected := range []string{"install", "Install KB Labs interactively", "--provider CAPABILITY=ADAPTER", "Usage: kb-create apply"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("help is missing %q: %s", expected, text)
		}
	}
	if strings.Contains(text, "wizard") || strings.Contains(text, "Usage:\n") {
		t.Fatalf("help exposed a retired command or raw flag dump: %s", text)
	}
}

func TestRunEmitsOnlyStructuredPlan(t *testing.T) {
	dir := t.TempDir()
	index := filepath.Join(dir, "index.json")
	input := filepath.Join(dir, "request.json")
	output := filepath.Join(dir, "output.json")
	release, err := catalog.Seal(catalog.Catalog{Channels: map[contracts.Channel]string{contracts.ChannelStable: "2.0.0"}, Compatibility: testCompatibility("2.0.0"), Platforms: []catalog.PlatformBundle{{ID: "platform", Version: "2.0.0", Package: "@kb/platform", Tarball: "https://example.test/platform.tgz", SHA256: "abc", Host: testHost(), Profiles: map[string]contracts.ServiceGraph{"default": {PlatformVersion: "2.0.0"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	releaseJSON, err := json.Marshal(release)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(index, releaseJSON, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(input, []byte(`{"schema":"kb.create/v2","platformRoot":"/tmp/platform","source":"offline"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(output)
	if err != nil {
		t.Fatal(err)
	}
	code := invocation{Operation: "plan", Index: index, Input: input}.run(file)
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil || decoded["ok"] != true {
		t.Fatalf("output = %s, error = %v", data, err)
	}
	plan, ok := decoded["plan"].(map[string]any)
	if !ok || plan["schema"] != "kb.create.resolved-plan/v2" {
		t.Fatalf("plan contract = %#v", decoded["plan"])
	}
}

func TestPopulateSecretsReadsEnvironmentWithoutSerialization(t *testing.T) {
	t.Setenv("KB_CREATE_TEST_SECRET", "private-value")
	root := t.TempDir()
	store := secrets.Store{PlatformRoot: root}
	if err := populateSecrets(store, "openai.key=KB_CREATE_TEST_SECRET"); err != nil {
		t.Fatal(err)
	}
	exists, err := store.Exists("openai.key")
	if err != nil || !exists {
		t.Fatalf("exists/error = %v / %v", exists, err)
	}
}

func TestParseComponentsSupportsScopedIDsAndPins(t *testing.T) {
	components, err := parseComponents("@kb-labs/review@1.2.3,commit")
	if err != nil || len(components) != 2 || components[0].ID != "@kb-labs/review" || components[0].Version.Version != "1.2.3" || components[1].ID != "commit" {
		t.Fatalf("components/error = %#v / %v", components, err)
	}
}

func TestRunRequiresBothMachineInputs(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "output")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if code := (invocation{Operation: "plan"}).run(file); code != 2 {
		t.Fatalf("exit code = %d", code)
	}
}

func TestRunRejectsUnknownOperation(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "output")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if code := (invocation{Operation: "destroy-everything"}).run(file); code != 2 {
		t.Fatalf("exit code = %d", code)
	}
}

func TestRecoveryRequiresPlatformRoot(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "output")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if code := (invocation{Operation: "uninstall"}).run(file); code != 2 {
		t.Fatalf("exit code = %d", code)
	}
}

func TestDoctorReturnsStructuredManifestFindings(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "doctor.json")
	output := filepath.Join(dir, "output.json")
	if err := os.WriteFile(input, []byte(`{"manifests":[{"id":"plugin","requirements":[{"id":"plugin.token","path":"/plugin/token","secret":true,"required":true,"hint":"set token"}]}],"configured":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(output)
	if err != nil {
		t.Fatal(err)
	}
	if code := (invocation{Operation: "doctor", DoctorInput: input, JSON: true}).run(file); code != 1 {
		t.Fatalf("exit code = %d", code)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	var response map[string]any
	if err := json.Unmarshal(data, &response); err != nil || response["ok"] != false {
		t.Fatalf("output/error = %s / %v", data, err)
	}
}

func TestStatusAndDoctorSummarizeMissingInstallationForHumans(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for _, operation := range []string{"status", "doctor"} {
		output := filepath.Join(t.TempDir(), "output")
		file, err := os.Create(output)
		if err != nil {
			t.Fatal(err)
		}
		code := (invocation{Operation: operation}).run(file)
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(output)
		if err != nil {
			t.Fatal(err)
		}
		if operation == "status" && code != 0 || operation == "doctor" && code != 1 {
			t.Fatalf("%s exit code = %d, output: %s", operation, code, data)
		}
		for _, expected := range []string{"Installed: no", "Running: no", "Reachable: no", "Next: kb-create install"} {
			if !strings.Contains(string(data), expected) {
				t.Fatalf("%s summary is missing %q: %s", operation, expected, data)
			}
		}
	}
}

func TestDirectRequestUsesSamePlanTransport(t *testing.T) {
	dir := t.TempDir()
	index := filepath.Join(dir, "index.json")
	release, err := catalog.Seal(catalog.Catalog{Channels: map[contracts.Channel]string{contracts.ChannelStable: "2.0.0"}, Compatibility: testCompatibility("2.0.0"), Platforms: []catalog.PlatformBundle{{ID: "platform", Version: "2.0.0", Package: "@kb/platform", Tarball: "https://example.test/platform.tgz", SHA256: "platform", Host: testHost(), Profiles: map[string]contracts.ServiceGraph{"default": {}}}}, Plugins: []catalog.Component{{ID: "review", Version: "1.2.0", Package: "@kb/review", Tarball: "https://example.test/review.tgz", SHA256: "review"}}})
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
	output, err := os.Create(filepath.Join(dir, "output.json"))
	if err != nil {
		t.Fatal(err)
	}
	if code := (invocation{Operation: "plan", Index: index, Direct: directRequest{PlatformRoot: "/tmp/platform", Plugins: "review@1.2.0", Offline: true, Policy: "strict"}}).run(output); code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	if err := output.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestScenarioAnswersCompileThroughManifestBoundPlan(t *testing.T) {
	dir := t.TempDir()
	index := filepath.Join(dir, "index.json")
	release, err := catalog.Seal(catalog.Catalog{Channels: map[contracts.Channel]string{contracts.ChannelStable: "2.0.0"}, Compatibility: testCompatibility("2.0.0"), Platforms: []catalog.PlatformBundle{{ID: "platform", Version: "2.0.0", Package: "@kb/platform", Tarball: "https://example.test/platform.tgz", SHA256: "platform", Host: testHost(), Profiles: map[string]contracts.ServiceGraph{"default": {}}, Config: []catalog.ConfigRequirement{{ID: "gateway.access.mode", Path: "/gateway/access/mode", Default: `"secured"`}, {ID: "gateway.authStorage.filename", Path: "/platform/adapterOptions/documentDatabase/filename", Default: `".kb/database/kb.sqlite"`}}}}, Adapters: []catalog.Adapter{{Component: catalog.Component{ID: "sqlite-stores", Version: "1.0.0", Package: "@kb/sqlite-stores", Tarball: "https://example.test/sqlite-stores.tgz", SHA256: "sqlite-stores"}, Provides: []string{"documentDatabase"}}}})
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
	output, err := os.Create(filepath.Join(dir, "output.json"))
	if err != nil {
		t.Fatal(err)
	}
	code := invocation{Operation: "plan", Index: index, ScenarioID: "custom", ScenarioAnswers: `{"access.mode":"local"}`, Direct: directRequest{PlatformRoot: "/tmp/platform", Offline: true, Policy: "compatible"}}.run(output)
	if err := output.Close(); err != nil {
		t.Fatal(err)
	}
	if code != 0 {
		result, readErr := os.ReadFile(filepath.Join(dir, "output.json"))
		if readErr != nil {
			t.Fatalf("exit code = %d; read output: %v", code, readErr)
		}
		t.Fatalf("exit code = %d; output = %s", code, result)
	}
	result, err := os.ReadFile(filepath.Join(dir, "output.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(result), `"path":"/gateway/access/mode"`) || !strings.Contains(string(result), `"json":"\"local\""`) {
		t.Fatalf("plan = %s", result)
	}
}
