// Package secrets owns V2's local secret boundary. Values are private,
// atomic, and deliberately absent from plans, receipts, scenario state, logs,
// and diagnostic dossiers.
package secrets

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kb-labs/clikit/operationlock"
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
		return store.mutate("secret-state", func(record *secretStoreRecord) error {
			origin, _ := record.BootstrapPasswordOrigin()
			if origin != BootstrapOriginGenerated && origin != BootstrapOriginSupplied {
				origin = BootstrapOriginSupplied
			}
			return setBootstrapPassword(record, value, origin)
		})
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
	return store.mutate("secret-state", func(record *secretStoreRecord) error {
		return setBootstrapPassword(record, value, origin)
	})
}

func setBootstrapPassword(record *secretStoreRecord, value string, origin BootstrapPasswordOrigin) error {
	current, _ := record.Requirement(BootstrapPassword)
	currentOrigin, _ := record.BootstrapPasswordOrigin()
	_, completed := record.BootstrapCompleted()
	if completed && (current != value || currentOrigin != origin || record.BootstrapPasswordDisclosed()) {
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
	return nil
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
	return store.mutate("secret-state", func(record *secretStoreRecord) error {
		record.SetRequirement(name, value)
		return nil
	})
}

// MarkBootstrapDisclosed atomically records handoff and removes both stored
// password names in the same state replacement.
func (store Store) MarkBootstrapDisclosed() error {
	if store.ReadOnly {
		return fmt.Errorf("secret store is read-only during staged update verification")
	}
	return store.mutate("secret-state", func(record *secretStoreRecord) error {
		record.MarkBootstrapDisclosed()
		return nil
	})
}

// MarkBootstrapCompleted records the Gateway's proven first-admin readiness.
func (store Store) MarkBootstrapCompleted(value string) error {
	if store.ReadOnly {
		return fmt.Errorf("secret store is read-only during staged update verification")
	}
	return store.mutate("secret-state", func(record *secretStoreRecord) error {
		record.MarkBootstrapCompleted(value)
		return nil
	})
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

// mutate owns the complete read-modify-write transaction under the same
// cross-process operation lock used by launcher commands.
func (store Store) mutate(operation string, update func(*secretStoreRecord) error) error {
	layout, err := versionroot.ForPlatformPath(store.PlatformRoot)
	if err != nil {
		return err
	}
	lockPath := operationlock.Path(layout.HostStateDir())
	lease := operationlock.Held(lockPath)
	if lease == nil {
		lease, err = operationlock.Acquire(lockPath, operation)
		if err != nil {
			if errors.Is(err, operationlock.ErrInProgress) {
				return contracts.NewDiag(contracts.CodeOperationInProgress,
					"Another launcher operation is already running.",
					"Wait for it to finish, then rerun the command.", err)
			}
			return err
		}
		defer lease.Release()
	}
	return lease.With(func() error {
		record, err := store.readRecord()
		if err != nil {
			return err
		}
		before, err := secretstate.Encode(record)
		if err != nil {
			return err
		}
		if err := update(&record); err != nil {
			return err
		}
		after, err := secretstate.Encode(record)
		if err != nil {
			return err
		}
		if bytes.Equal(before, after) {
			return nil
		}
		if store.ReadOnly {
			return fmt.Errorf("secret store is read-only during staged update verification")
		}
		return store.writeRecord(lease, record)
	})
}

func (store Store) writeRecord(lease *operationlock.Lease, record secretStoreRecord) error {
	path, err := store.path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	layout, err := versionroot.ForPlatformPath(store.PlatformRoot)
	if err != nil {
		return err
	}
	if operationlock.Held(operationlock.Path(layout.HostStateDir())) != lease {
		return errors.New("secret-state write requires its installation operation lock")
	}
	current, err := store.readRecord()
	if err != nil {
		return err
	}
	if !record.AdvanceFrom(current) {
		return contracts.NewDiag(contracts.CodeSecretStateChanged,
			"The installation secret state changed underneath this operation.",
			"Rerun `"+staleWriteRerunCommand(store.PlatformRoot)+"` to apply changes to the current state.", nil)
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

func staleWriteRerunCommand(platformRoot string) string {
	return contracts.ShellCommand("kb-create", "apply", "--platform-root", platformRoot)
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
	return store.mutate("secret-state", func(record *secretStoreRecord) error {
		delivered := map[string]string{}
		for _, patch := range patches {
			if patch.Environment == "" || !strings.HasPrefix(patch.Owner, "manifest:") {
				continue
			}
			id := strings.TrimPrefix(patch.Owner, "manifest:")
			if id == patch.Environment {
				continue
			}
			value, present := record.Requirement(id)
			if !present || value == "" {
				continue
			}
			if previous, seen := delivered[patch.Environment]; seen && previous != value {
				return errors.New("secrets bound to the same environment variable hold different values")
			}
			delivered[patch.Environment] = value
			record.SetRequirement(patch.Environment, value)
		}
		return nil
	})
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
	layout, err := versionroot.ForPlatformPath(store.PlatformRoot)
	if err != nil {
		return "", err
	}
	return layout.SharedRuntimePath(contracts.SharedRecordPath())
}

func validate(name string) error {
	if name == "" || strings.ContainsAny(name, "=\r\n") || strings.TrimSpace(name) != name {
		return fmt.Errorf("invalid secret name %q", name)
	}
	return nil
}
