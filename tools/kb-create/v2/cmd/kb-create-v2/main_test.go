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
	goruntime "runtime"
	"strings"
	"testing"
	"time"

	clikitcommand "github.com/kb-labs/clikit/command"
	"github.com/kb-labs/clikit/stream"
	"github.com/kb-labs/create/v2/catalog"
	"github.com/kb-labs/create/v2/contracts"
	"github.com/kb-labs/create/v2/gateway"
	"github.com/kb-labs/create/v2/host"
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
	if err := receipt.Write(root, contracts.InstallReceipt{ID: "client-r1", InstallationID: "installation-client", Plan: plan}); err != nil {
		t.Fatal(err)
	}
	statusResult, statusFailure, statusCode, _ := executeForTest(t, operationTestSetup{Operation: "status", PlatformRoot: root, JSON: true})
	if statusCode != 0 || statusFailure != nil {
		t.Fatalf("status exit = %d, failure=%v", statusCode, statusFailure)
	}
	statusData := statusResult.(map[string]any)
	if statusData["profile"] != "client" || statusData["installed"] != true || statusData["running"] != false {
		t.Fatalf("client status = %#v", statusData)
	}

	diagnosis, doctorFailure, doctorCode, _ := executeForTest(t, operationTestSetup{Operation: "doctor", PlatformRoot: root, JSON: true})
	if doctorCode != 0 || doctorFailure != nil {
		t.Fatalf("doctor exit = %d, failure=%v", doctorCode, doctorFailure)
	}
	diagnosisBytes, err := json.Marshal(diagnosis)
	if err != nil {
		t.Fatal(err)
	}
	var diagnosisData map[string]any
	if err := json.Unmarshal(diagnosisBytes, &diagnosisData); err != nil {
		t.Fatal(err)
	}
	if diagnosisData["profile"] != "client" || diagnosisData["ok"] != true || diagnosisData["running"] != false {
		t.Fatalf("client doctor = %#v", diagnosisData)
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

func TestGatewayURLFlagProducesAPlanForTheDeclaredGateway(t *testing.T) {
	listener := testListenerWithPortHeadroom(t, 100)
	port := listener.Addr().(*net.TCPAddr).Port
	gatewayURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	release, err := catalog.Seal(catalog.Catalog{
		Channels:      map[contracts.Channel]string{contracts.ChannelStable: "2.0.0"},
		Compatibility: testCompatibility("2.0.0"),
		Platforms: []catalog.PlatformBundle{{
			ID: "platform", Version: "2.0.0", Package: "@kb/platform", Tarball: "platform.tgz", SHA256: "platform",
			Host:     &contracts.HostSpec{Command: "kb-host", HealthURL: "http://127.0.0.1:4000/health"},
			Profiles: map[string]contracts.ServiceGraph{"default": {Services: []contracts.Service{{ID: "gateway", Port: 4000}}}},
			Config: []catalog.ConfigRequirement{{ID: "platform.adapterOptions", Path: "/platform/adapterOptions",
				Default: `{"serviceTransport":{"services":{"gateway":{"url":"http://127.0.0.1:4000"}}}}`}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	indexPath := filepath.Join(t.TempDir(), "release-index.json")
	encoded, err := json.Marshal(release)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(indexPath, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	args := []string{"plan", "--json", "--index", indexPath,
		"--request-platform-root", filepath.Join(t.TempDir(), "platform"), "--gateway-url", gatewayURL}
	var busyOutput, busyError bytes.Buffer
	busyCode := clikitcommand.Execute(newRootCommand(), args, &busyOutput, &busyError)
	var busyPayload map[string]any
	if err := json.Unmarshal(busyOutput.Bytes(), &busyPayload); err != nil {
		t.Fatal(err)
	}
	if busyCode != 2 || errorCode(busyPayload) != contracts.CodePortInUse {
		t.Fatalf("occupied Gateway plan = code %d, error %s: %s", busyCode, errorCode(busyPayload), busyOutput.String())
	}
	errorEnvelope := busyPayload["error"].(map[string]any)
	if !strings.Contains(errorEnvelope["hint"].(string), "--gateway-url") {
		t.Fatalf("occupied Gateway plan hint omits --gateway-url: %#v", errorEnvelope)
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := clikitcommand.Execute(newRootCommand(), args, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("plan exit = %d: %s", code, stdout.String()+stderr.String())
	}
	var payload struct {
		Data struct {
			Plan contracts.ResolvedInstallPlan `json:"plan"`
		} `json:"data"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Data.Plan.Request.GatewayURL != gatewayURL || payload.Data.Plan.Host.HealthURL != gatewayURL+"/health" ||
		payload.Data.Plan.ServiceGraph.Services[0].Port != port {
		t.Fatalf("declared Gateway was not applied to plan: %#v", payload.Data.Plan)
	}
}

func TestPlanAutomaticallyAllocatesGatewayFromInstalledRootIndex(t *testing.T) {
	t.Setenv("KB_HOME", t.TempDir())
	state, err := host.StateHome()
	if err != nil {
		t.Fatal(err)
	}
	rootA, rootB := filepath.Join(t.TempDir(), "platform-a"), filepath.Join(t.TempDir(), "platform-b")
	registry := gateway.Registry{Path: filepath.Join(state.Dir, "gateway-roots.json")}
	if err := registry.Register(rootA, "http://127.0.0.1:4000"); err != nil {
		t.Fatal(err)
	}
	release, err := catalog.Seal(catalog.Catalog{
		Channels:      map[contracts.Channel]string{contracts.ChannelStable: "2.0.0"},
		Compatibility: testCompatibility("2.0.0"),
		Platforms: []catalog.PlatformBundle{{
			ID: "platform", Version: "2.0.0", Package: "@kb/platform", Tarball: "platform.tgz", SHA256: "platform",
			Host:     &contracts.HostSpec{Command: "kb-host", HealthURL: "http://127.0.0.1:4000/health"},
			Profiles: map[string]contracts.ServiceGraph{"default": {Services: []contracts.Service{{ID: "gateway", Port: 4000}}}},
			Config: []catalog.ConfigRequirement{{ID: "platform.adapterOptions", Path: "/platform/adapterOptions",
				Default: `{"serviceTransport":{"services":{"gateway":{"url":"http://127.0.0.1:4000"}}}}`}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	indexPath := filepath.Join(t.TempDir(), "release-index.json")
	encoded, err := json.Marshal(release)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(indexPath, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := clikitcommand.Execute(newRootCommand(), []string{"plan", "--json", "--index", indexPath,
		"--request-platform-root", rootB}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("automatic Gateway plan exit=%d: %s", code, stdout.String()+stderr.String())
	}
	var payload struct {
		Data struct {
			Plan contracts.ResolvedInstallPlan `json:"plan"`
		} `json:"data"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	plan := payload.Data.Plan
	if plan.Request.GatewayURL != "http://127.0.0.1:4100" || plan.Host.HealthURL != "http://127.0.0.1:4100/health" ||
		plan.ServiceGraph.Services[0].Port != 4100 {
		t.Fatalf("automatic Gateway address not applied: %#v", plan)
	}
}

func TestRecordedGatewayURLKeepsUpdateAddressFromReceipt(t *testing.T) {
	active := contracts.InstallReceipt{Verification: contracts.Verification{HostHealthURL: "http://127.0.0.1:4100/health"}}
	if got := recordedGatewayURL(active); got != "http://127.0.0.1:4100" {
		t.Fatalf("receipt Gateway URL = %q", got)
	}
	active.Plan.Request.GatewayURL = "http://127.0.0.1:4200"
	if got := recordedGatewayURL(active); got != "http://127.0.0.1:4200" {
		t.Fatalf("request Gateway URL = %q, want stored origin", got)
	}
}

func TestHumanReleaseIndexFailurePrintsLoaderReason(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing-release-index.json")
	_, err := catalog.LoadFile(path)
	if err == nil {
		t.Fatal("expected release index read failure")
	}
	var output, stderr bytes.Buffer
	clikitcommand.Execute(newRootCommand(), []string{"install", "--index", path}, &output, &stderr)
	if !strings.Contains(output.String()+stderr.String(), "Release index read failed for "+path) {
		t.Fatalf("human output omitted loader reason: %s %s", output.String(), stderr.String())
	}
}

// case: S-214.1
func TestWriteIndexFailureSerializesCanonicalDetails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing-release-index.json")
	_, err := catalog.LoadFile(path)
	if err == nil {
		t.Fatal("expected release index read failure")
	}
	failure := indexFailure(err, "supply a valid index")
	if failure.Code != contracts.CodeReleaseIndexUnreadable || failure.Meta["gate"] != "read" ||
		failure.Meta["path"] != path || !strings.Contains(failure.Cause, "no such file or directory") {
		t.Fatalf("failure envelope omitted loader diagnostics: %#v", failure)
	}
}

// case: PC-001.4
func TestShimFailureNamesPathReasonAndRepairCommand(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bin", "kb")
	failure := shimFailure(contracts.CodeApplyFailed, "/tmp/platform root", &shim.Failure{
		Path: path, Reason: "permission denied", Cause: os.ErrPermission,
	})
	if failure.Code != contracts.CodeApplyFailed || failure.Meta["path"] != path || failure.Meta["reason"] != "permission denied" {
		t.Fatalf("shim failure omitted its code, path, or filesystem reason: %#v", failure)
	}
	if !strings.Contains(failure.Hint, `kb-create doctor --fix --platform-root "/tmp/platform root"`) {
		t.Fatalf("shim failure omitted its repair command: %#v", failure)
	}
	if !strings.Contains(failure.Cause, path) || !strings.Contains(failure.Cause, "permission denied") {
		t.Fatalf("shim failure omitted its path and filesystem reason: %#v", failure)
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
			Host:     testHostOnFreePort(t)}},
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
	_, failure, code, _ := executeForTest(t, operationTestSetup{Operation: "plan", Index: index, Direct: directRequest{PlatformRoot: root,
		PlatformVersion: "2.0.0", Policy: string(contracts.PolicyCompatible)}, JSON: true})
	if code != 0 || failure != nil {
		t.Fatalf("plan exit code = %d: %v", code, failure)
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
func (a installArtifacts) InstallWithProgress(items []contracts.Artifact, progress func(string, bool) error) error {
	if progress != nil {
		if err := progress("Download and verify artifacts", true); err != nil {
			return err
		}
		if err := progress("Download and verify artifacts", false); err != nil {
			return err
		}
		if err := progress("Install packages", true); err != nil {
			return err
		}
	}
	bin := filepath.Join(a.root, "node_modules", "@kb-labs", "cli-bin", "dist")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		if progress != nil {
			err = errors.Join(err, progress("Install packages", false))
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

func (installHost) Start(context.Context, contracts.HostSpec) (func() error, error) {
	return func() error { return nil }, nil
}

func installOutput(t *testing.T) (*bytes.Buffer, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "screen.txt")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	return &bytes.Buffer{}, path
}

// case: PC-001.16, S-054.2
func TestInstallRunsInteractiveCommandWithDefaultsAndProgress(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("KB_HOME", filepath.Join(home, ".kb"))
	listener := testListenerWithPortHeadroom(t, 100)
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(home, "kb-platform")
	index := filepath.Join(home, "release-index.json")
	compatibility := testCompatibility("2.0.0")
	compatibility.Labels = append(compatibility.Labels, catalog.CompatibilityLabel{
		ID:   fmt.Sprintf("binary:kb-create@2.0.0:%s/%s", goruntime.GOOS, goruntime.GOARCH),
		Kind: "binary", ArtifactID: "kb-create", Version: "2.0.0",
		Requires: []catalog.CompatibilityRelation{{Label: "platform@2.0.0"}}, Status: "prepared", ValidatedBy: []string{"test"},
	})
	release, err := catalog.Seal(catalog.Catalog{
		Channels:      map[contracts.Channel]string{contracts.ChannelStable: "2.0.0"},
		Compatibility: compatibility,
		Platforms: []catalog.PlatformBundle{{ID: "platform", Version: "2.0.0", Package: "@kb/platform", Tarball: "https://example.test/platform.tgz", SHA256: "platform",
			Host:     &contracts.HostSpec{Command: "kb-host", HealthURL: fmt.Sprintf("http://127.0.0.1:%d/health", port)},
			Profiles: map[string]contracts.ServiceGraph{"default": {PlatformVersion: "2.0.0", Services: []contracts.Service{{ID: "gateway", Port: port}}}},
			Binaries: []catalog.Binary{{ID: "kb-create", OS: goruntime.GOOS, Arch: goruntime.GOARCH, URL: "https://example.test/kb-create", SHA256: "binary", Filename: "kb-create"}},
			Config: []catalog.ConfigRequirement{{ID: "gateway.access.mode", Path: "/gateway/access/mode", Default: `"local"`},
				{ID: "platform.adapterOptions", Path: "/platform/adapterOptions", Default: fmt.Sprintf(`{"serviceTransport":{"services":{"gateway":{"url":"http://127.0.0.1:%d"}}}}`, port)}}}},
		Plugins: []catalog.Component{{ID: "@kb-labs/commit", Version: "1.0.0", Package: "@kb/commit", Tarball: "https://example.test/commit.tgz", SHA256: "commit"}},
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
	_, outputPath := installOutput(t)
	var screen bytes.Buffer
	_, failure, code, _ := executeForTest(t, operationTestSetup{Operation: "install", Index: index, Direct: directRequest{PlatformRoot: root}, Executable: launcher, InstallInput: strings.NewReader(strings.Repeat("\n", 12)), InstallOutput: &screen, RuntimeDeps: &runtime.Dependencies{Artifacts: installArtifacts{root: root}, Host: installHost{}}, NodePreflight: func() error { return nil }})
	if code != 0 {
		t.Fatalf("exit code %d, failure=%v, screen:\n%s", code, failure, screen.String())
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
	installedReceipt, err := receipt.Read(root)
	if err != nil {
		t.Fatal(err)
	}
	hasLauncherBinary := false
	for _, artifact := range installedReceipt.Plan.Artifacts {
		if artifact.Kind == "binary" && artifact.ID == "kb-create" {
			hasLauncherBinary = true
		}
	}
	if !hasLauncherBinary {
		t.Fatal("sealed install fixture did not resolve the shipped kb-create binary")
	}
	var doctorOutput, doctorError bytes.Buffer
	doctorCode := clikitcommand.Execute(newRootCommand(), []string{"doctor", "--platform-root", root, "--json"}, &doctorOutput, &doctorError)
	if doctorCode != 0 {
		t.Fatalf("automatic doctor after sealed install exited %d: stdout=%s stderr=%s", doctorCode, doctorOutput.String(), doctorError.String())
	}
	var doctorResponse struct {
		OK   bool `json:"ok"`
		Data struct {
			Findings []any `json:"findings"`
		} `json:"data"`
	}
	if err := json.Unmarshal(doctorOutput.Bytes(), &doctorResponse); err != nil || !doctorResponse.OK || len(doctorResponse.Data.Findings) != 0 {
		t.Fatalf("automatic doctor after clean sealed install should have no findings: err=%v stdout=%s", err, doctorOutput.String())
	}
	secondaryRoot := filepath.Join(home, "kb-platform-secondary")
	_, _, secondaryCode, secondaryText := executeForTest(t, operationTestSetup{Operation: "apply", Index: index,
		Direct:     directRequest{PlatformRoot: secondaryRoot, PlatformChannel: "stable", ServiceProfile: "default", Policy: "compatible"},
		Executable: launcher, RuntimeDeps: &runtime.Dependencies{Artifacts: installArtifacts{root: secondaryRoot}, Host: installHost{}},
		NodePreflight: func() error { return nil }, JSON: true,
	})
	if secondaryCode != 0 {
		t.Fatalf("secondary no-flag apply exit %d: %s", secondaryCode, secondaryText)
	}
	secondaryReceipt, err := receipt.Read(secondaryRoot)
	if err != nil || secondaryReceipt.Verification.HostHealthURL != fmt.Sprintf("http://127.0.0.1:%d/health", port+100) {
		t.Fatalf("secondary receipt Gateway = %q, error=%v; want next 100-port offset", secondaryReceipt.Verification.HostHealthURL, err)
	}
	registry, err := (gateway.Registry{Path: filepath.Join(home, ".kb", "launcher", "gateway-roots.json")}).Read()
	if err != nil || len(registry) != 2 {
		t.Fatalf("registered Gateway roots = %#v, error=%v", registry, err)
	}
}

// case: S-187.4
func TestRegisterProjectInvokesPlainProjectAdd(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "secondary-root")
	bin := filepath.Join(root, ".kb", "v2", "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	kb := filepath.Join(bin, "kb")
	argsFile := filepath.Join(home, "args")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$CAPTURE_ARGS\"\nprintf '{\"ok\":true,\"data\":{\"project\":{\"id\":\"prj_test123\",\"path\":\"%s\"}}}' \"$PROJECT_PATH\"\n"
	if err := os.WriteFile(kb, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CAPTURE_ARGS", argsFile)
	projectPath := filepath.Join(home, "project")
	t.Setenv("PROJECT_PATH", projectPath)
	if err := os.Mkdir(projectPath, 0o755); err != nil {
		t.Fatal(err)
	}

	projectID, err := registerProject(root, projectPath, nil)
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

// case: S-187.4
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
	_, failure, code, _ := executeForTest(t, operationTestSetup{
		Operation: "install", Index: index, Direct: directRequest{PlatformRoot: platformRoot, ProjectRoot: projectRoot},
		Executable: launcher, InstallInput: strings.NewReader(strings.Repeat("\n", 12)), InstallOutput: &screen,
		RuntimeDeps:      &runtime.Dependencies{Artifacts: installArtifacts{root: platformRoot}, Host: installHost{}},
		NodePreflight:    func() error { return nil },
		ProjectRegistrar: func(path string) (string, error) { registered = path; return "prj_test123", nil },
	})
	if code != 0 || registered != projectRoot {
		t.Fatalf("code=%d failure=%v registered=%q screen:\n%s", code, failure, registered, screen.String())
	}
	for _, expected := range []string{projectRoot, "prj_test123", "Verification: platform installed", "kb project show prj_test123"} {
		if !strings.Contains(screen.String(), expected) {
			t.Errorf("screen missing %q:\n%s", expected, screen.String())
		}
	}
	if _, err := os.Stat(filepath.Join(projectRoot, ".kb")); !os.IsNotExist(err) {
		t.Fatalf("installer created project files: %v", err)
	}

	failedRoot := filepath.Join(home, "kb-platform-registration-failure")
	var failedScreen bytes.Buffer
	_, failure, failedCode, _ := executeForTest(t, operationTestSetup{
		Operation: "install", Index: index,
		Direct:        directRequest{PlatformRoot: failedRoot, ProjectRoot: projectRoot},
		InstallInput:  strings.NewReader(strings.Repeat("\n", 12)),
		InstallOutput: &failedScreen,
		RuntimeDeps:   &runtime.Dependencies{Artifacts: installArtifacts{root: failedRoot}, Host: installHost{}},
		NodePreflight: func() error { return nil },
		ProjectRegistrar: func(string) (string, error) {
			return "", errors.New("project registry unavailable")
		},
	})
	if failedCode != 1 || failure == nil || failure.Code != contracts.CodeApplyFailed || !strings.Contains(failure.Hint, "kb project add") {
		t.Fatalf("project registration failure = code %d, output %#v", failedCode, failure)
	}
}

func writeJSONInstallFixture(t *testing.T, home string) string {
	t.Helper()
	index := filepath.Join(home, "release-index.json")
	release, err := catalog.Seal(catalog.Catalog{
		Channels:      map[contracts.Channel]string{contracts.ChannelStable: "2.0.0"},
		Compatibility: testCompatibility("2.0.0"),
		Platforms: []catalog.PlatformBundle{{ID: "platform", Version: "2.0.0", Package: "@kb/platform",
			Tarball: "https://example.test/platform.tgz", SHA256: "platform", Host: testHostOnFreePortWithPortHeadroom(t, 100),
			Profiles: map[string]contracts.ServiceGraph{"default": {PlatformVersion: "2.0.0"}},
			Config:   []catalog.ConfigRequirement{{ID: "gateway.access.mode", Path: "/gateway/access/mode", Default: `"local"`}}}},
		Adapters: []catalog.Adapter{{Component: catalog.Component{ID: "sqlite-stores", Version: "1.0.0",
			Package: "@kb/sqlite-stores", Tarball: "https://example.test/sqlite-stores.tgz", SHA256: "sqlite-stores"},
			Provides: []string{"documentDatabase"}}},
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
	return index
}

// case: S-187.6
func TestJSONScenarioInstallRegistersCanonicalProject(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	index := writeJSONInstallFixture(t, home)
	root, project := filepath.Join(home, "platform"), filepath.Join(home, "project")
	if err := os.Mkdir(project, 0o755); err != nil {
		t.Fatal(err)
	}
	canonical, err := filepath.EvalSymlinks(project)
	if err != nil {
		t.Fatal(err)
	}
	registry := map[string]string{}
	_, failure, code, raw := executeForTest(t, operationTestSetup{
		Operation: "install", JSON: true, Index: index, ScenarioID: "custom",
		ScenarioAnswers: `{"access.mode":"local"}`,
		Direct:          directRequest{PlatformRoot: root, ProjectRoot: project},
		RuntimeDeps:     &runtime.Dependencies{Artifacts: installArtifacts{root: root}, Host: installHost{}},
		NodePreflight:   func() error { return nil },
		ProjectRegistrar: func(path string) (string, error) {
			registry[path] = "prj_json"
			return registry[path], nil
		},
	})
	if code != 0 || registry[canonical] != "prj_json" {
		t.Fatalf("JSON scenario install exit=%d registry=%v failure=%v output=%s", code, registry, failure, raw)
	}
}

// case: S-187.6
func TestJSONRequestFileInstallRegistersCanonicalProject(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	index := writeJSONInstallFixture(t, home)
	root, project := filepath.Join(home, "platform"), filepath.Join(home, "project")
	if err := os.Mkdir(project, 0o755); err != nil {
		t.Fatal(err)
	}
	canonical, err := filepath.EvalSymlinks(project)
	if err != nil {
		t.Fatal(err)
	}
	request := contracts.InstallRequest{Schema: contracts.RequestSchema, PlatformRoot: root,
		ProjectRoot: project, ServiceProfile: "default", Source: contracts.SourceRegistry}
	requestData, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	requestPath := filepath.Join(home, "request.json")
	if err := os.WriteFile(requestPath, requestData, 0o600); err != nil {
		t.Fatal(err)
	}
	registry := map[string]string{}
	_, failure, code, raw := executeForTest(t, operationTestSetup{
		Operation: "install", JSON: true, Index: index, Input: requestPath,
		RuntimeDeps:   &runtime.Dependencies{Artifacts: installArtifacts{root: root}, Host: installHost{}},
		NodePreflight: func() error { return nil },
		ProjectRegistrar: func(path string) (string, error) {
			registry[path] = "prj_input"
			return registry[path], nil
		},
	})
	if code != 0 || registry[canonical] != "prj_input" {
		t.Fatalf("JSON request install exit=%d registry=%v failure=%v output=%s", code, registry, failure, raw)
	}
}

func TestInstallEarlyFailuresUseDiagRendering(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_, failure, code, _ := executeForTest(t, operationTestSetup{Operation: "install", Index: filepath.Join(t.TempDir(), "missing.json"), NodePreflight: func() error { return nil }})
	if code != 2 || failure == nil || failure.Code != contracts.CodeReleaseIndexUnreadable || failure.Hint == "" {
		t.Fatalf("code/failure = %d / %#v", code, failure)
	}
}

func TestInstallChecksNodeBeforeQuestions(t *testing.T) {
	_, path := installOutput(t)
	var screen bytes.Buffer
	_, failure, code, _ := executeForTest(t, operationTestSetup{Operation: "install", InstallInput: strings.NewReader("\n"), InstallOutput: &screen, NodePreflight: func() error { return errors.New("node missing") }})
	if code != 2 || failure == nil || failure.Code != contracts.CodeNodeVersion || strings.Contains(screen.String(), "Platform channel") {
		t.Fatalf("code/failure/output = %d / %#v / %s", code, failure, screen.String())
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}

func TestApplyReportsNodeVersionCodeBeforeResolution(t *testing.T) {
	_, failure, code, _ := executeForTest(t, operationTestSetup{Operation: "apply", NodePreflight: func() error { return errors.New("Node.js v22.11.0 is unsupported") }})
	if failure == nil {
		t.Fatalf("expected Node version failure, exit=%d", code)
	}
	data := fmt.Sprint(failure) + " " + failure.Hint
	if code != 2 || failure.Code != contracts.CodeNodeVersion || !strings.Contains(data, "Install Node.js 24") || strings.Contains(data, "KB_INSTALL_INPUT_REQUIRED") {
		t.Fatalf("code/output = %d / %s", code, data)
	}
}

func TestHumanProgressUsesElapsedPhaseEvents(t *testing.T) {
	output := &bytes.Buffer{}
	p := &humanProgress{out: output, started: map[string]time.Time{}}
	p.started["Install packages"] = time.Now().Add(-time.Millisecond)
	if err := p.Emit(runtime.ProgressEvent{Phase: "Install packages"}); err != nil {
		t.Fatal(err)
	}
	data := output.String()
	if !strings.Contains(data, "Install packages") || !strings.Contains(data, "ms)") {
		t.Fatalf("progress output = %s", data)
	}
}

type writeFailure struct{ err error }

func (failure writeFailure) Write([]byte) (int, error) { return 0, failure.err }

func TestStreamProgressPropagatesWriterFailure(t *testing.T) {
	want := errors.New("closed stream")
	progress := streamProgress{writer: stream.New(writeFailure{err: want})}
	if err := progress.Emit(runtime.ProgressEvent{Phase: "Install packages", Start: true}); !errors.Is(err, want) {
		t.Fatalf("stream progress error = %v, want %v", err, want)
	}
}

func TestHumanProgressPropagatesWriterFailure(t *testing.T) {
	want := errors.New("closed terminal")
	progress := &humanProgress{out: writeFailure{err: want}, started: map[string]time.Time{}}
	if err := progress.Emit(runtime.ProgressEvent{Phase: "Install packages", Start: true}); !errors.Is(err, want) {
		t.Fatalf("human progress error = %v, want %v", err, want)
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

func testHostOnFreePortWithPortHeadroom(t *testing.T, headroom int) *contracts.HostSpec {
	t.Helper()
	listener := testListenerWithPortHeadroom(t, headroom)
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return &contracts.HostSpec{Command: "kb-host", HealthURL: fmt.Sprintf("http://127.0.0.1:%d/health", port)}
}

func testListenerWithPortHeadroom(t *testing.T, headroom int) net.Listener {
	t.Helper()
	for range 128 {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		port := listener.Addr().(*net.TCPAddr).Port
		if port+headroom <= 65535 {
			return listener
		}
		if err := listener.Close(); err != nil {
			t.Fatal(err)
		}
	}
	t.Fatal("could not reserve a loopback port with the required allocation headroom")
	return nil
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
	for _, expected := range []string{"install", "Install KB Labs interactively", "apply"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("help is missing %q: %s", expected, text)
		}
	}
	if strings.Contains(text, "--provider") {
		t.Fatalf("root help includes command-local --provider: %s", text)
	}
	install := findCommand(root, "kb-create install")
	var installHelp bytes.Buffer
	install.SetOut(&installHelp)
	if err := install.Help(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(installHelp.String(), "--provider") {
		t.Fatalf("install help is missing --provider: %s", installHelp.String())
	}
}
