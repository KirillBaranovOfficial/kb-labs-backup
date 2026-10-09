package artifacts

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/kb-labs/clikit/diag"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kb-labs/create/v2/contracts"
	"github.com/kb-labs/create/v2/installed"
	"gopkg.in/yaml.v3"
)

type call struct {
	name string
	args []string
}

func TestPnpmUsesVerifiedTarballInsteadOfRegistrySpec(t *testing.T) {
	payload := []byte("package-bytes")
	sum := fmt.Sprintf("%x", sha256.Sum256(payload))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(payload) }))
	defer server.Close()
	root := t.TempDir()
	runner := &fakeRunner{}
	if err := (Pnpm{Root: root, Runner: runner}).Install([]contracts.Artifact{{ID: "platform", Package: "@kb/platform", Version: "2", SHA256: sum, Tarball: server.URL}}); err != nil {
		t.Fatal(err)
	}
	want := "file:" + filepath.Join(root, ".kb", "v2", "cache", "packages", sum+".tgz")
	if len(runner.calls) != 1 || !reflect.DeepEqual(runner.calls[0].args, []string{"add", "--dir", root, "--reporter=append-only", want}) {
		t.Fatalf("calls = %#v", runner.calls)
	}
}

func TestPnpmMinimumReleaseAgeExemptsOnlySealedExactVersions(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("COREPACK_HOME", filepath.Join(home, "corepack"))
	if _, err := exec.LookPath("node"); err != nil {
		t.Skipf("pnpm 11.4.0 integration requires Node.js; node is unavailable: %v", err)
	}
	if _, err := exec.LookPath("corepack"); err != nil {
		t.Skipf("pnpm 11.4.0 integration requires Corepack; corepack is unavailable: %v", err)
	}
	versionCheck := exec.Command("corepack", "pnpm@11.4.0", "--version")
	versionCheck.Env = os.Environ()
	if output, err := versionCheck.CombinedOutput(); err != nil {
		t.Skipf("pnpm 11.4.0 cannot run with the available Node.js/Corepack toolchain: %v: %s", err, strings.TrimSpace(string(output)))
	} else if strings.TrimSpace(string(output)) != "11.4.0" {
		t.Fatalf("Corepack selected unexpected pnpm version: %q", strings.TrimSpace(string(output)))
	}

	sealedV1 := packageTarball(t, "@kb-labs/sealed", "1.0.0", nil)
	sealedV2 := packageTarball(t, "@kb-labs/sealed", "1.0.1", nil)
	sealedWithFreshDependency := packageTarball(t, "@kb-labs/sealed-fresh-dependency", "1.0.1", map[string]string{"@unrelated/fresh": "1.0.0"})
	freshDependency := packageTarball(t, "@unrelated/fresh", "1.0.0", nil)
	var requestLog []string
	var requestLogMu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestLogMu.Lock()
		requestLog = append(requestLog, r.URL.Path)
		requestLogMu.Unlock()
		switch {
		case strings.HasSuffix(r.URL.Path, "/fresh/sealed-fresh-dependency-1.0.1.tgz"):
			_, _ = w.Write(sealedWithFreshDependency)
		case strings.HasSuffix(r.URL.Path, "/fresh/fresh-1.0.0.tgz"):
			_, _ = w.Write(freshDependency)
		case strings.HasSuffix(r.URL.Path, "/sealed-1.0.0.tgz"):
			_, _ = w.Write(sealedV1)
		case strings.HasSuffix(r.URL.Path, "/sealed-1.0.1.tgz"):
			_, _ = w.Write(sealedV2)
		case strings.HasSuffix(r.URL.Path, "/fresh-1.0.0.tgz"):
			_, _ = w.Write(freshDependency)
		case strings.Contains(strings.ToLower(r.URL.Path), "kb-labs") && strings.Contains(strings.ToLower(r.URL.Path), "sealed"):
			prefix := ""
			name := "@kb-labs/sealed"
			tarballs := map[string]string{
				"1.0.0": serverURL(r) + "/sealed-1.0.0.tgz",
				"1.0.1": serverURL(r) + "/sealed-1.0.1.tgz",
			}
			if strings.HasPrefix(r.URL.Path, "/fresh/") {
				prefix = "/fresh"
				name = "@kb-labs/sealed-fresh-dependency"
				tarballs = map[string]string{"1.0.1": serverURL(r) + prefix + "/sealed-fresh-dependency-1.0.1.tgz"}
			}
			dependencies := map[string]string(nil)
			if name == "@kb-labs/sealed-fresh-dependency" {
				dependencies = map[string]string{"@unrelated/fresh": "1.0.0"}
			}
			writeRegistryPackument(w, name, tarballs, dependencies)
		case strings.Contains(strings.ToLower(r.URL.Path), "unrelated"):
			prefix := ""
			if strings.HasPrefix(r.URL.Path, "/fresh/") {
				prefix = "/fresh"
			}
			writeRegistryPackument(w, "@unrelated/fresh", map[string]string{
				"1.0.0": serverURL(r) + prefix + "/fresh-1.0.0.tgz",
			}, nil)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	itemV1 := sealedArtifact("1.0.0")
	itemV2 := sealedArtifact("1.0.1")
	root := t.TempDir()
	writeMinimumReleaseAgeWorkspace(t, root)
	pm := Pnpm{Root: root, Registry: server.URL}
	if err := pm.Install([]contracts.Artifact{itemV1}); err != nil {
		t.Fatalf("install first sealed exact version: %v", err)
	}
	if err := pm.Install([]contracts.Artifact{itemV2}); err != nil {
		t.Fatalf("install second sealed exact version from the coalesced exclusion: %v", err)
	}
	workspace, err := os.ReadFile(filepath.Join(root, "pnpm-workspace.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(workspace), "@kb-labs/sealed@1.0.0 || 1.0.1") {
		t.Fatalf("workspace config did not retain both sealed exact versions: %s", workspace)
	}

	freshRoot := t.TempDir()
	writeMinimumReleaseAgeWorkspace(t, freshRoot)
	freshItem := contracts.Artifact{ID: "sealed-fresh-dependency", Package: "@kb-labs/sealed-fresh-dependency", Version: "1.0.1"}
	var transcript strings.Builder
	err = (Pnpm{Root: freshRoot, Registry: server.URL + "/fresh", Log: &transcript}).Install([]contracts.Artifact{freshItem})
	if err == nil {
		requestLogMu.Lock()
		requests := append([]string(nil), requestLog...)
		requestLogMu.Unlock()
		t.Fatalf("expected the unrelated freshly published dependency to be refused; transcript=%s requests=%v", transcript.String(), requests)
	}
	if !strings.Contains(transcript.String(), "ERR_PNPM_NO_MATURE_MATCHING_VERSION") || !strings.Contains(transcript.String(), "minimumReleaseAge cutoff") {
		t.Fatalf("pnpm error did not identify the release-age refusal: err=%v transcript=%s", err, transcript.String())
	}
	foundFreshMetadata := false
	requestLogMu.Lock()
	requests := append([]string(nil), requestLog...)
	requestLogMu.Unlock()
	for _, path := range requests {
		if strings.Contains(strings.ToLower(path), "unrelated") {
			foundFreshMetadata = true
		}
	}
	if !foundFreshMetadata {
		t.Fatalf("pnpm did not resolve the unrelated dependency from the local registry: requests=%v", requests)
	}
}

func writeMinimumReleaseAgeWorkspace(t *testing.T, root string) {
	t.Helper()
	if err := os.MkdirAll(root, 0o750); err != nil {
		t.Fatal(err)
	}
	config := "minimumReleaseAge: 1440\n"
	if err := os.WriteFile(filepath.Join(root, "pnpm-workspace.yaml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
}

func sealedArtifact(version string) contracts.Artifact {
	return contracts.Artifact{ID: "sealed", Package: "@kb-labs/sealed", Version: version}
}

func writeRegistryPackument(w http.ResponseWriter, name string, tarballs map[string]string, dependencies map[string]string) {
	versions := make(map[string]any, len(tarballs))
	times := make(map[string]string, len(tarballs))
	now := time.Now().UTC().Format(time.RFC3339)
	var latest string
	for version, tarball := range tarballs {
		metadata := map[string]any{
			"name": name, "version": version,
			"dist": map[string]string{"tarball": tarball},
		}
		if dependencies != nil {
			metadata["dependencies"] = dependencies
		}
		versions[version] = metadata
		times[version] = now
		latest = version
	}
	w.Header().Set("Content-Type", "application/vnd.npm.install-v1+json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"name": name, "dist-tags": map[string]string{"latest": latest}, "versions": versions, "time": times,
	})
}

func packageTarball(t *testing.T, name, version string, dependencies map[string]string) []byte {
	t.Helper()
	manifestValue := map[string]any{"name": name, "version": version}
	if dependencies != nil {
		manifestValue["dependencies"] = dependencies
	}
	manifest, err := json.Marshal(manifestValue)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	compressed := gzip.NewWriter(&output)
	archive := tar.NewWriter(compressed)
	if err := archive.WriteHeader(&tar.Header{Name: "package/package.json", Mode: 0o600, Size: int64(len(manifest))}); err != nil {
		t.Fatal(err)
	}
	if _, err := archive.Write(manifest); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := compressed.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func serverURL(request *http.Request) string {
	return "http://" + request.Host
}

func TestPnpmReportsExpectedAndReceivedTarballChecksums(t *testing.T) {
	payload := []byte("unexpected-package-bytes")
	sum := fmt.Sprintf("%x", sha256.Sum256([]byte("expected-package-bytes")))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(payload) }))
	defer server.Close()
	runner := &fakeRunner{}
	err := (Pnpm{Root: t.TempDir(), Runner: runner}).Install([]contracts.Artifact{{ID: "platform", Package: "@kb/platform", Version: "2", SHA256: sum, Tarball: server.URL}})
	if err == nil || !strings.Contains(err.Error(), "expected "+sum) || !strings.Contains(err.Error(), fmt.Sprintf("got %x", sha256.Sum256(payload))) || len(runner.calls) != 0 {
		t.Fatalf("error/calls = %v / %#v", err, runner.calls)
	}
}

func TestPnpmSealedArtifactOverridesFollowInstallUpdateAndRollback(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "pnpm-workspace.yaml")
	if err := os.WriteFile(workspace, []byte("overrides:\n  lodash: 4.17.21\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := contracts.Artifact{ID: "platform", Kind: "platform-member", Package: "@kb/platform", Version: "2.0.0", SHA256: fmt.Sprintf("%x", sha256.Sum256([]byte("old"))), Tarball: "https://registry.test/platform-old.tgz"}
	oldRuntime := contracts.Artifact{ID: "runtime", Kind: "core", Package: "@kb/runtime", Version: "2.0.0", SHA256: fmt.Sprintf("%x", sha256.Sum256([]byte("old-runtime"))), Tarball: "https://registry.test/runtime-old.tgz"}
	updated := contracts.Artifact{ID: "platform", Kind: "platform-member", Package: "@kb/platform", Version: "2.1.0", SHA256: fmt.Sprintf("%x", sha256.Sum256([]byte("new"))), Tarball: "https://registry.test/platform-new.tgz"}
	updatedRuntime := contracts.Artifact{ID: "runtime", Kind: "core", Package: "@kb/runtime", Version: "2.1.0", SHA256: fmt.Sprintf("%x", sha256.Sum256([]byte("new-runtime"))), Tarball: "https://registry.test/runtime-new.tgz"}
	for _, payload := range [][]byte{[]byte("old"), []byte("old-runtime"), []byte("new"), []byte("new-runtime")} {
		sum := fmt.Sprintf("%x", sha256.Sum256(payload))
		cache := filepath.Join(root, ".kb", "v2", "cache", "packages")
		if err := os.MkdirAll(cache, 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(cache, sum+".tgz"), payload, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	pm := Pnpm{Root: root, Runner: &fakeRunner{}}
	if err := pm.Install([]contracts.Artifact{old, oldRuntime}); err != nil {
		t.Fatalf("initial install: %v", err)
	}
	assertSealedArtifactOverrides(t, workspace, []contracts.Artifact{old, oldRuntime})
	if err := pm.Install([]contracts.Artifact{updated, updatedRuntime}); err != nil {
		t.Fatalf("update: %v", err)
	}
	assertSealedArtifactOverrides(t, workspace, []contracts.Artifact{updated, updatedRuntime})
	if err := pm.Restore([]contracts.Artifact{old, oldRuntime}); err != nil {
		t.Fatalf("rollback package restore: %v", err)
	}
	assertSealedArtifactOverrides(t, workspace, []contracts.Artifact{old, oldRuntime})
}

func assertSealedArtifactOverrides(t *testing.T, workspace string, artifacts []contracts.Artifact) {
	t.Helper()
	data, err := os.ReadFile(workspace)
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Overrides map[string]string `yaml:"overrides"`
	}
	if err := yaml.Unmarshal(data, &config); err != nil {
		t.Fatalf("parse pnpm workspace config: %v", err)
	}
	if config.Overrides["lodash"] != "4.17.21" {
		t.Fatalf("unrelated override was lost: %#v", config.Overrides)
	}
	wantNames := make(map[string]struct{}, len(artifacts))
	for _, artifact := range artifacts {
		want := filepath.ToSlash(filepath.Join("file:.kb", "v2", "cache", "packages", artifact.SHA256+".tgz"))
		wantNames[artifact.Package] = struct{}{}
		if config.Overrides[artifact.Package] != want {
			t.Fatalf("override for %s = %q, want %q; config: %s", artifact.Package, config.Overrides[artifact.Package], want, data)
		}
	}
	for name, value := range config.Overrides {
		if strings.HasPrefix(value, "file:.kb/v2/cache/packages/") {
			if _, exists := wantNames[name]; !exists {
				t.Fatalf("stale sealed override remains for %s: %q", name, value)
			}
		}
	}
}

func TestPnpmReportsSignalTerminationAsLikelyMemoryFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("signal termination is only available on Unix")
	}
	runner := runnerFunc(func(context.Context, io.Writer, string, ...string) error {
		return exec.Command("sh", "-c", "kill -9 $$").Run()
	})
	err := (Pnpm{Root: t.TempDir(), Runner: runner}).Install([]contracts.Artifact{{ID: "a", Package: "@kb/a", Version: "1.0.0"}})
	failure, ok := err.(*diag.Diag)
	if !ok || failure.Code != contracts.CodePackageManagerKilled || !strings.Contains(failure.Message, "likely out of memory") || !strings.Contains(failure.Message, "992.3 MiB RSS") || failure.Meta["requiredAvailableMemory"] != "1.20 GiB" || failure.Meta["tunedPeakMemory"] != "992.3 MiB" {
		t.Fatalf("error = %#v", err)
	}
}

type runnerFunc func(context.Context, io.Writer, string, ...string) error

func (run runnerFunc) Run(ctx context.Context, output io.Writer, name string, args ...string) error {
	return run(ctx, output, name, args...)
}

// Regression coverage for the mechanism, not just its absence from the pnpm
// invocation: pnpm 11.4.0 mis-parses a comma-joined --allow-build value as a
// single "name@version-union" spec (pnpm's invalid version-union error) the
// moment the list mixes scoped packages with others
// — confirmed directly against that pnpm version. The fix moves the
// approval list into pnpm-workspace.yaml's `allowBuilds` map instead, which
// handles scoped names correctly since it's YAML, not a CLI string.
func TestPnpmWritesWorkspaceAllowBuildsList(t *testing.T) {
	root := t.TempDir()
	runner := &fakeRunner{}
	if err := (Pnpm{Root: root, Runner: runner}).Install([]contracts.Artifact{{ID: "a", Package: "@kb/a", Version: "1.0.0"}}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "pnpm-workspace.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	for _, setting := range []string{"autoInstallPeers: false", "childConcurrency: 1", "networkConcurrency: 4", "sideEffectsCache: false"} {
		if !strings.Contains(got, setting) {
			t.Fatalf("pnpm-workspace.yaml = %q, missing %q", got, setting)
		}
	}
	for _, name := range approvedNativeBuilds {
		want := fmt.Sprintf("%q: true", name)
		if !strings.Contains(got, want) {
			t.Fatalf("pnpm-workspace.yaml = %q, missing %q", got, want)
		}
	}
	if strings.Contains(got, "@kb-labs/devkit") {
		t.Fatalf("pnpm-workspace.yaml must not approve @kb-labs/devkit build scripts: %q", got)
	}
}

func TestPnpmMaterializesSignedArtifactManifestForDoctor(t *testing.T) {
	root := t.TempDir()
	item := contracts.Artifact{
		ID: "disk-io-storage", Kind: "adapter", Package: "@kb/data-store", Version: "2.0.0",
		Config: []contracts.ConfigRequirement{{ID: "store.path", Path: "/storage/path", Required: true, Default: `".kb/data"`, Hint: "set a storage path"}, {ID: "gateway.jwtSecret", Secret: true, Generate: true, Env: "GATEWAY_JWT_SECRET", Services: []string{"gateway"}}},
	}
	if err := (Pnpm{Root: root, Runner: &fakeRunner{}}).Install([]contracts.Artifact{item}); err != nil {
		t.Fatal(err)
	}
	manifest, err := installed.Load(root, item)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Requirements) != 2 {
		requirements := manifest.Requirements
		t.Fatalf("requirements = %#v", requirements)
	}
	requirement := manifest.Requirements[0]
	if requirement.ID != "store.path" || requirement.Path != "/storage/path" || requirement.Hint != "set a storage path" || string(requirement.Default) != `".kb/data"` {
		t.Fatalf("requirement = %#v", requirement)
	}
	generated := manifest.Requirements[1]
	if generated.ID != "gateway.jwtSecret" || !generated.Generate || !generated.Secret {
		t.Fatalf("generated secret requirement = %#v", generated)
	}
}

