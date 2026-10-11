package versionroot

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kb-labs/clikit/diag"
	"github.com/kb-labs/create/v2/contracts"
	"github.com/kb-labs/create/v2/receipt"
	"time"
)

func initialLayout(t *testing.T, root, version string) (Layout, string) {
	t.Helper()
	layout, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	stage, err := layout.StageInitial()
	if err != nil {
		t.Fatal(err)
	}
	writeReceipt(t, stage, version, "receipt-"+version)
	active, err := layout.ActivateInitial(stage, version, "plan-"+version)
	if err != nil {
		t.Fatal(err)
	}
	active, err = layout.Active()
	if err != nil {
		t.Fatal(err)
	}
	return layout, active
}

func TestExistingPlatformRootHasOnlyNotInstalledOrValidatedStates(t *testing.T) {
	for _, target := range []string{"missing", "empty", "foreign", "installed"} {
		t.Run(target, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "install")
			switch target {
			case "empty":
				if err := os.MkdirAll(root, 0o700); err != nil {
					t.Fatal(err)
				}
			case "foreign":
				if err := os.MkdirAll(root, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, "foreign.txt"), []byte("owned by user"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "installed":
				initialLayout(t, root, "1.0.0")
			}
			layout, err := New(root)
			if err != nil {
				t.Fatal(err)
			}
			resolved, err := layout.ExistingPlatformRoot()
			if target == "missing" || target == "empty" {
				if !errors.Is(err, ErrPlatformNotInstalled) || resolved != "" {
					t.Fatalf("not-installed resolution = %q, %v", resolved, err)
				}
				return
			}
			if target == "foreign" {
				if err == nil || resolved != "" {
					t.Fatalf("foreign target resolution = %q, %v", resolved, err)
				}
				return
			}
			if err != nil || resolved != layout.PlatformRoot() {
				t.Fatalf("installed resolution = %q, %v; want %q", resolved, err, layout.PlatformRoot())
			}
		})
	}
}

