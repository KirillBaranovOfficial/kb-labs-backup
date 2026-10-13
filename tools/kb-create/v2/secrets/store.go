// Package secrets owns V2's local secret boundary. Values are private,
// atomic, and deliberately absent from plans, receipts, scenario state, logs,
// and diagnostic dossiers.
package secrets

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/kb-labs/create/v2/contracts"
	"github.com/kb-labs/create/v2/versionroot"
)

var (
	ErrBootstrapPasswordDisclosed   = errors.New("bootstrap password has already been disclosed")
	ErrUnsupportedSecretStoreFormat = errors.New("secret store format predates the typed bootstrap lifecycle record")
)

// EnsureGenerated fills every selected artifact's generated secret requirements
// with a fresh per-install value unless one already exists. Generated secrets
// are installer-owned and do not require a user secret input. Values stay in the
// private store and are never added to the plan or receipt.
func EnsureGenerated(plan *contracts.ResolvedInstallPlan, store Store) error {
	bootstrapCompleted, err := store.BootstrapCompleted()
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
	BootstrapPasswordEnvironment = "GATEWAY_BOOTSTRAP_ADMIN_PASSWORD"
)

// lifecycleRecord is installer-owned metadata. It is persisted separately from
// the operator-addressable requirement map and has no generic Put entry point.
type lifecycleRecord struct {
	BootstrapCompleted         string                  `json:"bootstrapCompleted,omitempty"`
	BootstrapPasswordOrigin    BootstrapPasswordOrigin `json:"bootstrapPasswordOrigin,omitempty"`
	BootstrapPasswordDisclosed bool                    `json:"bootstrapPasswordDisclosed,omitempty"`
}

type BootstrapPasswordOrigin string

// PendingSecret is a value gathered by a frontend but not yet persisted. The
// apply owner stores it only after validating the complete resolved request.
type PendingSecret struct {
	Value  string
	Origin BootstrapPasswordOrigin
}

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

// PutBootstrapPassword stores the value and its origin in the installer-owned
// lifecycle record. A missing origin fails closed as supplied.
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
	state, err := store.readLifecycle()
	if err != nil {
		return err
	}
	if state.BootstrapPasswordDisclosed {
		return ErrBootstrapPasswordDisclosed
	}
	values, err := store.readRaw()
	if err != nil {
		return err
	}
	values[BootstrapPassword] = value
	state.BootstrapPasswordOrigin = origin
	if err := store.ensureLifecycleRecord(); err != nil {
		return err
	}
	if err := store.write(values); err != nil {
		return err
	}
	return store.writeLifecycle(state)
}

func (store Store) put(name, value string) error {
	if store.ReadOnly {
		return fmt.Errorf("secret store is read-only during staged update verification")
	}
	if err := validate(name); err != nil {
		return err
	}
	if name == BootstrapPassword || name == BootstrapPasswordEnvironment {
		state, err := store.readLifecycle()
		if err != nil {
			return err
		}
		if state.BootstrapPasswordDisclosed {
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
	if err := store.ensureLifecycleRecord(); err != nil {
		return err
	}
	return store.write(values)
}

// MarkBootstrapDisclosed is the sole disclosure transition. It commits the
// lifecycle marker, then erases both stored password names. Store reads finish
// an interrupted erase before returning any secret values.
func (store Store) MarkBootstrapDisclosed() error {
	if store.ReadOnly {
		return fmt.Errorf("secret store is read-only during staged update verification")
	}
	values, err := store.readRaw()
	if err != nil {
		return err
	}
	state, err := store.readLifecycle()
	if err != nil {
		return err
	}
	state.BootstrapPasswordDisclosed = true
	if err := store.writeLifecycle(state); err != nil {
		return err
	}
	delete(values, BootstrapPassword)
	delete(values, BootstrapPasswordEnvironment)
	return store.write(values)
}

// MarkBootstrapCompleted records the Gateway's proven first-admin readiness.
func (store Store) MarkBootstrapCompleted(value string) error {
	if store.ReadOnly {
		return fmt.Errorf("secret store is read-only during staged update verification")
	}
	state, err := store.readLifecycle()
	if err != nil {
		return err
	}
	state.BootstrapCompleted = value
	return store.writeLifecycle(state)
}

func (store Store) BootstrapCompleted() (bool, error) {
	state, err := store.readLifecycle()
	return state.BootstrapCompleted != "", err
}

func (store Store) BootstrapCompletedValue() (string, bool, error) {
	state, err := store.readLifecycle()
	return state.BootstrapCompleted, state.BootstrapCompleted != "", err
}

func (store Store) BootstrapDisclosed() (bool, error) {
	state, err := store.readLifecycle()
	return state.BootstrapPasswordDisclosed, err
}

func (store Store) BootstrapOrigin() (BootstrapPasswordOrigin, bool, error) {
	state, err := store.readLifecycle()
	return state.BootstrapPasswordOrigin, state.BootstrapPasswordOrigin != "", err
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

func (store Store) writeLifecycle(state lifecycleRecord) error {
	path, err := store.lifecyclePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return versionroot.AtomicWriteFile(path, data, 0o600)
}

func (store Store) ensureLifecycleRecord() error {
	path, err := store.lifecyclePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if os.IsExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = file.Write([]byte("{}"))
	return err
}

func (store Store) readLifecycle() (lifecycleRecord, error) {
	values, err := store.readRaw()
	if err != nil {
		return lifecycleRecord{}, err
	}
	path, err := store.lifecyclePath()
	if err != nil {
		return lifecycleRecord{}, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return lifecycleRecord{}, nil
	}
	if err != nil {
		return lifecycleRecord{}, err
	}
	var state lifecycleRecord
	if err := json.Unmarshal(data, &state); err != nil {
		return lifecycleRecord{}, fmt.Errorf("read typed bootstrap lifecycle record: %w", err)
	}
	if state.BootstrapPasswordDisclosed && (values[BootstrapPassword] != "" || values[BootstrapPasswordEnvironment] != "") {
		delete(values, BootstrapPassword)
		delete(values, BootstrapPasswordEnvironment)
		if err := store.write(values); err != nil {
			return lifecycleRecord{}, err
		}
	}
	return state, nil
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
	completed, err := store.BootstrapCompleted()
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
	if _, err := store.readLifecycle(); err != nil {
		return nil, err
	}
	values, err := store.readRaw()
	if err != nil {
		return nil, err
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
	lifecyclePath, pathErr := store.lifecyclePath()
	if pathErr != nil {
		return nil, pathErr
	}
	if _, statErr := os.Stat(lifecyclePath); os.IsNotExist(statErr) {
		return nil, ErrUnsupportedSecretStoreFormat
	} else if statErr != nil {
		return nil, statErr
	}
	return values, nil
}

func (store Store) lifecyclePath() (string, error) {
	layout, err := versionroot.ForPlatformPath(store.PlatformRoot)
	if err != nil {
		return "", err
	}
	sharedSecretPath, err := layout.SharedRuntimePath(".kb/v2/secrets.env")
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(sharedSecretPath), "bootstrap-lifecycle.json"), nil
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
