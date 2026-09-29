// Package shim manages the kb command alongside the kb-create launcher.
package shim

import (
	"fmt"
	"os"
	"path/filepath"
)

func Path(launcher string) string { return filepath.Join(filepath.Dir(launcher), "kb") }

func Target(platformRoot string) string {
	return filepath.Join(platformRoot, "node_modules", ".bin", "kb")
}

func Ensure(launcher, platformRoot string) error {
	path, target := Path(launcher), Target(platformRoot)
	if _, err := os.Stat(target); err != nil {
		return fmt.Errorf("installed kb command is missing at %s: %w", target, err)
	}
	if existing, err := os.Readlink(path); err == nil {
		if existing == target {
			return nil
		}
		return fmt.Errorf("%s already points to %s", path, existing)
	} else if !os.IsNotExist(err) {
		return err
	}
	return os.Symlink(target, path)
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