// S-024.5: package-manager restore can replace pnpm's virtual-store package
// directory, so doctor metadata must be materialized again for the receipt
// restored from the snapshot.
func TestPnpmInstallUpdateRestoreKeepsManifestsForEveryReceiptArtifact(t *testing.T) {
	root := t.TempDir()
	old := []contracts.Artifact{
		{ID: "analytics-file", Kind: "adapter", Package: "@kb-labs/adapters-analytics-file", Version: "2.120.1"},
		{ID: "gateway", Kind: "platform-member", Package: "@kb-labs/gateway-app", Version: "2.120.1"},
	}
	updated := []contracts.Artifact{
		{ID: "analytics-file", Kind: "adapter", Package: "@kb-labs/adapters-analytics-file", Version: "2.120.2"},
		{ID: "gateway", Kind: "platform-member", Package: "@kb-labs/gateway-app", Version: "2.120.2"},
	}
	runner := &relinkingPnpmRunner{root: root, restore: old}
	pm := Pnpm{Root: root, Runner: runner}
	if err := pm.Install(old); err != nil {
		t.Fatalf("install initial receipt: %v", err)
	}
	assertReceiptManifestsLoad(t, root, old)
	if err := pm.Install(updated); err != nil {
		t.Fatalf("apply update: %v", err)
	}
	assertReceiptManifestsLoad(t, root, updated)

	if err := pm.Restore(old); err != nil {
		t.Fatalf("restore snapshot package state: %v", err)
	}
	assertReceiptManifestsLoad(t, root, old)
}

