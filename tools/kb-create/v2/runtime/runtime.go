// Package runtime is the autonomous V2 application boundary. It accepts an
// already resolved plan, applies exact artifacts, renders its projections,
// verifies the resolved service graph and only then commits a receipt.
package runtime

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/kb-labs/create/v2/contracts"
	"github.com/kb-labs/create/v2/doctor"
	"github.com/kb-labs/create/v2/lifecycle"
	"github.com/kb-labs/create/v2/marketplace"
	"github.com/kb-labs/create/v2/receipt"
	"github.com/kb-labs/create/v2/render"
	"github.com/kb-labs/create/v2/secrets"
	"github.com/kb-labs/create/v2/verify"
)

type ArtifactInstaller interface {
	Install([]contracts.Artifact) error
	Restore([]contracts.Artifact) error
}
type ProgressArtifactInstaller interface {
	InstallWithProgress([]contracts.Artifact, func(string, bool) error) error
}
type ArtifactUninstaller interface {
	Uninstall([]contracts.Artifact) error
}

// HostStarter starts the resolved host and returns the stop operation used to
// clean up if the remaining apply verification fails.
type HostStarter interface {
	Start(ctx context.Context, host contracts.HostSpec) (func() error, error)
}

// HostProbe performs a temporary start/health/stop check for an update whose
// previous running state must remain unchanged until its swap succeeds.
type HostProbe interface {
	Probe(ctx context.Context, host contracts.HostSpec) error
}

type Clock interface{ Now() time.Time }

// Progress events describe work performed by the shared apply engine. Frontends
// may render them for people or ignore them for machine calls.
type ProgressEvent struct {
	Phase string
	Start bool
}

type ProgressSink interface{ Emit(ProgressEvent) error }

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now().UTC() }

type Dependencies struct {
	Artifacts ArtifactInstaller
	Host      HostStarter
	Probe     HostProbe
	Clock     Clock
	// CorrelationID is supplied by a frontend; it is persisted verbatim in the
	// receipt and must be safe to disclose in a diagnostic bundle.
	CorrelationID string
	Secrets       *secrets.Store
	Progress      ProgressSink
}

