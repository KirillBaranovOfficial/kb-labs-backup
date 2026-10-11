// Package secrets owns V2's local secret boundary. Values are private,
// atomic, and deliberately absent from plans, receipts, scenario state, logs,
// and diagnostic dossiers.
package secrets

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/kb-labs/create/v2/contracts"
	"github.com/kb-labs/create/v2/versionroot"
)

// EnsureGenerated fills every selected artifact's generated secret requirements
// with a fresh per-install value unless one already exists. Generated secrets
// are installer-owned and do not require a user secret input. Values stay in the
// private store and are never added to the plan or receipt.
func EnsureGenerated(plan *contracts.ResolvedInstallPlan, store Store) error {
	for _, artifact := range plan.Artifacts {
		for _, requirement := range artifact.Config {
			if !requirement.Secret || !requirement.Generate {
				continue
			}
			exists, err := store.Exists(requirement.ID)
			if err != nil {
				return fmt.Errorf("check generated secret %s: %w", requirement.ID, err)
			}
			if exists {
				continue
			}
			random := make([]byte, 32)
			if _, err := rand.Read(random); err != nil {
				return fmt.Errorf("generate secret %s: %w", requirement.ID, err)
			}
			if err := store.Put(requirement.ID, hex.EncodeToString(random)); err != nil {
				return fmt.Errorf("store generated secret %s: %w", requirement.ID, err)
			}
		}
	}
	return nil
}

type Store struct {
	PlatformRoot string
	ReadOnly     bool
}

const (
	BootstrapCompleted = "gateway.bootstrap.completed"
	BootstrapGenerated = "gateway.bootstrap.generated"
	BootstrapDisclosed = "gateway.bootstrap.disclosed"
)

func (store Store) Put(name, value string) error {
	if store.ReadOnly {
		return fmt.Errorf("secret store is read-only during staged update verification")
	}
	if err := validate(name); err != nil {
		return err
	}
	if value == "" {
		return fmt.Errorf("secret %q is empty", name)
	}
	path, err := store.path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	values, err := store.read()
	if err != nil {
		return err
	}
	values[name] = value
	data := make([]string, 0, len(values))
	for key, item := range values {
		data = append(data, key+"="+item)
	}
	sortStrings(data)
	return versionroot.AtomicWriteFile(path, []byte(strings.Join(data, "\n")+"\n"), 0o600)
}
func (store Store) Exists(name string) (bool, error) {
	if err := validate(name); err != nil {
		return false, err
	}
	values, err := store.read()
	if err != nil {
		return false, err
	}
	return values[name] != "", nil
}

// Get returns a stored secret. The bool reports presence (an empty stored value
// counts as absent, matching Exists).
func (store Store) Get(name string) (string, bool, error) {
	if err := validate(name); err != nil {
		return "", false, err
	}
	values, err := store.read()
	if err != nil {
		return "", false, err
	}
	value := values[name]
	return value, value != "", nil
}

// BindEnvironments makes each stored secret reachable under the environment
// variable its plan patch binds it to.
//
// Secrets are stored under their requirement ID (what the launcher verifies and
// doctor checks), but the host process receives them as environment variables
// named by the plan (Environment). Without the second key any secret whose ID
// differs from its variable (`gateway.jwtSecret` vs `GATEWAY_JWT_SECRET`) is
// stored yet never delivered to the host. Only secrets that are present are
// bound: a missing required one is reported by the runtime. Two secrets bound to
// one variable must hold the same value. Safe to run repeatedly.
func (store Store) BindEnvironments(patches []contracts.ConfigPatch) error {
	delivered := map[string]string{}
	for _, patch := range patches {
		if patch.Environment == "" || !strings.HasPrefix(patch.Owner, "manifest:") {
			continue
		}
		id := strings.TrimPrefix(patch.Owner, "manifest:")
		if id == patch.Environment {
			continue
		}
		value, present, err := store.Get(id)
		if err != nil {
			return err
		}
		if !present {
			continue
		}
		if previous, seen := delivered[patch.Environment]; seen && previous != value {
			return fmt.Errorf("secrets bound to environment variable %q hold different values", patch.Environment)
		}
		delivered[patch.Environment] = value
		if existing, present, err := store.Get(patch.Environment); err != nil {
			return err
		} else if present && existing == value {
			continue
		}
		if err := store.Put(patch.Environment, value); err != nil {
			return err
		}
	}
	return nil
}

var environmentName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Environment returns the stored values that are valid environment variable
// names, for the host process. Requirement IDs such as "gateway.jwtSecret" are
// not variable names; each secret reaches the host under the variable its plan
// patch binds it to (see BindEnvironments).
func (store Store) Environment() (map[string]string, error) {
	values, err := store.read()
	if err != nil {
		return nil, err
	}
	result := make(map[string]string, len(values))
	for name, value := range values {
		if environmentName.MatchString(name) {
			result[name] = value
		}
	}
	return result, nil
}

func (store Store) path() (string, error) {
	marker := filepath.Join(store.PlatformRoot, ".kb", "v2", "platform-layout.json")
	if _, err := os.Stat(marker); err == nil {
		return filepath.Join(store.PlatformRoot, ".kb", "v2", "secrets.env"), nil
	} else if !os.IsNotExist(err) {
		return "", err
	}
	layout, err := versionroot.New(store.PlatformRoot)
	if err != nil {
		return "", err
	}
	return layout.SharedRuntimePath(".kb/v2/secrets.env")
}
func (store Store) read() (map[string]string, error) {
	path, err := store.path()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	values := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if ok {
			values[key] = value
		}
	}
	return values, nil
}
func validate(name string) error {
	if name == "" || strings.ContainsAny(name, "=\r\n") || strings.TrimSpace(name) != name {
		return fmt.Errorf("invalid secret name %q", name)
	}
	return nil
}
func sortStrings(values []string) {
	for i := range values {
		for j := i + 1; j < len(values); j++ {
			if values[j] < values[i] {
				values[i], values[j] = values[j], values[i]
			}
		}
	}
}
