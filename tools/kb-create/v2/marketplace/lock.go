// Package marketplace writes .kb/marketplace.lock after Apply installs exact
// artifacts. core-discovery (the plugin registry consumed by REST, gateway,
// workflow and oauth at runtime) reads that lock file to know which packages
// to scan — without it, every installed plugin/adapter is physically on disk
// but invisible to the running platform.
//
// The pre-V2 launcher wrote this file via a Node.js node_modules scanner
// (internal/scan, removed in the breaking cutover). V2 does not need to
// scan anything: contracts.Artifact already carries the exact package/kind
// resolved from the sealed release index, so the lock can be derived
// directly from the plan that was just applied.
package marketplace

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/kb-labs/create/v2/contracts"
)

// Schema must match core-discovery's MarketplaceLock schema
// (core/discovery/src/marketplace-lock.ts) — the TS reader rejects any
// other value outright.
const Schema = "kb.marketplace/3"

type Lock struct {
	Schema   string          `json:"schema"`
	Plugins  map[string]Item `json:"plugins"`
	Adapters map[string]Item `json:"adapters,omitempty"`
}

type Item struct {
	Version      string `json:"version"`
	Integrity    string `json:"integrity"`
	ResolvedPath string `json:"resolvedPath"`
	Package      string `json:"package,omitempty"`
	// Source identifies machine-installed platform artifacts.
	Source  string `json:"source"`
	Enabled bool   `json:"enabled"`
}

// WriteLock regenerates .kb/marketplace.lock at platformRoot from the exact
// artifacts a resolved plan installed. Only "plugin" and "adapter" kinds are
// discoverable registry entities; "platform" and "binary" artifacts are
// skipped, matching what the pre-cutover scanner reported.
//
// The platform lock is machine-local: it records its platform-relative path.
// Integrity matches core-discovery's package.json hash contract, not the
// artifact tarball digest in the resolved plan.
func WriteLock(platformRoot string, artifacts []contracts.Artifact, now time.Time) error {
	existing, err := readLock(platformRoot)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read existing marketplace lock: %w", err)
	}

	lock := Lock{Schema: Schema, Plugins: make(map[string]Item), Adapters: make(map[string]Item)}
	_ = now // retained for call-site compatibility; lock declarations have no timestamp.
	for _, a := range artifacts {
		kind := a.Kind
		if a.Registry != "" {
			kind = a.Registry
		}
		if kind != "plugin" && kind != "adapter" {
			continue
		}
		integrity, err := packageIntegrity(platformRoot, a.Package)
		if err != nil {
			return fmt.Errorf("read installed package metadata for %s: %w", a.Package, err)
		}
		enabled := true
		if existing != nil {
			prior, ok := existing.Plugins[a.ID]
			if kind == "adapter" {
				prior, ok = existing.Adapters[a.ID]
			}
			if ok {
				enabled = prior.Enabled
			}
		}
		item := Item{
			Version:      a.Version,
			Integrity:    integrity,
			ResolvedPath: "node_modules/" + a.Package,
			Source:       "platform",
			Enabled:      enabled,
		}
		if kind == "adapter" {
			lock.Adapters[a.ID] = item
		} else {
			item.Package = a.Package
			lock.Plugins[a.ID] = item
		}
	}

	if len(lock.Plugins) == 0 && len(lock.Adapters) == 0 {
		return nil
	}
	if len(lock.Adapters) == 0 {
		lock.Adapters = nil
	}

	kbDir := filepath.Join(platformRoot, ".kb")
	if err := os.MkdirAll(kbDir, 0o750); err != nil {
		return err
	}
	data, err := json.MarshalIndent(lock, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	target := filepath.Join(kbDir, "marketplace.lock")
	tmp := target + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, target)
}

func packageIntegrity(platformRoot, packageName string) (string, error) {
	if packageName == "" {
		return "", fmt.Errorf("package name is required")
	}
	packageJSON, err := os.ReadFile(filepath.Join(platformRoot, "node_modules", filepath.FromSlash(packageName), "package.json"))
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(packageJSON)
	return "sha256-" + base64.StdEncoding.EncodeToString(digest[:]), nil
}

func readLock(platformRoot string) (*Lock, error) {
	// #nosec G304 -- platformRoot is caller-controlled, not user input
	data, err := os.ReadFile(filepath.Join(platformRoot, ".kb", "marketplace.lock"))
	if err != nil {
		return nil, err
	}
	var lock Lock
	if err := json.Unmarshal(data, &lock); err != nil {
		return nil, err
	}
	if lock.Schema != Schema {
		return nil, fmt.Errorf("invalid marketplace lock schema %q", lock.Schema)
	}
	for id, entry := range lock.Plugins {
		if entry.Source != "platform" || entry.Package == "" {
			return nil, fmt.Errorf("invalid marketplace lock plugin %q: platform entries require a package", id)
		}
	}
	return &lock, nil
}
