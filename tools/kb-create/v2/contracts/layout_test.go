package contracts

import (
	"path/filepath"
	"testing"
)

func TestRuntimeStatePathsAreUniqueRootRelativePaths(t *testing.T) {
	seen := map[string]bool{}
	for _, path := range RuntimeStatePaths() {
		if path == "" || filepath.IsAbs(path) || filepath.Clean(path) != path || seen[path] {
			t.Fatalf("invalid or duplicate runtime state path %q", path)
		}
		seen[path] = true
	}
	if len(seen) == 0 {
		t.Fatal("platform layout contract contains no runtime state paths")
	}
}

func TestVersionRootsDirectoryFollowsEmbeddedPlatformLayout(t *testing.T) {
	root := filepath.Join(t.TempDir(), "platform")
	want := filepath.Join(root, "versions")
	if got := VersionRootsDirectory(root); got != want {
		t.Fatalf("VersionRootsDirectory(%q) = %q, want %q", root, got, want)
	}
	if got, want := HostStateDirectory(root), filepath.Join(root, "installer", "host"); got != want {
		t.Fatalf("HostStateDirectory(%q) = %q, want %q", root, got, want)
	}
	for _, shared := range SharedRuntimePaths() {
		if shared.PlatformPath == ".kb/marketplace.lock" {
			t.Fatal("marketplace lock describes one sealed version and must not be shared between roots")
		}
	}
	versionState := VersionStatePaths()
	if len(versionState) == 0 {
		t.Fatal("platform layout contract does not declare version-owned runtime state")
	}
	foundMarketplaceLock := false
	for _, path := range versionState {
		if filepath.IsAbs(path) || filepath.Clean(path) != path {
			t.Fatalf("invalid version-owned state path %q", path)
		}
		if path == ".kb/marketplace.lock" {
			foundMarketplaceLock = true
		}
	}
	if !foundMarketplaceLock {
		t.Fatal("contract must classify the sealed package lock as version-owned")
	}
	sharedPaths := map[string]string{}
	for _, shared := range SharedRuntimePaths() {
		sharedPaths[shared.PlatformPath] = shared.StatePath
	}
	for platformPath, statePath := range map[string]string{".kb/logs": "state/logs", ".kb/diagnostics": "state/diagnostics", ".kb/database": "state/data/database"} {
		if sharedPaths[platformPath] != statePath {
			t.Fatalf("runtime history path %s maps to %q, want %q", platformPath, sharedPaths[platformPath], statePath)
		}
	}
	if got, want := InstallerRecordPath(root, "snapshots"), filepath.Join(root, "installer", "snapshots"); got != want {
		t.Fatalf("snapshot record path = %q, want %q", got, want)
	}
	if SnapshotRetention() != 2 {
		t.Fatalf("snapshot retention = %d, want 2", SnapshotRetention())
	}
}