// case: S-024.7
func TestRollbackSnapshotSurvivesVersionRootPruning(t *testing.T) {
	root := filepath.Join(t.TempDir(), "platform")
	layout, v1 := initialLayout(t, root, "1.0.0")
	snapshot, err := receipt.CreateSnapshot(v1, layout.SnapshotsDir(), time.Unix(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	if err := layout.RecordHealthy(); err != nil {
		t.Fatal(err)
	}
	v2 := stageVersion(t, layout, "2.0.0")
	if _, err := layout.Activate(v2, "2.0.0", "plan-2", ""); err != nil {
		t.Fatal(err)
	}
	if err := layout.RecordHealthy(); err != nil {
		t.Fatal(err)
	}
	if err := layout.Retain(""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(v1); !os.IsNotExist(err) {
		t.Fatalf("old version root was not pruned: %v", err)
	}
	if got, err := receipt.ReadSnapshot(layout.SnapshotsDir(), snapshot.ID); err != nil || got.ID != snapshot.ID {
		t.Fatalf("snapshot after old-root prune = %#v, %v", got, err)
	}
}

func writeReceipt(t *testing.T, root, version, id string) {
	t.Helper()
	installationID, err := InstallationIDForPath(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := receipt.Write(root, contracts.InstallReceipt{ID: id, InstallationID: installationID,
		Plan: contracts.ResolvedInstallPlan{Request: contracts.InstallRequest{PlatformRoot: root},
			ServiceGraph: contracts.ServiceGraph{PlatformVersion: version}, PlanHash: "plan-" + version}}); err != nil {
		t.Fatal(err)
	}
}

func TestInitialLayoutAndRepeatedActivationRetainLastHealthyRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "platform")
	layout, v1 := initialLayout(t, root, "1.0.0")
	if err := layout.RecordHealthy(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(v1, "marker"), []byte("v1"), 0o600); err != nil {
		t.Fatal(err)
	}
	v2 := stageVersion(t, layout, "2.0.0")
	if _, err := layout.Activate(v2, "2.0.0", "plan-2", ""); err != nil {
		t.Fatal(err)
	}
	v2Active, err := layout.Active()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(v1, ".kb", "logs", "post-update.log"), []byte("runtime log survives pruning"), 0o600); err != nil {
		t.Fatal(err)
	}
	v3 := stageVersion(t, layout, "3.0.0")
	if _, err := layout.Activate(v3, "3.0.0", "plan-3", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(v1); err != nil {
		t.Fatalf("last healthy root was pruned: %v", err)
	}
	if _, err := os.Stat(v2Active); !os.IsNotExist(err) {
		t.Fatalf("never-started intermediate root remains: %s (%v)", v2Active, err)
	}
	if target, err := layout.Active(); err != nil || target == v1 {
		t.Fatalf("active root = %s, %v", target, err)
	}
	recovery, err := layout.RecoveryRoot()
	if err != nil || recovery != v1 {
		t.Fatalf("recovery root = %s, %v; want last healthy %s", recovery, err, v1)
	}
	retained, err := layout.Retained("")
	if err != nil || len(retained) != 2 {
		t.Fatalf("retained roots = %#v, %v; want active and last healthy", retained, err)
	}
	if err := layout.RecordHealthy(); err != nil {
		t.Fatal(err)
	}
	if err := layout.Retain(""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(v1); !os.IsNotExist(err) {
		t.Fatalf("last healthy root survived the new version's successful health check: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(layout.PlatformRoot(), ".kb", "logs", "post-update.log")); err != nil || string(data) != "runtime log survives pruning" {
		t.Fatalf("runtime log after prune = %q, %v", data, err)
	}
}

func stageVersion(t *testing.T, layout Layout, version string) string {
	t.Helper()
	stage, err := layout.Stage()
	if err != nil {
		t.Fatal(err)
	}
	writeReceipt(t, stage, version, "receipt-"+version)
	return stage
}

func TestActivationRefusalLeavesActiveRootUntouched(t *testing.T) {
	root := filepath.Join(t.TempDir(), "platform")
	layout, active := initialLayout(t, root, "1.0.0")
	marker := filepath.Join(active, "marker")
	if err := os.WriteFile(marker, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	occupied := filepath.Join(layout.VersionsDir(), versionKey("2.0.0", "plan-2"))
	if err := os.MkdirAll(occupied, 0o700); err != nil {
		t.Fatal(err)
	}
	stage := stageVersion(t, layout, "2.0.0")
	if _, err := layout.Activate(stage, "2.0.0", "plan-2", ""); err == nil {
		t.Fatal("activation unexpectedly replaced an existing version root")
	}
	if got, err := os.ReadFile(marker); err != nil || string(got) != "original" {
		t.Fatalf("active marker = %q, %v", got, err)
	}
}

// case: S-223.1
func TestHandoffCrashBoundariesKeepCompleteActiveAndRecoveryRoots(t *testing.T) {
	points := []HandoffPoint{HandoffMetadataWritten, HandoffTargetFinalized, HandoffLinkPrepared, HandoffLinkSwitched}
	for _, point := range points {
		t.Run(string(point), func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "platform")
			layout, v1 := initialLayout(t, root, "1.0.0")
			if err := layout.RecordHealthy(); err != nil {
				t.Fatal(err)
			}
			stage := stageVersion(t, layout, "2.0.0")
			crash := errors.New("simulated process termination")
			_, err := layout.activate(stage, "2.0.0", "plan-2", "", func(current HandoffPoint) error {
				if current == point {
					return crash
				}
				return nil
			})
			if !errors.Is(err, crash) {
				t.Fatalf("activation error = %v, want injected crash", err)
			}
			if _, err := os.Stat(root); err != nil {
				t.Fatalf("stable root is absent after handoff point %s: %v", point, err)
			}
			if err := layout.Validate(); err != nil {
				t.Fatalf("active root is incomplete after handoff point %s: %v", point, err)
			}
			recovery, err := layout.RecoveryRoot()
			if err != nil || recovery != v1 {
				t.Fatalf("recovery root = %s, %v; want %s", recovery, err, v1)
			}
			if _, err := os.Stat(recovery); err != nil {
				t.Fatalf("recorded recovery root is missing: %v", err)
			}
		})
	}
}

// case: S-223.1
func TestInitialHandoffLeavesOnlyCompleteInstallTreesAtEachBoundary(t *testing.T) {
	points := []HandoffPoint{HandoffInitialMetadataWritten, HandoffInitialTargetFinalized, HandoffInitialLinkPrepared, HandoffInitialLinkSwitched}
	for _, point := range points {
		t.Run(string(point), func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "platform")
			layout, err := New(root)
			if err != nil {
				t.Fatal(err)
			}
			stage, err := layout.StageInitial()
			if err != nil {
				t.Fatal(err)
			}
			writeReceipt(t, stage, "1.0.0", "receipt-1")
			crash := errors.New("simulated process termination")
			_, err = layout.activateInitial(stage, "1.0.0", "plan-1", func(current HandoffPoint) error {
				if current == point {
					return crash
				}
				return nil
			})
			if !errors.Is(err, crash) {
				t.Fatalf("initial activation error = %v, want injected crash", err)
			}
			if point == HandoffInitialLinkSwitched {
				if err := layout.Validate(); err != nil {
					t.Fatalf("completed initial link is incomplete: %v", err)
				}
			} else if point == HandoffInitialMetadataWritten {
				if _, err := receipt.Read(stage); err != nil {
					t.Fatalf("pre-move staged install tree is incomplete: %v", err)
				}
			} else {
				if _, err := os.Lstat(layout.PlatformRoot()); !os.IsNotExist(err) {
					t.Fatalf("pre-link current entry exists: %v", err)
				}
				entries, readErr := os.ReadDir(layout.VersionsDir())
				if readErr != nil || len(entries) == 0 {
					t.Fatalf("complete staged installation is not recoverable: %v", readErr)
				}
				found := false
				for _, entry := range entries {
					if strings.HasPrefix(entry.Name(), "1.0.0-") {
						_, readErr := receipt.Read(filepath.Join(layout.VersionsDir(), entry.Name()))
						found = readErr == nil
					}
				}
				if !found {
					t.Fatal("version directory has no complete initial receipt")
				}
			}
		})
	}
}

func TestInterruptedInitialActivationResumesFromTheInstallContainer(t *testing.T) {
	for _, point := range []HandoffPoint{HandoffInitialMetadataWritten, HandoffInitialTargetFinalized, HandoffInitialLinkPrepared} {
		t.Run(string(point), func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "install")
			layout, err := New(root)
			if err != nil {
				t.Fatal(err)
			}
			stage, err := layout.StageInitial()
			if err != nil {
				t.Fatal(err)
			}
			writeReceipt(t, stage, "1.0.0", "receipt-1")
			crash := errors.New("simulated interruption")
			_, err = layout.activateInitial(stage, "1.0.0", "plan-1", func(current HandoffPoint) error {
				if current == point {
					return crash
				}
				return nil
			})
			if !errors.Is(err, crash) {
				t.Fatalf("activation error = %v, want interruption", err)
			}
			if err := layout.ValidateInitialTarget(); err != nil {
				t.Fatalf("validate interrupted install: %v", err)
			}
			if point == HandoffInitialMetadataWritten {
				retry, err := layout.StageInitial()
				if err != nil {
					t.Fatalf("restart initial staging: %v", err)
				}
				writeReceipt(t, retry, "1.0.0", "receipt-1-retry")
				if _, err := layout.ActivateInitial(retry, "1.0.0", "plan-1-retry"); err != nil {
					t.Fatalf("complete retried install: %v", err)
				}
			}
			if err := layout.Validate(); err != nil {
				t.Fatalf("resumed current link does not resolve to a complete version: %v", err)
			}
		})
	}
}

