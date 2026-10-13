package artifacts

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/kb-labs/create/v2/contracts"
)

func TestBinariesInstallsOnlyVerifiedReleaseAsset(t *testing.T) {
	payload := []byte("kb-dev-binary")
	sum := fmt.Sprintf("%x", sha256.Sum256(payload))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(payload) }))
	defer server.Close()
	root := t.TempDir()
	if err := (Binaries{Root: root, CacheRoot: filepath.Join(root, ".kb", "v2", "cache")}).Install([]contracts.Artifact{{ID: "kb-dev", Kind: "binary", URL: server.URL, SHA256: sum, Target: "kb-dev"}}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, ".kb", "v2", "bin", "kb-dev"))
	if err != nil || string(data) != string(payload) {
		t.Fatalf("binary/error = %q / %v", data, err)
	}
}

func TestBinariesInstallPrimesChecksumKeyedOfflineCache(t *testing.T) {
	payload := []byte("kb-dev-binary")
	sum := fmt.Sprintf("%x", sha256.Sum256(payload))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(payload) }))
	defer server.Close()
	root := t.TempDir()
	item := contracts.Artifact{ID: "kb-dev", Kind: "binary", URL: server.URL, SHA256: sum, Target: "kb-dev"}
	cacheRoot := filepath.Join(root, ".kb", "v2", "cache")
	if err := (Binaries{Root: root, CacheRoot: cacheRoot}).Install([]contracts.Artifact{item}); err != nil {
		t.Fatal(err)
	}
	cachePath := filepath.Join(cacheRoot, "binaries", sum)
	if data, err := os.ReadFile(cachePath); err != nil || string(data) != string(payload) {
		t.Fatalf("cached binary/error = %q / %v", data, err)
	}
	if err := (Binaries{Root: root, CacheRoot: cacheRoot, Offline: true}).Install([]contracts.Artifact{item}); err != nil {
		t.Fatalf("cached binary was not available offline: %v", err)
	}
}

func TestBinariesCanUseInstallSharedCacheWhileInstallingIntoStage(t *testing.T) {
	payload := []byte("staged-kb-dev-binary")
	sum := fmt.Sprintf("%x", sha256.Sum256(payload))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(payload) }))
	defer server.Close()
	stage, sharedCache := t.TempDir(), t.TempDir()
	item := contracts.Artifact{ID: "kb-dev", Kind: "binary", URL: server.URL, SHA256: sum, Target: "kb-dev"}
	if err := (Binaries{Root: stage, CacheRoot: sharedCache}).Install([]contracts.Artifact{item}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(stage, ".kb", "v2", "cache")); !os.IsNotExist(err) {
		t.Fatalf("staged version contains the shared cache path: %v", err)
	}
	cachePath := filepath.Join(sharedCache, "binaries", sum)
	if data, err := os.ReadFile(cachePath); err != nil || string(data) != string(payload) {
		t.Fatalf("shared cached binary/error = %q / %v", data, err)
	}
	if err := (Binaries{Root: stage, CacheRoot: sharedCache, Offline: true}).Install([]contracts.Artifact{item}); err != nil {
		t.Fatalf("staged offline install could not read shared cache: %v", err)
	}
}

func TestBinariesRejectsChecksumMismatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("wrong")) }))
	defer server.Close()
	root := t.TempDir()
	err := (Binaries{Root: root, CacheRoot: filepath.Join(root, ".kb", "v2", "cache")}).Install([]contracts.Artifact{{ID: "kb-dev", Kind: "binary", URL: server.URL, SHA256: "bad", Target: "kb-dev"}})
	if err == nil {
		t.Fatal("expected checksum error")
	}
}

func TestBinariesOfflineUsesOnlyVerifiedLocalCache(t *testing.T) {
	payload := []byte("cached-kb-dev")
	sum := fmt.Sprintf("%x", sha256.Sum256(payload))
	root := t.TempDir()
	cache := filepath.Join(root, ".kb", "v2", "cache", "binaries")
	if err := os.MkdirAll(cache, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cache, sum), payload, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := (Binaries{Root: root, CacheRoot: filepath.Join(root, ".kb", "v2", "cache"), Offline: true}).Install([]contracts.Artifact{{ID: "kb-dev", Kind: "binary", URL: "https://must-not-be-fetched.test/kb-dev", SHA256: sum, Target: "kb-dev"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, ".kb", "v2", "bin", "kb-dev")); err != nil {
		t.Fatal(err)
	}
}

func TestBinariesOfflineFailsWithoutCache(t *testing.T) {
	root := t.TempDir()
	err := (Binaries{Root: root, CacheRoot: filepath.Join(root, ".kb", "v2", "cache"), Offline: true}).Install([]contracts.Artifact{{ID: "kb-dev", Kind: "binary", URL: "https://must-not-be-fetched.test/kb-dev", SHA256: "abc", Target: "kb-dev"}})
	if err == nil {
		t.Fatal("expected offline cache failure")
	}
}

func TestCompositeRemovesBinaryWhenPackageInstallFails(t *testing.T) {
	payload := []byte("kb-dev-binary")
	sum := fmt.Sprintf("%x", sha256.Sum256(payload))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(payload) }))
	defer server.Close()
	root := t.TempDir()
	runner := &fakeRunner{err: fmt.Errorf("pnpm failed")}
	executor := Composite{Packages: Pnpm{Root: root, Runner: runner}, Binaries: Binaries{Root: root, CacheRoot: filepath.Join(root, ".kb", "v2", "cache")}}
	err := executor.Install([]contracts.Artifact{{ID: "kb-dev", Kind: "binary", URL: server.URL, SHA256: sum, Target: "kb-dev"}, {ID: "platform", Package: "@kb/platform", Version: "2"}})
	if err == nil {
		t.Fatal("expected package failure")
	}
	if _, err := os.Stat(filepath.Join(root, ".kb", "v2", "bin", "kb-dev")); !os.IsNotExist(err) {
		t.Fatalf("binary remains after failed package install: %v", err)
	}
}
