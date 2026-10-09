package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/kb-labs/create/v2/catalog"
	"github.com/kb-labs/create/v2/contracts"
)

func TestRunSealsNormalizedReleaseExport(t *testing.T) {
	input, output, manifestRoot := validReleaseSealerFixture(t)
	if err := run(input, output, manifestRoot); err != nil {
		t.Fatal(err)
	}
	sealed, err := catalog.LoadFile(output)
	if err != nil || sealed.Digest == "" {
		t.Fatalf("sealed/error = %#v / %v", sealed, err)
	}
}

func validReleaseSealerFixture(t *testing.T) (input, output, manifestRoot string) {
	t.Helper()
	dir := t.TempDir()
	input, output = filepath.Join(dir, "export.json"), filepath.Join(dir, "release", "index.json")
	manifestRoot = filepath.Join(dir, "staging")
	var source catalog.Catalog
	if err := json.Unmarshal([]byte(`{"channels":{"stable":"2.0.0"},"compatibility":{"schema":"kb.release-compatibility/2","labels":[{"id":"platform@2.0.0","kind":"platform","artifactId":"platform","version":"2.0.0","status":"prepared","validatedBy":["stage"]}]},"platforms":[{"id":"platform","version":"2.0.0","package":"@kb/platform","tarball":"https://example.test/platform.tgz","sha256":"artifact","host":{"command":"node_modules/.bin/kb-host","healthUrl":"http://127.0.0.1:4000/health"},"profiles":{"default":{}}}]}`), &source); err != nil {
		t.Fatal(err)
	}
	source.SDKs = []catalog.Component{{ID: "sdk", Version: "2.0.0", Package: "@kb/sdk", Tarball: "https://example.test/sdk.tgz", SHA256: "sdk"}}
	source.Compatibility.Labels[0].Requires = []catalog.CompatibilityRelation{{Label: "sdk@2.0.0"}}
	source.Compatibility.Labels = append(source.Compatibility.Labels, catalog.CompatibilityLabel{ID: "sdk@2.0.0", Kind: "sdk", ArtifactID: "sdk", Version: "2.0.0", Status: "prepared", ValidatedBy: []string{"stage"}})
	source.Platforms[0].Config = []catalog.ConfigRequirement{{ID: "gateway.access.mode", Path: "/gateway/access/mode", Default: `"local"`}}
	for _, id := range []string{"@kb-labs/marketplace", "@kb-labs/review", "@kb-labs/scaffold"} {
		source.Platforms[0].Members = append(source.Platforms[0].Members, catalog.Component{ID: id, Version: "2.0.0", Package: "@kb-labs/" + id[len("@kb-labs/"):], Tarball: "https://example.test/" + id + ".tgz", SHA256: id})
	}
	for _, id := range []string{"@kb-labs/commit", "@kb-labs/release"} {
		component := catalog.Component{ID: id, Version: "2.0.0", Package: "@kb-labs/" + id[len("@kb-labs/"):], Tarball: "https://example.test/" + id + ".tgz", SHA256: id}
		if id == "@kb-labs/commit" {
			component.Requires = []catalog.Requirement{{Capability: "cache"}}
		}
		source.Plugins = append(source.Plugins, component)
	}
	source.Adapters = []catalog.Adapter{
		{Component: catalog.Component{ID: "state-broker", Version: "2.0.0", Package: "@kb/state-broker", Tarball: "https://example.test/state-broker.tgz", SHA256: "state-broker"}, Provides: []string{"cache"}},
		{Component: catalog.Component{ID: "sqlite-stores", Version: "2.0.0", Package: "@kb-labs/adapters-sqlite", Tarball: "https://example.test/adapters-sqlite.tgz", SHA256: "sqlite"}, Provides: []string{"documentDatabase", "kvStore"}},
	}
	export, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(input, export, 0o600); err != nil {
		t.Fatal(err)
	}
	components := []catalog.Component{{ID: "platform", Version: "2.0.0", Package: "@kb/platform"}}
	components = append(components, source.SDKs...)
	components = append(components, source.Platforms[0].Members...)
	components = append(components, source.Plugins...)
	for _, adapter := range source.Adapters {
		components = append(components, adapter.Component)
	}
	for _, component := range components {
		packageDir := filepath.Join(manifestRoot, "node_modules", filepath.FromSlash(component.Package))
		if err := os.MkdirAll(packageDir, 0o750); err != nil {
			t.Fatal(err)
		}
		manifestValue := map[string]any{"schema": "kb.create.artifact-manifest/v2", "id": component.ID, "package": component.Package, "version": component.Version}
		if component.ID == "platform" {
			manifestValue["requirements"] = []map[string]any{{"id": "gateway.access.mode", "path": "/gateway/access/mode", "default": json.RawMessage(`"local"`)}}
		}
		manifest, _ := json.Marshal(manifestValue)
		if err := os.WriteFile(filepath.Join(packageDir, "kb-create.manifest.json"), manifest, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return input, output, manifestRoot
}

func TestRunRejectsMissingPublishPaths(t *testing.T) {
	if err := run("", "", ""); err == nil {
		t.Fatal("expected missing paths")
	}
}

func TestRunRejectsInvalidHistoryIndexesWithCataloguedSealFailure(t *testing.T) {
	input, _, manifestRoot := validReleaseSealerFixture(t)
	validHistory, err := catalog.Seal(catalog.Catalog{Platforms: []catalog.PlatformBundle{{
		ID: "platform", Version: "1.0.0", Package: "@kb/platform", Tarball: "https://example.test/platform.tgz",
		SHA256: "platform-sha", SDKRange: "*", Host: &contracts.HostSpec{Command: "node_modules/.bin/kb-host", HealthURL: "http://127.0.0.1:4000/health"},
		Profiles: map[string]contracts.ServiceGraph{"default": {}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	sealedBytes, err := json.Marshal(validHistory)
	if err != nil {
		t.Fatal(err)
	}

	for _, testCase := range []struct {
		name   string
		mutate func([]byte) []byte
	}{
		{name: "tampered", mutate: func(data []byte) []byte {
			var history catalog.Catalog
			if err := json.Unmarshal(data, &history); err != nil {
				t.Fatal(err)
			}
			history.Platforms[0].Version = "1.0.1"
			tampered, err := json.Marshal(history)
			if err != nil {
				t.Fatal(err)
			}
			return tampered
		}},
		{name: "unsealed", mutate: func(data []byte) []byte {
			var history catalog.Catalog
			if err := json.Unmarshal(data, &history); err != nil {
				t.Fatal(err)
			}
			history.Digest = ""
			unsealed, err := json.Marshal(history)
			if err != nil {
				t.Fatal(err)
			}
			return unsealed
		}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			dir := t.TempDir()
			historyPath := filepath.Join(dir, "history.json")
			output := filepath.Join(dir, "release", "index.json")
			if err := os.WriteFile(historyPath, testCase.mutate(sealedBytes), 0o600); err != nil {
				t.Fatal(err)
			}
			err := run(input, output, manifestRoot, historyPath)
			if err == nil {
				t.Fatal("expected history seal validation failure")
			}
			envelope, err := sealingFailureEnvelope(err)
			if err != nil {
				t.Fatal(err)
			}
			if envelope.Code != "KB_RELEASE_INDEX_SEAL_FAILED" {
				t.Fatalf("diagnostic code = %q", envelope.Code)
			}
			if _, err := os.Stat(output); !os.IsNotExist(err) {
				t.Fatalf("output index exists or could not be checked: %v", err)
			}
		})
	}
}

func TestMergeReleaseHistoryCarriesPlatformSDKAndCompatibilityEvidence(t *testing.T) {
	target := catalog.Catalog{
		Platforms: []catalog.PlatformBundle{{ID: "platform", Version: "3.0.2"}},
		SDKs:      []catalog.Component{{ID: "sdk", Version: "3.0.2"}},
		Compatibility: &catalog.CompatibilityMatrix{Schema: catalog.CompatibilitySchema,
			Labels: []catalog.CompatibilityLabel{{ID: "platform@3.0.2"}}},
	}
	history := catalog.Catalog{
		Platforms: []catalog.PlatformBundle{{ID: "platform", Version: "3.0.0"}, {ID: "platform", Version: "3.0.1"}},
		SDKs:      []catalog.Component{{ID: "sdk", Version: "3.0.0"}, {ID: "sdk", Version: "3.0.1"}},
		Compatibility: &catalog.CompatibilityMatrix{Schema: catalog.CompatibilitySchema,
			Labels: []catalog.CompatibilityLabel{{ID: "platform@3.0.0"}, {ID: "sdk@3.0.0"}, {ID: "platform@3.0.1"}}},
	}
	merged, err := mergeReleaseHistory(target, history)
	if err != nil {
		t.Fatal(err)
	}
	if len(merged.Platforms) != 3 || len(merged.SDKs) != 3 || len(merged.Compatibility.Labels) != 4 {
		t.Fatalf("merged history platform/sdk/label counts = %d/%d/%d", len(merged.Platforms), len(merged.SDKs), len(merged.Compatibility.Labels))
	}
	merged, err = mergeReleaseHistory(merged, history)
	if err != nil {
		t.Fatal(err)
	}
	if len(merged.Platforms) != 3 || len(merged.SDKs) != 3 || len(merged.Compatibility.Labels) != 4 {
		t.Fatalf("repeated history merge duplicated entries: platforms=%d sdks=%d labels=%d", len(merged.Platforms), len(merged.SDKs), len(merged.Compatibility.Labels))
	}
}

func TestSealingFailureEnvelopePreservesValidationReason(t *testing.T) {
	reason := "secret manifest requirement gateway.jwt must declare env and services"
	envelope, err := sealingFailureEnvelope(&sealingFailure{
		gate: "manifest validation",
		rule: reason,
		hint: "Add env and services to the secret requirement described in the cause, then rerun release-index preparation.",
		err:  errors.New(reason),
	})
	if err != nil {
		t.Fatal(err)
	}
	if envelope.Cause != reason {
		t.Fatalf("cause = %q, want %q", envelope.Cause, reason)
	}
	if envelope.Details["gate"] != "manifest validation" || envelope.Details["rule"] != reason {
		t.Fatalf("details = %#v", envelope.Details)
	}
	if envelope.Code != "KB_RELEASE_INDEX_SEAL_FAILED" {
		t.Fatalf("code = %q", envelope.Code)
	}
	if envelope.Retryable {
		t.Fatal("deterministic manifest validation must not be retryable")
	}
	if envelope.Hint == "" {
		t.Fatal("expected actionable remediation hint")
	}
}
