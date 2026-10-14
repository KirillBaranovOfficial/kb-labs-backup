package artifacts

import (
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/kb-labs/create/v2/contracts"
)

func artifactCacheRootViolations(sources map[string]string, projections []string) []string {
	var violations []string
	for file, source := range sources {
		parsed, err := parser.ParseFile(token.NewFileSet(), file, source, 0)
		if err != nil {
			violations = append(violations, file+": parse source: "+err.Error())
			continue
		}
		ast.Inspect(parsed, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok || len(call.Args) < 2 {
				return true
			}
			function, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || function.Sel.Name != "Join" {
				return true
			}
			var parts []string
			for _, argument := range call.Args[1:] {
				ast.Inspect(argument, func(value ast.Node) bool {
					literal, ok := value.(*ast.BasicLit)
					if !ok || literal.Kind != token.STRING {
						return true
					}
					part, unquoteErr := strconv.Unquote(literal.Value)
					if unquoteErr == nil {
						parts = append(parts, part)
					}
					return true
				})
			}
			joined := filepath.ToSlash(filepath.Clean(filepath.Join(parts...)))
			for _, projection := range projections {
				projection = strings.Trim(filepath.ToSlash(projection), "/")
				if joined == projection || strings.HasPrefix(joined, projection+"/") {
					violations = append(violations, file+": constructs shared projection "+projection+" from a joined base path")
					break
				}
			}
			return true
		})
	}
	return violations
}

func TestArtifactClassesUseDeclaredSharedCacheRoot(t *testing.T) {
	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not locate artifact package sources")
	}
	packageDir := filepath.Dir(testFile)
	packageSources, err := build.Default.ImportDir(packageDir, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(packageSources.GoFiles) == 0 {
		t.Fatal("artifact package source discovery found no compiled production files")
	}
	sources := make(map[string]string, len(packageSources.GoFiles))
	for _, file := range packageSources.GoFiles {
		content, readErr := os.ReadFile(filepath.Join(packageDir, file))
		if readErr != nil {
			t.Fatal(readErr)
		}
		sources[file] = string(content)
	}
	projections := make([]string, 0, len(contracts.SharedRuntimeProjections()))
	for _, projection := range contracts.SharedRuntimeProjections() {
		projections = append(projections, projection.PlatformPath)
	}
	if violations := artifactCacheRootViolations(sources, projections); len(violations) > 0 {
		t.Fatalf("artifact cache path was derived from an artifact Root: %v", violations)
	}

	mutated := make(map[string]string, len(sources)+1)
	for file, source := range sources {
		mutated[file] = source
	}
	mutated["mutation.go"] = `package artifacts
import "path/filepath"
func mutation(stagePath string) string { return filepath.Join(stagePath, ".kb", "v2", "cache", "packages") }
`
	if violations := artifactCacheRootViolations(mutated, projections); len(violations) == 0 {
		t.Fatal("joined shared projection mutation was not detected")
	}
}