func assertReceiptManifestsLoad(t *testing.T, root string, artifacts []contracts.Artifact) {
	t.Helper()
	for _, artifact := range artifacts {
		if _, err := installed.Load(root, artifact); err != nil {
			t.Errorf("load manifest for receipt artifact %s (%s@%s): %v", artifact.ID, artifact.Package, artifact.Version, err)
		}
	}
}

// relinkingPnpmRunner models pnpm's node_modules symlink into the virtual
// store. Each add/install makes fresh package directories from package
// metadata, which drops files that the previous operation wrote into them.
type relinkingPnpmRunner struct {
	root    string
	restore []contracts.Artifact
	active  []contracts.Artifact
}

func (r *relinkingPnpmRunner) Run(_ context.Context, _ io.Writer, _ string, args ...string) error {
	if len(args) == 0 {
		return fmt.Errorf("pnpm command is missing")
	}
	switch args[0] {
	case "add":
		var next []contracts.Artifact
		for _, arg := range args[1:] {
			if !strings.HasPrefix(arg, "@kb-labs/") {
				continue
			}
			separator := strings.LastIndex(arg, "@")
			if separator < 0 {
				continue
			}
			next = append(next, contracts.Artifact{ID: strings.TrimPrefix(arg[:separator], "@kb-labs/"), Package: arg[:separator], Version: arg[separator+1:]})
		}
		r.active = next
	case "install":
		r.active = append([]contracts.Artifact(nil), r.restore...)
	default:
		return fmt.Errorf("unexpected pnpm command %q", args[0])
	}
	for _, artifact := range r.active {
		if err := r.relink(artifact); err != nil {
			return err
		}
	}
	return nil
}

