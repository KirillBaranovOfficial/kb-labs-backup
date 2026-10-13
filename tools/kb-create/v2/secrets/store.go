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
	"strings"

	"github.com/kb-labs/clikit/secretstate"
	"github.com/kb-labs/create/v2/contracts"
	"github.com/kb-labs/create/v2/versionroot"
)

var (
	ErrBootstrapPasswordDisclosed   = errors.New("bootstrap password has already been disclosed")
	ErrBootstrapCredentialLocked    = errors.New("bootstrap credential cannot change after completion")
	ErrUnsupportedSecretStoreFormat = secretstate.ErrUnsupportedFormat
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
	atomicWrite  func(string, []byte, os.FileMode) error
}

const (
	BootstrapPassword            = secretstate.BootstrapPasswordRequirement
	BootstrapPasswordEnvironment = secretstate.BootstrapPasswordEnvironment
)

type secretStoreRecord = secretstate.Record
type BootstrapPasswordOrigin = secretstate.BootstrapPasswordOrigin

// PendingSecret is a value gathered by a frontend but not yet persisted. The
// apply owner stores it only after validating the complete resolved request.
type PendingSecret struct {
	Value  string
	Origin BootstrapPasswordOrigin
}

const (
	BootstrapOriginSupplied  BootstrapPasswordOrigin = secretstate.OriginSupplied
	BootstrapOriginGenerated BootstrapPasswordOrigin = secretstate.OriginGenerated
)

type Environment struct {
	Values     map[string]string
	Suppressed []string
}

