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

	clikitcommand "github.com/kb-labs/clikit/command"
	"github.com/kb-labs/create/v2/catalog"
	"github.com/kb-labs/create/v2/contracts"
	"github.com/kb-labs/create/v2/installed"
	"github.com/kb-labs/create/v2/receipt"
	"github.com/kb-labs/create/v2/runtime"
	"github.com/kb-labs/create/v2/shim"
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

// case: S-005.6
func TestClientStatusAndDoctorAreHealthyWithoutHost(t *testing.T) {
	root := t.TempDir()
	plan := contracts.ResolvedInstallPlan{Schema: contracts.ResolvedPlanSchema,
		ServiceGraph: contracts.ServiceGraph{PlatformVersion: "2.0.0", Profile: "client"}}
	if err := receipt.Write(root, contracts.InstallReceipt{ID: "client-r1", Plan: plan}); err != nil {
		t.Fatal(err)
	}
	statusFile, err := os.CreateTemp(t.TempDir(), "status-*.json")
	if err != nil {
		t.Fatal(err)
	}
	defer statusFile.Close()
	if code := (invocation{Operation: "status", PlatformRoot: root, JSON: true}).runStatus(statusFile); code != 0 {
		t.Fatalf("status exit = %d", code)
	}
	if _, err := statusFile.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	var status map[string]any
	if err := json.NewDecoder(statusFile).Decode(&status); err != nil {
		t.Fatal(err)
	}
	if status["profile"] != "client" || status["installed"] != true || status["running"] != false {
		t.Fatalf("client status = %#v", status)
	}

	doctorFile, err := os.CreateTemp(t.TempDir(), "doctor-*.json")
	if err != nil {
		t.Fatal(err)
	}
	defer doctorFile.Close()
	if code := (invocation{Operation: "doctor", PlatformRoot: root, JSON: true}).runDoctor(doctorFile); code != 0 {
		t.Fatalf("doctor exit = %d", code)
	}
	if _, err := doctorFile.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	var diagnosis map[string]any
	if err := json.NewDecoder(doctorFile).Decode(&diagnosis); err != nil {
		t.Fatal(err)
	}
	if diagnosis["profile"] != "client" || diagnosis["ok"] != true || diagnosis["running"] != false {
		t.Fatalf("client doctor = %#v", diagnosis)
	}
}

