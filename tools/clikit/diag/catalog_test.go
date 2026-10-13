package diag

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kb-labs/clikit/diag/catalogcheck"
)

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
			Code             string           `json:"code"`
			HTTPStatus       int              `json:"httpStatus,omitempty"`
			Area             string           `json:"area"`
			Stage            string           `json:"stage"`
			Severity         string           `json:"severity"`
			Retryable        bool             `json:"retryable"`
			ProcessExitClass ProcessExitClass `json:"processExitClass"`
			Message          string           `json:"message"`
			Hint             string           `json:"hint"`
		} `json:"codes"`
	}
	if err := json.Unmarshal(content, &catalog); err != nil {
		t.Fatal(err)
	}
	entries := make(map[string]CatalogEntry, len(catalog.Codes))
	entryCodes := make(map[string]struct{}, len(catalog.Codes))
	for _, entry := range catalog.Codes {
		if entry.Area == "" || entry.Severity == "" || entry.Message == "" || entry.Hint == "" ||
			(entry.ProcessExitClass != ExitClassInputRefusal && entry.ProcessExitClass != ExitClassOperationFailure) {
			t.Errorf("catalog entry %s is missing required metadata or process exit class", entry.Code)
		}
		entries[entry.Code] = CatalogEntry(entry)
		entryCodes[entry.Code] = struct{}{}
		if embedded, ok := LookupCatalog(entry.Code); !ok || embedded != CatalogEntry(entry) {
			t.Errorf("clikit metadata for %s differs from the platform catalog", entry.Code)
		}
	}
	if len(catalogEntries) != len(entries) {
		t.Fatalf("generated Go catalog has %d entries; platform catalog has %d", len(catalogEntries), len(entries))
	}

	scan, err := catalogcheck.Scan(filepath.Join(root, "tools"))
	if err != nil {
		t.Fatal(err)
	}
	missing := catalogcheck.Uncatalogued(scan.Codes, entryCodes)
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

func TestGoToolsDoNotAssignProcessExitStatusOutsideDiag(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	ownerDir, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	toolsDir := filepath.Join(root, "tools")
	check := func(source string) bool {
		file, err := parser.ParseFile(token.NewFileSet(), "fixture.go", source, 0)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		ast.Inspect(file, func(node ast.Node) bool {
			switch value := node.(type) {
			case *ast.SelectorExpr:
				if value.Sel.Name == "ProcessExitCode" || value.Sel.Name == "ProcessExitClass" || value.Sel.Name == "WithProcessExitCode" {
					found = true
				}
			case *ast.KeyValueExpr:
				if key, ok := value.Key.(*ast.Ident); ok && (key.Name == "ProcessExitCode" || key.Name == "ProcessExitClass") {
					found = true
				}
			}
			return true
		})
		return found
	}
	if !check("package p\nfunc f() { failure.ProcessExitClass = \"input-refusal\" }\n") {
		t.Fatal("rail mutation was not detected")
	}
	err = filepath.WalkDir(toolsDir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() && (entry.Name() == "vendor" || entry.Name() == "node_modules") {
			return filepath.SkipDir
		}
		if entry.IsDir() {
			absolute, _ := filepath.Abs(path)
			if absolute == ownerDir {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}
		source, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if check(string(source)) {
			relative, _ := filepath.Rel(root, path)
			t.Errorf("%s assigns or constructs a process exit status outside the diag owner", relative)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
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
	scan, err := catalogcheck.Scan(filepath.Dir(filepath.Dir(toolsDir)))
	if err != nil {
		t.Fatal(err)
	}
	if missing := catalogcheck.Uncatalogued(scan.Codes, map[string]struct{}{}); len(missing) != 1 {
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
	scan, err := catalogcheck.Scan(filepath.Dir(filepath.Dir(toolsDir)))
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := scan.Codes["KB_INSTALLATION_ID"]; exists {
		t.Fatalf("map index key was reported as a diagnostic code: %v", scan.Codes)
	}
}
