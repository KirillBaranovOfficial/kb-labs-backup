// Package shim manages the kb command alongside the kb-create launcher.
package shim

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

func Ensure(launcher, platformRoot string) error {
	path, target := Path(launcher), Target(platformRoot)
	cliEntry := filepath.Join(platformRoot, "node_modules", "@kb-labs", "cli-bin", "dist", "bin.js")
	if _, err := os.Stat(cliEntry); err != nil {
		return failed(cliEntry, "cannot read the installed kb command", err)
	}
	if existing, err := os.Readlink(path); err == nil {
		if existing == target {
			return ensureTarget(target, cliEntry)
		}
		if !managedTarget(existing) {
			return failed(path, fmt.Sprintf("already points to unmanaged target %s", existing), nil)
		}
		if err := ensureTarget(target, cliEntry); err != nil {
			return err
		}
		return replaceLink(path, target)
	} else if !os.IsNotExist(err) {
		return failed(path, "cannot inspect the launcher path", err)
	}
	if err := ensureTarget(target, cliEntry); err != nil {
		return err
	}
	if err := os.Symlink(target, path); err != nil {
		return failed(path, "cannot create the launcher symlink", err)
	}
	return nil
}

func managedTarget(target string) bool {
	root := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(target))))
	if Target(root) != target {
		return false
	}
	cliEntry := filepath.Join(root, "node_modules", "@kb-labs", "cli-bin", "dist", "bin.js")
	content, err := os.ReadFile(target)
	return err == nil && string(content) == shimContent(cliEntry)
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
	path, target := Path(launcher), Target(platformRoot)
	existing, err := os.Readlink(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if existing != target {
		return fmt.Errorf("refusing to remove %s because it points to %s", path, existing)
	}
	return os.Remove(path)
}

func Correct(launcher, platformRoot string) bool {
	target, err := os.Readlink(Path(launcher))
	return err == nil && target == Target(platformRoot)
}
