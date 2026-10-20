package v2cli

import (
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"sort"
	"testing"
)

func TestDescriptorFlagsMatchFlagsReadByInputConstructors(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "root.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	descriptors := descriptorLiterals(t, file)
	reader := functionBody(file, "readDirectRequestAt")
	for _, descriptor := range operationDescriptors {
		literal := descriptors[descriptor.Name]
		if literal == nil {
			t.Fatalf("command %q has no descriptor literal", descriptor.Name)
		}
		handler := literalField(literal, "Handler")
		function, ok := handler.(*ast.FuncLit)
		if !ok {
			t.Fatalf("command %q Handler is not a function", descriptor.Name)
		}
		readFlags := make(map[string]struct{})
		ast.Inspect(function.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			if selector, ok := call.Fun.(*ast.SelectorExpr); ok {
				if identifier, ok := selector.X.(*ast.Ident); ok && identifier.Name == "flags" && len(call.Args) > 0 {
					if name, ok := call.Args[0].(*ast.BasicLit); ok {
						readFlags[unquoteFlag(name.Value)] = struct{}{}
					}
				}
			}
			if identifier, ok := call.Fun.(*ast.Ident); ok && identifier.Name == "readDirectRequest" {
				collectFlagReads(reader, readFlags, "platform-root")
			}
			if identifier, ok := call.Fun.(*ast.Ident); ok && identifier.Name == "readUpdateRequest" {
				collectFlagReads(reader, readFlags, "platform-root")
			}
			return true
		})
		declared := make(map[string]struct{}, len(descriptor.Flags))
		for _, name := range descriptor.Flags {
			declared[name] = struct{}{}
		}
		if !reflect.DeepEqual(readFlags, declared) {
			t.Errorf("command %q constructor reads flags %v; descriptor declares %v", descriptor.Name, sortedKeys(readFlags), sortedKeys(declared))
		}
	}
}

// case: S-039.1
func TestApplyDescriptorAcceptsAndReadsScenarioRequestFlags(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "root.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	literal := descriptorLiterals(t, file)["apply"]
	if literal == nil {
		t.Fatal("apply command has no descriptor literal")
	}
	declared := make(map[string]struct{})
	for _, flag := range stringSlice(t, literalField(literal, "Flags")) {
		declared[flag] = struct{}{}
	}
	for _, flag := range []string{"scenario", "scenario-answers"} {
		if _, ok := declared[flag]; !ok {
			t.Errorf("apply descriptor does not declare --%s", flag)
		}
	}
}

func stringSlice(t *testing.T, expression ast.Expr) []string {
	t.Helper()
	literal, ok := expression.(*ast.CompositeLit)
	if !ok {
		t.Fatal("Flags is not a composite literal")
	}
	values := make([]string, 0, len(literal.Elts))
	for _, element := range literal.Elts {
		value, ok := element.(*ast.BasicLit)
		if !ok {
			t.Fatal("flag is not a string literal")
		}
		values = append(values, unquoteFlag(value.Value))
	}
	return values
}

func descriptorLiterals(t *testing.T, file *ast.File) map[string]*ast.CompositeLit {
	t.Helper()
	descriptors := map[string]*ast.CompositeLit{}
	for _, declaration := range file.Decls {
		general, ok := declaration.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, spec := range general.Specs {
			value, ok := spec.(*ast.ValueSpec)
			if !ok || len(value.Names) == 0 || value.Names[0].Name != "operationDescriptors" {
				continue
			}
			list, ok := value.Values[0].(*ast.CompositeLit)
			if !ok {
				t.Fatal("operationDescriptors is not a composite literal")
			}
			for _, element := range list.Elts {
				literal, ok := element.(*ast.CompositeLit)
				if !ok {
					t.Fatal("operation descriptor is not a composite literal")
				}
				name, ok := literalField(literal, "Name").(*ast.BasicLit)
				if !ok {
					t.Fatal("operation descriptor has no literal name")
				}
				descriptors[unquoteFlag(name.Value)] = literal
			}
		}
	}
	return descriptors
}

func literalField(literal *ast.CompositeLit, name string) ast.Expr {
	for _, element := range literal.Elts {
		field, ok := element.(*ast.KeyValueExpr)
		if ok {
			if identifier, ok := field.Key.(*ast.Ident); ok && identifier.Name == name {
				return field.Value
			}
		}
	}
	return nil
}

func functionBody(file *ast.File, name string) *ast.BlockStmt {
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if ok && function.Name.Name == name {
			return function.Body
		}
	}
	return nil
}

func collectFlagReads(body *ast.BlockStmt, flags map[string]struct{}, platformRootFlag string) {
	ast.Inspect(body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		if identifier, ok := selector.X.(*ast.Ident); ok && identifier.Name == "flags" {
			if name, ok := call.Args[0].(*ast.BasicLit); ok {
				flags[unquoteFlag(name.Value)] = struct{}{}
			} else if name, ok := call.Args[0].(*ast.Ident); ok && name.Name == "platformRootFlag" {
				flags[platformRootFlag] = struct{}{}
			}
		}
		return true
	})
}

func sortedKeys(values map[string]struct{}) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func unquoteFlag(value string) string {
	if len(value) >= 2 {
		return value[1 : len(value)-1]
	}
	return value
}