// case: PC-001.18
func TestEveryDeclaredCrashPointCanRetryInstallOrRemoveOwnedLayout(t *testing.T) {
	for _, point := range CrashPoints() {
		t.Run(string(point)+"/retry", func(t *testing.T) {
			layout := interruptAtPoint(t, point)
			if strings.HasPrefix(string(point), "initial-") {
				if err := layout.ValidateInitialTarget(); err != nil {
					t.Fatalf("recognize interrupted initial install: %v", err)
				}
				if _, activeErr := layout.Active(); activeErr == nil {
					if err := layout.Validate(); err != nil {
						t.Fatalf("recovered initial install is invalid: %v", err)
					}
					return
				}
				stage, err := layout.StageInitial()
				if err != nil {
					t.Fatalf("restart initial staging: %v", err)
				}
				writeReceipt(t, stage, "1.0.0", "receipt-retry")
				if _, err := layout.ActivateInitial(stage, "1.0.0", "retry-plan"); err != nil {
					t.Fatalf("activate retried initial install: %v", err)
				}
				if err := layout.Validate(); err != nil {
					t.Fatalf("retried initial install is invalid: %v", err)
				}
				return
			}
			if err := layout.Validate(); err != nil {
				t.Fatalf("active installation is invalid after interrupted update %s: %v", point, err)
			}
			stage, err := layout.Stage()
			if err != nil {
				t.Fatalf("restart update staging: %v", err)
			}
			writeReceipt(t, stage, "3.0.0", "receipt-retry")
			if _, err := layout.Activate(stage, "3.0.0", "retry-plan", ""); err != nil {
				t.Fatalf("activate retried update: %v", err)
			}
			if err := layout.Validate(); err != nil {
				t.Fatalf("retried update is invalid: %v", err)
			}
		})

		t.Run(string(point)+"/uninstall", func(t *testing.T) {
			layout := interruptAtPoint(t, point)
			foreign := filepath.Join(layout.InstallRoot(), "foreign.txt")
			if err := os.WriteFile(foreign, []byte("preserve me"), 0o600); err != nil {
				t.Fatal(err)
			}
			err := layout.RemoveOwned()
			if err != nil && layout.HasOwnershipMarker() {
				t.Fatalf("uninstall rejected an owned interrupted container: %v", err)
			}
			if got, readErr := os.ReadFile(foreign); readErr != nil || string(got) != "preserve me" {
				t.Fatalf("foreign entry changed during uninstall: %q, %v", got, readErr)
			}
			for _, entry := range contracts.OwnedInstallEntries() {
				if _, statErr := os.Lstat(filepath.Join(layout.InstallRoot(), entry)); statErr == nil {
					t.Fatalf("uninstall left owned entry %q at crash point %s", entry, point)
				}
			}
		})
	}
}

