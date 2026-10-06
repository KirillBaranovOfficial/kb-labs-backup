package shim

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/kb-labs/create/v2/contracts"
	"github.com/kb-labs/create/v2/receipt"
)

func installFixture(t *testing.T, root string) {
	t.Helper()
	cliEntry := filepath.Join(root, "node_modules", "@kb-labs", "cli-bin", "dist", "bin.js")
	if err := os.MkdirAll(filepath.Dir(cliEntry), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cliEntry, []byte("process.stdout.write('KB Labs\\n')\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := receipt.Write(root, contracts.InstallReceipt{Schema: contracts.ReceiptSchema}); err != nil {
		t.Fatal(err)
	}
}

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
	if _, err := Ensure(launcher, platformRoot); err != nil || !Correct(launcher, platformRoot) {
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

func TestRemoveLeavesUnownedShimPointingElsewhere(t *testing.T) {
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
	if err := Remove(launcher, filepath.Join(root, "platform")); err != nil {
		t.Fatalf("uninstall failed for a shim owned by another target: %v", err)
	}
	if got := mustReadlink(t, path); got != "/elsewhere/kb" {
		t.Fatalf("unowned shim changed to %s", got)
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
	installFixture(t, firstRoot)
	installFixture(t, secondRoot)
	if _, err := Ensure(launcher, firstRoot); err != nil {
		t.Fatal(err)
	}
	result, err := Ensure(launcher, secondRoot)
	if err != nil {
		t.Fatalf("Ensure rejected a live managed owner: %v", err)
	}
	if result.Linked || result.Owner != firstRoot || !Correct(launcher, firstRoot) {
		t.Fatalf("Ensure changed the live owner: result=%+v target=%s", result, mustReadlink(t, Path(launcher)))
	}
	if err := receipt.Delete(firstRoot); err != nil {
		t.Fatal(err)
	}
	result, err = Ensure(launcher, secondRoot)
	if err != nil || !result.Linked || result.Owner != secondRoot || !Correct(launcher, secondRoot) {
		t.Fatalf("Ensure did not take a dangling managed shim: result=%+v error=%v", result, err)
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
	installFixture(t, platformRoot)
	if _, err := Ensure(launcher, platformRoot); err == nil {
		t.Fatal("Ensure replaced a user-owned launcher path")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "user data" {
		t.Fatalf("user-owned launcher path changed: %q / %v", data, err)
	}
}

func TestShimOwnershipMatrix(t *testing.T) {
	for _, operation := range []string{"apply", "doctor --fix"} {
		for _, state := range []string{"absent", "own", "live-other", "dangling-other", "unmanaged"} {
			t.Run(operation+"/"+state, func(t *testing.T) {
				root := t.TempDir()
				launcherDir := filepath.Join(root, "bin")
				if err := os.MkdirAll(launcherDir, 0o750); err != nil {
					t.Fatal(err)
				}
				launcher := filepath.Join(launcherDir, "kb-create")
				owner := filepath.Join(root, "owner")
				other := filepath.Join(root, "other")
				installFixture(t, owner)
				installFixture(t, other)
				path := Path(launcher)
				switch state {
				case "own":
					if _, err := Ensure(launcher, owner); err != nil {
						t.Fatal(err)
					}
				case "live-other":
					if _, err := Ensure(launcher, other); err != nil {
						t.Fatal(err)
					}
				case "dangling-other":
					if _, err := Ensure(launcher, other); err != nil {
						t.Fatal(err)
					}
					if err := receipt.Delete(other); err != nil {
						t.Fatal(err)
					}
				case "unmanaged":
					if err := os.WriteFile(path, []byte("user file"), 0o600); err != nil {
						t.Fatal(err)
					}
				}

				result, err := Ensure(launcher, owner)
				if state == "unmanaged" {
					if err == nil {
						t.Fatal("apply replaced an unmanaged user file")
					}
					data, readErr := os.ReadFile(path)
					if readErr != nil || string(data) != "user file" {
						t.Fatalf("unmanaged file changed: %q %v", data, readErr)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				wantOwner, wantLinked := owner, true
				if state == "live-other" {
					wantOwner, wantLinked = other, false
				}
				if state == "own" {
					wantLinked = true
				}
				if state == "absent" || state == "dangling-other" {
					wantLinked = true
				}
				if result.Owner != wantOwner || result.Linked != wantLinked || !Correct(launcher, wantOwner) {
					t.Fatalf("result=%+v, target=%s; want owner=%s linked=%t", result, mustReadlink(t, path), wantOwner, wantLinked)
				}
			})
		}
	}

	for _, state := range []string{"absent", "own", "live-other", "dangling-other", "unmanaged"} {
		t.Run("uninstall/"+state, func(t *testing.T) {
			root := t.TempDir()
			launcherDir := filepath.Join(root, "bin")
			if err := os.MkdirAll(launcherDir, 0o750); err != nil {
				t.Fatal(err)
			}
			launcher := filepath.Join(launcherDir, "kb-create")
			owner := filepath.Join(root, "owner")
			other := filepath.Join(root, "other")
			installFixture(t, owner)
			installFixture(t, other)
			path := Path(launcher)
			switch state {
			case "own":
				if _, err := Ensure(launcher, owner); err != nil {
					t.Fatal(err)
				}
			case "live-other":
				if _, err := Ensure(launcher, other); err != nil {
					t.Fatal(err)
				}
			case "dangling-other":
				if _, err := Ensure(launcher, other); err != nil {
					t.Fatal(err)
				}
				if err := receipt.Delete(other); err != nil {
					t.Fatal(err)
				}
			case "unmanaged":
				if err := os.WriteFile(path, []byte("user file"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if err := Remove(launcher, owner); err != nil {
				t.Fatal(err)
			}
			switch state {
			case "own":
				if _, err := os.Lstat(path); !os.IsNotExist(err) {
					t.Fatalf("own shim remains: %v", err)
				}
			case "live-other", "dangling-other":
				if got := mustReadlink(t, path); got != Target(other) {
					t.Fatalf("other root shim changed to %s", got)
				}
			case "unmanaged":
				data, err := os.ReadFile(path)
				if err != nil || string(data) != "user file" {
					t.Fatalf("unmanaged file changed: %q %v", data, err)
				}
			case "absent":
				if _, err := os.Lstat(path); !os.IsNotExist(err) {
					t.Fatalf("unexpected shim: %v", err)
				}
			}
		})
	}
}

func mustReadlink(t *testing.T, path string) string {
	t.Helper()
	target, err := os.Readlink(path)
	if err != nil {
		t.Fatal(err)
	}
	return target
}
