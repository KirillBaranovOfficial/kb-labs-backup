package v2cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/kb-labs/create/v2/catalog"
	"github.com/kb-labs/create/v2/contracts"
	"github.com/kb-labs/create/v2/receipt"
)

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
			active := contracts.InstallReceipt{Schema: contracts.ReceiptSchema, ID: "active", Plan: contracts.ResolvedInstallPlan{
				Schema: contracts.ResolvedPlanSchema, PlatformChannel: "stable",
				ServiceGraph: contracts.ServiceGraph{PlatformVersion: test.installed},
				Artifacts: func() []contracts.Artifact {
					if test.kind == "" {
						return nil
					}
					return []contracts.Artifact{{ID: "fixture-compat", Kind: test.kind, Version: "1.0.0", PlatformRange: test.componentRange}}
				}(),
			}}
			if err := receipt.Write(inst.root, active); err != nil {
				t.Fatal(err)
			}
			receiptPath := filepath.Join(inst.root, ".kb", "v2", "receipt.json")
			before, err := os.ReadFile(receiptPath)
			if err != nil {
				t.Fatal(err)
			}

			index := catalog.Catalog{
				Channels:      map[contracts.Channel]string{contracts.ChannelStable: test.target},
				Compatibility: &catalog.CompatibilityMatrix{Schema: catalog.CompatibilitySchema, Labels: []catalog.CompatibilityLabel{{ID: "platform@" + test.target, Kind: "platform", ArtifactID: "platform", Version: test.target, Status: "prepared", ValidatedBy: []string{"test"}}}},
				Platforms:     []catalog.PlatformBundle{{ID: "platform", Version: test.target, Package: "@kb/platform", Tarball: "platform.tgz", SHA256: "platform", Host: &contracts.HostSpec{Command: "kb-host", HealthURL: "http://127.0.0.1:4000/health"}, Profiles: map[string]contracts.ServiceGraph{"default": {}}}},
			}
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
			if _, err := os.Stat(filepath.Join(inst.root, ".kb", "v2", "snapshots")); !os.IsNotExist(err) {
				t.Fatalf("refused update created snapshot directory: %v", err)
			}
		})
	}
}

func TestUpdateMapsTargetPlanCompatibilityToUpdateCode(t *testing.T) {
	inst := newInstallation(t)
	index := filepath.Join(t.TempDir(), "release-index.json")
	sealed, err := catalog.Seal(catalog.Catalog{
		Channels:      map[contracts.Channel]string{contracts.ChannelStable: "3.2.0"},
		Compatibility: testCompatibility("3.2.0"),
		Platforms: []catalog.PlatformBundle{{
			ID: "platform", Version: "3.2.0", Package: "@kb/platform", Tarball: "platform.tgz", SHA256: "platform",
			SDKRange: "<3.0.0", Host: &contracts.HostSpec{Command: "kb-host", HealthURL: "http://127.0.0.1:4000/health"},
			Profiles: map[string]contracts.ServiceGraph{"default": {}},
		}},
		SDKs: []catalog.Component{{ID: "sdk", Version: "3.2.0", Package: "@kb/sdk", Tarball: "sdk.tgz", SHA256: "sdk"}},
	})
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
