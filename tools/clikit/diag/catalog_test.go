package diag

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
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

func TestPrimaryCLIsExitOnlyAtTheirTypedMainBoundary(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	toolRoots := []string{"kb-create", "kb-dev", "kb-devkit", "kb-deploy", "kb-monitor"}
	testedMainPaths := map[string]struct{}{}
	for _, tool := range toolRoots {
		toolRoot := filepath.Join(root, "tools", tool)
		mainPath := filepath.Join(toolRoot, "main.go")
		testedMainPaths[filepath.Clean(mainPath)] = struct{}{}
		mainSource, err := os.ReadFile(mainPath)
		if err != nil {
			t.Fatal(err)
		}
		mainFile, err := parser.ParseFile(token.NewFileSet(), mainPath, mainSource, 0)
		if err != nil {
			t.Fatal(err)
		}
		mainExits := 0
		ast.Inspect(mainFile, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok || !isExitCall(call) {
				return true
			}
			mainExits++
			if len(call.Args) != 1 || !isCodeMethod(call.Args[0]) {
				t.Errorf("%s process boundary must exit with the typed Execute status Code()", mainPath)
			}
			return true
		})
		if mainExits != 1 {
			t.Errorf("%s has %d process exits, want exactly one typed boundary", mainPath, mainExits)
		}

		commandDir := filepath.Join(toolRoot, "cmd")
		if tool == "kb-create" {
			commandDir = filepath.Join(toolRoot, "v2", "cmd", "kb-create-v2")
		}
		err = filepath.WalkDir(commandDir, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() && (entry.Name() == "vendor" || entry.Name() == "node_modules") {
				return filepath.SkipDir
			}
			if entry.IsDir() || filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			source, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			file, err := parser.ParseFile(token.NewFileSet(), path, source, 0)
			if err != nil {
				return err
			}
			ast.Inspect(file, func(node ast.Node) bool {
				if call, ok := node.(*ast.CallExpr); ok && isExitCall(call) {
					t.Errorf("%s bypasses the typed process boundary", path)
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	// Discover additional user-facing command binaries inside those modules by
	// their use of the shared diagnostic package. Their main boundary must obey
	// the same typed exit contract as the primary commands.
	for _, tool := range toolRoots {
		toolRoot := filepath.Join(root, "tools", tool)
		err := filepath.WalkDir(toolRoot, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() && (entry.Name() == "vendor" || entry.Name() == "node_modules") {
				return filepath.SkipDir
			}
			if entry.IsDir() || filepath.Base(path) != "main.go" {
				return nil
			}
			if _, tested := testedMainPaths[filepath.Clean(path)]; tested {
				return nil
			}
			mainSource, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			mainFile, err := parser.ParseFile(token.NewFileSet(), path, mainSource, 0)
			if err != nil {
				return err
			}
			if mainFile.Name.Name != "main" {
				return nil
			}
			usesDiag := false
			for _, imported := range mainFile.Imports {
				if strings.Trim(imported.Path.Value, "\"") == "github.com/kb-labs/clikit/diag" {
					usesDiag = true
				}
			}
			if !usesDiag {
				return nil
			}
			exits := 0
			ast.Inspect(mainFile, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok || !isExitCall(call) {
					return true
				}
				exits++
				if len(call.Args) != 1 || !isCodeMethod(call.Args[0]) {
					t.Errorf("%s process boundary must exit with the typed Execute status Code()", path)
				}
				return true
			})
			if exits != 1 {
				t.Errorf("%s has %d process exits, want exactly one typed boundary", path, exits)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	if !hasExitBypass("package command\nfunc run() { os.Exit(1) }\n") {
		t.Fatal("os.Exit in a command handler mutation was not detected")
	}
}

func isExitCall(call *ast.CallExpr) bool {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || (selector.Sel.Name != "Exit") {
		return false
	}
	packageName, ok := selector.X.(*ast.Ident)
	return ok && (packageName.Name == "os" || packageName.Name == "syscall")
}

func isCodeMethod(node ast.Expr) bool {
	call, ok := node.(*ast.CallExpr)
	if !ok || len(call.Args) != 0 {
		return false
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	return ok && selector.Sel.Name == "Code"
}

func hasExitBypass(source string) bool {
	file, err := parser.ParseFile(token.NewFileSet(), "fixture.go", source, 0)
	if err != nil {
		return false
	}
	found := false
	ast.Inspect(file, func(node ast.Node) bool {
		if call, ok := node.(*ast.CallExpr); ok && isExitCall(call) {
			found = true
		}
		return true
	})
	return found
}

func TestExitStatusTypeRejectsIntegerAndWrappedLiteralMutations(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	fixtures := []string{
		`package mutation
import ("github.com/kb-labs/clikit/diag"; "github.com/kb-labs/clikit/result")
func chosen(d *diag.Diag) int { if d.Code != "KB_INSTALL_GATEWAY_INDEX_UNAVAILABLE" { return 0 }; return 2 }
func bad() { _ = result.StreamComplete(chosen(diag.New("KB_INSTALL_GATEWAY_INDEX_UNAVAILABLE", "bad"))) }`,
		`package mutation
import "github.com/kb-labs/clikit/result"
func bad() { _ = result.StreamComplete(2) }`,
		`package mutation
import "github.com/kb-labs/clikit/result"
func leaf() int { return 2 }
func middle() int { return leaf() }
func bad() { _ = result.StreamComplete(middle()) }`,
	}
	for index, source := range fixtures {
		dir, err := os.MkdirTemp(root, "exit-status-mutation-")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(dir) })
		if err := os.WriteFile(filepath.Join(dir, "mutation.go"), []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
		command := exec.Command("go", "test", "./"+filepath.Base(dir))
		command.Dir = root
		if output, err := command.CombinedOutput(); err == nil {
			t.Errorf("mutation fixture %d compiled; typed exit status accepted a literal: %s", index+1, output)
		} else if !strings.Contains(string(output), "diag.ExitStatus") {
			t.Errorf("mutation fixture %d failed for a reason unrelated to the typed status boundary: %s", index+1, output)
		}
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