// Apply is the one V2 fresh-install operation. It deliberately has no
// dependency on legacy manifests, state or package scanning.
func Apply(ctx context.Context, plan contracts.ResolvedInstallPlan, deps Dependencies) (contracts.InstallReceipt, error) {
	emit := func(phase string, start bool) error {
		if deps.Progress != nil {
			return deps.Progress.Emit(ProgressEvent{Phase: phase, Start: start})
		}
		return nil
	}
	if plan.Schema != contracts.ResolvedPlanSchema {
		return contracts.InstallReceipt{}, fmt.Errorf("apply resolved plan: unsupported schema %q", plan.Schema)
	}
	if err := contracts.ValidatePlanArtifactIdentities(plan); err != nil {
		return contracts.InstallReceipt{}, err
	}
	if deps.Artifacts == nil {
		return contracts.InstallReceipt{}, fmt.Errorf("apply resolved plan: artifact installer is required")
	}
	installationID, err := installationID(plan.Request.PlatformRoot, deps.Secrets)
	if err != nil {
		return contracts.InstallReceipt{}, err
	}
	if plan.Host != nil {
		if plan.Host.Env == nil {
			plan.Host.Env = map[string]string{}
		}
		plan.Host.Env["KB_INSTALLATION_ID"] = installationID
	}
	if err := verifySecrets(plan, deps.Secrets); err != nil {
		return contracts.InstallReceipt{}, err
	}
	previousProjections, err := captureProjections(plan.Request.PlatformRoot)
	if err != nil {
		return contracts.InstallReceipt{}, fmt.Errorf("preserve previous install projections: %w", err)
	}
	if deps.Secrets != nil {
		if err := deps.Secrets.BindEnvironments(plan.ConfigPatches); err != nil {
			return contracts.InstallReceipt{}, fmt.Errorf("bind secrets to service environment: %w", err)
		}
	}
	var artifactErr error
	if phased, ok := deps.Artifacts.(ProgressArtifactInstaller); ok {
		artifactErr = phased.InstallWithProgress(plan.Artifacts, emit)
	} else {
		if err := emit("Download and verify artifacts / install packages", true); err != nil {
			return contracts.InstallReceipt{}, fmt.Errorf("report apply progress: %w", err)
		}
		artifactErr = deps.Artifacts.Install(plan.Artifacts)
		if err := emit("Download and verify artifacts / install packages", false); err != nil {
			artifactErr = errors.Join(artifactErr, err)
		}
	}
	if artifactErr != nil {
		return contracts.InstallReceipt{}, fmt.Errorf("apply exact artifacts: %w", artifactErr)
	}
	// core-discovery's plugin registry reads .kb/marketplace.lock, not the
	// resolved plan — without this, every installed plugin/adapter is on
	// disk but invisible to the running platform (empty registry at /ready).
	if err := marketplace.WriteLock(plan.Request.PlatformRoot, plan.Artifacts, now(deps.Clock)); err != nil {
		_ = previousProjections.Restore()
		return contracts.InstallReceipt{}, fmt.Errorf("write marketplace lock: %w", err)
	}
	if plan.Host != nil {
		if err := emit("Render config", true); err != nil {
			return contracts.InstallReceipt{}, errors.Join(fmt.Errorf("report apply progress: %w", err), previousProjections.Restore())
		}
		if _, err := render.Write(plan); err != nil {
			progressErr := emit("Render config", false)
			_ = previousProjections.Restore()
			return contracts.InstallReceipt{}, fmt.Errorf("render resolved projections: %w", errors.Join(err, progressErr))
		}
		if err := emit("Render config", false); err != nil {
			return contracts.InstallReceipt{}, errors.Join(fmt.Errorf("report apply progress: %w", err), previousProjections.Restore())
		}
	}
	now := now(deps.Clock)
	var stopHost func() error
	if (deps.Host != nil || deps.Probe != nil) && plan.Host != nil {
		if err := emit("Start host", true); err != nil {
			return contracts.InstallReceipt{}, errors.Join(fmt.Errorf("report apply progress: %w", err), previousProjections.Restore())
		}
		var stop func() error
		var startErr error
		if deps.Host != nil {
			stop, startErr = deps.Host.Start(ctx, *plan.Host)
		} else if deps.Probe != nil {
			startErr = deps.Probe.Probe(ctx, *plan.Host)
		}
		if startErr != nil {
			progressErr := emit("Start host", false)
			return contracts.InstallReceipt{}, errors.Join(fmt.Errorf("start the resolved host: %w", errors.Join(startErr, progressErr)), previousProjections.Restore())
		}
		stopHost = stop
		if err := emit("Start host", false); err != nil {
			return contracts.InstallReceipt{}, stopFailedHost(stopHost, errors.Join(fmt.Errorf("report apply progress: %w", err), previousProjections.Restore()))
		}
	}
	if err := emit("Verify installation", true); err != nil {
		return contracts.InstallReceipt{}, stopFailedHost(stopHost, errors.Join(fmt.Errorf("report apply progress: %w", err), previousProjections.Restore()))
	}
	check, err := verify.Run(plan, now)
	if err != nil {
		progressErr := emit("Verify installation", false)
		return contracts.InstallReceipt{}, stopFailedHost(stopHost, errors.Join(fmt.Errorf("verify installation: %w", errors.Join(err, progressErr)), previousProjections.Restore()))
	}
	if err := emit("Verify installation", false); err != nil {
		return contracts.InstallReceipt{}, stopFailedHost(stopHost, errors.Join(fmt.Errorf("report apply progress: %w", err), previousProjections.Restore()))
	}
	result := contracts.InstallReceipt{Schema: contracts.ReceiptSchema, ID: receiptID(plan.PlanHash, now), InstallationID: installationID, CreatedAt: now, CorrelationID: deps.CorrelationID, Plan: plan, Verification: check}
	if err := receipt.Write(plan.Request.PlatformRoot, result); err != nil {
		return contracts.InstallReceipt{}, stopFailedHost(stopHost, errors.Join(fmt.Errorf("commit verified receipt: %w", err), previousProjections.Restore()))
	}
	return result, nil
}

