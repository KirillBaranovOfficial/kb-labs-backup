// Package lifecycle owns the recovery semantics shared by V2 update,
// uninstall and doctor --fix. Commands supply the mutation; this package
// guarantees the snapshot/restore boundary around it.
package lifecycle

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/kb-labs/create/v2/contracts"
	"github.com/kb-labs/create/v2/receipt"
)

type Mutation func() error
type PackageRestore func([]contracts.Artifact) error

// Mutate snapshots the active verified receipt before a destructive change.
// A failed mutation or verification restores the exact managed state and
// returns the snapshot ID, so both humans and agents have a deterministic
// recovery reference.
func Mutate(platformRoot string, now time.Time, apply, verify Mutation, restorePackages PackageRestore) (contracts.Snapshot, error) {
	snapshot, err := receipt.CreateSnapshot(platformRoot, now)
	if err != nil {
		return contracts.Snapshot{}, fmt.Errorf("create recovery snapshot: %w", err)
	}
	if err := apply(); err != nil {
		return recover(platformRoot, snapshot, "apply", err, restorePackages)
	}
	if verify != nil {
		if err := verify(); err != nil {
			return recover(platformRoot, snapshot, "verify", err, restorePackages)
		}
	}
	return snapshot, nil
}

// Rollback applies the saved package-manager state before restoring packages,
// then gives the package callback artifacts from the saved receipt explicitly.
// Package restoration is separate from config rendering; the snapshot is the
// only writer of generated platform files in this lifecycle.
func Rollback(platformRoot, snapshotID string, restorePackages PackageRestore) (contracts.Snapshot, error) {
	snapshot, err := receipt.ReadSnapshot(platformRoot, snapshotID)
	if err != nil {
		return contracts.Snapshot{}, fmt.Errorf("read snapshot %s: %w", snapshotID, err)
	}
	var artifacts []contracts.Artifact
	if restorePackages != nil {
		receiptBytes, ok := snapshot.Files[".kb/v2/receipt.json"]
		if !ok {
			return snapshot, fmt.Errorf("restore packages for snapshot %s: snapshot receipt is missing", snapshotID)
		}
		var restored contracts.InstallReceipt
		if err := json.Unmarshal(receiptBytes, &restored); err != nil {
			return snapshot, fmt.Errorf("restore packages for snapshot %s: decode snapshot receipt: %w", snapshotID, err)
		}
		if restored.Schema != contracts.ReceiptSchema || restored.ID != snapshot.ReceiptID {
			return snapshot, fmt.Errorf("restore packages for snapshot %s: snapshot receipt identity is invalid", snapshotID)
		}
		artifacts = restored.Plan.Artifacts
	}
	if _, err := receipt.RestoreSnapshot(platformRoot, snapshotID); err != nil {
		return snapshot, fmt.Errorf("restore snapshot %s: %w", snapshotID, err)
	}
	if restorePackages != nil {
		if err := restorePackages(artifacts); err != nil {
			return snapshot, fmt.Errorf("restore packages for snapshot %s: %w", snapshotID, err)
		}
	}
	return snapshot, nil
}

func recover(platformRoot string, snapshot contracts.Snapshot, stage string, cause error, restorePackages PackageRestore) (contracts.Snapshot, error) {
	if _, restoreErr := Rollback(platformRoot, snapshot.ID, restorePackages); restoreErr != nil {
		return snapshot, fmt.Errorf("%s failed: %w; rollback to snapshot %s also failed: %v", stage, cause, snapshot.ID, restoreErr)
	}
	return snapshot, fmt.Errorf("%s failed: %w; restored snapshot %s", stage, cause, snapshot.ID)
}
