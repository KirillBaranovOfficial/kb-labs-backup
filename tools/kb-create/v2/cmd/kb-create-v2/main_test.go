package v2cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	goruntime "runtime"
	"strings"
	"testing"
	"time"

	clikitcommand "github.com/kb-labs/clikit/command"
	"github.com/kb-labs/clikit/diag"
	"github.com/kb-labs/clikit/stream"
	"github.com/kb-labs/create/v2/catalog"
	"github.com/kb-labs/create/v2/contracts"
	"github.com/kb-labs/create/v2/gateway"
	"github.com/kb-labs/create/v2/host"
	"github.com/kb-labs/create/v2/installed"
	"github.com/kb-labs/create/v2/internal/testfixtures"
	"github.com/kb-labs/create/v2/receipt"
	"github.com/kb-labs/create/v2/runtime"
	"github.com/kb-labs/create/v2/shim"
	"github.com/kb-labs/create/v2/versionroot"
	"github.com/spf13/cobra"
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
	writeVersionedReceipt(t, root, contracts.InstallReceipt{ID: "client-r1", InstallationID: "installation-client", Plan: plan})
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
	source := testReleaseCatalog(t, "2.0.0")
	source.Platforms[0].Profiles = map[string]contracts.ServiceGraph{"default": {PlatformVersion: "2.0.0", Services: []contracts.Service{{ID: "gateway", Port: 4000}}}}
	source.Platforms[0].Config = []catalog.ConfigRequirement{{ID: "platform.adapterOptions", Path: "/platform/adapterOptions",
		Default: `{"serviceTransport":{"services":{"gateway":{"url":"http://127.0.0.1:4000"}}}}`}}
	release, err := catalog.Seal(source)
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
		"--platform-root", filepath.Join(t.TempDir(), "platform"), "--gateway-url", gatewayURL}
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

