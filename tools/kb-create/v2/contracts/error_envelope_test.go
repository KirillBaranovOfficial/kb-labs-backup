package contracts

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/kb-labs/clikit/diag"
)

const sharedDir = "../../../../core/platform/src/error-envelope"

type catalogFile struct {
	Codes []struct {
		Code      string     `json:"code"`
		Area      string     `json:"area"`
		Stage     ErrorStage `json:"stage"`
		Severity  string     `json:"severity"`
		Retryable bool       `json:"retryable"`
		Message   string     `json:"message"`
		Hint      string     `json:"hint"`
	} `json:"codes"`
}

func loadCatalog(t *testing.T) catalogFile {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(sharedDir, "errors.catalog.json"))
	if err != nil {
		t.Fatalf("read shared catalog: %v", err)
	}
	var catalog catalogFile
	if err := json.Unmarshal(data, &catalog); err != nil {
		t.Fatalf("decode shared catalog: %v", err)
	}
	return catalog
}

func TestNewDiagUsesSharedCatalogAndDoesNotSerializeCause(t *testing.T) {
	cause := os.ErrNotExist
	value := NewDiag(CodeInputRequired, "required input is missing", "pass --input", cause)
	if value.Code != CodeInputRequired || value.Hint != "pass --input" || value.Unwrap() != cause {
		t.Fatalf("diag = %#v; cause was not kept internal", value)
	}
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	if _, exists := fields["cause"]; exists {
		t.Fatalf("internal cause serialized: %s", data)
	}
}

func TestCatalogCodesAreUniqueAndMatchSharedClikitCatalog(t *testing.T) {
	catalog := loadCatalog(t)
	seen := map[string]bool{}
	for _, entry := range catalog.Codes {
		if seen[entry.Code] {
			t.Fatalf("duplicate catalog code %s", entry.Code)
		}
		seen[entry.Code] = true
		if entry.Hint == "" || entry.Message == "" {
			t.Fatalf("%s needs a message and hint", entry.Code)
		}
		shared, ok := diag.LookupCatalog(entry.Code)
		if !ok || shared.Area != string(entry.Area) || shared.Stage != string(entry.Stage) || shared.Severity != string(entry.Severity) || shared.Retryable != entry.Retryable || shared.Message != entry.Message || shared.Hint != entry.Hint {
			t.Fatalf("%s differs from shared clikit catalog: %+v", entry.Code, shared)
		}
	}
}

func TestEmittedCodesAreKnown(t *testing.T) {
	literal := regexp.MustCompile(`"(KB_INSTALL_[A-Z_]+)"`)
	for _, source := range []string{"../cmd/kb-create-v2/main.go", "../cmd/kb-create-release-index/main.go"} {
		data, err := os.ReadFile(source)
		if err != nil {
			t.Fatal(err)
		}
		for _, match := range literal.FindAllSubmatch(data, -1) {
			if _, ok := LookupCode(string(match[1])); !ok {
				t.Fatalf("%s uses unknown code %s", source, match[1])
			}
		}
	}
	for _, code := range []string{CodeIncompatibleComponents, CodeProviderUnresolved, CodeProviderAmbiguous, CodeInputRequired, CodeConfigRequired, CodeArtifactMismatch, CodeServiceGraphMismatch} {
		if _, ok := LookupCode(code); !ok {
			t.Fatalf("%s missing from shared catalog", code)
		}
	}
}
