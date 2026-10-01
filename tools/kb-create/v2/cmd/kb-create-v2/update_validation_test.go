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
		name, installed, target, pluginRange, wantCode string
	}{
		{"downgrade", "3.2.0", "3.1.0", "", "KB_UPDATE_DOWNGRADE_REFUSED"},
		{"installed plugin range", "3.2.0", "3.3.0", "=3.2.0", "KB_UPDATE_INCOMPATIBLE"},
	} {
		t.Run(test.name, func(t *testing.T) {
			inst := newInstallation(t)
			active := contracts.InstallReceipt{Schema: contracts.ReceiptSchema, ID: "active", Plan: contracts.ResolvedInstallPlan{
				Schema: contracts.ResolvedPlanSchema, PlatformChannel: "stable",
				ServiceGraph: contracts.ServiceGraph{PlatformVersion: test.installed},
				Artifacts: func() []contracts.Artifact {
					if test.pluginRange == "" {
						return nil
					}
					return []contracts.Artifact{{ID: "fixture-compat", Kind: "plugin", Version: "1.0.0", PlatformRange: test.pluginRange}}
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
			output, err := os.CreateTemp(t.TempDir(), "update-output")
			if err != nil {
				t.Fatal(err)
			}
			code := (invocation{Operation: "update", Index: indexPath, Input: requestPath}).run(output)
			if err := output.Close(); err != nil {
				t.Fatal(err)
			}
			payload := readJSON(t, output.Name())
			if code != 2 || errorCode(payload) != test.wantCode {
				t.Fatalf("update = code %d, error %s: %#v", code, errorCode(payload), payload)
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
