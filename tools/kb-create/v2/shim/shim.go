// Package shim manages the kb command alongside the kb-create launcher.
package shim

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kb-labs/create/v2/versionroot"
)

// Failure identifies the filesystem operation that prevented shim creation.
type Failure struct {
	Path   string
	Reason string
	Cause  error
}

func (e *Failure) Error() string { return fmt.Sprintf("%s: %s", e.Path, e.Reason) }
func (e *Failure) Unwrap() error { return e.Cause }

func failed(path, reason string, cause error) error {
	return &Failure{Path: path, Reason: reason, Cause: cause}
}

func Path(launcher string) string { return filepath.Join(filepath.Dir(launcher), "kb") }

func Target(platformRoot string) string {
	return filepath.Join(platformRoot, ".kb", "v2", "bin", "kb")
}

type EnsureResult struct {
	Linked bool   `json:"linked"`
	Owner  string `json:"owner"`
}

func Ensure(launcher, platformRoot string) (EnsureResult, error) {
	path := Path(launcher)
	requested, err := installation(platformRoot)
	if err != nil {
		return EnsureResult{}, failed(path, "cannot verify the installation that should own the kb command", err)
	}
	target := Target(requested.platformRoot)
	cliEntry := filepath.Join(requested.platformRoot, "node_modules", "@kb-labs", "cli-bin", "dist", "bin.js")
	if _, err := os.Stat(cliEntry); err != nil {
		return EnsureResult{}, failed(cliEntry, "cannot read the installed kb command", err)
	}
	if existing, err := os.Readlink(path); err == nil {
		owner, valid := installationForTarget(existing)
		if !valid {
			return EnsureResult{}, failed(path, fmt.Sprintf("already points to an unverified target %s; inspect it and remove it with %s before installing the kb command", existing, RemoveCommand(path)), nil)
		}
		if owner.id != requested.id {
			if err := ensureTarget(target, cliEntry); err != nil {
				return EnsureResult{}, err
			}
			return EnsureResult{Linked: false, Owner: owner.root}, nil
		}
		if err := ensureTarget(target, cliEntry); err != nil {
			return EnsureResult{}, err
		}
		if existing != target {
			if err := replaceLink(path, target); err != nil {
				return EnsureResult{}, err
			}
		}
		return EnsureResult{Linked: true, Owner: requested.root}, nil
	} else if !os.IsNotExist(err) {
		return EnsureResult{}, failed(path, "cannot inspect the launcher path", err)
	}
	if err := ensureTarget(target, cliEntry); err != nil {
		return EnsureResult{}, err
	}
	if err := os.Symlink(target, path); err != nil {
		return EnsureResult{}, failed(path, "cannot create the launcher symlink", err)
	}
	return EnsureResult{Linked: true, Owner: requested.root}, nil
}

type installationOwner struct {
	id           string
	root         string
	platformRoot string
}

func installation(platformRoot string) (installationOwner, error) {
	layout, err := versionroot.ForPlatformPath(platformRoot)
	if err != nil {
		return installationOwner{}, err
	}
	if err := layout.Validate(); err != nil {
		return installationOwner{}, err
	}
	id, err := versionroot.InstallationIDForPath(platformRoot)
	if err != nil {
		return installationOwner{}, err
	}
	return installationOwner{id: id, root: layout.InstallRoot(), platformRoot: layout.PlatformRoot()}, nil
}

func installationForTarget(target string) (installationOwner, bool) {
	candidate, managed := managedRoot(target)
	if !managed {
		return installationOwner{}, false
	}
	owner, err := installation(candidate)
	return owner, err == nil
}

func managedRoot(target string) (string, bool) {
	clean := filepath.Clean(target)
	root := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(clean))))
	if root == "." || Target(root) != clean {
		return "", false
	}
	return root, true
}

func Dangling(launcher string) (string, bool) {
	target, err := os.Readlink(Path(launcher))
	if err != nil {
		return "", false
	}
	_, managed := managedRoot(target)
	if !managed {
		return "", false
	}
	if _, valid := installationForTarget(target); valid {
		return "", false
	}
	return target, true
}

func replaceLink(path, target string) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".kb-shim-*")
	if err != nil {
		return failed(path, "cannot prepare an atomic launcher update", err)
	}
	temporary := file.Name()
	if err := file.Close(); err != nil {
		_ = os.Remove(temporary)
		return failed(path, "cannot prepare an atomic launcher update", err)
	}
	if err := os.Remove(temporary); err != nil {
		return failed(path, "cannot prepare an atomic launcher update", err)
	}
	if err := os.Symlink(target, temporary); err != nil {
		return failed(path, "cannot create an atomic launcher update", err)
	}
	defer os.Remove(temporary)
	if err := os.Rename(temporary, path); err != nil {
		return failed(path, "cannot replace the managed launcher symlink", err)
	}
	return nil
}

func ensureTarget(target, cliEntry string) error {
	content := []byte(shimContent(cliEntry))
	if existing, err := os.ReadFile(target); err == nil {
		if string(existing) != string(content) {
			return failed(target, "refusing to overwrite an unexpected kb launcher", nil)
		}
		return nil
	} else if !os.IsNotExist(err) {
		return failed(target, "cannot inspect the kb launcher target", err)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
		return failed(filepath.Dir(target), "cannot create the kb launcher directory", err)
	}
	if err := os.WriteFile(target, content, 0o750); err != nil {
		return failed(target, "cannot write the kb launcher target", err)
	}
	return nil
}

func shimContent(cliEntry string) string {
	return "#!/bin/sh\nexec node " + shellQuote(cliEntry) + " \"$@\"\n"
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func Remove(launcher, platformRoot string) error {
	token, owned, err := PrepareRemoval(launcher, platformRoot)
	if err != nil || !owned {
		return err
	}
	return token.Remove(launcher)
}

type Removal struct {
	target string
	id     string
}

func PrepareRemoval(launcher, platformRoot string) (Removal, bool, error) {
	path := Path(launcher)
	existing, err := os.Readlink(path)
	if os.IsNotExist(err) {
		return Removal{}, false, nil
	}
	if err != nil {
		info, statErr := os.Lstat(path)
		if statErr == nil && info.Mode()&os.ModeSymlink == 0 {
			return Removal{}, false, nil
		}
		return Removal{}, false, err
	}
	requested, err := installation(platformRoot)
	if err != nil {
		return Removal{}, false, nil
	}
	owner, valid := installationForTarget(existing)
	if !valid || owner.id != requested.id {
		return Removal{}, false, nil
	}
	return Removal{target: existing, id: owner.id}, true, nil
}

func (r Removal) Remove(launcher string) error {
	path := Path(launcher)
	existing, err := os.Readlink(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if existing != r.target || r.id == "" {
		return nil
	}
	return os.Remove(path)
}

func Correct(launcher, platformRoot string) bool {
	requested, err := installation(platformRoot)
	if err != nil {
		return false
	}
	target, err := os.Readlink(Path(launcher))
	if err != nil {
		return false
	}
	owner, valid := installationForTarget(target)
	return valid && owner.id == requested.id
}

// Owner returns the installed platform root currently named by the managed kb
// shim. It does not infer owners from unmanaged files or dangling symlinks.
func Owner(launcher string) (string, bool) {
	target, err := os.Readlink(Path(launcher))
	if err != nil {
		return "", false
	}
	owner, valid := installationForTarget(target)
	if !valid {
		return "", false
	}
	return owner.root, true
}

func RemoveCommand(path string) string { return "`rm -- " + shellQuote(path) + "`" }
