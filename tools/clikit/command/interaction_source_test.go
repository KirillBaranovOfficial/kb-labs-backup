package command

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// case: S-203.12
func TestInteractiveIOUsesClikitChannelAcrossExecuteCallers(t *testing.T) {
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not locate source tree")
	}
	repository := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "../../.."))
	toolsRoot := filepath.Join(repository, "tools")
	fileSet := token.NewFileSet()
	callers := map[string]struct{}{}
	err := filepath.WalkDir(toolsRoot, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && (entry.Name() == "node_modules" || strings.HasPrefix(entry.Name(), ".")) {
			return filepath.SkipDir
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, parseErr := parser.ParseFile(fileSet, path, nil, 0)
		if parseErr != nil {
			return parseErr
		}
		commandAliases := map[string]bool{}
		osAliases := map[string]bool{}
		fmtAliases := map[string]bool{}
		for _, spec := range file.Imports {
			importPath, unquoteErr := strconv.Unquote(spec.Path.Value)
			if unquoteErr != nil {
				continue
			}
			alias := filepath.Base(importPath)
			if spec.Name != nil {
				alias = spec.Name.Name
			}
			switch importPath {
			case "github.com/kb-labs/clikit/command":
				commandAliases[alias] = true
			case "os":
				osAliases[alias] = true
			case "fmt":
				fmtAliases[alias] = true
			}
		}
		usesExecute := false
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || selector.Sel.Name != "Execute" {
				return true
			}
			owner, ok := selector.X.(*ast.Ident)
			if ok && commandAliases[owner.Name] {
				usesExecute = true
			}
			return true
		})
		if usesExecute {
			callers[filepath.Dir(path)] = struct{}{}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(callers) == 0 {
		t.Fatal("source scan found no clikitcommand.Execute callers")
	}

	for directory := range callers {
		entries, readErr := os.ReadDir(directory)
		if readErr != nil {
			t.Fatal(readErr)
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
				continue
			}
			path := filepath.Join(directory, entry.Name())
			file, parseErr := parser.ParseFile(fileSet, path, nil, 0)
			if parseErr != nil {
				t.Fatal(parseErr)
			}
			osAliases := map[string]bool{}
			fmtAliases := map[string]bool{}
			for _, spec := range file.Imports {
				importPath, unquoteErr := strconv.Unquote(spec.Path.Value)
				if unquoteErr != nil || importPath != "os" && importPath != "fmt" {
					continue
				}
				alias := filepath.Base(importPath)
				if spec.Name != nil {
					alias = spec.Name.Name
				}
				if importPath == "os" {
					osAliases[alias] = true
				} else {
					fmtAliases[alias] = true
				}
			}
			ast.Inspect(file, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				if function, ok := call.Fun.(*ast.SelectorExpr); ok {
					if owner, ok := function.X.(*ast.Ident); ok && fmtAliases[owner.Name] && strings.HasPrefix(function.Sel.Name, "Print") {
						t.Errorf("%s calls fmt.%s; route text through the clikit interaction channel", path, function.Sel.Name)
					}
					if stream, ok := function.X.(*ast.SelectorExpr); ok && (function.Sel.Name == "Read" || function.Sel.Name == "Write" || function.Sel.Name == "WriteString") {
						if owner, ok := stream.X.(*ast.Ident); ok && osAliases[owner.Name] && (stream.Sel.Name == "Stdin" || stream.Sel.Name == "Stdout" || stream.Sel.Name == "Stderr") {
							t.Errorf("%s calls os.%s.%s directly; use clikit interaction", path, stream.Sel.Name, function.Sel.Name)
						}
					}
				}
				if selector, ok := call.Fun.(*ast.SelectorExpr); ok && (selector.Sel.Name == "OutOrStdout" || selector.Sel.Name == "InOrStdin") {
					t.Errorf("%s calls %s; route prompts/progress through clikit interaction", path, selector.Sel.Name)
				}
				for _, argument := range call.Args {
					selector, ok := argument.(*ast.SelectorExpr)
					if !ok || selector.Sel.Name != "Stdout" && selector.Sel.Name != "Stderr" && selector.Sel.Name != "Stdin" {
						continue
					}
					owner, ok := selector.X.(*ast.Ident)
					if !ok || !osAliases[owner.Name] {
						continue
					}
					function, ok := call.Fun.(*ast.SelectorExpr)
					if ok && function.Sel.Name == "Execute" {
						continue
					}
					t.Errorf("%s passes os.%s to a command; use clikit interaction", path, selector.Sel.Name)
				}
				return true
			})
		}
	}
}
