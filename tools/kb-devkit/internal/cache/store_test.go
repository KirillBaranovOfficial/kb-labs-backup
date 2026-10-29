package cache

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocalStorePutGetAndRestoreFile(t *testing.T) {
	store, err := NewLocalStore(filepath.Join(t.TempDir(), ".kb", "devkit"))
	if err != nil {
		t.Fatalf("NewLocalStore error: %v", err)
	}

	sha, err := store.Put(strings.NewReader("hello world"))
	if err != nil {
		t.Fatalf("Put error: %v", err)
	}
	if !store.Has(sha) {
		t.Fatalf("Has(%q) = false, want true", sha)
	}

	r, err := store.Get(sha)
	if err != nil {
		t.Fatalf("Get error: %v", err)
	}
	data, err := io.ReadAll(r)
	r.Close()
	if err != nil {
		t.Fatalf("ReadAll error: %v", err)
	}
	if string(data) != "hello world" {
		t.Fatalf("stored content = %q, want hello world", data)
	}

	restorePath := filepath.Join(t.TempDir(), "restored", "artifact.txt")
	if err := store.RestoreFile(sha, restorePath, 0o644); err != nil {
		t.Fatalf("RestoreFile error: %v", err)
	}
	restored, err := os.ReadFile(restorePath)
	if err != nil {
		t.Fatalf("read restored file: %v", err)
	}
	if string(restored) != "hello world" {
		t.Fatalf("restored content = %q, want hello world", restored)
	}
}

func TestLocalStoreDeduplicatesAndPutFile(t *testing.T) {
	storeRoot := filepath.Join(t.TempDir(), ".kb", "devkit")
	store, err := NewLocalStore(storeRoot)
	if err != nil {
		t.Fatalf("NewLocalStore error: %v", err)
	}

	input := filepath.Join(t.TempDir(), "input.txt")
	if err := os.WriteFile(input, []byte("same content"), 0o644); err != nil {
		t.Fatalf("write input: %v", err)
	}

	sha1, err := store.PutFile(input)
	if err != nil {
		t.Fatalf("PutFile error: %v", err)
	}
	sha2, err := store.Put(strings.NewReader("same content"))
	if err != nil {
		t.Fatalf("Put error: %v", err)
	}
	if sha1 != sha2 {
		t.Fatalf("dedupe sha mismatch: %q vs %q", sha1, sha2)
	}

	objectDir := filepath.Join(storeRoot, "objects", sha1[:2])
	entries, err := os.ReadDir(objectDir)
	if err != nil {
		t.Fatalf("ReadDir error: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("objects count = %d, want 1", len(entries))
	}
}

func TestHashInputsIsDeterministicAndSkipsIgnoredDirs(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "src", "a.ts"), "export const a = 1\n")
	mustWriteFile(t, filepath.Join(root, "src", "b.ts"), "export const b = 2\n")
	mustWriteFile(t, filepath.Join(root, "node_modules", "leftpad", "index.js"), "ignored\n")
	mustWriteFile(t, filepath.Join(root, ".git", "HEAD"), "ignored\n")

	hash1, err := HashInputs(root, nil, []string{"src/**/*.ts", "src/*.ts"})
	if err != nil {
		t.Fatalf("HashInputs error: %v", err)
	}
	hash2, err := HashInputs(root, nil, []string{"src/*.ts", "src/**/*.ts"})
	if err != nil {
		t.Fatalf("HashInputs error: %v", err)
	}
	if hash1 != hash2 {
		t.Fatalf("hashes differ for same logical inputs: %q vs %q", hash1, hash2)
	}

	mustWriteFile(t, filepath.Join(root, "node_modules", "leftpad", "index.js"), "changed but ignored\n")
	hash3, err := HashInputs(root, nil, []string{"src/**/*.ts", "src/*.ts"})
	if err != nil {
		t.Fatalf("HashInputs error: %v", err)
	}
	if hash1 != hash3 {
		t.Fatalf("ignored dir changed hash: %q vs %q", hash1, hash3)
	}
}

func TestHashInputsWithFingerprintPartitionsTaskSemantics(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "src", "index.ts"), "export const value = 1\n")

	first, err := HashInputsWithFingerprint(root, nil, []string{"src/**"}, "command=tsup")
	if err != nil {
		t.Fatalf("HashInputsWithFingerprint error: %v", err)
	}
	second, err := HashInputsWithFingerprint(root, nil, []string{"src/**"}, "command=pnpm build")
	if err != nil {
		t.Fatalf("HashInputsWithFingerprint error: %v", err)
	}
	if first == second {
		t.Fatal("task fingerprints must partition identical file inputs")
	}
}

func TestHashInputsDepPatternInvalidatesOnDepChange(t *testing.T) {
	pkgDir := t.TempDir()
	dep1Dir := t.TempDir()
	dep2Dir := t.TempDir()

	mustWriteFile(t, filepath.Join(pkgDir, "src", "index.ts"), "export {}\n")
	mustWriteFile(t, filepath.Join(dep1Dir, "dist", "index.js"), "v1\n")
	mustWriteFile(t, filepath.Join(dep2Dir, "dist", "index.js"), "v1\n")

	depDirs := []string{dep1Dir, dep2Dir}

	hash1, err := HashInputs(pkgDir, depDirs, []string{"src/**", "^dist/**"})
	if err != nil {
		t.Fatalf("HashInputs error: %v", err)
	}

	// Change a dep output — hash must change.
	mustWriteFile(t, filepath.Join(dep1Dir, "dist", "index.js"), "v2\n")

	hash2, err := HashInputs(pkgDir, depDirs, []string{"src/**", "^dist/**"})
	if err != nil {
		t.Fatalf("HashInputs error: %v", err)
	}
	if hash1 == hash2 {
		t.Fatal("hash unchanged after dep output change, want different")
	}

	// Changing local src — hash must also change.
	mustWriteFile(t, filepath.Join(pkgDir, "src", "index.ts"), "export { x }\n")

	hash3, err := HashInputs(pkgDir, depDirs, []string{"src/**", "^dist/**"})
	if err != nil {
		t.Fatalf("HashInputs error: %v", err)
	}
	if hash2 == hash3 {
		t.Fatal("hash unchanged after local src change, want different")
	}
}

