package shim

import (
	"os"
	"os/exec"
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
	cliEntry := filepath.Join(platformRoot, "node_modules", "@kb-labs", "cli-bin", "dist", "bin.js")
	if err := os.MkdirAll(filepath.Dir(cliEntry), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cliEntry, []byte("process.stdout.write(process.argv.slice(2).join(' '))\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	launcher := filepath.Join(launcherDir, "kb-create")
	if err := Ensure(launcher, platformRoot); err != nil || !Correct(launcher, platformRoot) {
		t.Fatalf("Ensure error = %v; path points correctly = %t", err, Correct(launcher, platformRoot))
	}
	output, err := exec.Command(Path(launcher), "first", "value").Output()
	if err != nil || string(output) != "first value" {
		t.Fatalf("shim output/error = %q / %v", output, err)
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