func TestPreActivationUpdateHandoffsLeaveSharedStateAndRunningVersionUntouched(t *testing.T) {
	for _, point := range CrashPoints() {
		if strings.HasPrefix(string(point), "initial-") || point == HandoffLinkSwitched {
			continue
		}
		t.Run(string(point), func(t *testing.T) {
			layout, active := initialLayout(t, filepath.Join(t.TempDir(), "install"), "1.0.0")
			if err := layout.RecordHealthy(); err != nil {
				t.Fatal(err)
			}
			shared := filepath.Join(layout.SharedStateDir(), "data", "shared.db")
			if err := os.MkdirAll(filepath.Dir(shared), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(shared, []byte("live data"), 0o600); err != nil {
				t.Fatal(err)
			}
			lock := filepath.Join(active, ".kb", "marketplace.lock")
			config := filepath.Join(active, ".kb", "kb.config.jsonc")
			for path, contents := range map[string]string{lock: "old packages", config: "old config"} {
				if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			stateBefore := snapshotVersionrootTree(t, layout.SharedStateDir())
			lockBefore, err := os.ReadFile(lock)
			if err != nil {
				t.Fatal(err)
			}
			configBefore, err := os.ReadFile(config)
			if err != nil {
				t.Fatal(err)
			}
			interrupt := errors.New("simulated interrupted update")
			var stage string
			if point == HandoffUpdateStageCreated || point == HandoffUpdateReceiptSeeded {
				stage, err = layout.stage(func(current HandoffPoint) error {
					if current == point {
						return interrupt
					}
					return nil
				})
			} else {
				stage, err = layout.Stage()
				if err == nil {
					writeReceipt(t, stage, "2.0.0", "receipt-2.0.0")
					_, err = layout.activate(stage, "2.0.0", "plan-2.0.0", active, func(current HandoffPoint) error {
						if current == point {
							return interrupt
						}
						return nil
					})
				}
			}
			if !errors.Is(err, interrupt) {
				t.Fatalf("handoff at %s returned %v, want injected interruption", point, err)
			}
			if after := snapshotVersionrootTree(t, layout.SharedStateDir()); !equalVersionrootSnapshots(stateBefore, after) {
				t.Fatalf("shared state changed at %s: before=%#v after=%#v", point, stateBefore, after)
			}
			if got, readErr := os.ReadFile(lock); readErr != nil || string(got) != string(lockBefore) {
				t.Fatalf("running version lock changed at %s: %q, %v", point, got, readErr)
			}
			if got, readErr := os.ReadFile(config); readErr != nil || string(got) != string(configBefore) {
				t.Fatalf("running version config changed at %s: %q, %v", point, got, readErr)
			}
		})
	}
}

func snapshotVersionrootTree(t *testing.T, root string) map[string]string {
	t.Helper()
	snapshot := make(map[string]string)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		value := fmt.Sprintf("mode=%v;mtime=%d;size=%d", info.Mode(), info.ModTime().UnixNano(), info.Size())
		if info.Mode().IsRegular() {
			contents, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			value += ";bytes=" + string(contents)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			value += ";link=" + target
		}
		snapshot[relative] = value
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func equalVersionrootSnapshots(left, right map[string]string) bool {
	if len(left) != len(right) {
		return false
	}
	for path, value := range left {
		if right[path] != value {
			return false
		}
	}
	return true
}

func interruptAtPoint(t *testing.T, point HandoffPoint) Layout {
	t.Helper()
	root := filepath.Join(t.TempDir(), "install")
	layout, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	crash := errors.New("simulated process termination")
	if strings.HasPrefix(string(point), "initial-") && point != HandoffInitialMetadataWritten && point != HandoffInitialTargetFinalized && point != HandoffInitialLinkPrepared && point != HandoffInitialLinkSwitched {
		_, err := layout.stageInitial(func(current HandoffPoint) error {
			if current == point {
				return crash
			}
			return nil
		})
		if !errors.Is(err, crash) {
			t.Fatalf("initial staging at %s returned %v", point, err)
		}
		return layout
	}
	stage, err := layout.StageInitial()
	if err != nil {
		t.Fatal(err)
	}
	writeReceipt(t, stage, "1.0.0", "receipt-initial")
	if strings.HasPrefix(string(point), "initial-") {
		_, err = layout.activateInitial(stage, "1.0.0", "initial-plan", func(current HandoffPoint) error {
			if current == point {
				return crash
			}
			return nil
		})
		if !errors.Is(err, crash) {
			t.Fatalf("initial activation at %s returned %v", point, err)
		}
		return layout
	}
	if _, err := layout.ActivateInitial(stage, "1.0.0", "initial-plan"); err != nil {
		t.Fatal(err)
	}
	if err := layout.RecordHealthy(); err != nil {
		t.Fatal(err)
	}
	if point == HandoffUpdateStageCreated || point == HandoffUpdateReceiptSeeded {
		_, err = layout.stage(func(current HandoffPoint) error {
			if current == point {
				return crash
			}
			return nil
		})
		if !errors.Is(err, crash) {
			t.Fatalf("update staging at %s returned %v", point, err)
		}
		return layout
	}
	stage, err = layout.Stage()
	if err != nil {
		t.Fatal(err)
	}
	writeReceipt(t, stage, "2.0.0", "receipt-update")
	_, err = layout.activate(stage, "2.0.0", "update-plan", "", func(current HandoffPoint) error {
		if current == point {
			return crash
		}
		return nil
	})
	if !errors.Is(err, crash) {
		t.Fatalf("update activation at %s returned %v", point, err)
	}
	return layout
}

func TestRemoveOwnedRemovesStableLinkAndAllVersionRoots(t *testing.T) {
	root := filepath.Join(t.TempDir(), "platform")
	layout, _ := initialLayout(t, root, "1.0.0")
	stage := stageVersion(t, layout, "2.0.0")
	if _, err := layout.Activate(stage, "2.0.0", "plan-2", ""); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(layout.VersionsDir())
	if err != nil || len(entries) != 2 {
		t.Fatalf("fixture must have two version roots: %d, %v", len(entries), err)
	}
	if err := layout.RemoveOwned(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(root); !os.IsNotExist(err) {
		t.Fatalf("stable root remains after uninstall: %v", err)
	}
	if _, err := os.Stat(layout.VersionsDir()); !os.IsNotExist(err) {
		t.Fatalf("version roots remain after uninstall: %v", err)
	}
}

func TestUnversionedDirectoryIsNotMigratedOnActivation(t *testing.T) {
	root := filepath.Join(t.TempDir(), "platform")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "user-file"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	layout, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := layout.Validate(); err == nil {
		t.Fatal("unversioned directory was accepted as a versioned platform root")
	}
	if got, err := os.ReadFile(filepath.Join(root, "user-file")); err != nil || string(got) != "keep" {
		t.Fatalf("legacy directory changed: %q, %v", got, err)
	}
}

func TestInitialTargetAcceptsOnlyContractDeclaredRuntimeState(t *testing.T) {
	root := filepath.Join(t.TempDir(), "platform")
	if err := os.MkdirAll(filepath.Join(root, "state", "cache"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "state", "cache", "plan.json"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	layout, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := layout.ValidateInitialTarget(); err != nil {
		t.Fatalf("declared resolver cache was rejected: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "user-data.txt"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := layout.ValidateInitialTarget(); err == nil {
		t.Fatal("unrelated user data was accepted as a fresh install target")
	}
}

func TestInstallRootContainerKeepsLifecycleWritesInsideTheChosenDirectory(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "install")
	layout, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	stage, err := layout.StageInitial()
	if err != nil {
		t.Fatal(err)
	}
	writeReceipt(t, stage, "1.0.0", "receipt-1")
	if _, err := layout.ActivateInitial(stage, "1.0.0", "plan-1"); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(root); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("install root = %v, %v; want a real directory", info, err)
	}
	current := filepath.Join(root, "current")
	link, err := os.Readlink(current)
	if err != nil || filepath.IsAbs(link) || !strings.HasPrefix(filepath.Clean(filepath.Join(root, link)), filepath.Join(root, "versions")+string(filepath.Separator)) {
		t.Fatalf("current link = %q, %v; want relative link into install-root versions", link, err)
	}
	entries, err := os.ReadDir(parent)
	if err != nil || len(entries) != 1 || entries[0].Name() != "install" {
		t.Fatalf("parent entries = %#v, %v; lifecycle wrote outside install root", entries, err)
	}
	assertOnlyInstallRoot(t, parent)
	if err := layout.RecordHealthy(); err != nil {
		t.Fatal(err)
	}
	v1, err := layout.Active()
	if err != nil {
		t.Fatal(err)
	}
	for _, version := range []string{"2.0.0", "3.0.0"} {
		stage, err := layout.Stage()
		if err != nil {
			t.Fatal(err)
		}
		writeReceipt(t, stage, version, "receipt-"+version)
		if _, err := layout.Activate(stage, version, "plan-"+version, ""); err != nil {
			t.Fatal(err)
		}
		assertOnlyInstallRoot(t, parent)
	}
	if _, err := os.Stat(v1); err != nil {
		t.Fatalf("last healthy root disappeared before restart: %v", err)
	}
	v2 := filepath.Join(layout.VersionsDir(), versionKey("2.0.0", "plan-2.0.0"))
	if _, err := os.Stat(v2); !os.IsNotExist(err) {
		t.Fatalf("unhealthy intermediate v2 was retained: %v", err)
	}
	data := filepath.Join(layout.SharedStateDir(), "data", "after-update.db")
	if err := os.WriteFile(data, []byte("written-after-stage"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := layout.RecordHealthy(); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(layout.PlatformRoot(), ".kb", "data", "after-update.db")); err != nil || string(got) != "written-after-stage" {
		t.Fatalf("post-update data after restart = %q, %v", got, err)
	}
	if _, err := os.Stat(v1); !os.IsNotExist(err) {
		t.Fatalf("old healthy root survived successful v3 restart: %v", err)
	}
	assertOnlyInstallRoot(t, parent)
	if err := layout.RemoveOwned(); err != nil {
		t.Fatal(err)
	}
	entries, err = os.ReadDir(parent)
	if err != nil || len(entries) != 0 {
		t.Fatalf("parent after uninstall = %#v, %v; owned root remained", entries, err)
	}
}

func assertOnlyInstallRoot(t *testing.T, parent string) {
	t.Helper()
	entries, err := os.ReadDir(parent)
	if err != nil || len(entries) != 1 || entries[0].Name() != "install" {
		t.Fatalf("parent entries = %#v, %v; expected only install root", entries, err)
	}
}

func TestUnsupportedOperatingSystemIsCataloguedBeforeStaging(t *testing.T) {
	err := supportedOS("windows")
	var diagnostic *diag.Diag
	if !errors.As(err, &diagnostic) || diagnostic.Code != contracts.CodeInstallLayoutUnsupported {
		t.Fatalf("unsupported operating-system error = %#v", err)
	}
}

func TestVersionRootsShareMutableDataInsteadOfCopyingIt(t *testing.T) {
	root := filepath.Join(t.TempDir(), "install")
	layout, active := initialLayout(t, root, "1.0.0")
	data := filepath.Join(root, "state", "data")
	if err := os.MkdirAll(data, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, "after-update.db"), []byte("latest"), 0o600); err != nil {
		t.Fatal(err)
	}
	wantData, err := filepath.EvalSymlinks(data)
	if err != nil {
		t.Fatal(err)
	}
	stage, err := layout.Stage()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(stage, ".kb", "data")); !os.IsNotExist(err) {
		t.Fatalf("staged version has a writable shared-state route before activation: %v", err)
	}
	activeData, err := filepath.EvalSymlinks(filepath.Join(active, ".kb", "data"))
	if err != nil || filepath.Clean(activeData) != filepath.Clean(wantData) {
		t.Fatalf("active data path = %q, %v; want shared install-root data %q", activeData, err, data)
	}
	writeReceipt(t, stage, "2.0.0", "receipt-2")
	if _, err := layout.Activate(stage, "2.0.0", "plan-2", ""); err != nil {
		t.Fatal(err)
	}
	active, err = layout.Active()
	if err != nil {
		t.Fatal(err)
	}
	activatedData, err := filepath.EvalSymlinks(filepath.Join(active, ".kb", "data"))
	if err != nil || filepath.Clean(activatedData) != filepath.Clean(wantData) {
		t.Fatalf("activated data path = %q, %v; want shared install-root data %q", activatedData, err, data)
	}
	if _, err := os.Stat(filepath.Join(active, ".kb", "marketplace.lock")); !os.IsNotExist(err) {
		t.Fatalf("empty marketplace lock was expected in this catalog fixture, got %v", err)
	}
	if err := os.RemoveAll(stage); err != nil {
		t.Fatal(err)
	}
}

func TestUninstallPreservesForeignInstallRootEntriesAndSibling(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "install")
	layout, _ := initialLayout(t, root, "1.0.0")
	foreignSibling := filepath.Join(parent, "unrelated")
	if err := os.Mkdir(foreignSibling, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(foreignSibling, "keep"), []byte("sibling"), 0o600); err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(root, "keep.txt")
	if err := os.WriteFile(foreign, []byte("user"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := layout.RemoveOwned(); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(foreign); err != nil || string(got) != "user" {
		t.Fatalf("foreign install-root entry = %q, %v", got, err)
	}
	if got, err := os.ReadFile(filepath.Join(foreignSibling, "keep")); err != nil || string(got) != "sibling" {
		t.Fatalf("foreign sibling = %q, %v", got, err)
	}
}