func TestVersionJSONIncludesBuildCommit(t *testing.T) {
	previousVersion, previousCommit := buildVersion, buildCommit
	t.Cleanup(func() { buildVersion, buildCommit = previousVersion, previousCommit })
	SetVersionInfo("2.3.4", "abc1234", "2026-10-06")
	var stdout, stderr bytes.Buffer
	if code := clikitcommand.Execute(newRootCommand(), []string{"version", "--json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("version command exit = %d: %s", code, stderr.String())
	}
	var payload struct {
		OK   bool              `json:"ok"`
		Data map[string]string `json:"data"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("version JSON is invalid: %s (%v)", stdout.String(), err)
	}
	if !payload.OK || payload.Data["version"] != "2.3.4" || payload.Data["commit"] != "abc1234" {
		t.Fatalf("version identity = %#v", payload)
	}
}

func TestHumanReleaseIndexFailurePrintsLoaderReason(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing-release-index.json")
	_, err := catalog.LoadFile(path)
	if err == nil {
		t.Fatal("expected release index read failure")
	}
	var output bytes.Buffer
	writeHumanFailure(&output, asDiag(err))
	if !strings.Contains(output.String(), "Why: Release index read failed for "+path) {
		t.Fatalf("human output omitted loader reason: %s", output.String())
	}
}

// case: S-214.1
func TestWriteIndexFailureSerializesCanonicalDetails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing-release-index.json")
	_, err := catalog.LoadFile(path)
	if err == nil {
		t.Fatal("expected release index read failure")
	}
	output, createErr := os.CreateTemp(t.TempDir(), "index-error-*.json")
	if createErr != nil {
		t.Fatal(createErr)
	}
	defer output.Close()
	writeIndexFailure(output, err, "supply a valid index")
	if _, err := output.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	var payload struct {
		OK    bool `json:"ok"`
		Error struct {
			Code    string            `json:"code"`
			Cause   string            `json:"cause"`
			Details map[string]string `json:"details"`
		} `json:"error"`
	}
	if err := json.NewDecoder(output).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload.OK || payload.Error.Code != contracts.CodeReleaseIndexUnreadable || payload.Error.Details["gate"] != "read" ||
		payload.Error.Details["path"] != path || !strings.Contains(payload.Error.Cause, "no such file or directory") {
		t.Fatalf("failure envelope omitted loader diagnostics: %#v", payload)
	}
}

// case: PC-001.4
func TestShimFailureNamesPathReasonAndRepairCommand(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bin", "kb")
	output, err := os.CreateTemp(t.TempDir(), "result-*.json")
	if err != nil {
		t.Fatal(err)
	}
	writeShimFailure(output, contracts.CodeApplyFailed, "/tmp/platform root", &shim.Failure{
		Path: path, Reason: "permission denied", Cause: os.ErrPermission,
	})
	if err := output.Close(); err != nil {
		t.Fatal(err)
	}
	encoded, err := os.ReadFile(output.Name())
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Error struct {
			Code    string            `json:"code"`
			Hint    string            `json:"hint"`
			Cause   string            `json:"cause"`
			Details map[string]string `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(encoded, &result); err != nil {
		t.Fatal(err)
	}
	if result.Error.Code != contracts.CodeApplyFailed || result.Error.Details["path"] != path || result.Error.Details["reason"] != "permission denied" {
		t.Fatalf("shim failure omitted its code, path, or filesystem reason: %s", encoded)
	}
	if !strings.Contains(result.Error.Hint, `kb-create doctor --fix --platform-root "/tmp/platform root"`) {
		t.Fatalf("shim failure omitted its repair command: %s", encoded)
	}
	if !strings.Contains(result.Error.Cause, path) || !strings.Contains(result.Error.Cause, "permission denied") {
		t.Fatalf("shim failure omitted its path and filesystem reason: %s", encoded)
	}
}

// case: PC-001.8
func TestPlanLeavesPlatformRootByteIdentical(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	root := filepath.Join(home, "platform")
	marker := filepath.Join(root, ".kb", "user-data.json")
	if err := os.MkdirAll(filepath.Dir(marker), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, []byte("{\"keep\":true}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	index := filepath.Join(home, "release-index.json")
	release, err := catalog.Seal(catalog.Catalog{
		Channels:      map[contracts.Channel]string{contracts.ChannelStable: "2.0.0"},
		Compatibility: testCompatibility("2.0.0"),
		Platforms: []catalog.PlatformBundle{{ID: "platform", Version: "2.0.0", Package: "@kb/platform",
			Tarball: "https://example.test/platform.tgz", SHA256: "platform",
			Profiles: map[string]contracts.ServiceGraph{"default": {PlatformVersion: "2.0.0"}},
			Host:     &contracts.HostSpec{Command: "kb-host", HealthURL: "http://127.0.0.1:4000/health"}}},
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
	before := snapshotTree(t, root)
	output, _ := installOutput(t)
	code := (invocation{Operation: "plan", Index: index, Direct: directRequest{PlatformRoot: root,
		PlatformVersion: "2.0.0", Policy: string(contracts.PolicyCompatible)}, JSON: true}).run(output)
	output.Close()
	if code != 0 {
		t.Fatalf("plan exit code = %d", code)
	}
	if after := snapshotTree(t, root); !bytes.Equal(before, after) {
		t.Fatalf("plan changed the platform root: before %q, after %q", before, after)
	}
}

func snapshotTree(t *testing.T, root string) []byte {
	t.Helper()
	var snapshot bytes.Buffer
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		fmt.Fprintf(&snapshot, "%s:%t:%o\n", relative, info.IsDir(), info.Mode())
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			snapshot.WriteString(target)
		}
		if info.Mode().IsRegular() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			snapshot.Write(data)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return snapshot.Bytes()
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
		Plugins:       []catalog.Component{{ID: "@kb-labs/commit", Version: "1.0.0", Package: "@kb/commit", Tarball: "https://example.test/commit.tgz", SHA256: "commit"}},
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
	root := filepath.Join(home, "secondary-root")
	bin := filepath.Join(root, ".kb", "v2", "bin")
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

	projectID, err := (invocation{Executable: filepath.Join(home, "bin", "kb-create")}).registerProject(root, projectPath)
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
		Platforms:     []catalog.PlatformBundle{{ID: "platform", Version: "2.0.0", Package: "@kb/platform", Tarball: "https://example.test/platform.tgz", SHA256: "platform", Host: testHostOnFreePort(t), Profiles: map[string]contracts.ServiceGraph{"default": {PlatformVersion: "2.0.0"}}, Config: []catalog.ConfigRequirement{{ID: "gateway.access.mode", Path: "/gateway/access/mode", Default: `"local"`}}}},
		Plugins:       []catalog.Component{{ID: "@kb-labs/commit", Version: "1.0.0", Package: "@kb/commit", Tarball: "https://example.test/commit.tgz", SHA256: "commit"}},
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
	if code != 2 || !strings.Contains(string(data), "Install failed [KB_INSTALL_RELEASE_INDEX_UNREADABLE]") || !strings.Contains(string(data), "Next:") {
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

func testHostOnFreePort(t *testing.T) *contracts.HostSpec {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return &contracts.HostSpec{Command: "kb-host", HealthURL: fmt.Sprintf("http://127.0.0.1:%d/health", port)}
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
	if err := root.Help(); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	for _, expected := range []string{"install", "Install KB Labs interactively", "--provider", "apply"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("help is missing %q: %s", expected, text)
		}
	}
}