func TestHashInputsDepPatternWithNilDepDirsIsNoop(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "src", "a.ts"), "export const a = 1\n")

	// "^dist/**" with nil depDirs should match nothing — hash equals local-only hash.
	hashWithDep, err := HashInputs(root, nil, []string{"src/**", "^dist/**"})
	if err != nil {
		t.Fatalf("HashInputs error: %v", err)
	}
	hashLocalOnly, err := HashInputs(root, nil, []string{"src/**"})
	if err != nil {
		t.Fatalf("HashInputs error: %v", err)
	}
	if hashWithDep != hashLocalOnly {
		t.Fatal("nil depDirs: ^dist/** should contribute nothing, hashes must match")
	}
}

func TestHashInputsIncludesDependencyPublishedAndExportedSurface(t *testing.T) {
	consumerDir := t.TempDir()
	dependencyDir := t.TempDir()
	mustWriteFile(t, filepath.Join(consumerDir, "src", "index.ts"), "import '@fixture/devkit/tsconfig/base.json'\n")
	mustWriteFile(t, filepath.Join(dependencyDir, "package.json"), `{"name":"@fixture/devkit","exports":{"./tsconfig/base.json":"./tsconfig/base.json"},"files":["tsconfig"]}`)
	mustWriteFile(t, filepath.Join(dependencyDir, "tsconfig", "base.json"), `{"compilerOptions":{"strict":true}}`)

	first, err := HashInputs(consumerDir, []string{dependencyDir}, []string{"src/**"})
	if err != nil {
		t.Fatalf("hash initial consumer inputs: %v", err)
	}
	mustWriteFile(t, filepath.Join(dependencyDir, "tsconfig", "base.json"), `{"compilerOptions":{"strict":false}}`)
	second, err := HashInputs(consumerDir, []string{dependencyDir}, []string{"src/**"})
	if err != nil {
		t.Fatalf("hash consumer inputs after exported config change: %v", err)
	}
	if first == second {
		t.Fatal("consumer task hash unchanged after only an exported dependency config changed")
	}
}

func TestHashInputsIncludesWorkspaceDependencyWithoutPackageManifest(t *testing.T) {
	consumerDir := t.TempDir()
	replacementDir := filepath.Join(consumerDir, "..", "clikit")
	mustWriteFile(t, filepath.Join(consumerDir, "go.mod"), "module fixture/consumer\n\ngo 1.22\n\nrequire example.com/clikit v0.0.0\n\nreplace example.com/clikit => ../clikit\n")
	mustWriteFile(t, filepath.Join(consumerDir, "main.go"), "package main\nimport \"example.com/clikit/diag\"\nfunc main() { _ = diag.Message() }\n")
	mustWriteFile(t, filepath.Join(replacementDir, "go.mod"), "module example.com/clikit\n\ngo 1.22\n")
	mustWriteFile(t, filepath.Join(replacementDir, "diag", "envelope.go"), "package diag\nimport _ \"embed\"\n//go:embed prompt.md\nvar prompt string\nfunc Message() string { return prompt }\n")
	mustWriteFile(t, filepath.Join(replacementDir, "diag", "prompt.md"), "before\n")

	first, err := HashInputs(consumerDir, []string{replacementDir}, []string{"**/*.go", "go.mod", "go.sum"})
	if err != nil {
		t.Fatalf("hash initial Go consumer inputs: %v", err)
	}
	mustWriteFile(t, filepath.Join(replacementDir, "diag", "prompt.md"), "after\n")
	second, err := HashInputs(consumerDir, []string{replacementDir}, []string{"**/*.go", "go.mod", "go.sum"})
	if err != nil {
		t.Fatalf("hash Go consumer inputs after local replacement change: %v", err)
	}
	if first == second {
		t.Fatal("Go consumer task hash unchanged after its local replacement module changed")
	}
}

func TestGlobalInputDigestTracksProvidedTrackedContentOnly(t *testing.T) {
	root := t.TempDir()
	lockfile := filepath.Join(root, "pnpm-lock.yaml")
	untracked := filepath.Join(root, "untracked.txt")
	mustWriteFile(t, lockfile, "lockfileVersion: 9\n")
	mustWriteFile(t, untracked, "first\n")

	first, err := GlobalInputDigest(root, []string{lockfile})
	if err != nil {
		t.Fatalf("global input digest: %v", err)
	}
	mustWriteFile(t, untracked, "second\n")
	second, err := GlobalInputDigest(root, []string{lockfile})
	if err != nil {
		t.Fatalf("global input digest after untracked change: %v", err)
	}
	if first != second {
		t.Fatal("global digest changed when only an untracked path changed")
	}
	mustWriteFile(t, lockfile, "lockfileVersion: 9\n# tracked edit\n")
	third, err := GlobalInputDigest(root, []string{lockfile})
	if err != nil {
		t.Fatalf("global input digest after tracked content change: %v", err)
	}
	if second == third {
		t.Fatal("global digest unchanged after tracked lockfile content changed")
	}
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
