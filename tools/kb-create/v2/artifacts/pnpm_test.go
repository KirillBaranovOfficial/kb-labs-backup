package artifacts

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/kb-labs/create/v2/contracts"
	"github.com/kb-labs/create/v2/installed"
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

func TestPnpmReportsSignalTerminationAsLikelyMemoryFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("signal termination is only available on Unix")
	}
	runner := runnerFunc(func(context.Context, io.Writer, string, ...string) error {
		return exec.Command("sh", "-c", "kill -9 $$").Run()
	})
	err := (Pnpm{Root: t.TempDir(), Runner: runner}).Install([]contracts.Artifact{{ID: "a", Package: "@kb/a", Version: "1.0.0"}})
	failure, ok := err.(*contracts.LauncherError)
	if !ok || failure.Code != contracts.CodePackageManagerKilled || !strings.Contains(failure.Message, "likely out of memory") || !strings.Contains(failure.Message, "992.3 MiB RSS") || failure.Details["requiredAvailableMemory"] != "1.20 GiB" || failure.Details["tunedPeakMemory"] != "992.3 MiB" {
		t.Fatalf("error = %#v", err)
	}
}

type runnerFunc func(context.Context, io.Writer, string, ...string) error

func (run runnerFunc) Run(ctx context.Context, output io.Writer, name string, args ...string) error {
	return run(ctx, output, name, args...)
}

// Regression coverage for the mechanism, not just its absence from the pnpm
// invocation: pnpm 11.4.0 mis-parses a comma-joined --allow-build value as a
// single "name@version-union" spec (ERR_PNPM_INVALID_VERSION_UNION) the
// moment the list mixes a scoped package (like @kb-labs/devkit) with others
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
}

func TestPnpmMaterializesSignedArtifactManifestForDoctor(t *testing.T) {
	root := t.TempDir()
	item := contracts.Artifact{
		ID: "disk-io-storage", Kind: "adapter", Package: "@kb/data-store", Version: "2.0.0",
		Config: []contracts.ConfigRequirement{{ID: "store.path", Path: "/storage/path", Required: true, Default: `".kb/data"`, Hint: "set a storage path"}},
	}
	if err := (Pnpm{Root: root, Runner: &fakeRunner{}}).Install([]contracts.Artifact{item}); err != nil {
		t.Fatal(err)
	}
	manifest, err := installed.Load(root, item)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Requirements) != 1 {
		requirements := manifest.Requirements
		t.Fatalf("requirements = %#v", requirements)
	}
	requirement := manifest.Requirements[0]
	if requirement.ID != "store.path" || requirement.Path != "/storage/path" || requirement.Hint != "set a storage path" || string(requirement.Default) != `".kb/data"` {
		t.Fatalf("requirement = %#v", requirement)
	}
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

func TestPnpmUninstallDoesNotPassRegistryOption(t *testing.T) {
	root := t.TempDir()
	runner := &fakeRunner{}
	if err := (Pnpm{Root: root, Registry: "https://registry.test", Runner: runner}).Uninstall([]contracts.Artifact{{ID: "platform", Package: "@kb/platform", Version: "2.0.0"}}); err != nil {
		t.Fatal(err)
	}
	want := []string{"remove", "--dir", root, "--reporter=append-only", "--config.minimumReleaseAge=0", "@kb/platform"}
	if len(runner.calls) != 1 || !reflect.DeepEqual(runner.calls[0].args, want) {
		t.Fatalf("calls = %#v, want %q", runner.calls, want)
	}
}

func TestPnpmDoesNotOverwriteExistingWorkspaceYAML(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "pnpm-workspace.yaml")
	custom := "allowBuilds:\n  \"esbuild\": true\nminimumReleaseAgeExclude:\n  - \"@kb/a\"\n"
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
	if string(data) != custom {
		t.Fatalf("pnpm-workspace.yaml was overwritten: got %q, want unchanged %q", data, custom)
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
	err := (Pnpm{Root: root, Runner: runner}).Restore()
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
