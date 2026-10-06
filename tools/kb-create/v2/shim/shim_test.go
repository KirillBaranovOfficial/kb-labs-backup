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

func TestEnsureReplacesOnlyAnInstallerManagedShim(t *testing.T) {
	root := t.TempDir()
	launcherDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(launcherDir, 0o750); err != nil {
		t.Fatal(err)
	}
	launcher := filepath.Join(launcherDir, "kb-create")
	firstRoot := filepath.Join(root, "first")
	secondRoot := filepath.Join(root, "second")
	for _, platformRoot := range []string{firstRoot, secondRoot} {
		cliEntry := filepath.Join(platformRoot, "node_modules", "@kb-labs", "cli-bin", "dist", "bin.js")
		if err := os.MkdirAll(filepath.Dir(cliEntry), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(cliEntry, []byte("export {};\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := Ensure(launcher, firstRoot); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(launcher, secondRoot); err != nil {
		t.Fatalf("Ensure did not switch the managed shim to the newly applied root: %v", err)
	}
	if !Correct(launcher, secondRoot) {
		t.Fatalf("shim does not point at the new platform root: %s", Path(launcher))
	}
	if err := Ensure(launcher, firstRoot); err != nil {
		t.Fatalf("Ensure did not switch a managed shim back: %v", err)
	}
	if !Correct(launcher, firstRoot) {
		t.Fatalf("shim does not point at the selected platform root: %s", Path(launcher))
	}
}

func TestEnsureRefusesToReplaceAUserOwnedLauncherPath(t *testing.T) {
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
	if err := os.WriteFile(cliEntry, []byte("export {};\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	launcher := filepath.Join(launcherDir, "kb-create")
	path := Path(launcher)
	if err := os.WriteFile(path, []byte("user data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(launcher, platformRoot); err == nil {
		t.Fatal("Ensure replaced a user-owned launcher path")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "user data" {
		t.Fatalf("user-owned launcher path changed: %q / %v", data, err)
	}
}
