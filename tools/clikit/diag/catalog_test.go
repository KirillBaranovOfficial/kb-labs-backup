package diag

import (
	"encoding/json"
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

var goErrorCode = regexp.MustCompile(`^KB_[A-Z][A-Z0-9]*(_[A-Z0-9]+)+$`)

// case: S-203.2, S-203.6
func TestGoErrorCodesExistInPlatformCatalog(t *testing.T) {
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

	toolsDir := filepath.Join(root, "tools")
	var files []string
	if err := filepath.WalkDir(toolsDir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		files = append(files, path)
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	emitted := make(map[string][]string)
	for _, path := range files {
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		add := func(literal ast.Expr) {
			value, ok := literal.(*ast.BasicLit)
			if !ok || value.Kind != token.STRING {
				return
			}
			code, err := strconv.Unquote(value.Value)
			if err == nil && goErrorCode.MatchString(code) {
				emitted[code] = append(emitted[code], path)
			}
		}
		ast.Inspect(file, func(node ast.Node) bool {
			switch typed := node.(type) {
			case *ast.KeyValueExpr:
				if key, ok := typed.Key.(*ast.Ident); ok && key.Name == "Code" {
					add(typed.Value)
				}
			case *ast.ValueSpec:
				for i, name := range typed.Names {
					if strings.HasPrefix(strings.ToLower(name.Name), "code") && i < len(typed.Values) {
						add(typed.Values[i])
					}
				}
			case *ast.AssignStmt:
				for i, left := range typed.Lhs {
					name, ok := left.(*ast.Ident)
					if ok && strings.HasPrefix(strings.ToLower(name.Name), "code") && i < len(typed.Rhs) {
						add(typed.Rhs[i])
					}
				}
			case *ast.CallExpr:
				name := ""
				switch function := typed.Fun.(type) {
				case *ast.Ident:
					name = function.Name
				case *ast.SelectorExpr:
					name = function.Sel.Name
				}
				if name == "New" || name == "Wrap" || name == "NewLauncherError" || name == "failure" {
					index := 0
					if name == "Wrap" {
						index = 1
					}
					if index < len(typed.Args) {
						add(typed.Args[index])
					}
				}
			}
			return true
		})
	}

	for code, locations := range emitted {
		if _, ok := entries[code]; !ok {
			relative := make([]string, 0, len(locations))
			for _, location := range locations {
				if value, err := filepath.Rel(root, location); err == nil {
					relative = append(relative, value)
				}
			}
			t.Errorf("Go tool emits uncatalogued code %s in %s", code, strings.Join(relative, ", "))
		}
	}
}