func installationID(platformRoot string, store *secrets.Store) (string, error) {
	active, err := receipt.Read(platformRoot)
	if err == nil {
		if active.InstallationID == "" {
			return "", contracts.NewDiag(contracts.CodeReceiptUnavailable,
				"The active install receipt has no installation ID.",
				fmt.Sprintf("Run `kb-create update --platform-root %q` or reinstall the platform, then retry.", platformRoot), nil)
		}
		if store != nil {
			stored, present, err := store.Get("installation.id")
			if err != nil {
				return "", fmt.Errorf("read persistent installation identity: %w", err)
			}
			if present && stored != active.InstallationID {
				return "", contracts.NewDiag(contracts.CodeReceiptUnavailable,
					"The active receipt and persistent installation ID do not match.",
					fmt.Sprintf("Run `kb-create update --platform-root %q` or reinstall the platform, then retry.", platformRoot), nil)
			}
			if !present {
				if err := store.Put("installation.id", active.InstallationID); err != nil {
					return "", fmt.Errorf("persist active installation identity: %w", err)
				}
			}
		}
		return active.InstallationID, nil
	}
	if !os.IsNotExist(err) {
		return "", fmt.Errorf("read active installation identity: %w", err)
	}
	if store != nil {
		stored, present, err := store.Get("installation.id")
		if err != nil {
			return "", fmt.Errorf("read persistent installation identity: %w", err)
		}
		if present {
			return stored, nil
		}
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", fmt.Errorf("generate installation identity: %w", err)
	}
	identity := hex.EncodeToString(random[:])
	if store != nil {
		if err := store.Put("installation.id", identity); err != nil {
			return "", fmt.Errorf("persist installation identity: %w", err)
		}
	}
	return identity, nil
}

func verifySecrets(plan contracts.ResolvedInstallPlan, store *secrets.Store) error {
	if len(plan.Request.SecretInputs) == 0 {
		return nil
	}
	if store == nil {
		return fmt.Errorf("apply resolved plan: secret store is required for declared secret inputs")
	}
	for _, name := range plan.Request.SecretInputs {
		exists, err := store.Exists(name)
		if err != nil {
			return fmt.Errorf("check secret %s: %w", name, err)
		}
		if !exists {
			return fmt.Errorf("required secret input %s is not set", name)
		}
	}
	return nil
}

// Update creates a recovery snapshot before replacing the resolved artifact
// set. Any apply or graph-verification failure restores managed files, receipt
// and the package-manager lock state through ArtifactInstaller.Restore.
func Update(ctx context.Context, plan contracts.ResolvedInstallPlan, deps Dependencies) (contracts.InstallReceipt, contracts.Snapshot, error) {
	if err := contracts.ValidatePlanArtifactIdentities(plan); err != nil {
		return contracts.InstallReceipt{}, contracts.Snapshot{}, err
	}
	if deps.Artifacts == nil {
		return contracts.InstallReceipt{}, contracts.Snapshot{}, fmt.Errorf("update resolved plan: artifact installer is required")
	}
	var result contracts.InstallReceipt
	snapshot, err := lifecycle.Mutate(plan.Request.PlatformRoot, now(deps.Clock), func() error {
		var applyErr error
		result, applyErr = Apply(ctx, plan, deps)
		return applyErr
	}, nil, restorePlanArtifacts(deps.Artifacts))
	if err != nil {
		return contracts.InstallReceipt{}, snapshot, err
	}
	result.SnapshotID = snapshot.ID
	if err := receipt.Write(plan.Request.PlatformRoot, result); err != nil {
		return contracts.InstallReceipt{}, snapshot, fmt.Errorf("attach recovery snapshot to receipt: %w", err)
	}
	return result, snapshot, nil
}

// Uninstall removes the artifacts in the verified receipt and, after teardown
// succeeds, the entire platform root. The host must already be stopped (the
// caller halts it): the installed host binary is one of the artifacts being
// removed. The recovery snapshot is retained only until teardown succeeds.
func Uninstall(platformRoot string, deps Dependencies) (contracts.Snapshot, error) {
	if deps.Artifacts == nil {
		return contracts.Snapshot{}, fmt.Errorf("uninstall: artifact installer is required")
	}
	uninstaller, ok := deps.Artifacts.(ArtifactUninstaller)
	if !ok {
		return contracts.Snapshot{}, fmt.Errorf("uninstall: artifact installer does not support removal")
	}
	active, err := receipt.Read(platformRoot)
	if err != nil {
		return contracts.Snapshot{}, fmt.Errorf("uninstall: read active receipt: %w", err)
	}
	snapshot, err := lifecycle.Mutate(platformRoot, now(deps.Clock), func() error {
		if err := uninstaller.Uninstall(active.Plan.Artifacts); err != nil {
			return err
		}
		// Only V2-owned projections are removed. Project roots and any user
		// authored files remain outside this operation's authority.
		if err := removeProjections(platformRoot); err != nil {
			return err
		}
		return receipt.Delete(platformRoot)
	}, nil, restorePlanArtifacts(deps.Artifacts))
	if err != nil {
		return snapshot, err
	}
	if err := os.RemoveAll(platformRoot); err != nil {
		return snapshot, fmt.Errorf("uninstall: remove platform root: %w", err)
	}
	return snapshot, nil
}

