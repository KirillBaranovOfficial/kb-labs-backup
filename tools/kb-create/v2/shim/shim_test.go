package shim

import (
	"os"
	"path/filepath"
	"testing"
)

func TestShimLifecycle(t *testing.T) {
	root := t.TempDir()
	launcherDir := filepath.Join(root, "bin")
	platformRoot := filepath.Join(root, "platform")
	if err := os.MkdirAll(launcherDir, 0o750); err != nil {
		t.Fatal(err)
	}
	target := Target(platformRoot)
	if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	launcher := filepath.Join(launcherDir, "kb-create")
	if err := Ensure(launcher, platformRoot); err != nil || !Correct(launcher, platformRoot) {
		t.Fatalf("Ensure error = %v; path points correctly = %t", err, Correct(launcher, platformRoot))
	}
	if err := Remove(launcher, platformRoot); err != nil || Correct(launcher, platformRoot) {
		t.Fatalf("Remove error = %v; shim still correct = %t", err, Correct(launcher, platformRoot))
	}
}

func TestRemoveRefusesToDeleteShimPointingElsewhere(t *testing.T) {
	root := t.TempDir()
	launcherDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(launcherDir, 0o750); err != nil {
		t.Fatal(err)
	}
	launcher := filepath.Join(launcherDir, "kb-create")
	path := Path(launcher)
	if err := os.Symlink("/elsewhere/kb", path); err != nil {
		t.Fatal(err)
	}
	if err := Remove(launcher, filepath.Join(root, "platform")); err == nil {
		t.Fatal("expected ownership mismatch")
	}
}