// case: PC-001.7, PC-002.2, S-040.3
func TestExecuteInstallThenStatusUsesTheInstallRootIdentity(t *testing.T) {
	_, fakehost := binaries(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(host.KBHomeEnv, filepath.Join(home, ".kb"))
	root := filepath.Join(home, "kb-platform")
	launcher, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	indexPath := filepath.Join(home, "release-index.json")
	port := freeLoopbackPort(t)
	source := testReleaseCatalog(t, "2.0.0")
	source.Platforms[0].Host = &contracts.HostSpec{Command: launcher, Args: []string{"-test.run=^TestHostExecLauncher$"},
		Env:       map[string]string{"KB_TEST_FAKEHOST": fakehost, "KB_TEST_FAKEHOST_PORT": fmt.Sprint(port)},
		HealthURL: fmt.Sprintf("http://127.0.0.1:%d/health", port), ReadyTimeoutSeconds: 10, StopGraceSeconds: 5}
	source.Platforms[0].Profiles = map[string]contracts.ServiceGraph{"default": {PlatformVersion: "2.0.0", Services: []contracts.Service{{ID: "gateway", Port: port}}}}
	source.Platforms[0].Config = []catalog.ConfigRequirement{{ID: "gateway.access.mode", Path: "/gateway/access/mode", Default: `"local"`},
		{ID: "platform.adapterOptions", Path: "/platform/adapterOptions", Default: fmt.Sprintf(`{"serviceTransport":{"services":{"gateway":{"url":"http://127.0.0.1:%d"}}}}`, port)}}
	source.Plugins = []catalog.Component{{ID: "@kb-labs/commit", Version: "1.0.0", Package: "@kb/commit", Tarball: "https://example.test/commit.tgz", SHA256: "commit"}}
	release, err := catalog.Seal(source)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(release)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(indexPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	deps := commandDependencies{NodeCheck: func() error { return nil }, ArtifactRunner: operationLogTestRunner{},
		Runtime: &runtime.Dependencies{Artifacts: installArtifacts{root: root}}}
	commandFactory := func(cmd *cobra.Command) commandDependencies {
		deps.Command = cmd
		return deps
	}
	var installOutput, installError bytes.Buffer
	installCode := clikitcommand.Execute(newRootCommandWithDependencyFactory(commandFactory), []string{"install", "--json", "--index", indexPath,
		"--platform-root", root, "--service-profile", "default", "--scenario", "explore"}, &installOutput, &installError)
	if installCode != 0 {
		state, _ := host.StateForRoot(root)
		hostLog, _ := os.ReadFile(state.HostLog())
		supervisorLog, _ := os.ReadFile(state.SupervisorLog())
		t.Fatalf("install argv exit=%d: stdout=%s stderr=%s hostLog=%s supervisorLog=%s", installCode, installOutput.String(), installError.String(), hostLog, supervisorLog)
	}
	t.Cleanup(func() {
		var stdout, stderr bytes.Buffer
		code := clikitcommand.Execute(newRootCommand(), []string{"stop", "--platform-root", root, "--json"}, &stdout, &stderr)
		if code != 0 {
			t.Errorf("stop installed test host: code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
		}
	})
	layout, err := versionroot.New(root)
	if err != nil {
		t.Fatal(err)
	}
	physicalRoot, err := layout.Active()
	if err != nil {
		t.Fatal(err)
	}
	for _, spelling := range []string{root, layout.PlatformRoot(), physicalRoot} {
		var stdout, stderr bytes.Buffer
		code := clikitcommand.Execute(newRootCommand(), []string{"status", "--platform-root", spelling, "--json"}, &stdout, &stderr)
		var response struct {
			Data map[string]any `json:"data"`
		}
		if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
			t.Fatalf("status for %q was not JSON: %v; stdout=%s stderr=%s", spelling, err, stdout.String(), stderr.String())
		}
		if code != 0 || response.Data["installed"] != true || response.Data["running"] != true || response.Data["reachable"] != true {
			t.Fatalf("status for %q after Execute install: code=%d data=%#v stdout=%s stderr=%s", spelling, code, response.Data, stdout.String(), stderr.String())
		}
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
	source := testReleaseCatalog(t, "2.0.0")
	source.Platforms[0].Profiles = map[string]contracts.ServiceGraph{"default": {PlatformVersion: "2.0.0", Services: []contracts.Service{{ID: "gateway", Port: 4000}}}}
	source.Platforms[0].Config = []catalog.ConfigRequirement{{ID: "platform.adapterOptions", Path: "/platform/adapterOptions",
		Default: `{"serviceTransport":{"services":{"gateway":{"url":"http://127.0.0.1:4000"}}}}`}}
	release, err := catalog.Seal(source)
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
		"--platform-root", rootB}, &stdout, &stderr)
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
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("KB_HOME", filepath.Join(home, ".kb"))
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

func TestReceiptArtifactIdentityFailureGivesExactReinstallCommand(t *testing.T) {
	index := filepath.Join(t.TempDir(), "release index.json")
	platformRoot := filepath.Join(t.TempDir(), "installed platform")
	original := diag.New(contracts.CodeArtifactIdentityInvalid, "the active receipt contains repeated artifacts")
	failure := receiptArtifactIdentityFailure(original, index, platformRoot)
	if failure == nil || failure.Code != contracts.CodeArtifactIdentityInvalid {
		t.Fatalf("identity failure = %#v", failure)
	}
	want := "Reinstall this platform with `" + contracts.ShellCommand("kb-create", "apply", "--index", index, "--platform-root", platformRoot) + "`."
	if failure.Hint != want {
		t.Fatalf("hint = %q, want %q", failure.Hint, want)
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
	if !strings.Contains(failure.Hint, contracts.ShellCommand("kb-create", "doctor", "--fix", "--platform-root", "/tmp/platform root")) {
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
	if err := os.MkdirAll(root, 0o750); err != nil {
		t.Fatal(err)
	}
	index := filepath.Join(home, "release-index.json")
	source := testReleaseCatalog(t, "2.0.0")
	source.Platforms[0].Profiles = map[string]contracts.ServiceGraph{"default": {PlatformVersion: "2.0.0"}}
	source.Platforms[0].Host = testHostOnFreePort(t)
	release, err := catalog.Seal(source)
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
	foreign := filepath.Join(root, "user-owned.txt")
	if err := os.WriteFile(foreign, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	foreignBefore := snapshotTree(t, root)
	_, failure, code, _ = executeForTest(t, operationTestSetup{Operation: "plan", Index: index, Direct: directRequest{PlatformRoot: root,
		PlatformVersion: "2.0.0", Policy: string(contracts.PolicyCompatible)}, JSON: true})
	if code == 0 || failure == nil || failure.Code != contracts.CodeInstallLayoutUnsupported {
		t.Fatalf("plan on a non-empty foreign root = code %d, diagnostic %#v", code, failure)
	}
	if after := snapshotTree(t, root); !bytes.Equal(foreignBefore, after) {
		t.Fatalf("plan changed a foreign target: before %q, after %q", foreignBefore, after)
	}
}

// case: PC-001.8
func TestUpdatePlanPreservesLivePlatformDatabaseFiles(t *testing.T) {
	inst := newInstallation(t)
	projection := contracts.PlatformDataRootPath()
	dataRoot := filepath.Join(inst.root, filepath.FromSlash(projection.StatePath))
	database := filepath.Join(dataRoot, "journey-plan.sqlite")
	files := startLiveSQLiteDatabase(t, database)
	before := make(map[string][]byte, len(files))
	for _, path := range files {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		before[path] = content
	}

	index := filepath.Join(t.TempDir(), "release-index.json")
	source := testReleaseCatalog(t, "2.0.0")
	source.Platforms[0].Profiles = map[string]contracts.ServiceGraph{"default": {PlatformVersion: "2.0.0"}}
	source.Platforms[0].Host = testHostOnFreePort(t)
	release, err := catalog.Seal(source)
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
	_, failure, code, output := executeForTest(t, operationTestSetup{Operation: "plan", Index: index, Direct: directRequest{
		PlatformRoot: inst.root, PlatformVersion: "2.0.0", Policy: string(contracts.PolicyCompatible),
	}, JSON: true})
	if code != 0 || failure != nil {
		t.Fatalf("update plan failed with live platform data: code=%d diagnostic=%#v output=%s", code, failure, output)
	}
	for _, path := range files {
		content, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(content, before[path]) {
			t.Errorf("planning changed %s: read error %v", filepath.Base(path), err)
		}
		if strings.Contains(output, filepath.Base(path)) {
			t.Errorf("plan reported platform data file %s as a change: %s", filepath.Base(path), output)
		}
	}
	err = filepath.WalkDir(filepath.Join(inst.root, "versions"), func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 || entry.IsDir() {
			return nil
		}
		for _, name := range files {
			if filepath.Base(path) == filepath.Base(name) {
				t.Errorf("plan copied platform data file into version root: %s", path)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func startLiveSQLiteDatabase(t *testing.T, database string) []string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(database), 0o700); err != nil {
		t.Fatal(err)
	}
	script := `import sqlite3,sys
c=sqlite3.connect(sys.argv[1])
c.execute("PRAGMA journal_mode=WAL")
c.execute("PRAGMA wal_autocheckpoint=0")
c.execute("CREATE TABLE entries (value TEXT)")
c.execute("INSERT INTO entries VALUES ('live')")
c.commit()
print("READY", flush=True)
sys.stdin.readline()
c.close()`
	command := exec.Command("python3", "-u", "-c", script, database)
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = io.WriteString(stdin, "stop\n")
		_ = stdin.Close()
		if err := command.Wait(); err != nil {
			t.Errorf("SQLite fixture process failed: %v; stderr: %s", err, stderr.String())
		}
	})
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || strings.TrimSpace(line) != "READY" {
		t.Fatalf("SQLite fixture did not become ready: line=%q err=%v stderr=%s", line, err, stderr.String())
	}
	files := []string{database, database + "-wal", database + "-shm"}
	for _, path := range files {
		if info, err := os.Stat(path); err != nil || info.Size() == 0 {
			t.Fatalf("live SQLite file %s is missing or empty: %v", path, err)
		}
	}
	return files
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
	root, err := a.installRoot()
	if err != nil {
		return err
	}
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
	bin := filepath.Join(root, "node_modules", "@kb-labs", "cli-bin", "dist")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		if progress != nil {
			err = errors.Join(err, progress("Install packages", false))
		}
		return err
	}
	err = os.WriteFile(filepath.Join(bin, "bin.js"), []byte("export {};\n"), 0o644)
	// Like a real install, every artifact package lands with its package.json;
	// the platform lock hashes it for discovery's integrity check.
	for _, item := range items {
		if err != nil || item.Package == "" {
			continue
		}
		dir := filepath.Join(root, "node_modules", filepath.FromSlash(item.Package))
		if err = os.MkdirAll(dir, 0o755); err == nil {
			err = os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"name":"`+item.Package+`","version":"`+item.Version+`"}`), 0o644)
		}
		if err == nil {
			err = installed.Write(root, item)
		}
	}
	if progress != nil {
		progress("Install packages", false)
	}
	return err
}

func (a installArtifacts) installRoot() (string, error) {
	versions := filepath.Join(a.root, "versions")
	entries, err := os.ReadDir(versions)
	if os.IsNotExist(err) {
		return a.root, nil
	}
	if err != nil {
		return "", err
	}
	stages := make([]string, 0, 1)
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".stage-") && entry.IsDir() {
			stages = append(stages, filepath.Join(versions, entry.Name()))
		}
	}
	if len(stages) == 1 {
		return stages[0], nil
	}
	return a.root, nil
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

// case: PC-001.17, PC-001.18
func TestInstallCommandClassifiesInstallRootTargets(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("KB_HOME", filepath.Join(home, ".kb"))
	index := filepath.Join(home, "release-index.json")
	source, err := catalog.LoadFile(filepath.Join("..", "..", "..", "..", "..", "e2e", "platform", "fixtures", "release-index-v2.json"))
	if err != nil {
		t.Fatal(err)
	}
	graph := source.Platforms[0].Profiles["default"]
	baseGatewayPort := 0
	highestServicePort := 0
	for _, service := range graph.Services {
		if service.ID == "gateway" {
			baseGatewayPort = service.Port
		}
		if service.Port > highestServicePort {
			highestServicePort = service.Port
		}
	}
	if baseGatewayPort == 0 {
		t.Fatal("default service graph has no Gateway port")
	}
	source.Platforms[0].Host = testHostOnFreePortWithPortHeadroom(t, highestServicePort)
	release, err := catalog.Seal(source)
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
	project := filepath.Join(home, "fresh-project")
	if err := os.Mkdir(project, 0o700); err != nil {
		t.Fatal(err)
	}
	launcher := filepath.Join(home, "bin", "kb-create")
	if err := os.MkdirAll(filepath.Dir(launcher), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(launcher, []byte("launcher"), 0o700); err != nil {
		t.Fatal(err)
	}

	runInstall := func(root string) (int, string) {
		t.Helper()
		dependencyFactory := func(cmd *cobra.Command) commandDependencies {
			return commandDependencies{
				Runtime:       &runtime.Dependencies{Artifacts: installArtifacts{root: root}, Host: installHost{}},
				NodeCheck:     func() error { return nil },
				InstallInput:  strings.NewReader(strings.Repeat("\n", 12)),
				InstallOutput: clikitcommand.FromCommand(cmd).ProgressWriter(),
				ProjectRegistrar: func(_ string, path string) (projectRegistrationResult, error) {
					return projectRegistrationResult{ID: "prj_0123456789abcdef", CanonicalPath: path}, nil
				},
				Command: cmd, Executable: launcher,
			}
		}
		args := []string{"install", "--json", "--index", index, "--platform-root", root,
			"--service-profile", "default", "--scenario", "explore", "--registry", "http://verdaccio:4873",
			"--project-root", project}
		var stdout, stderr bytes.Buffer
		code := clikitcommand.Execute(newRootCommandWithDependencyFactory(dependencyFactory), args, &stdout, &stderr)
		return code, stdout.String() + stderr.String()
	}

	for _, target := range []string{"missing", "empty"} {
		t.Run(target, func(t *testing.T) {
			root := filepath.Join(home, target)
			if target == "empty" {
				if err := os.Mkdir(root, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			if code, output := runInstall(root); code != 0 {
				t.Fatalf("install target %s: code=%d output=%s", target, code, output)
			}
			layout, err := versionroot.New(root)
			if err != nil {
				t.Fatal(err)
			}
			if err := layout.Validate(); err != nil {
				t.Fatalf("install target %s did not become a valid versioned install: %v", target, err)
			}
			if _, err := receipt.Read(layout.PlatformRoot()); err != nil {
				t.Fatal(err)
			}
		})
	}

	partialRoot := filepath.Join(home, "partial")
	partialLayout, err := versionroot.New(partialRoot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := partialLayout.StageInitial(); err != nil {
		t.Fatal(err)
	}
	if code, output := runInstall(partialRoot); code != 0 {
		t.Fatalf("install retry from a declared initial handoff: code=%d output=%s", code, output)
	}
	if err := partialLayout.Validate(); err != nil {
		t.Fatalf("install retry did not recover the declared initial handoff: %v", err)
	}

	foreignRoot := filepath.Join(home, "foreign")
	if err := os.Mkdir(foreignRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	foreignFile := filepath.Join(foreignRoot, "keep.txt")
	if err := os.WriteFile(foreignFile, []byte("user data\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	foreignBefore := snapshotTree(t, foreignRoot)
	if code, output := runInstall(foreignRoot); code == 0 || !strings.Contains(output, contracts.CodeInstallLayoutUnsupported) {
		t.Fatalf("foreign target was not refused with KB_INSTALL_LAYOUT_UNSUPPORTED: code=%d output=%s", code, output)
	}
	if after := snapshotTree(t, foreignRoot); !bytes.Equal(after, foreignBefore) {
		t.Fatalf("foreign target changed: before=%q after=%q", foreignBefore, after)
	}

	for _, currentEntry := range []string{"file", "directory"} {
		t.Run("damaged-current-"+currentEntry, func(t *testing.T) {
			damagedRoot := filepath.Join(home, "damaged-"+currentEntry)
			if err := os.Mkdir(damagedRoot, 0o700); err != nil {
				t.Fatal(err)
			}
			current := filepath.Join(damagedRoot, "current")
			if currentEntry == "directory" {
				if err := os.Mkdir(current, 0o700); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(current, []byte("not a link\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			before := snapshotTree(t, damagedRoot)
			if code, output := runInstall(damagedRoot); code == 0 || !strings.Contains(output, contracts.CodeInstallLayoutUnsupported) {
				t.Fatalf("damaged target was not refused with KB_INSTALL_LAYOUT_UNSUPPORTED: code=%d output=%s", code, output)
			}
			if after := snapshotTree(t, damagedRoot); !bytes.Equal(after, before) {
				t.Fatalf("damaged target changed: before=%q after=%q", before, after)
			}
		})
	}
}

func TestInstallAndApplyArgvRefuseUnattributedRootsUnchanged(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("KB_HOME", filepath.Join(home, ".kb"))
	index := filepath.Join("..", "..", "..", "..", "..", "e2e", "platform", "fixtures", "release-index-v2.json")
	project := filepath.Join(home, "fresh-project")
	if err := os.Mkdir(project, 0o700); err != nil {
		t.Fatal(err)
	}

	targets := []struct {
		name  string
		setup func(*testing.T, string)
	}{
		{"foreign preinstall scenario", func(t *testing.T, root string) {
			path := filepath.Join(root, "state", "scenarios", "foreign.json")
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("foreign bytes\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{"ownership and versions without current", func(t *testing.T, root string) {
			if err := os.MkdirAll(filepath.Join(root, "versions", "1.0.0"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "ownership.json"), []byte(`{"schema":"kb.create.installation-owner/v1","installationId":"00112233445566778899aabbccddeeff"}`), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, target := range targets {
		t.Run(target.name, func(t *testing.T) {
			root := filepath.Join(home, strings.ReplaceAll(target.name, " ", "-"))
			if err := os.Mkdir(root, 0o700); err != nil {
				t.Fatal(err)
			}
			target.setup(t, root)
			before := snapshotTree(t, root)
			for _, operation := range []string{"install", "plan", "apply"} {
				args := []string{operation, "--json", "--index", index, "--platform-root", root,
					"--service-profile", "default", "--scenario", "commit", "--registry", "http://verdaccio:4873",
					"--project-root", project}
				var stdout, stderr bytes.Buffer
				code := clikitcommand.Execute(newRootCommand(), args, &stdout, &stderr)
				var payload map[string]any
				decodeErr := json.Unmarshal(stdout.Bytes(), &payload)
				if decodeErr != nil {
					firstRecord := strings.SplitN(stdout.String(), "\n", 2)[0]
					decodeErr = json.Unmarshal([]byte(firstRecord), &payload)
				}
				if decodeErr != nil {
					t.Fatalf("%s argv output is not JSON: %s stderr=%s: %v", operation, stdout.String(), stderr.String(), decodeErr)
				}
				if code == 0 || errorCode(payload) != contracts.CodeInstallLayoutUnsupported {
					t.Fatalf("%s argv refusal = exit %d, code %s, stdout=%s stderr=%s", operation, code, errorCode(payload), stdout.String(), stderr.String())
				}
				if after := snapshotTree(t, root); !bytes.Equal(after, before) {
					t.Fatalf("%s changed refused target: before=%q after=%q", operation, before, after)
				}
			}
		})
	}
}

func TestOwnershipPublicationErrorsHaveActionableEnvelopes(t *testing.T) {
	root := filepath.Join(t.TempDir(), "kb-platform")
	conflict := unsupportedPlatformLayout(root, versionroot.ErrOwnershipPublicationConflict)
	installCommand := contracts.ShellCommand("kb-create", "install")
	if conflict.Code != contracts.CodeOperationInProgress || !strings.Contains(conflict.Hint, "Wait for the other install to finish") || !strings.Contains(conflict.Hint, installCommand) {
		t.Fatalf("publication conflict envelope = %#v", conflict)
	}
	stagingPath := filepath.Join(root, contracts.OwnershipRecordStagingPrefix()+"00112233445566778899aabbccddeeff")
	staging := unsupportedPlatformLayout(root, &versionroot.OwnershipStagingArtifactError{Path: stagingPath, Cause: errors.New("truncated record")})
	cleanupCommand := contracts.ShellCommand("rm", "--", stagingPath)
	if staging.Code != contracts.CodeInstallLayoutUnsupported || !strings.Contains(staging.Hint, stagingPath) || !strings.Contains(staging.Hint, cleanupCommand) {
		t.Fatalf("staging artifact envelope = %#v", staging)
	}
}

// case: PC-001.16, S-054.2
func TestInstallRunsInteractiveCommandWithDefaultsAndProgress(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("KB_HOME", filepath.Join(home, ".kb"))
	_, fakehost := binaries(t)
	port := freeLoopbackPort(t)
	root := filepath.Join(home, "kb-platform")
	index := filepath.Join(home, "release-index.json")
	testExecutable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	source := testReleaseCatalog(t, "2.0.0")
	source.Platforms[0].Host = &contracts.HostSpec{Command: testExecutable, Args: []string{"-test.run=^TestHostExecLauncher$"},
		Env:       map[string]string{"KB_TEST_FAKEHOST": fakehost, "KB_TEST_FAKEHOST_PORT": fmt.Sprint(port)},
		HealthURL: fmt.Sprintf("http://127.0.0.1:%d/health", port), ReadyTimeoutSeconds: 10, StopGraceSeconds: 5}
	source.Platforms[0].Profiles = map[string]contracts.ServiceGraph{"default": {PlatformVersion: "2.0.0", Services: []contracts.Service{{ID: "gateway", Port: port}}}}
	source.Platforms[0].Config = []catalog.ConfigRequirement{{ID: "gateway.access.mode", Path: "/gateway/access/mode", Default: `"local"`},
		{ID: "platform.adapterOptions", Path: "/platform/adapterOptions", Default: fmt.Sprintf(`{"serviceTransport":{"services":{"gateway":{"url":"http://127.0.0.1:%d"}}}}`, port)}}
	source.Plugins = []catalog.Component{{ID: "@kb-labs/commit", Version: "1.0.0", Package: "@kb/commit", Tarball: "https://example.test/commit.tgz", SHA256: "commit"}}
	release, err := catalog.Seal(source)
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
	t.Cleanup(func() {
		runner, runnerErr := host.NewRunner(root)
		if runnerErr != nil {
			if _, statErr := os.Stat(filepath.Join(root, "current")); os.IsNotExist(statErr) {
				return
			}
			t.Errorf("load interactive-install test host runner: %v", runnerErr)
			return
		}
		if _, runnerErr = runner.Stop(context.Background()); runnerErr != nil {
			t.Errorf("stop interactive-install test host: %v", runnerErr)
		}
	})
	_, failure, code, _ := executeForTest(t, operationTestSetup{Operation: "install", Index: index, Direct: directRequest{PlatformRoot: root}, Executable: launcher, InstallInput: strings.NewReader(strings.Repeat("\n", 12)), InstallOutput: &screen, RuntimeDeps: &runtime.Dependencies{Artifacts: installArtifacts{root: root}}, NodePreflight: func() error { return nil }})
	if code != 0 {
		t.Fatalf("exit code %d, failure=%v, screen:\n%s", code, failure, screen.String())
	}
	for _, expected := range []string{"Resolve (", "Download and verify artifacts (", "Install packages (", "Render config (", "Verify installation (", "Start host (", "Create kb shim (", "KB Labs is installed", contracts.ShellCommand("kb", "project", "add", "<path>")} {
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
	if target, err := os.Readlink(filepath.Join(home, "bin", "kb")); err != nil || target != filepath.Join(platformRootForTest(t, root), ".kb", "v2", "bin", "kb") {
		t.Fatalf("kb shim target/error = %q / %v", target, err)
	}
	installedReceipt, err := receipt.Read(platformRootForTest(t, root))
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
	doctorCommand := newRootCommandWithDependencyFactory(func(cmd *cobra.Command) commandDependencies {
		deps := productionCommandDependencies(cmd)
		deps.Executable = launcher
		return deps
	})
	doctorCode := clikitcommand.Execute(doctorCommand, []string{"doctor", "--platform-root", root, "--json"}, &doctorOutput, &doctorError)
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
	secondaryReceipt, err := receipt.Read(platformRootForTest(t, secondaryRoot))
	if err != nil || secondaryReceipt.Verification.HostHealthURL != fmt.Sprintf("http://127.0.0.1:%d/health", port+100) {
		t.Fatalf("secondary receipt Gateway = %q, error=%v; want next 100-port offset", secondaryReceipt.Verification.HostHealthURL, err)
	}
	registry, err := (gateway.Registry{Path: filepath.Join(home, ".kb", "launcher", "gateway-roots.json")}).Read()
	if err != nil || len(registry) != 2 {
		t.Fatalf("registered Gateway roots = %#v, error=%v", registry, err)
	}
}

// case: S-187.6
func TestLocalProjectRegistrationUsesHostProjectAPIWithoutCredentials(t *testing.T) {
	project := t.TempDir()
	root := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/projects" || r.Header.Get("Authorization") != "" {
			t.Errorf("request path/auth = %s / %q", r.URL.Path, r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = fmt.Fprintf(w, `{"ok":true,"data":{"id":"prj_0123456789abcdef","path":%q,"alreadyExisted":false}}`, project)
	}))
	defer server.Close()
	active := contracts.InstallReceipt{ID: "local-receipt", Plan: contracts.ResolvedInstallPlan{Request: contracts.InstallRequest{Values: map[string]string{
		"gateway.access.mode": `"local"`,
	}}}, Verification: contracts.Verification{HostHealthURL: server.URL + "/health"}}
	if err := receipt.Write(root, active); err != nil {
		t.Fatal(err)
	}
	projectID, err := registerProject(root, project)
	if err != nil || projectID.ID != "prj_0123456789abcdef" || projectID.CanonicalPath != project || projectID.AlreadyExisted {
		t.Fatalf("project registration/error = %#v / %v", projectID, err)
	}
}

// case: S-187.4
func TestInstallRegistersSelectedProjectAndReportsBothResults(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	platformRoot := filepath.Join(home, "kb-platform")
	projectTarget := filepath.Join(home, "work", "project")
	projectRoot := filepath.Join(home, "work", "project-alias")
	if err := os.MkdirAll(projectTarget, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(projectTarget, projectRoot); err != nil {
		t.Fatal(err)
	}
	selectedProjectPath, err := contracts.AbsolutePath(projectRoot)
	if err != nil {
		t.Fatal(err)
	}
	canonicalProjectRoot, err := filepath.EvalSymlinks(projectRoot)
	if err != nil {
		t.Fatal(err)
	}
	index := filepath.Join(home, "release-index.json")
	source := testReleaseCatalog(t, "2.0.0")
	source.Platforms[0].Host = testHostOnFreePort(t)
	source.Platforms[0].Profiles = map[string]contracts.ServiceGraph{"default": {PlatformVersion: "2.0.0"}}
	source.Platforms[0].Config = []catalog.ConfigRequirement{{ID: "gateway.access.mode", Path: "/gateway/access/mode", Default: `"local"`}}
	source.Plugins = []catalog.Component{{ID: "@kb-labs/commit", Version: "1.0.0", Package: "@kb/commit", Tarball: "https://example.test/commit.tgz", SHA256: "commit"}}
	release, err := catalog.Seal(source)
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
	installData, failure, code, _ := executeForTest(t, operationTestSetup{
		Operation: "install", Index: index, Direct: directRequest{PlatformRoot: platformRoot, ProjectRoot: projectRoot},
		Executable: launcher, InstallInput: strings.NewReader(strings.Repeat("\n", 12)), InstallOutput: &screen,
		RuntimeDeps:   &runtime.Dependencies{Artifacts: installArtifacts{root: platformRoot}, Host: installHost{}},
		NodePreflight: func() error { return nil },
		ProjectRegistrar: func(path string) (projectRegistrationResult, error) {
			registered = path
			return projectRegistrationResult{ID: "prj_0123456789abcdef", CanonicalPath: canonicalProjectRoot}, nil
		},
	})
	if code != 0 || registered != selectedProjectPath {
		t.Fatalf("code=%d failure=%v cause=%q registered=%q screen:\n%s", code, failure, failure.Cause, registered, screen.String())
	}
	for _, expected := range []string{selectedProjectPath, canonicalProjectRoot, "prj_0123456789abcdef", "Verification: platform installed", contracts.ShellCommand("kb", "project", "show", canonicalProjectRoot)} {
		if !strings.Contains(screen.String(), expected) {
			t.Errorf("screen missing %q:\n%s", expected, screen.String())
		}
	}
	installedResult, ok := installData.(applyCommandResult)
	if !ok || installedResult.Receipt.ProjectSelectedPath != selectedProjectPath || installedResult.Receipt.ProjectID != "prj_0123456789abcdef" || installedResult.Receipt.ProjectCanonicalPath != canonicalProjectRoot {
		t.Fatalf("install result did not carry registry project identity: %#v", installData)
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
		ProjectRegistrar: func(string) (projectRegistrationResult, error) {
			return projectRegistrationResult{}, errors.New("project registry unavailable")
		},
	})
	if failedCode != 1 || failure == nil || failure.Code != contracts.CodeProjectRegistrationFailed ||
		failure.Message != "The platform is installed, but the selected project is not registered." ||
		failure.Meta["causeCode"] != contracts.CodeProjectRegistrationFailed ||
		!strings.Contains(failure.Hint, contracts.ShellCommand("kb", "project", "add", projectRoot)) {
		t.Fatalf("project registration failure = code %d, output %#v", failedCode, failure)
	}
	if _, err := os.Stat(receipt.Path(platformRootForTest(t, failedRoot))); err != nil {
		t.Fatalf("late registration failure should retain the completed platform install receipt: %v", err)
	}
}

// case: S-187.7
func TestHumanInstallRejectsMissingProjectBeforeCreatingPlatformRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	index := writeJSONInstallFixture(t, home)
	platformRoot := filepath.Join(home, "platform")
	projectRoot := filepath.Join(home, "missing-project")
	launcher := filepath.Join(home, ".local", "bin", "kb-create")
	registrarCalled := false
	_, failure, code, _ := executeForTest(t, operationTestSetup{
		Operation: "install", Index: index,
		Direct:     directRequest{PlatformRoot: platformRoot, ProjectRoot: projectRoot},
		Executable: launcher,
		ProjectRegistrar: func(string) (projectRegistrationResult, error) {
			registrarCalled = true
			return projectRegistrationResult{}, nil
		},
	})
	if code != 2 || failure == nil || failure.Code != contracts.CodeInputRequired || !strings.Contains(failure.Hint, contracts.ShellCommand("mkdir", "-p", projectRoot)) {
		t.Fatalf("install missing-project validation = exit %d, failure %#v", code, failure)
	}
	if _, err := os.Stat(platformRoot); !os.IsNotExist(err) {
		t.Fatalf("invalid project selection created platform root: %v", err)
	}
	if _, err := os.Stat(receipt.Path(platformRoot)); !os.IsNotExist(err) {
		t.Fatalf("invalid project selection created install receipt: %v", err)
	}
	if _, err := os.Lstat(shim.Path(launcher)); !os.IsNotExist(err) {
		t.Fatalf("invalid project selection created launcher shim: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".kb", "projects.json")); !os.IsNotExist(err) {
		t.Fatalf("invalid project selection created project registry state: %v", err)
	}
	if registrarCalled {
		t.Fatal("invalid project selection called the project registrar")
	}
}

// case: S-187.8
func TestApplyRejectsMissingProjectBeforeSavingScenarioAnswers(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	index := writeJSONInstallFixture(t, home)
	platformRoot := filepath.Join(home, "platform")
	projectRoot := filepath.Join(home, "missing-project")
	_, failure, code, _ := executeForTest(t, operationTestSetup{
		Operation: "apply", Index: index, PlatformRoot: platformRoot,
		Direct:     directRequest{PlatformRoot: platformRoot, ProjectRoot: projectRoot},
		ScenarioID: "custom", ScenarioAnswers: `{"access.mode":"local"}`,
	})
	if code != 2 || failure == nil || failure.Code != contracts.CodeInputRequired || !strings.Contains(failure.Hint, contracts.ShellCommand("mkdir", "-p", projectRoot)) {
		t.Fatalf("apply missing-project validation = exit %d, failure %#v", code, failure)
	}
	if _, err := os.Stat(platformRoot); !os.IsNotExist(err) {
		t.Fatalf("invalid project selection saved scenario answers or created platform root: %v", err)
	}
	for _, statePath := range []string{
		filepath.Join(platformRoot, ".kb", "v2", "scenarios", "custom.json"),
		filepath.Join(platformRoot, ".kb", "v2", "scenarios", "custom.json.tmp"),
	} {
		if _, err := os.Stat(statePath); !os.IsNotExist(err) {
			t.Fatalf("invalid project selection saved scenario state at %s: %v", statePath, err)
		}
	}
}

func writeJSONInstallFixture(t *testing.T, home string) string {
	t.Helper()
	index := filepath.Join(home, "release-index.json")
	source := testReleaseCatalog(t, "2.0.0")
	source.Platforms[0].Host = testHostOnFreePortWithPortHeadroom(t, 100)
	source.Platforms[0].Profiles = map[string]contracts.ServiceGraph{"default": {PlatformVersion: "2.0.0"}}
	source.Platforms[0].Config = []catalog.ConfigRequirement{{ID: "gateway.access.mode", Path: "/gateway/access/mode", Default: `"local"`}}
	source.Adapters = []catalog.Adapter{{Component: catalog.Component{ID: "sqlite-stores", Version: "1.0.0",
		Package: "@kb/sqlite-stores", Tarball: "https://example.test/sqlite-stores.tgz", SHA256: "sqlite-stores"},
		Provides: []string{"documentDatabase"}}}
	release, err := catalog.Seal(source)
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
	canonical, err := contracts.AbsolutePath(project)
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
		ProjectRegistrar: func(path string) (projectRegistrationResult, error) {
			registry[path] = "prj_0123456789abcdef"
			return projectRegistrationResult{ID: registry[path], CanonicalPath: canonical}, nil
		},
	})
	if code != 0 || registry[canonical] != "prj_0123456789abcdef" {
		t.Fatalf("JSON scenario install exit=%d registry=%v failure=%v output=%s", code, registry, failure, raw)
	}
}

// case: S-039.1
func TestApplyCommandForwardsScenarioAnswersBeforeMutation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	index := writeJSONInstallFixture(t, home)
	root := filepath.Join(home, "platform")
	_, failure, code, output := executeForTest(t, operationTestSetup{
		Operation: "apply", JSON: true, Index: index, PlatformRoot: root,
		ScenarioID: "commit", ScenarioAnswers: "{invalid",
		RuntimeDeps:   &runtime.Dependencies{Artifacts: installArtifacts{root: root}, Host: installHost{}},
		NodePreflight: func() error { return nil },
	})
	if code == 0 || (failure != nil && failure.Code != contracts.CodeScenarioInvalid) ||
		!strings.Contains(output, contracts.CodeScenarioInvalid) || !strings.Contains(output, "decode scenario answers") {
		t.Fatalf("apply scenario validation exit=%d failure=%v output=%s", code, failure, output)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("invalid scenario answers mutated platform root %q: stat error=%v", root, err)
	}
}

// case: S-039.1
func TestApplyJSONStreamReturnsItsAppliedReceipt(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	index := writeJSONInstallFixture(t, home)
	root := filepath.Join(home, "platform")
	_, failure, code, output := executeForTest(t, operationTestSetup{
		Operation: "apply", JSON: true, Index: index, PlatformRoot: root,
		ScenarioID: "custom", ScenarioAnswers: `{"access.mode":"local"}`,
		RuntimeDeps:   &runtime.Dependencies{Artifacts: installArtifacts{root: root}, Host: installHost{}},
		NodePreflight: func() error { return nil },
	})
	if code != 0 || failure != nil {
		t.Fatalf("apply JSON exit=%d failure=%v output=%s", code, failure, output)
	}
	var applyResult map[string]any
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		var envelope struct {
			OK   bool           `json:"ok"`
			Data map[string]any `json:"data"`
		}
		if err := json.Unmarshal([]byte(line), &envelope); err != nil {
			t.Fatalf("invalid apply JSONL record %q: %v", line, err)
		}
		if envelope.OK && envelope.Data["operation"] == "apply" {
			applyResult = envelope.Data
		}
	}
	if applyResult == nil || applyResult["receipt"] == nil || applyResult["logPath"] == "" {
		t.Fatalf("apply JSON stream omitted the completed receipt result: %s", output)
	}
}

// case: S-039.4
func TestApplyRequestValidationHintUsesActualRetryArguments(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	index := writeJSONInstallFixture(t, home)
	requestPath := filepath.Join(home, "invalid request.json")
	if err := os.WriteFile(requestPath, []byte(`{"schema":"kb.create/v2","platformRoot":""}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, failure, code, output := executeForTest(t, operationTestSetup{
		Operation: "apply", Index: index, Input: requestPath,
		RuntimeDeps:   &runtime.Dependencies{Artifacts: installArtifacts{root: filepath.Join(home, "platform")}, Host: installHost{}},
		NodePreflight: func() error { return nil },
	})
	if failure == nil || code == 0 || failure.Code != contracts.CodeInputRequired {
		t.Fatalf("invalid request exit=%d failure=%v output=%s", code, failure, output)
	}
	want := "Correct platformRoot in request file '" + requestPath + "', then retry: " + contracts.ShellCommand("kb-create", "apply", "--index", index, "--input", requestPath, "--json")
	if !strings.Contains(failure.Hint, want) || !strings.Contains(failure.Hint, "Set `platformRoot` to a non-empty directory") {
		t.Fatalf("validation hint = %q, want validation guidance and exact retry instruction %q", failure.Hint, want)
	}
}

func TestApplyRequestValidationHintDoesNotEchoCredentialValues(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	index := writeJSONInstallFixture(t, home)
	_, failure, code, output := executeForTest(t, operationTestSetup{
		Operation: "apply", Index: index, PlatformRoot: filepath.Join(home, "platform"),
		ScenarioID: "explore", ScenarioAnswers: `{"admin.password":"scenario-secret"}`,
		Direct:        directRequest{GatewayURL: "http://operator:gateway-secret@127.0.0.1:4100"},
		NodePreflight: func() error { return nil },
	})
	if failure == nil || code == 0 {
		t.Fatalf("invalid request exit=%d failure=%v output=%s", code, failure, output)
	}
	if strings.Contains(failure.Hint, "gateway-secret") || strings.Contains(failure.Hint, "scenario-secret") {
		t.Fatalf("retry hint exposed credential values: %q", failure.Hint)
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
	canonical, err := contracts.AbsolutePath(project)
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
		ProjectRegistrar: func(path string) (projectRegistrationResult, error) {
			registry[path] = "prj_0123456789abcdef"
			return projectRegistrationResult{ID: registry[path], CanonicalPath: canonical}, nil
		},
	})
	if code != 0 || registry[canonical] != "prj_0123456789abcdef" {
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
	for port := 65535 - headroom; port >= 1024; port-- {
		listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err == nil {
			return listener
		}
	}
	t.Fatal("could not reserve a loopback port with the required allocation headroom")
	return nil
}

func testReleaseCatalog(t *testing.T, platformVersion string) catalog.Catalog {
	t.Helper()
	data := testfixtures.ReleaseIndexJSON(platformVersion, platformVersion, goruntime.GOOS, goruntime.GOARCH)
	var source catalog.Catalog
	if err := json.Unmarshal(data, &source); err != nil {
		t.Fatal(err)
	}
	return source
}

func TestParseProvidersSupportsCommaSeparatedPreferences(t *testing.T) {
	got, err := parseProviders([]string{"storage=disk-io-storage, cache=state-broker"})
	if err != nil || got["storage"] != "disk-io-storage" || got["cache"] != "state-broker" {
		t.Fatalf("providers/error = %#v / %v", got, err)
	}
	if _, err := parseProviders([]string{"storage=disk,storage=other"}); err == nil {
		t.Fatal("expected duplicate capability to fail")
	}
}

// case: S-179.25
func TestParseProvidersRejectsEmptyItems(t *testing.T) {
	if _, err := parseProviders([]string{"", "storage=disk-io-storage"}); err == nil {
		t.Fatal("expected an empty repeated provider item to fail")
	}
	if values, err := parseProviders(nil); err != nil || len(values) != 0 {
		t.Fatalf("no provider input = %#v / %v; want an empty preference set", values, err)
	}
}

// case: S-179.25
func TestExecuteResetsRepeatedProviderFlagBetweenInvocations(t *testing.T) {
	var observed []string
	root := newRootCommandWithDependencyFactory(func(command *cobra.Command) commandDependencies {
		provider := command.Flags().Lookup("provider")
		if provider == nil {
			t.Error("actual kb-create command did not register --provider")
		} else {
			observed = append(observed, provider.Value.String())
		}
		return commandDependencies{}
	})

	for _, args := range [][]string{
		{"plan", "--provider", "storage=first", "--json"},
		{"plan", "--provider", "cache=second", "--json"},
	} {
		var stdout, stderr bytes.Buffer
		clikitcommand.Execute(root, args, &stdout, &stderr)
	}
	if !reflect.DeepEqual(observed, []string{"storage=first", "cache=second"}) {
		t.Fatalf("actual kb-create provider values by invocation = %#v; want only each invocation's non-empty values", observed)
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
