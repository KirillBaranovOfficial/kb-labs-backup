// Package catalogcheck scans Go sources for diagnostic code literals.
package catalogcheck

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var kbCodeLiteral = regexp.MustCompile(`^KB_[A-Z0-9_]+$`)

// These exact names are environment-variable keys, not diagnostic codes.
var nonDiagnosticKBNames = map[string]struct{}{
	"KB_CREATE_INTERNAL_STARTUP_DEADLINE_UNIX_NANO": {},
	"KB_CREATE_PROCESS_IDENTITY":                    {},
	"KB_DEPLOY_BASE_REF":                            {},
	"KB_DEVKIT_FAIL_LINES":                          {},
	"KB_DEV_INSTANCE":                               {},
	"KB_DEV_PROJECT_ID":                             {},
	"KB_DEV_PROJECT_ROOT":                           {},
	"KB_DEV_RUNTIME_DIR":                            {},
	"KB_DEV_SERVICE":                                {},
	"KB_ENV_HOME":                                   {},
	"KB_ENV_OFFSET":                                 {},
	"KB_ENV_SHELL":                                  {},
	"KB_HOME":                                       {},
	"KB_NET_OFFSET":                                 {},
	"KB_PLATFORM_ROOT":                              {},
	"KB_PROJECT_ROOT":                               {},
	"KB_SOCKET_HASH":                                {},
	"KB_SOCKET_PATH":                                {},
}

type SourceScan struct {
	Files []string
	Codes map[string][]string
}

func Scan(sourceRoot string) (SourceScan, error) {
	scan := SourceScan{Codes: make(map[string][]string)}
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
		source := filepath.Clean(path)
		scan.Files = append(scan.Files, source)
		ast.Inspect(file, func(node ast.Node) bool {
			literal, ok := node.(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING || isMapIndexKey(file, literal) {
				return true
			}
			code, err := strconv.Unquote(literal.Value)
			if err != nil || !kbCodeLiteral.MatchString(code) {
				return true
			}
			if _, envName := nonDiagnosticKBNames[code]; !envName {
				scan.Codes[code] = append(scan.Codes[code], source)
			}
			return true
		})
		return nil
	})
	return scan, err
}

func Uncatalogued(codes map[string][]string, entries map[string]struct{}) map[string][]string {
	missing := make(map[string][]string)
	for code, locations := range codes {
		if _, ok := entries[code]; !ok {
			missing[code] = locations
		}
	}
	return missing
}

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