func (r *relinkingPnpmRunner) relink(artifact contracts.Artifact) error {
	packageRoot := filepath.Join(r.root, "node_modules", filepath.FromSlash(artifact.Package))
	storePackage := filepath.Join(r.root, "node_modules", ".pnpm", strings.ReplaceAll(artifact.Package, "/", "+")+"@"+artifact.Version, "node_modules", filepath.FromSlash(artifact.Package))
	if err := os.RemoveAll(storePackage); err != nil {
		return err
	}
	if err := os.MkdirAll(storePackage, 0o755); err != nil {
		return err
	}
	packageJSON, err := json.Marshal(map[string]string{"name": artifact.Package, "version": artifact.Version})
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(storePackage, "package.json"), packageJSON, 0o644); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(packageRoot), 0o755); err != nil {
		return err
	}
	if err := os.Remove(packageRoot); err != nil && !os.IsNotExist(err) {
		return err
	}
	relative, err := filepath.Rel(filepath.Dir(packageRoot), storePackage)
	if err != nil {
		return err
	}
	return os.Symlink(relative, packageRoot)
}

func TestPnpmPersistsExplicitRegistryForLifecycleOperations(t *testing.T) {
	root := t.TempDir()
	runner := &fakeRunner{}
	if err := (Pnpm{Root: root, Registry: "https://registry.test", Runner: runner}).Install([]contracts.Artifact{{ID: "a", Package: "@kb/a", Version: "1.0.0"}}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, ".npmrc"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(data), "registry=https://registry.test\n"; got != want {
		t.Fatalf(".npmrc = %q, want %q", got, want)
	}
}

func TestPnpmPreservesExistingNpmrc(t *testing.T) {
	root := t.TempDir()
	want := "registry=https://existing.test\n"
	if err := os.WriteFile(filepath.Join(root, ".npmrc"), []byte(want), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := (Pnpm{Root: root, Registry: "https://registry.test", Runner: &fakeRunner{}}).Install([]contracts.Artifact{{ID: "a", Package: "@kb/a", Version: "1.0.0"}}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, ".npmrc"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != want {
		t.Fatalf("existing .npmrc changed: got %q, want %q", data, want)
	}
}

func TestPnpmDoesNotPinGeneratedProjectToOnePatchPnpmVersion(t *testing.T) {
	root := t.TempDir()
	runner := &fakeRunner{}
	if err := (Pnpm{Root: root, Runner: runner}).Install([]contracts.Artifact{{ID: "a", Package: "@kb/a", Version: "1.0.0"}}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if _, exists := manifest["packageManager"]; exists {
		t.Fatalf("generated package.json must not pin packageManager: %s", data)
	}
}

func TestPnpmUninstallRemovesInstalledArtifactsByPackageName(t *testing.T) {
	root := t.TempDir()
	runner := &fakeRunner{}
	items := []contracts.Artifact{
		{ID: "platform", Package: "@kb/platform", Version: "2.0.0"},
		{ID: "duplicate", Package: "@kb/platform", Version: "2.0.0"},
		{ID: "binary", Kind: "binary", Version: "2.0.0"},
	}
	if err := (Pnpm{Root: root, Runner: runner}).Uninstall(items); err != nil {
		t.Fatal(err)
	}
	want := []string{"remove", "--dir", root, "--reporter=append-only", "--config.minimumReleaseAge=0", "@kb/platform"}
	if len(runner.calls) != 1 || !reflect.DeepEqual(runner.calls[0].args, want) {
		t.Fatalf("calls = %#v", runner.calls)
	}
}

func TestPnpmUninstallUsesRegistryConfigWithoutUnsupportedFlag(t *testing.T) {
	root := t.TempDir()
	runner := &fakeRunner{}
	if err := (Pnpm{Root: root, Registry: "https://registry.test", Runner: runner}).Uninstall([]contracts.Artifact{{ID: "platform", Package: "@kb/platform", Version: "2.0.0"}}); err != nil {
		t.Fatal(err)
	}
	want := []string{"remove", "--dir", root, "--reporter=append-only", "--config.minimumReleaseAge=0", "--config.registry=https://registry.test", "@kb/platform"}
	if len(runner.calls) != 1 || !reflect.DeepEqual(runner.calls[0].args, want) {
		t.Fatalf("calls = %#v, want %q", runner.calls, want)
	}
}

func TestPnpmMergesExactPlanVersionIntoExistingWorkspaceYAML(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "pnpm-workspace.yaml")
	custom := "allowBuilds:\n  \"esbuild\": true\nminimumReleaseAgeExclude:\n  - \"@kb/a@0.9.0\"\n"
	if err := os.WriteFile(path, []byte(custom), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"name":"kb-platform","private":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{}
	if err := (Pnpm{Root: root, Runner: runner}).Install([]contracts.Artifact{{ID: "a", Package: "@kb/a", Version: "1.0.0"}}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		MinimumReleaseAgeExclude []string `yaml:"minimumReleaseAgeExclude"`
	}
	if err := yaml.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(config.MinimumReleaseAgeExclude, []string{"@kb/a@0.9.0 || 1.0.0"}) {
		t.Fatalf("minimumReleaseAgeExclude = %#v, want one disjunction for the exact versions; yaml: %s", config.MinimumReleaseAgeExclude, data)
	}
}

func TestPnpmCoalescesEveryExactVersionForTheSamePackage(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "pnpm-workspace.yaml")
	if err := os.WriteFile(workspace, []byte("minimumReleaseAgeExclude:\n  - '@kb/runtime@1.0.0'\n  - '@kb/runtime@1.0.1'\n  - '@user/tool@4.2.0'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{}
	plan := []contracts.Artifact{{ID: "runtime", Package: "@kb/runtime", Version: "1.0.2"}}
	if err := (Pnpm{Root: root, Runner: runner}).Install(plan); err != nil {
		t.Fatal(err)
	}
	assertReleaseAgeExclusions(t, workspace, []string{
		"@kb/runtime@1.0.0 || 1.0.1 || 1.0.2", "@user/tool@4.2.0",
	})
}

// case: S-023.7
func TestPnpmExcludesExactPlanVersionsAndPreservesWorkspaceEntries(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "pnpm-workspace.yaml")
	if err := os.WriteFile(workspace, []byte("minimumReleaseAgeExclude:\n  - '@user/tool@4.2.0'\n  - '@pnpm/tool@3.1.0'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	installedDir := filepath.Join(root, "node_modules", "@kb", "runtime")
	if err := os.MkdirAll(installedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(installedDir, "package.json"), []byte(`{"name":"@kb/runtime","version":"2.0.0"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	payload := []byte("verified-file-artifact")
	sum := fmt.Sprintf("%x", sha256.Sum256(payload))
	cache := filepath.Join(root, ".kb", "v2", "cache", "packages")
	if err := os.MkdirAll(cache, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cache, sum+".tgz"), payload, 0o600); err != nil {
		t.Fatal(err)
	}
	plan := []contracts.Artifact{
		{ID: "runtime", Package: "@kb/runtime", Version: "2.0.0"},
		{ID: "new-file", Package: "@kb/new-file", Version: "1.5.0", SHA256: sum, Tarball: "https://registry.test/new-file.tgz"},
	}
	runner := &fakeRunner{}
	if err := (Pnpm{Root: root, Runner: runner}).Install(plan); err != nil {
		t.Fatal(err)
	}
	assertReleaseAgeExclusions(t, workspace, []string{
		"@user/tool@4.2.0", "@pnpm/tool@3.1.0", "@kb/runtime@2.0.0", "@kb/new-file@1.5.0",
	})

	// A rollback to plan N must merge N's exact versions after the snapshot is restored.
	if err := (Pnpm{Root: root, Runner: &fakeRunner{}}).Restore(plan); err != nil {
		t.Fatal(err)
	}
	assertReleaseAgeExclusions(t, workspace, []string{
		"@user/tool@4.2.0", "@pnpm/tool@3.1.0", "@kb/runtime@2.0.0", "@kb/new-file@1.5.0",
	})
}

func assertReleaseAgeExclusions(t *testing.T, path string, want []string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		MinimumReleaseAgeExclude []string `yaml:"minimumReleaseAgeExclude"`
	}
	if err := yaml.Unmarshal(data, &config); err != nil {
		t.Fatalf("parse pnpm-workspace.yaml: %v", err)
	}
	got := append([]string(nil), config.MinimumReleaseAgeExclude...)
	sort.Strings(got)
	wantSorted := append([]string(nil), want...)
	sort.Strings(wantSorted)
	if !reflect.DeepEqual(got, wantSorted) {
		t.Fatalf("minimumReleaseAgeExclude = %#v, want %#v; yaml: %s", got, wantSorted, data)
	}
	for _, bare := range []string{"@kb/runtime", "@kb/new-file"} {
		for _, exclusion := range config.MinimumReleaseAgeExclude {
			if exclusion == bare {
				t.Fatalf("minimumReleaseAgeExclude widened to bare package %q", bare)
			}
		}
	}
}

type fakeRunner struct {
	calls []call
	err   error
}

func (r *fakeRunner) Run(_ context.Context, _ io.Writer, name string, args ...string) error {
	r.calls = append(r.calls, call{name, args})
	return r.err
}

func TestPnpmInstallsSortedExactArtifactBatch(t *testing.T) {
	root := t.TempDir()
	runner := &fakeRunner{}
	err := (Pnpm{Root: root, Registry: "https://registry.test", Runner: runner}).Install([]contracts.Artifact{{ID: "b", Package: "@kb/b", Version: "2.0.0"}, {ID: "a", Package: "@kb/a", Version: "1.0.0"}, {ID: "repeat", Package: "@kb/a", Version: "1.0.0"}})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"add", "--dir", root, "--reporter=append-only", "@kb/a@1.0.0", "@kb/b@2.0.0", "--registry", "https://registry.test"}
	if len(runner.calls) != 1 || !reflect.DeepEqual(runner.calls[0].args, want) {
		t.Fatalf("calls = %#v, want %q", runner.calls, want)
	}
}

func TestPnpmSkipsAlreadyInstalledExactArtifact(t *testing.T) {
	root := t.TempDir()
	pkg := filepath.Join(root, "node_modules", "@kb", "a")
	if err := os.MkdirAll(pkg, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkg, "package.json"), []byte(`{"name":"@kb/a","version":"1.0.0"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{}
	if err := (Pnpm{Root: root, Runner: runner}).Install([]contracts.Artifact{{ID: "a", Package: "@kb/a", Version: "1.0.0"}}); err != nil {
		t.Fatal(err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("calls = %#v", runner.calls)
	}
}
func TestPnpmDoesNotRunForInvalidArtifact(t *testing.T) {
	runner := &fakeRunner{}
	err := (Pnpm{Root: t.TempDir(), Runner: runner}).Install([]contracts.Artifact{{ID: "broken", Package: "@kb/broken"}})
	if err == nil || len(runner.calls) != 0 {
		t.Fatalf("err/calls = %v / %#v", err, runner.calls)
	}
}
func TestPnpmPreservesPackageManagerFailure(t *testing.T) {
	root := t.TempDir()
	runner := &fakeRunner{err: errors.New("exit status 1")}
	err := (Pnpm{Root: root, Runner: runner}).Restore(nil)
	if err == nil || !errors.Is(err, runner.err) {
		t.Fatalf("err = %v", err)
	}
}
func TestPnpmSkipsReleaseManagedBinary(t *testing.T) {
	runner := &fakeRunner{}
	if err := (Pnpm{Root: t.TempDir(), Runner: runner}).Install([]contracts.Artifact{{ID: "kb-dev", Kind: "binary", Version: "2"}}); err != nil {
		t.Fatal(err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("calls = %#v", runner.calls)
	}
}

func TestPnpmUsesOfflineModeWhenRequested(t *testing.T) {
	root := t.TempDir()
	runner := &fakeRunner{}
	if err := (Pnpm{Root: root, Offline: true, Runner: runner}).Install([]contracts.Artifact{{ID: "platform", Package: "@kb/platform", Version: "2"}}); err != nil {
		t.Fatal(err)
	}
	want := []string{"add", "--dir", root, "--reporter=append-only", "--offline", "@kb/platform@2"}
	if len(runner.calls) != 1 || !reflect.DeepEqual(runner.calls[0].args, want) {
		t.Fatalf("calls = %#v", runner.calls)
	}
}
