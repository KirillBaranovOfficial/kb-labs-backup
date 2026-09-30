// Package shim manages the kb command alongside the kb-create launcher.
package shim

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func Path(launcher string) string { return filepath.Join(filepath.Dir(launcher), "kb") }

func Target(platformRoot string) string {
	return filepath.Join(platformRoot, ".kb", "v2", "bin", "kb")
}

func Ensure(launcher, platformRoot string) error {
	path, target := Path(launcher), Target(platformRoot)
	cliEntry := filepath.Join(platformRoot, "node_modules", "@kb-labs", "cli-bin", "dist", "bin.js")
	if _, err := os.Stat(cliEntry); err != nil {
		return fmt.Errorf("installed kb command is missing at %s: %w", cliEntry, err)
	}
	if existing, err := os.Readlink(path); err == nil {
		if existing == target {
			return ensureTarget(target, cliEntry)
		}
		return fmt.Errorf("%s already points to %s", path, existing)
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := ensureTarget(target, cliEntry); err != nil {
		return err
	}
	return os.Symlink(target, path)
}

func ensureTarget(target, cliEntry string) error {
	content := []byte("#!/bin/sh\nexec node " + shellQuote(cliEntry) + " \"$@\"\n")
	if existing, err := os.ReadFile(target); err == nil {
		if string(existing) != string(content) {
			return fmt.Errorf("refusing to overwrite unexpected kb launcher at %s", target)
		}
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
		return err
	}
	return os.WriteFile(target, content, 0o750)
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
