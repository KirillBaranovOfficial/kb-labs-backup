package installed

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/kb-labs/create/v2/contracts"
)

func TestDoctorInputRequiresExactInstalledManifest(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "node_modules", "@kb", "plugin")
	if err := os.MkdirAll(path, 0o750); err != nil {
		t.Fatal(err)
	}
	data := []byte(`{"schema":"kb.create.artifact-manifest/v2","id":"plugin","package":"@kb/plugin","version":"1.2.3","requirements":[{"id":"plugin.url","path":"/plugin/url","required":true,"hint":"set URL"},{"id":"plugin.token","secret":true,"required":true,"hint":"set token"}]}`)
	if err := os.WriteFile(filepath.Join(path, "kb-create.manifest.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	input, err := DoctorInput(root, []contracts.Artifact{{ID: "plugin", Package: "@kb/plugin", Version: "1.2.3"}}, map[string]bool{})
	if err != nil || len(input.Manifests) != 1 || input.Manifests[0].Requirements[0].ID != "plugin.url" || input.Configured["plugin.token"] {
		t.Fatalf("input/error = %#v / %v", input, err)
	}
}

func TestDoctorInputRecognizesConfiguredSecretOption(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "node_modules", "@kb", "plugin")
	if err := os.MkdirAll(path, 0o750); err != nil {
		t.Fatal(err)
	}
	data := []byte(`{"schema":"kb.create.artifact-manifest/v2","id":"plugin","package":"@kb/plugin","version":"1.2.3","requirements":[{"id":"openai.apiKey","path":"/platform/adapterOptions/llm/apiKey","secret":true,"required":true,"env":"OPENAI_API_KEY","hint":"set key"}]}`)
	if err := os.WriteFile(filepath.Join(path, "kb-create.manifest.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	input, err := DoctorInput(root, []contracts.Artifact{{ID: "plugin", Package: "@kb/plugin", Version: "1.2.3"}}, map[string]bool{"/platform/adapterOptions/llm/apiKey": true})
	if err != nil || !input.Configured["openai.apiKey"] {
		t.Fatalf("input/error = %#v / %v", input, err)
	}
	t.Setenv("OPENAI_API_KEY", "configured")
	input, err = DoctorInput(root, []contracts.Artifact{{ID: "plugin", Package: "@kb/plugin", Version: "1.2.3"}}, map[string]bool{})
	if err != nil || !input.Configured["openai.apiKey"] {
		t.Fatalf("environment-backed input/error = %#v / %v", input, err)
	}
}

func TestDoctorInputRejectsMissingManifest(t *testing.T) {
	_, err := DoctorInput(t.TempDir(), []contracts.Artifact{{ID: "plugin", Package: "@kb/plugin", Version: "1.2.3"}}, map[string]bool{})
	if err == nil {
		t.Fatal("expected manifest integrity error")
	}
}

func TestInspectProjectionsReportsMissingManifestWithoutWriting(t *testing.T) {
	root := t.TempDir()
	packageRoot := filepath.Join(root, "node_modules", "@kb", "plugin")
	if err := os.MkdirAll(packageRoot, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packageRoot, "package.json"), []byte(`{"name":"@kb/plugin","version":"1.2.3"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	artifact := contracts.Artifact{
		ID: "plugin", Package: "@kb/plugin", Version: "1.2.3",
		Config: []contracts.ConfigRequirement{{ID: "plugin.url", Path: "/plugin/url", Required: true}},
	}

	issues, err := InspectProjections(root, []contracts.Artifact{artifact})
	if err != nil || len(issues) != 1 || issues[0].Package != "@kb/plugin" || !issues[0].Repairable {
		t.Fatalf("issues/error = %#v / %v", issues, err)
	}
	manifestPath := filepath.Join(packageRoot, "kb-create.manifest.json")
	if _, err := os.Stat(manifestPath); !os.IsNotExist(err) {
		t.Fatalf("read-only inspection wrote a manifest: %v", err)
	}
}

func TestEnsureProjectionsRestoresMissingManifestAfterPackageRelink(t *testing.T) {
	root := t.TempDir()
	packageRoot := filepath.Join(root, "node_modules", "@kb", "plugin")
	if err := os.MkdirAll(packageRoot, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packageRoot, "package.json"), []byte(`{"name":"@kb/plugin","version":"1.2.3"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	artifact := contracts.Artifact{ID: "plugin", Package: "@kb/plugin", Version: "1.2.3", Config: []contracts.ConfigRequirement{{ID: "plugin.url", Path: "/plugin/url", Required: true}}}
	if err := EnsureProjections(root, []contracts.Artifact{artifact}); err != nil {
		t.Fatal(err)
	}
	manifest, err := Load(root, artifact)
	if err != nil || len(manifest.Requirements) != 1 || manifest.Requirements[0].ID != "plugin.url" {
		t.Fatalf("manifest/error = %#v / %v", manifest, err)
	}
}

func TestEnsureProjectionsValidatesAllPackageIdentitiesBeforeWriting(t *testing.T) {
	root := t.TempDir()
	firstRoot := filepath.Join(root, "node_modules", "@kb", "first")
	secondRoot := filepath.Join(root, "node_modules", "@kb", "second")
	for _, packageRoot := range []string{firstRoot, secondRoot} {
		if err := os.MkdirAll(packageRoot, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(firstRoot, "package.json"), []byte(`{"name":"@kb/first","version":"1.0.0"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(secondRoot, "package.json"), []byte(`{"name":"@kb/second","version":"9.0.0"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	artifacts := []contracts.Artifact{
		{ID: "first", Package: "@kb/first", Version: "1.0.0"},
		{ID: "second", Package: "@kb/second", Version: "2.0.0"},
	}
	if err := EnsureProjections(root, artifacts); err == nil {
		t.Fatal("expected receipt identity mismatch")
	}
	if _, err := os.Stat(filepath.Join(firstRoot, "kb-create.manifest.json")); !os.IsNotExist(err) {
		t.Fatalf("repair wrote a projection before validating all package identities: %v", err)
	}
}

func TestEnsureProjectionsRejectsPackageIdentityMismatch(t *testing.T) {
	root := t.TempDir()
	packageRoot := filepath.Join(root, "node_modules", "@kb", "plugin")
	if err := os.MkdirAll(packageRoot, 0o750); err != nil {
		t.Fatal(err)
	}
	identity, err := json.Marshal(map[string]string{"name": "@kb/plugin", "version": "1.2.4"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packageRoot, "package.json"), identity, 0o600); err != nil {
		t.Fatal(err)
	}
	err = EnsureProjections(root, []contracts.Artifact{{ID: "plugin", Package: "@kb/plugin", Version: "1.2.3"}})
	if err == nil {
		t.Fatal("expected receipt package version mismatch")
	}
	if _, err := os.Stat(filepath.Join(packageRoot, "kb-create.manifest.json")); !os.IsNotExist(err) {
		t.Fatalf("manifest should not be created for mismatched package: %v", err)
	}
}
