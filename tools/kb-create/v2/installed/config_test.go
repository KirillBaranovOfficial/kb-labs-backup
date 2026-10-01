package installed

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfiguredPathsReturnsPresenceOnly(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".kb"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".kb", "kb.config.jsonc"), []byte(`{"plugin":{"url":"https://example.test"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	paths, err := ConfiguredPaths(root, []string{"/plugin/url", "/plugin/token"})
	if err != nil || !paths["/plugin/url"] || paths["/plugin/token"] {
		t.Fatalf("paths/error = %#v / %v", paths, err)
	}
}

func TestConfiguredPathsChecksEnvironmentReferencesWithoutReturningValues(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".kb"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".kb", "kb.config.jsonc"), []byte(`{"platform":{"adapterOptions":{"llm":{"apiKey":"${KB_DOCTOR_TEST_OPENAI_KEY}"}}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KB_DOCTOR_TEST_OPENAI_KEY", "present")
	paths, err := ConfiguredPaths(root, []string{"/platform/adapterOptions/llm/apiKey"})
	if err != nil || !paths["/platform/adapterOptions/llm/apiKey"] {
		t.Fatalf("paths/error = %#v / %v", paths, err)
	}
	t.Setenv("KB_DOCTOR_TEST_OPENAI_KEY", "")
	paths, err = ConfiguredPaths(root, []string{"/platform/adapterOptions/llm/apiKey"})
	if err != nil || paths["/platform/adapterOptions/llm/apiKey"] {
		t.Fatalf("unset reference paths/error = %#v / %v", paths, err)
	}
}
