package diag

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

var kbCodeLiteral = regexp.MustCompile(`^KB_[A-Z0-9_]+$`)

// These exact names are environment-variable keys, not diagnostic codes. Keep
// the allowlist explicit so a new KB_ string cannot bypass catalog coverage.
var nonDiagnosticKBNames = map[string]struct{}{
	"KB_DEPLOY_BASE_REF":   {},
	"KB_DEVKIT_FAIL_LINES": {},
	"KB_DEV_INSTANCE":      {},
	"KB_DEV_PROJECT_ID":    {},
	"KB_DEV_PROJECT_ROOT":  {},
	"KB_DEV_RUNTIME_DIR":   {},
	"KB_DEV_SERVICE":       {},
	"KB_ENV_HOME":          {},
	"KB_ENV_OFFSET":        {},
	"KB_ENV_SHELL":         {},
	"KB_HOME":              {},
	"KB_NET_OFFSET":        {},
	"KB_PLATFORM_ROOT":     {},
	"KB_PROJECT_ROOT":      {},
	"KB_SOCKET_HASH":       {},
	"KB_SOCKET_PATH":       {},
}

// case: S-203.2, S-203.6
func TestKBCodeLiteralsExistInPlatformCatalog(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	catalogPath := filepath.Join(root, "core", "platform", "src", "error-envelope", "errors.catalog.json")
	content, err := os.ReadFile(catalogPath)
	if err != nil {
		t.Fatal(err)
	}
	var catalog struct {
		Codes []struct {
			Code      string `json:"code"`
			Area      string `json:"area"`
			Stage     string `json:"stage"`
			Severity  string `json:"severity"`
			Retryable bool   `json:"retryable"`
			Message   string `json:"message"`
			Hint      string `json:"hint"`
		} `json:"codes"`
	}
	if err := json.Unmarshal(content, &catalog); err != nil {
		t.Fatal(err)
	}
	entries := make(map[string]CatalogEntry, len(catalog.Codes))
	for _, entry := range catalog.Codes {
		if entry.Area == "" || entry.Severity == "" || entry.Message == "" || entry.Hint == "" {
			t.Errorf("catalog entry %s is missing area, severity, message, or hint", entry.Code)
		}
		entries[entry.Code] = CatalogEntry(entry)
		if embedded, ok := LookupCatalog(entry.Code); !ok || embedded != CatalogEntry(entry) {
			t.Errorf("clikit metadata for %s differs from the platform catalog", entry.Code)
		}
	}
	if len(catalogEntries) != len(entries) {
		t.Fatalf("generated Go catalog has %d entries; platform catalog has %d", len(catalogEntries), len(entries))
	}

	emitted, err := scanKBCodeLiterals(filepath.Join(root, "tools"))
	if err != nil {
		t.Fatal(err)
	}
	missing := uncataloguedKBCodeLiterals(emitted, entries)
	for code, locations := range missing {
		relative := make([]string, 0, len(locations))
		for _, location := range locations {
			if value, err := filepath.Rel(root, location); err == nil {
				relative = append(relative, value)
			}
		}
		t.Errorf("Go tool emits uncatalogued code %s in %s", code, strings.Join(relative, ", "))
	}
}

func scanKBCodeLiterals(sourceRoot string) (map[string][]string, error) {
	emitted := make(map[string][]string)
	err := filepath.WalkDir(sourceRoot, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if path != sourceRoot && (entry.Name() == "e2e" || entry.Name() == "vendor") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || filepath.Base(path) == "catalog_gen.go" {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
		ast.Inspect(file, func(node ast.Node) bool {
			literal, ok := node.(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				return true
			}
			if isMapIndexKey(file, literal) {
				return true
			}
			code, err := strconv.Unquote(literal.Value)
			if err != nil || !kbCodeLiteral.MatchString(code) {
				return true
			}
			if _, envName := nonDiagnosticKBNames[code]; !envName {
				emitted[code] = append(emitted[code], path)
			}
			return true
		})
		return nil
	})
	return emitted, err
}

// isMapIndexKey excludes string literals used to read or write map entries.
// Environment variable names commonly have the KB_ prefix but are not
// diagnostic codes; diagnostics are emitted as values or constructor args.
func isMapIndexKey(file *ast.File, target *ast.BasicLit) bool {
	found := false
	ast.Inspect(file, func(node ast.Node) bool {
		index, ok := node.(*ast.IndexExpr)
		if ok && index.Index == target {
			found = true
			return false
		}
		return !found
	})
	return found
}

func uncataloguedKBCodeLiterals(emitted map[string][]string, entries map[string]CatalogEntry) map[string][]string {
	missing := make(map[string][]string)
	for code, locations := range emitted {
		if _, ok := entries[code]; !ok {
			missing[code] = locations
		}
	}
	return missing
}

func TestCatalogScannerRejectsUncataloguedFixtureLiteral(t *testing.T) {
	toolsDir := filepath.Join(t.TempDir(), "tools", "fixture")
	if err := os.MkdirAll(toolsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	fixture := filepath.Join(toolsDir, "fixture.go")
	if err := os.WriteFile(fixture, []byte("package fixture\nconst code = \"KB_FIXTURE_404\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	emitted, err := scanKBCodeLiterals(filepath.Dir(filepath.Dir(toolsDir)))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := nonDiagnosticKBNames["KB_FIXTURE_404"]; ok {
		t.Fatal("fixture literal unexpectedly appears in environment-name allowlist")
	}
	if missing := uncataloguedKBCodeLiterals(emitted, map[string]CatalogEntry{}); len(missing) != 1 {
		t.Fatalf("uncatalogued fixture should fail catalog check; got missing codes %v", missing)
	} else if _, ok := missing["KB_FIXTURE_404"]; !ok {
		t.Fatalf("uncatalogued fixture was not rejected: %v", missing)
	}
}

func TestCatalogScannerIgnoresKBMapIndexKeys(t *testing.T) {
	toolsDir := filepath.Join(t.TempDir(), "tools", "fixture")
	if err := os.MkdirAll(toolsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	fixture := filepath.Join(toolsDir, "fixture.go")
	if err := os.WriteFile(fixture, []byte("package fixture\nvar env = map[string]string{}\nfunc set() { env[\"KB_INSTALLATION_ID\"] = \"id\" }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	emitted, err := scanKBCodeLiterals(filepath.Dir(filepath.Dir(toolsDir)))
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := emitted["KB_INSTALLATION_ID"]; exists {
		t.Fatalf("map index key was reported as a diagnostic code: %v", emitted)
	}
}
