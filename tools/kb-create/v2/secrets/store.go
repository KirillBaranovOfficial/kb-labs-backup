// Package secrets owns V2's local secret boundary. Values are private,
// atomic, and deliberately absent from plans, receipts, scenario state, logs,
// and diagnostic dossiers.
package secrets

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/kb-labs/create/v2/contracts"
	"github.com/kb-labs/create/v2/versionroot"
)

var ErrBootstrapPasswordDisclosed = errors.New("bootstrap password has already been disclosed")

// EnsureGenerated fills every selected artifact's generated secret requirements
// with a fresh per-install value unless one already exists. Generated secrets
// are installer-owned and do not require a user secret input. Values stay in the
// private store and are never added to the plan or receipt.
func EnsureGenerated(plan *contracts.ResolvedInstallPlan, store Store) error {
	bootstrapCompleted, err := store.Exists(BootstrapCompleted)
	if err != nil {
		return fmt.Errorf("check first-administrator bootstrap completion: %w", err)
	}
	for _, artifact := range plan.Artifacts {
		for _, requirement := range artifact.Config {
			if !requirement.Secret || !requirement.Generate {
				continue
			}
			if bootstrapCompleted && requirement.ID == BootstrapPassword {
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
			var storeErr error
			if requirement.ID == BootstrapPassword {
				storeErr = store.PutBootstrapPassword(hex.EncodeToString(random), BootstrapOriginGenerated)
			} else {
				storeErr = store.Put(requirement.ID, hex.EncodeToString(random))
			}
			if storeErr != nil {
				return fmt.Errorf("store generated secret %s: %w", requirement.ID, storeErr)
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
	BootstrapPassword            = "gateway.bootstrap.password"
	BootstrapCompleted           = "gateway.bootstrap.completed"
	BootstrapGenerated           = "gateway.bootstrap.generated"
	BootstrapDisclosed           = "gateway.bootstrap.disclosed"
	BootstrapPasswordEnvironment = "GATEWAY_BOOTSTRAP_ADMIN_PASSWORD"
)

type BootstrapPasswordOrigin string

const (
	BootstrapOriginSupplied  BootstrapPasswordOrigin = "false"
	BootstrapOriginGenerated BootstrapPasswordOrigin = "true"
)

type Environment struct {
	Values     map[string]string
	Suppressed []string
}

func (store Store) Put(name, value string) error {
	if name == BootstrapPassword {
		return store.PutBootstrapPassword(value, BootstrapOriginSupplied)
	}
	return store.put(name, value)
}

// PutBootstrapPassword stores the password and its origin in one atomic secret
// store write. This is the only boundary that records whether a result may
// disclose the value.
func (store Store) PutBootstrapPassword(value string, origin BootstrapPasswordOrigin) error {
	if store.ReadOnly {
		return fmt.Errorf("secret store is read-only during staged update verification")
	}
	if value == "" {
		return fmt.Errorf("secret %q is empty", BootstrapPassword)
	}
	if err := validate(BootstrapPassword); err != nil {
		return err
	}
	if origin != BootstrapOriginGenerated && origin != BootstrapOriginSupplied {
		return errors.New("bootstrap password origin is invalid")
	}
	disclosed, err := store.Exists(BootstrapDisclosed)
	if err != nil {
		return err
	}
	if disclosed {
		return ErrBootstrapPasswordDisclosed
	}
	values, err := store.readRaw()
	if err != nil {
		return err
	}
	values[BootstrapPassword] = value
	values[BootstrapGenerated] = string(origin)
	return store.write(values)
}

func (store Store) put(name, value string) error {
	if store.ReadOnly {
		return fmt.Errorf("secret store is read-only during staged update verification")
	}
	if err := validate(name); err != nil {
		return err
	}
	if name == BootstrapDisclosed {
		return errors.New("bootstrap disclosure must atomically erase the password")
	}
	if name == BootstrapPassword || name == BootstrapPasswordEnvironment {
		disclosed, err := store.Exists(BootstrapDisclosed)
		if err != nil {
			return err
		}
		if disclosed {
			return ErrBootstrapPasswordDisclosed
		}
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
	return store.write(values)
}

// MarkBootstrapDisclosed atomically records disclosure and removes every
// stored representation of the one-time bootstrap password. Both values are
// in the shared runtime secret file, so an interrupted write leaves either
// the undisclosed password or the committed marker without the password.
func (store Store) MarkBootstrapDisclosed() error {
	if store.ReadOnly {
		return fmt.Errorf("secret store is read-only during staged update verification")
	}
	values, err := store.readRaw()
	if err != nil {
		return err
	}
	return store.commitBootstrapDisclosure(values)
}

func (store Store) commitBootstrapDisclosure(values map[string]string) error {
	values[BootstrapDisclosed] = "true"
	delete(values, BootstrapPassword)
	delete(values, BootstrapPasswordEnvironment)
	return store.write(values)
}

func (store Store) write(values map[string]string) error {
	path, err := store.path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
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
			return errors.New("secrets bound to the same environment variable hold different values")
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
func (store Store) Environment() (Environment, error) {
	values, err := store.read()
	if err != nil {
		return Environment{}, err
	}
	completed, err := store.Exists(BootstrapCompleted)
	if err != nil {
		return Environment{}, err
	}
	result := make(map[string]string, len(values))
	for name, value := range values {
		if environmentName.MatchString(name) {
			result[name] = value
		}
	}
	environment := Environment{Values: result}
	if completed {
		delete(environment.Values, BootstrapPasswordEnvironment)
		environment.Suppressed = []string{BootstrapPasswordEnvironment}
	}
	return environment, nil
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
	values, err := store.readRaw()
	if err != nil {
		return nil, err
	}
	if values[BootstrapDisclosed] == "true" && (values[BootstrapPassword] != "" || values[BootstrapPasswordEnvironment] != "") {
		if err := store.commitBootstrapDisclosure(values); err != nil {
			return nil, err
		}
		return store.readRaw()
	}
	return values, nil
}

func (store Store) readRaw() (map[string]string, error) {
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
