package v2cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/kb-labs/clikit/diag"
	"github.com/kb-labs/create/v2/artifacts"
	"github.com/kb-labs/create/v2/catalog"
	"github.com/kb-labs/create/v2/contracts"
	"github.com/kb-labs/create/v2/receipt"
	"github.com/kb-labs/create/v2/runtime"
	"github.com/kb-labs/create/v2/versionroot"
)

func TestMarketplaceLockReadFailureIsNotReportedAsComponentIncompatibility(t *testing.T) {
	root := t.TempDir()
	kbDir := filepath.Join(root, ".kb")
	if err := os.MkdirAll(kbDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(kbDir, "marketplace.lock"), []byte("not-json\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	plan := contracts.ResolvedInstallPlan{Request: contracts.InstallRequest{PlatformRoot: root}}
	_, failure := planUpdateWithMarketplaceLock(root, plan, catalog.Catalog{}, "kb-create update")
	if failure == nil {
		t.Fatal("invalid marketplace lock unexpectedly accepted")
	}
	if failure.Code != contracts.CodeOperationFailed {
		t.Fatalf("marketplace lock failure code = %q, want %q", failure.Code, contracts.CodeOperationFailed)
	}
	if _, err := diag.ToEnvelope(failure); err != nil {
		t.Fatalf("marketplace lock failure violated its diagnostic contract: %v", err)
	}
	if got, want := diag.ExitCode(failure).Code(), 1; got != want {
		t.Fatalf("process exit code = %d, want %d", got, want)
	}
}

func TestUpdatePreservesTheResolverCodeForMalformedRequestData(t *testing.T) {
	indexPath := filepath.Join(t.TempDir(), "release-index.json")
	source, err := sealTestCatalog(t, testReleaseCatalog(t, "2.0.0"))
	if err != nil {
		t.Fatal(err)
	}
	encodedIndex, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(indexPath, encodedIndex, 0o600); err != nil {
		t.Fatal(err)
	}
	requestPath := filepath.Join(t.TempDir(), "request.json")
	if err := os.WriteFile(requestPath, []byte(`{"platformRoot":"/tmp/update-request","policy":"not-a-policy"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	_, failure, code, _ := executeForTest(t, operationTestSetup{
		Operation: "update", Index: indexPath, Input: requestPath, JSON: true,
		NodePreflight: func() error { return nil },
	})
	if failure == nil {
		t.Fatal("invalid update request unexpectedly resolved")
	}
	if failure.Code != contracts.CodeInputRequired {
		t.Fatalf("invalid request code = %q, want %q", failure.Code, contracts.CodeInputRequired)
	}
	if code != diag.ExitCode(failure).Code() {
		t.Fatalf("invalid request process code = %d, catalog code = %d", code, diag.ExitCode(failure).Code())
	}
	if _, err := diag.ToEnvelope(failure); err != nil {
		t.Fatalf("invalid request diagnostic violated its declared contract: %v", err)
	}
}

func TestUpdateBadChecksumIndexReturnsCataloguedApplyFailure(t *testing.T) {
	inst := newInstallation(t)
	if _, code, output := inst.run("start", "--json"); code != 0 {
		t.Fatalf("start fixture host: %d %s", code, output)
	}

	goodBinary := []byte("sealed update binary")
	corruptBinary := []byte("corrupt update binary")
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = writer.Write(corruptBinary)
	}))
	t.Cleanup(server.Close)

	source := testReleaseCatalog(t, "2.0.0")
	source.Platforms[0].Binaries[0].URL = server.URL + "/kb-create"
	digest := sha256.Sum256(goodBinary)
	source.Platforms[0].Binaries[0].SHA256 = hex.EncodeToString(digest[:])
	sealed, err := sealTestCatalog(t, source)
	if err != nil {
		t.Fatal(err)
	}
	indexPath := filepath.Join(t.TempDir(), "bad-checksum-release-index.json")
	encodedIndex, err := json.Marshal(sealed)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(indexPath, encodedIndex, 0o600); err != nil {
		t.Fatal(err)
	}

	layout, err := versionroot.New(inst.root)
	if err != nil {
		t.Fatal(err)
	}
	cacheRoot, err := layout.SharedRuntimePath(".kb/v2/cache")
	if err != nil {
		t.Fatal(err)
	}
	stateBefore := snapshotStateTree(t, filepath.Join(inst.root, "state"))
	receiptPath := receipt.Path(platformRootForTest(t, inst.root))
	receiptBefore, err := os.ReadFile(receiptPath)
	if err != nil {
		t.Fatal(err)
	}
	_, failure, exitCode, _ := executeForTest(t, operationTestSetup{
		Operation: "update", Index: indexPath, JSON: true,
		Direct: directRequest{PlatformRoot: inst.root, PlatformVersion: "2.0.0", SDKVersion: "2.0.0", Policy: "compatible"},
		RuntimeDeps: &runtime.Dependencies{Artifacts: artifacts.Composite{
			Binaries: artifacts.Binaries{Root: inst.root, CacheRoot: cacheRoot, Client: server.Client()},
		}},
		NodePreflight: func() error { return nil },
	})
	if failure == nil {
		t.Fatal("update accepted the checksum-corrupt artifact")
	}
	if failure.Code != contracts.CodeApplyFailed || exitCode != 1 {
		t.Fatalf("bad-checksum update = code %q / exit %d; want %q / exit 1", failure.Code, exitCode, contracts.CodeApplyFailed)
	}
	if _, err := diag.ToEnvelope(failure); err != nil {
		t.Fatalf("bad-checksum update violated the diagnostic contract: %v", err)
	}
	if got, err := os.ReadFile(receiptPath); err != nil || string(got) != string(receiptBefore) {
		t.Fatalf("bad-checksum update changed the active receipt: %v", err)
	}
	if after := snapshotStateTree(t, filepath.Join(inst.root, "state")); !reflect.DeepEqual(after, stateBefore) {
		t.Fatalf("bad-checksum update changed shared state: before=%#v after=%#v", stateBefore, after)
	}
}

// case: S-023.6
func TestEqualVersionUpdateReturnsBeforeSnapshotWritesOrLifecycle(t *testing.T) {
	assertEqualVersionUpdateDoesNotRequireProjectDirectory(t)
}

// case: S-187.9
func TestUpdateReceiptReplayAllowsRemovedProjectDirectory(t *testing.T) {
	assertEqualVersionUpdateDoesNotRequireProjectDirectory(t)
}

func assertEqualVersionUpdateDoesNotRequireProjectDirectory(t *testing.T) {
	t.Helper()
	inst := newInstallation(t)
	projectRoot := filepath.Join(t.TempDir(), "previously-registered-project")
	if err := os.Mkdir(projectRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	active, err := receipt.Read(platformRootForTest(t, inst.root))
	if err != nil {
		t.Fatal(err)
	}
	active.Schema, active.ID, active.InstallationID = contracts.ReceiptSchema, "active", "installation-r1"
	active.InstallationID, err = versionroot.InstallationIDForPath(inst.root)
	if err != nil {
		t.Fatal(err)
	}
	active.Plan.Schema, active.Plan.PlatformChannel = contracts.ResolvedPlanSchema, "stable"
	active.Plan.Request = contracts.InstallRequest{PlatformRoot: inst.root, ProjectRoot: projectRoot}
	active.Plan.Request, err = active.Plan.Request.Normalize()
	if err != nil {
		t.Fatal(err)
	}
	active.Plan.ServiceGraph = contracts.ServiceGraph{PlatformVersion: "3.2.0"}
	active.Plan.Artifacts = []contracts.Artifact{{ID: "platform", Kind: "platform", Package: "@kb/platform", Version: "3.2.0"}}
	if err := receipt.Write(platformRootForTest(t, inst.root), active); err != nil {
		t.Fatal(err)
	}
	receiptPath := receipt.Path(platformRootForTest(t, inst.root))
	if err := os.Remove(projectRoot); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(receiptPath)
	if err != nil {
		t.Fatal(err)
	}

	index, err := sealTestCatalog(t, testReleaseCatalog(t, "3.2.0"))
	if err != nil {
		t.Fatal(err)
	}
	indexPath := filepath.Join(t.TempDir(), "release-index.json")
	encoded, err := json.Marshal(index)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(indexPath, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	requestPath := filepath.Join(t.TempDir(), "receipt-request.json")
	receiptRequest, err := json.Marshal(active.Plan.Request)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(requestPath, receiptRequest, 0o600); err != nil {
		t.Fatal(err)
	}
	installer := &recordingInstaller{}
	result, failure, code, raw := executeForTest(t, operationTestSetup{
		Operation: "update", Index: indexPath, Input: requestPath, PlatformRoot: inst.root,
		RuntimeDeps:   &runtime.Dependencies{Artifacts: installer},
		NodePreflight: func() error { return nil },
	})
	if failure != nil || code != 0 {
		t.Fatalf("update = code %d, error %v, result %s", code, failure, raw)
	}
	output, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(output, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["alreadyUpToDate"] != true || payload["hostRestarted"] != false {
		t.Fatalf("up-to-date result = %s", output)
	}
	if len(installer.installed) != 0 || installer.restores != 0 {
		t.Fatalf("equal update invoked artifact lifecycle: %#v", installer)
	}
	after, err := os.ReadFile(receiptPath)
	if err != nil || string(before) != string(after) {
		t.Fatalf("equal update changed receipt: %v", err)
	}
	layout, err := versionroot.New(inst.root)
	if err != nil {
		t.Fatal(err)
	}
	assertEmptyOrMissing(t, layout.SnapshotsDir())
}

func TestUpdateValidationRefusesBeforeReceiptOrSnapshotMutation(t *testing.T) {
	for _, test := range []struct {
		name, installed, target, kind, componentRange, wantCode string
		wantExit                                                int
	}{
		{"downgrade", "3.2.0", "3.1.0", "", "", "KB_UPDATE_DOWNGRADE_REFUSED", 2},
		{"installed plugin range", "3.0.0", "3.2.0", "plugin", "<3.2.0", "KB_UPDATE_INCOMPATIBLE", 1},
		{"installed adapter range", "3.0.0", "3.2.0", "adapter", "<3.2.0", "KB_UPDATE_INCOMPATIBLE", 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			inst := newInstallation(t)
			active := contracts.InstallReceipt{Schema: contracts.ReceiptSchema, ID: "active", InstallationID: "installation-r1", Plan: contracts.ResolvedInstallPlan{
				Schema: contracts.ResolvedPlanSchema, PlatformChannel: "stable",
				ServiceGraph: contracts.ServiceGraph{PlatformVersion: test.installed},
				Artifacts: func() []contracts.Artifact {
					if test.kind == "" {
						return nil
					}
					return []contracts.Artifact{{ID: "fixture-compat", Kind: test.kind, Version: "1.0.0", PlatformRange: test.componentRange}}
				}(),
			}}
			installationID, err := versionroot.InstallationIDForPath(inst.root)
			if err != nil {
				t.Fatal(err)
			}
			active.InstallationID = installationID
			if err := receipt.Write(platformRootForTest(t, inst.root), active); err != nil {
				t.Fatal(err)
			}
			receiptPath := receipt.Path(platformRootForTest(t, inst.root))
			before, err := os.ReadFile(receiptPath)
			if err != nil {
				t.Fatal(err)
			}

			index := testReleaseCatalog(t, test.target)
			sealed, err := sealTestCatalog(t, index)
			if err != nil {
				t.Fatal(err)
			}
			indexPath := filepath.Join(t.TempDir(), "release-index.json")
			encoded, err := json.Marshal(sealed)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(indexPath, encoded, 0o600); err != nil {
				t.Fatal(err)
			}
			requestPath := filepath.Join(t.TempDir(), "request.json")
			request, _ := json.Marshal(contracts.InstallRequest{PlatformRoot: inst.root, Platform: contracts.VersionSelector{Version: test.target}, Policy: contracts.PolicyCompatible})
			if err := os.WriteFile(requestPath, request, 0o600); err != nil {
				t.Fatal(err)
			}
			_, failure, code, _ := executeForTest(t, operationTestSetup{
				Operation: "update", Index: indexPath, Input: requestPath,
				NodePreflight: func() error { return nil },
			})
			payload := diagValue(t, failure)
			if code != test.wantExit || errorCode(payload) != test.wantCode {
				t.Fatalf("update = code %d, error %s: %#v", code, errorCode(payload), payload)
			}
			if test.kind != "" {
				errorValue, ok := payload["error"].(map[string]any)
				if !ok {
					t.Fatalf("update error = %#v", payload["error"])
				}
				details, ok := errorValue["details"].(map[string]any)
				if !ok || details["kind"] != test.kind || details["id"] != "fixture-compat" ||
					details["selectedVersion"] != test.target || details["requiredRange"] != test.componentRange {
					t.Fatalf("update details = %#v", errorValue["details"])
				}
				for _, alias := range []string{"component", "version", "range", "subject", "value"} {
					if _, exists := details[alias]; exists {
						t.Fatalf("update details contain duplicate alias %q: %#v", alias, details)
					}
				}
			}
			after, err := os.ReadFile(receiptPath)
			if err != nil {
				t.Fatal(err)
			}
			if string(before) != string(after) {
				t.Fatal("refused update changed active receipt")
			}
			layout, err := versionroot.New(inst.root)
			if err != nil {
				t.Fatal(err)
			}
			assertEmptyOrMissing(t, layout.SnapshotsDir())
		})
	}
}

func TestUpdateMapsTargetPlanCompatibilityToUpdateCode(t *testing.T) {
	inst := newInstallation(t)
	index := filepath.Join(t.TempDir(), "release-index.json")
	source := testReleaseCatalog(t, "3.2.0")
	source.Platforms[0].SDKRange = "<3.0.0"
	sealed, err := sealTestCatalog(t, source)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(sealed)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(index, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	_, failure, code, _ := executeForTest(t, operationTestSetup{
		Operation: "update", Index: index, PlatformRoot: inst.root,
		Direct:        directRequest{PlatformVersion: "3.2.0", SDKVersion: "3.2.0", Policy: "compatible"},
		NodePreflight: func() error { return nil },
	})
	payload := diagValue(t, failure)
	if code != 1 || errorCode(payload) != "KB_UPDATE_INCOMPATIBLE" {
		t.Fatalf("update target plan = code %d, error %s: %#v", code, errorCode(payload), payload)
	}
	errorValue := payload["error"].(map[string]any)
	details := errorValue["details"].(map[string]any)
	if details["kind"] != "sdk" || details["id"] != "sdk" || details["selectedVersion"] != "3.2.0" || details["requiredRange"] != "<3.0.0" {
		t.Fatalf("update target plan details = %#v", details)
	}
}