func (store Store) Put(name, value string) error {
	if name == BootstrapPassword {
		return store.PutBootstrapPassword(value, BootstrapOriginSupplied)
	}
	if name == BootstrapPasswordEnvironment {
		record, err := store.readRecord()
		if err != nil {
			return err
		}
		origin, _ := record.BootstrapPasswordOrigin()
		if origin != BootstrapOriginGenerated && origin != BootstrapOriginSupplied {
			origin = BootstrapOriginSupplied
		}
		return store.PutBootstrapPassword(value, origin)
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
	record, err := store.readRecord()
	if err != nil {
		return err
	}
	current, _ := record.Requirement(BootstrapPassword)
	currentOrigin, _ := record.BootstrapPasswordOrigin()
	_, completed := record.BootstrapCompleted()
	if completed &&
		(current != value || currentOrigin != origin || record.BootstrapPasswordDisclosed()) {
		return ErrBootstrapCredentialLocked
	}
	if record.BootstrapPasswordDisclosed() {
		return ErrBootstrapPasswordDisclosed
	}
	environmentPassword, _ := record.Requirement(BootstrapPasswordEnvironment)
	if current == value && currentOrigin == origin && environmentPassword == value {
		return nil
	}
	record.SetBootstrapPassword(value, origin)
	return store.writeRecord(record)
}

func (store Store) put(name, value string) error {
	if store.ReadOnly {
		return fmt.Errorf("secret store is read-only during staged update verification")
	}
	if err := validate(name); err != nil {
		return err
	}
	if value == "" {
		return fmt.Errorf("secret %q is empty", name)
	}
	record, err := store.readRecord()
	if err != nil {
		return err
	}
	record.SetRequirement(name, value)
	return store.writeRecord(record)
}

// MarkBootstrapDisclosed atomically records handoff and removes both stored
// password names in the same state replacement.
func (store Store) MarkBootstrapDisclosed() error {
	if store.ReadOnly {
		return fmt.Errorf("secret store is read-only during staged update verification")
	}
	record, err := store.readRecord()
	if err != nil {
		return err
	}
	record.MarkBootstrapDisclosed()
	return store.writeRecord(record)
}

// MarkBootstrapCompleted records the Gateway's proven first-admin readiness.
func (store Store) MarkBootstrapCompleted(value string) error {
	if store.ReadOnly {
		return fmt.Errorf("secret store is read-only during staged update verification")
	}
	record, err := store.readRecord()
	if err != nil {
		return err
	}
	record.MarkBootstrapCompleted(value)
	return store.writeRecord(record)
}

func (store Store) BootstrapCompleted() (bool, error) {
	record, err := store.readRecord()
	_, completed := record.BootstrapCompleted()
	return completed, err
}

func (store Store) BootstrapCompletedValue() (string, bool, error) {
	record, err := store.readRecord()
	completed, present := record.BootstrapCompleted()
	return completed, present, err
}

func (store Store) BootstrapDisclosed() (bool, error) {
	record, err := store.readRecord()
	return record.BootstrapPasswordDisclosed(), err
}

func (store Store) BootstrapOrigin() (BootstrapPasswordOrigin, bool, error) {
	record, err := store.readRecord()
	origin, present := record.BootstrapPasswordOrigin()
	return origin, present, err
}

func (store Store) writeRecord(record secretStoreRecord) error {
	path, err := store.path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	data, err := secretstate.Encode(record)
	if err != nil {
		return err
	}
	if store.atomicWrite != nil {
		return store.atomicWrite(path, data, 0o600)
	}
	return versionroot.AtomicWriteFile(path, data, 0o600)
}

func (store Store) readRecord() (secretStoreRecord, error) {
	path, err := store.path()
	if err != nil {
		return secretStoreRecord{}, err
	}
	record, exists, err := secretstate.Load(path)
	if err != nil {
		return secretStoreRecord{}, err
	}
	if !exists {
		for _, oldPath := range store.previousStorePaths(path) {
			if _, statErr := os.Lstat(oldPath); statErr == nil || !os.IsNotExist(statErr) {
				return secretStoreRecord{}, ErrUnsupportedSecretStoreFormat
			}
		}
		return secretstate.NewRecord(), nil
	}
	return record, nil
}

func (store Store) previousStorePaths(path string) []string {
	dir := filepath.Dir(path)
	return []string{filepath.Join(dir, "secrets.env"), filepath.Join(dir, "bootstrap-lifecycle.json")}
}
func (store Store) Exists(name string) (bool, error) {
	if err := validate(name); err != nil {
		return false, err
	}
	record, err := store.readRecord()
	if err != nil {
		return false, err
	}
	value, _ := record.Requirement(name)
	return value != "", nil
}

// Get returns a stored secret. The bool reports presence (an empty stored value
// counts as absent, matching Exists).
func (store Store) Get(name string) (string, bool, error) {
	if err := validate(name); err != nil {
		return "", false, err
	}
	record, err := store.readRecord()
	if err != nil {
		return "", false, err
	}
	value, _ := record.Requirement(name)
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

// Environment returns the stored values that are valid environment variable
// names, for the host process. Requirement IDs such as "gateway.jwtSecret" are
// not variable names; each secret reaches the host under the variable its plan
// patch binds it to (see BindEnvironments).
func (store Store) Environment() (Environment, error) {
	path, err := store.path()
	if err != nil {
		return Environment{}, err
	}
	projection, err := secretstate.HostEnvironment(path)
	if err != nil {
		return Environment{}, err
	}
	return Environment{Values: projection.Values, Suppressed: projection.Suppressed}, nil
}

func (store Store) path() (string, error) {
	marker := filepath.Join(store.PlatformRoot, ".kb", "v2", "platform-layout.json")
	if _, err := os.Stat(marker); err == nil {
		return filepath.Join(store.PlatformRoot, ".kb", "v2", "secret-state.json"), nil
	} else if !os.IsNotExist(err) {
		return "", err
	}
	layout, err := versionroot.New(store.PlatformRoot)
	if err != nil {
		return "", err
	}
	return layout.SharedRuntimePath(".kb/v2/secret-state.json")
}

func validate(name string) error {
	if name == "" || strings.ContainsAny(name, "=\r\n") || strings.TrimSpace(name) != name {
		return fmt.Errorf("invalid secret name %q", name)
	}
	return nil
}
