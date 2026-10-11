// Package receipt persists the recovery boundary atomically under .kb/v2.
package receipt

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kb-labs/create/v2/contracts"
)

func Path(platformRoot string) string {
	return filepath.Join(platformRoot, ".kb", "v2", "receipt.json")
}

func SnapshotPath(directory, id string) string {
	return filepath.Join(directory, id+".json")
}

type SnapshotUsage struct {
	ID             string    `json:"id"`
	CreatedAt      time.Time `json:"createdAt"`
	DiskUsageBytes int64     `json:"diskUsageBytes"`
}

func ListSnapshots(directory string) ([]SnapshotUsage, error) {
	entries, err := os.ReadDir(directory)
	if os.IsNotExist(err) {
		return []SnapshotUsage{}, nil
	}
	if err != nil {
		return nil, err
	}
	items := make([]SnapshotUsage, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		snapshot, err := ReadSnapshot(directory, id)
		if err != nil {
			return nil, fmt.Errorf("read snapshot %s for retention: %w", id, err)
		}
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		items = append(items, SnapshotUsage{ID: id, CreatedAt: snapshot.CreatedAt, DiskUsageBytes: info.Size()})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.After(items[j].CreatedAt) })
	return items, nil
}

func PruneSnapshots(directory string, keep int) error {
	if keep < 0 {
		return fmt.Errorf("snapshot retention count cannot be negative")
	}
	items, err := ListSnapshots(directory)
	if err != nil {
		return err
	}
	if keep > len(items) {
		keep = len(items)
	}
	for _, item := range items[keep:] {
		if err := os.Remove(SnapshotPath(directory, item.ID)); err != nil {
			return fmt.Errorf("prune recovery snapshot %s: %w", item.ID, err)
		}
	}
	return nil
}
func Write(platformRoot string, receipt contracts.InstallReceipt) error {
	if receipt.Schema == "" {
		receipt.Schema = contracts.ReceiptSchema
	}
	if receipt.Schema != contracts.ReceiptSchema {
		return fmt.Errorf("unsupported receipt schema %q", receipt.Schema)
	}
	data, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return err
	}
	writeRoot, err := writableRoot(platformRoot)
	if err != nil {
		return err
	}
	path := Path(writeRoot)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	if err := os.WriteFile(path+".tmp", append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(path+".tmp", path)
}

// CreateSnapshot captures only launcher-managed projections. It must run
// before update/uninstall and is intentionally unable to copy arbitrary user
// files from project roots.
func CreateSnapshot(platformRoot, directory string, now time.Time) (contracts.Snapshot, error) {
	current, err := Read(platformRoot)
	if err != nil {
		return contracts.Snapshot{}, fmt.Errorf("read active receipt: %w", err)
	}
	files := map[string][]byte{}
	for _, relative := range []string{".kb/v2/receipt.json", ".kb/v2/secrets.env", ".kb/v2/bin/kb-dev", ".kb/kb.config.jsonc", ".kb/install.json", "package.json", "pnpm-lock.yaml", "pnpm-workspace.yaml"} {
		data, readErr := os.ReadFile(filepath.Join(platformRoot, relative))
		if readErr == nil {
			files[relative] = data
			continue
		}
		if !os.IsNotExist(readErr) {
			return contracts.Snapshot{}, fmt.Errorf("read managed file %s: %w", relative, readErr)
		}
	}
	id := snapshotID(current.ID, now, files)
	snapshot := contracts.Snapshot{Schema: contracts.SnapshotSchema, ID: id, CreatedAt: now.UTC(), ParentID: current.SnapshotID, ReceiptID: current.ID, ArtifactState: current.Plan.PlanHash, ConfigState: current.Verification.ConfigSHA256, Files: files}
	if err := writeSnapshot(directory, snapshot); err != nil {
		return contracts.Snapshot{}, err
	}
	return snapshot, nil
}

func RestoreSnapshot(platformRoot, directory, id string) (contracts.Snapshot, error) {
	snapshot, err := ReadSnapshot(directory, id)
	if err != nil {
		return contracts.Snapshot{}, err
	}
	writeRoot, err := writableRoot(platformRoot)
	if err != nil {
		return contracts.Snapshot{}, err
	}
	keys := make([]string, 0, len(snapshot.Files))
	for relative := range snapshot.Files {
		keys = append(keys, relative)
	}
	sort.Strings(keys)
	for _, relative := range keys {
		path := filepath.Join(writeRoot, relative)
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			return contracts.Snapshot{}, err
		}
		if err := os.WriteFile(path, snapshot.Files[relative], 0o600); err != nil {
			return contracts.Snapshot{}, err
		}
	}
	return snapshot, nil
}

func writableRoot(platformRoot string) (string, error) {
	resolved, err := filepath.EvalSymlinks(platformRoot)
	if err == nil {
		return filepath.Clean(resolved), nil
	}
	if os.IsNotExist(err) {
		return filepath.Clean(platformRoot), nil
	}
	return "", err
}

// ReadSnapshot validates a saved recovery boundary without applying any of
// its files. Lifecycle rollback uses this to restore external artifacts before
// the snapshot becomes the single writer of generated platform files.
func ReadSnapshot(directory, id string) (contracts.Snapshot, error) {
	data, err := os.ReadFile(SnapshotPath(directory, id))
	if err != nil {
		return contracts.Snapshot{}, err
	}
	var snapshot contracts.Snapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return contracts.Snapshot{}, err
	}
	if snapshot.Schema != contracts.SnapshotSchema || snapshot.ID != id {
		return contracts.Snapshot{}, fmt.Errorf("invalid snapshot %q", id)
	}
	keys := make([]string, 0, len(snapshot.Files))
	for relative := range snapshot.Files {
		keys = append(keys, relative)
	}
	sort.Strings(keys)
	for _, relative := range keys {
		if filepath.IsAbs(relative) || filepath.Clean(relative) != relative || relative == "." || relative == ".." {
			return contracts.Snapshot{}, fmt.Errorf("snapshot contains unsafe managed path %q", relative)
		}
	}
	return snapshot, nil
}

func writeSnapshot(directory string, snapshot contracts.Snapshot) error {
	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return err
	}
	path := SnapshotPath(directory, snapshot.ID)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o600)
}

func snapshotID(receiptID string, now time.Time, files map[string][]byte) string {
	h := sha256.New()
	_, _ = h.Write([]byte(receiptID + "\n" + now.UTC().Format(time.RFC3339Nano)))
	keys := make([]string, 0, len(files))
	for key := range files {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		_, _ = h.Write([]byte(key))
		_, _ = h.Write(files[key])
	}
	return fmt.Sprintf("%x", h.Sum(nil))[:24]
}
func Read(platformRoot string) (contracts.InstallReceipt, error) {
	data, err := os.ReadFile(Path(platformRoot))
	if err != nil {
		return contracts.InstallReceipt{}, err
	}
	var result contracts.InstallReceipt
	if err := json.Unmarshal(data, &result); err != nil {
		return contracts.InstallReceipt{}, err
	}
	if result.Schema != contracts.ReceiptSchema {
		return contracts.InstallReceipt{}, fmt.Errorf("unsupported receipt schema %q", result.Schema)
	}
	return result, nil
}

// Delete removes only the active V2 receipt. Snapshots remain under the
// install-root-owned installer/snapshots directory as explicit recovery evidence.
func Delete(platformRoot string) error {
	err := os.Remove(Path(platformRoot))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
