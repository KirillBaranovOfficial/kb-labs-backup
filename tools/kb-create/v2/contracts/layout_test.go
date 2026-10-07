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
