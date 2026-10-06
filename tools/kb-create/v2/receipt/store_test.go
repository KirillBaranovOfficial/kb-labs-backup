package receipt

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kb-labs/create/v2/contracts"
)

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
	snapshot, err := CreateSnapshot(root, time.Unix(1, 0))
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
	if _, err := RestoreSnapshot(root, snapshot.ID); err != nil {
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

// case: S-024.3
func TestRollbackLeavesInstallerManagedSecretsAvailable(t *testing.T) {
	root := t.TempDir()
	if err := Write(root, contracts.InstallReceipt{Schema: contracts.ReceiptSchema, ID: "receipt-1", Plan: contracts.ResolvedInstallPlan{PlanHash: "plan"}}); err != nil {
		t.Fatal(err)
	}
	secretPath := filepath.Join(root, ".kb", "v2", "secrets.env")
	if err := os.MkdirAll(filepath.Dir(secretPath), 0o750); err != nil {
		t.Fatal(err)
	}
	secret := "GATEWAY_INTERNAL_SECRET=generated-value\n"
	if err := os.WriteFile(secretPath, []byte(secret), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := CreateSnapshot(root, time.Unix(2, 0))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secretPath, []byte("GATEWAY_INTERNAL_SECRET=updated-value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := RestoreSnapshot(root, snapshot.ID); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(secretPath); err != nil || string(data) != secret {
		t.Fatalf("rollback secret store = %q, err=%v", data, err)
	}
}
