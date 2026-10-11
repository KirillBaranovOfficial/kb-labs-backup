package v2cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/kb-labs/create/v2/catalog"
	"github.com/kb-labs/create/v2/contracts"
	"github.com/kb-labs/create/v2/receipt"
	"github.com/kb-labs/create/v2/runtime"
	"github.com/kb-labs/create/v2/versionroot"
)

// case: S-023.6
func TestEqualVersionUpdateReturnsBeforeSnapshotWritesOrLifecycle(t *testing.T) {
	inst := newInstallation(t)
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
	active.Plan.Request = contracts.InstallRequest{PlatformRoot: inst.root}
	active.Plan.ServiceGraph = contracts.ServiceGraph{PlatformVersion: "3.2.0"}
	active.Plan.Artifacts = []contracts.Artifact{{ID: "platform", Kind: "platform", Package: "@kb/platform", Version: "3.2.0"}}
	if err := receipt.Write(platformRootForTest(t, inst.root), active); err != nil {
		t.Fatal(err)
	}
	receiptPath := receipt.Path(platformRootForTest(t, inst.root))
	before, err := os.ReadFile(receiptPath)
	if err != nil {
		t.Fatal(err)
	}

	index, err := catalog.Seal(testReleaseCatalog(t, "3.2.0"))
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
	installer := &recordingInstaller{}
	result, failure, code, raw := executeForTest(t, operationTestSetup{
		Operation: "update", Index: indexPath, PlatformRoot: inst.root,
		Direct:        directRequest{PlatformVersion: "3.2.0", Policy: "compatible"},
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
	}{
		{"downgrade", "3.2.0", "3.1.0", "", "", "KB_UPDATE_DOWNGRADE_REFUSED"},
		{"installed plugin range", "3.0.0", "3.2.0", "plugin", "<3.2.0", "KB_UPDATE_INCOMPATIBLE"},
		{"installed adapter range", "3.0.0", "3.2.0", "adapter", "<3.2.0", "KB_UPDATE_INCOMPATIBLE"},
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
			sealed, err := catalog.Seal(index)
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
			if code != 2 || errorCode(payload) != test.wantCode {
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
	sealed, err := catalog.Seal(source)
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
	if code != 2 || errorCode(payload) != "KB_UPDATE_INCOMPATIBLE" {
		t.Fatalf("update target plan = code %d, error %s: %#v", code, errorCode(payload), payload)
	}
	errorValue := payload["error"].(map[string]any)
	details := errorValue["details"].(map[string]any)
	if details["kind"] != "sdk" || details["id"] != "sdk" || details["selectedVersion"] != "3.2.0" || details["requiredRange"] != "<3.0.0" {
		t.Fatalf("update target plan details = %#v", details)
	}
}
