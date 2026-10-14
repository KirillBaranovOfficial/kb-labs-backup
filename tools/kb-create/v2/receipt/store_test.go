package receipt

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kb-labs/clikit/diag"
	"github.com/kb-labs/create/v2/contracts"
)

func TestReadRefusesReceiptWithDuplicateArtifactIdentity(t *testing.T) {
	root := t.TempDir()
	data, err := json.Marshal(contracts.InstallReceipt{
		Schema: contracts.ReceiptSchema,
		Plan: contracts.ResolvedInstallPlan{Artifacts: []contracts.Artifact{
			{ID: "sqlite", Kind: "platform-member", Package: "@kb/adapters-sqlite"},
			{ID: "sqlite", Kind: "adapter", Package: "@kb/adapters-sqlite"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(Path(root)), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(Path(root), data, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = Read(root)
	if err == nil {
		t.Fatal("expected duplicate artifact identity receipt refusal")
	}
	failure, ok := err.(*diag.Diag)
	if !ok || failure.Code != contracts.CodeArtifactIdentityInvalid {
		t.Fatalf("receipt error = %#v, want catalogued artifact identity diagnostic", err)
	}
	if !strings.Contains(failure.Hint, "current sealed release index") || !strings.Contains(failure.Hint, "kb-create launcher") {
		t.Fatalf("receipt recovery hint = %q", failure.Hint)
	}
}

func TestReadRefusesPluginReceiptWithPackageButNoID(t *testing.T) {
	root := t.TempDir()
	data, err := json.Marshal(contracts.InstallReceipt{
		Schema: contracts.ReceiptSchema,
		Plan: contracts.ResolvedInstallPlan{Artifacts: []contracts.Artifact{{
			Kind: "plugin", Package: "@kb/fixture-plugin", Version: "1.0.0",
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(Path(root)), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(Path(root), data, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = Read(root)
	failure, ok := err.(*diag.Diag)
	if !ok || failure.Code != contracts.CodeArtifactIdentityInvalid {
		t.Fatalf("receipt error = %#v, want catalogued missing-identity diagnostic", err)
	}
}

func TestWriteRejectsDuplicateArtifactIdentity(t *testing.T) {
	err := Write(t.TempDir(), contracts.InstallReceipt{Plan: contracts.ResolvedInstallPlan{Artifacts: []contracts.Artifact{
		{ID: "sqlite", Kind: "platform-member", Package: "@kb/adapters-sqlite"},
		{ID: "sqlite", Kind: "adapter", Package: "@kb/adapters-sqlite"},
	}}})
	failure, ok := err.(*diag.Diag)
	if !ok || failure.Code != contracts.CodeArtifactIdentityInvalid {
		t.Fatalf("write error = %#v, want catalogued artifact identity diagnostic", err)
	}
}

func TestSnapshotRestoresManagedFilesWithoutTouchingProjectFiles(t *testing.T) {
	root := t.TempDir()
	if err := Write(root, contracts.InstallReceipt{Schema: contracts.ReceiptSchema, ID: "receipt-1", Plan: contracts.ResolvedInstallPlan{PlanHash: "plan"}, Verification: contracts.Verification{ConfigSHA256: "config"}}); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(root, ".kb", "kb.config.jsonc")
	if err := os.WriteFile(config, []byte("before"), 0o600); err != nil {
		t.Fatal(err)
	}
	workspace := filepath.Join(root, "pnpm-workspace.yaml")
	if err := os.WriteFile(workspace, []byte("overrides:\n  '@kb/platform': file:.kb/v2/cache/packages/old.tgz\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	userFile := filepath.Join(root, "user.txt")
	if err := os.WriteFile(userFile, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := CreateSnapshot(root, filepath.Join(root, "snapshots"), time.Unix(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config, []byte("after"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(workspace, []byte("overrides:\n  '@kb/platform': file:.kb/v2/cache/packages/new.tgz\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Write(root, contracts.InstallReceipt{Schema: contracts.ReceiptSchema, ID: "receipt-2"}); err != nil {
		t.Fatal(err)
	}
	if _, err := RestoreSnapshot(root, filepath.Join(root, "snapshots"), snapshot.ID); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(config); string(data) != "before" {
		t.Fatalf("config = %q", data)
	}
	if data, _ := os.ReadFile(workspace); string(data) != "overrides:\n  '@kb/platform': file:.kb/v2/cache/packages/old.tgz\n" {
		t.Fatalf("pnpm workspace config = %q", data)
	}
	if data, _ := os.ReadFile(userFile); string(data) != "keep" {
		t.Fatalf("user file = %q", data)
	}
	restored, err := Read(root)
	if err != nil || restored.ID != "receipt-1" {
		t.Fatalf("receipt = %#v, %v", restored, err)
	}
}

func TestSnapshotRetentionKeepsNewestRecoveryBoundaries(t *testing.T) {
	root := t.TempDir()
	for _, created := range []int64{1, 2, 3} {
		if err := Write(root, contracts.InstallReceipt{Schema: contracts.ReceiptSchema, ID: fmt.Sprintf("receipt-%d", created), Plan: contracts.ResolvedInstallPlan{PlanHash: fmt.Sprintf("plan-%d", created)}}); err != nil {
			t.Fatal(err)
		}
		if _, err := CreateSnapshot(root, filepath.Join(root, "snapshots"), time.Unix(created, 0)); err != nil {
			t.Fatal(err)
		}
	}
	if err := PruneSnapshots(filepath.Join(root, "snapshots"), 2); err != nil {
		t.Fatal(err)
	}
	items, err := ListSnapshots(filepath.Join(root, "snapshots"))
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || !items[0].CreatedAt.After(items[1].CreatedAt) || items[0].CreatedAt.Unix() != 3 || items[1].CreatedAt.Unix() != 2 {
		t.Fatalf("retained snapshots = %#v", items)
	}
}
