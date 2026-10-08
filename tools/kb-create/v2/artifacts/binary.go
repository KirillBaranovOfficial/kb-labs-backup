package artifacts

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/kb-labs/create/v2/contracts"
)

// Binaries installs immutable platform tools after verifying their release
// checksum. Files are private to the V2 platform root, never global PATH.
type Binaries struct {
	Root    string
	Offline bool
	Client  *http.Client
}

func (b Binaries) Install(items []contracts.Artifact) error {
	for _, item := range items {
		if !item.IsBinary() {
			continue
		}
		if item.URL == "" || item.SHA256 == "" || item.Target == "" {
			return fmt.Errorf("binary %q lacks url, hash or target", item.ID)
		}
		data, err := b.download(item)
		if err != nil {
			return err
		}
		sum := fmt.Sprintf("%x", sha256.Sum256(data))
		if sum != item.SHA256 {
			return fmt.Errorf("binary %s checksum mismatch", item.ID)
		}
		if !b.Offline {
			cachePath := filepath.Join(b.Root, ".kb", "v2", "cache", "binaries", item.SHA256)
			if err := os.MkdirAll(filepath.Dir(cachePath), 0o750); err != nil {
				return err
			}
			if err := os.WriteFile(cachePath, data, 0o600); err != nil {
				return err
			}
		}
		path := filepath.Join(b.Root, ".kb", "v2", "bin", filepath.Base(item.Target))
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			return err
		}
		if err := os.WriteFile(path+".tmp", data, 0o700); err != nil {
			return err
		}
		if err := os.Rename(path+".tmp", path); err != nil {
			return err
		}
	}
	return nil
}

func (b Binaries) download(item contracts.Artifact) ([]byte, error) {
	if b.Offline {
		path := filepath.Join(b.Root, ".kb", "v2", "cache", "binaries", item.SHA256)
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("offline binary %s is absent from V2 cache: %w", item.ID, err)
		}
		return data, nil
	}
	client := b.Client
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Get(item.URL)
	if err != nil {
		return nil, fmt.Errorf("download binary %s: %w", item.ID, err)
	}
	data, readErr := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if readErr != nil || response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("download binary %s: status %d: %v", item.ID, response.StatusCode, readErr)
	}
	return data, nil
}

func (b Binaries) Remove(items []contracts.Artifact) error {
	for _, item := range items {
		if !item.IsBinary() {
			continue
		}
		path := filepath.Join(b.Root, ".kb", "v2", "bin", filepath.Base(item.Target))
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

type Composite struct {
	Packages Pnpm
	Binaries Binaries
}

func (c Composite) Install(items []contracts.Artifact) error {
	return c.InstallWithProgress(items, nil)
}

// InstallWithProgress exposes the real download/verification and package
// installation boundaries to the shared apply engine's frontends.
func (c Composite) InstallWithProgress(items []contracts.Artifact, progress func(string, bool) error) error {
	if progress != nil {
		if err := progress("Download and verify artifacts", true); err != nil {
			return fmt.Errorf("report artifact progress: %w", err)
		}
	}
	// A binary is small and independently hash-verifiable, so install it first.
	// If pnpm then fails, remove V2-owned binaries immediately; a later update
	// additionally restores the pre-mutation snapshot and package lock state.
	if err := c.Binaries.Install(items); err != nil {
		if progress != nil {
			if progressErr := progress("Download and verify artifacts", false); progressErr != nil {
				return errors.Join(err, fmt.Errorf("report artifact progress: %w", progressErr))
			}
		}
		return err
	}
	if progress != nil {
		if err := progress("Download and verify artifacts", false); err != nil {
			removeErr := c.Binaries.Remove(items)
			return errors.Join(fmt.Errorf("report artifact progress: %w", err), removeErr)
		}
		if err := progress("Install packages", true); err != nil {
			removeErr := c.Binaries.Remove(items)
			return errors.Join(fmt.Errorf("report artifact progress: %w", err), removeErr)
		}
	}
	if err := c.Packages.Install(items); err != nil {
		if progress != nil {
			if progressErr := progress("Install packages", false); progressErr != nil {
				err = errors.Join(err, fmt.Errorf("report artifact progress: %w", progressErr))
			}
		}
		if removeErr := c.Binaries.Remove(items); removeErr != nil {
			return fmt.Errorf("install packages: %w; remove staged binaries: %v", err, removeErr)
		}
		return err
	}
	if progress != nil {
		if err := progress("Install packages", false); err != nil {
			return fmt.Errorf("report artifact progress: %w", err)
		}
	}
	return nil
}
func (c Composite) Restore(items []contracts.Artifact) error { return c.Packages.Restore(items) }
func (c Composite) Uninstall(items []contracts.Artifact) error {
	if err := c.Packages.Uninstall(items); err != nil {
		return err
	}
	return c.Binaries.Remove(items)
}