// Rollback restores an explicit V2 snapshot and reinstalls its exact lock
// state. It never infers a target from package tags or legacy state, and it
// leaves the host to the caller (which restarts it from the restored receipt).
func Rollback(platformRoot, snapshotID string, deps Dependencies) (contracts.Snapshot, error) {
	if snapshotID == "" {
		return contracts.Snapshot{}, fmt.Errorf("rollback: snapshot ID is required")
	}
	if deps.Artifacts == nil {
		return contracts.Snapshot{}, fmt.Errorf("rollback: artifact installer is required")
	}
	snapshot, err := lifecycle.Rollback(platformRoot, snapshotID, restorePlanArtifacts(deps.Artifacts))
	if err != nil {
		return snapshot, err
	}
	active, err := receipt.Read(platformRoot)
	if err != nil {
		return snapshot, fmt.Errorf("rollback: read restored receipt: %w", err)
	}
	if _, err := verify.Run(active.Plan, now(deps.Clock)); err != nil {
		return snapshot, fmt.Errorf("rollback: verify restored installation: %w", err)
	}
	return snapshot, nil
}

// DoctorFix applies only manifest-declared safe defaults under a snapshot, then
// verifies the receipt's installation. Required user input is never guessed:
// callers receive an error before any write occurs. The host reads its config
// at start, so the caller restarts it afterwards.
func DoctorFix(platformRoot string, repair doctor.RepairPlan, deps Dependencies) (contracts.Snapshot, error) {
	if len(repair.RequiredInput) != 0 {
		return contracts.Snapshot{}, fmt.Errorf("doctor fix requires %d explicit input value(s)", len(repair.RequiredInput))
	}
	active, err := receipt.Read(platformRoot)
	if err != nil {
		return contracts.Snapshot{}, fmt.Errorf("doctor fix: read active receipt: %w", err)
	}
	return lifecycle.Mutate(platformRoot, now(deps.Clock), func() error {
		return doctor.ApplyDefaults(platformRoot, repair)
	}, func() error {
		_, err := verify.Run(active.Plan, now(deps.Clock))
		return err
	}, nil)
}

func restorePlanArtifacts(installer ArtifactInstaller) lifecycle.PackageRestore {
	return installer.Restore
}

func removeProjections(platformRoot string) error {
	for _, relative := range []string{".kb/kb.config.jsonc", ".kb/marketplace.lock"} {
		if err := os.Remove(filepath.Join(platformRoot, relative)); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove managed projection %s: %w", relative, err)
		}
	}
	return nil
}

type projectionFile struct {
	path    string
	content []byte
	mode    os.FileMode
	present bool
}

type projectionState []projectionFile

func captureProjections(platformRoot string) (projectionState, error) {
	paths := []string{filepath.Join(platformRoot, ".kb", "kb.config.jsonc"), filepath.Join(platformRoot, ".kb", "marketplace.lock")}
	state := make(projectionState, 0, len(paths))
	for _, path := range paths {
		info, err := os.Stat(path)
		if os.IsNotExist(err) {
			state = append(state, projectionFile{path: path})
			continue
		}
		if err != nil {
			return nil, err
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		state = append(state, projectionFile{path: path, content: content, mode: info.Mode().Perm(), present: true})
	}
	return state, nil
}

func (state projectionState) Restore() error {
	var failures []error
	for _, file := range state {
		if !file.present {
			if err := os.Remove(file.path); err != nil && !os.IsNotExist(err) {
				failures = append(failures, err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(file.path), 0o750); err != nil {
			failures = append(failures, err)
			continue
		}
		if err := os.WriteFile(file.path, file.content, file.mode); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func stopFailedHost(stop func() error, cause error) error {
	if stop == nil {
		return cause
	}
	return errors.Join(cause, stop())
}

func now(clock Clock) time.Time {
	if clock == nil {
		return systemClock{}.Now()
	}
	return clock.Now().UTC()
}

func receiptID(planHash string, createdAt time.Time) string {
	if len(planHash) > 12 {
		planHash = planHash[:12]
	}
	return createdAt.Format("20060102T150405Z") + "-" + planHash
}
