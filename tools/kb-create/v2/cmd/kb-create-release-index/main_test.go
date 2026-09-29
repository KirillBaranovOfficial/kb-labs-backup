package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/kb-labs/create/v2/catalog"
)

func TestRunSealsNormalizedReleaseExport(t *testing.T) {
	dir := t.TempDir()
	input, output := filepath.Join(dir, "export.json"), filepath.Join(dir, "release", "index.json")
	manifestRoot := filepath.Join(dir, "staging")
	var source catalog.Catalog
	if err := json.Unmarshal([]byte(`{"channels":{"stable":"2.0.0"},"compatibility":{"schema":"kb.release-compatibility/2","labels":[{"id":"platform@2.0.0","kind":"platform","artifactId":"platform","version":"2.0.0","status":"prepared","validatedBy":["stage"]}]},"platforms":[{"id":"platform","version":"2.0.0","package":"@kb/platform","tarball":"https://example.test/platform.tgz","sha256":"artifact","host":{"command":"node_modules/.bin/kb-host","healthUrl":"http://127.0.0.1:4000/health"},"profiles":{"default":{}}}]}`), &source); err != nil {
		t.Fatal(err)
	}
	source.Platforms[0].Config = []catalog.ConfigRequirement{{ID: "gateway.access.mode", Path: "/gateway/access/mode", Default: `"local"`}}
	for _, id := range []string{"marketplace", "review", "scaffold"} {
		source.Platforms[0].Members = append(source.Platforms[0].Members, catalog.Component{ID: id, Version: "2.0.0", Package: "@kb/" + id, Tarball: "https://example.test/" + id + ".tgz", SHA256: id})
	}
	for _, id := range []string{"commit", "release"} {
		component := catalog.Component{ID: id, Version: "2.0.0", Package: "@kb/" + id, Tarball: "https://example.test/" + id + ".tgz", SHA256: id}
		if id == "commit" {
			component.Requires = []catalog.Requirement{{Capability: "cache"}}
		}
		source.Plugins = append(source.Plugins, component)
	}
	source.Adapters = []catalog.Adapter{{Component: catalog.Component{ID: "state-broker", Version: "2.0.0", Package: "@kb/state-broker", Tarball: "https://example.test/state-broker.tgz", SHA256: "state-broker"}, Provides: []string{"cache"}}}
	export, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(input, export, 0o600); err != nil {
		t.Fatal(err)
	}
	components := []catalog.Component{{ID: "platform", Version: "2.0.0", Package: "@kb/platform"}}
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
	if err := run(input, output, manifestRoot); err != nil {
		t.Fatal(err)
	}
	sealed, err := catalog.LoadFile(output)
	if err != nil || sealed.Digest == "" {
		t.Fatalf("sealed/error = %#v / %v", sealed, err)
	}
}

func TestRunRejectsMissingPublishPaths(t *testing.T) {
	if err := run("", "", ""); err == nil {
		t.Fatal("expected missing paths")
	}
}
