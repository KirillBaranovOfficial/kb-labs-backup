package contracts

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/kb-labs/clikit/diag"
)

func TestNewDiagUsesSharedCatalogWithoutSerializingRawCause(t *testing.T) {
	cause := os.ErrNotExist
	value := NewDiag(CodeInputRequired, "", "", cause)
	if value.Code != CodeInputRequired || value.Unwrap() != cause {
		t.Fatalf("diag = %#v; cause was not kept internal", value)
	}
	envelope, err := diag.ToEnvelope(value)
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := diag.LookupCatalog(CodeInputRequired)
	if !ok {
		t.Fatal("input-required code is absent from the generated shared catalog")
	}
	if envelope.Message != entry.Message || envelope.Hint != entry.Hint || envelope.Area != entry.Area || envelope.Stage != entry.Stage || envelope.Severity != entry.Severity || envelope.Retryable != entry.Retryable {
		t.Fatalf("envelope = %+v; shared catalog entry = %+v", envelope, entry)
	}
	data, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	if _, exists := fields["cause"]; exists {
		t.Fatalf("raw Go cause was serialized: %s", data)
	}
	for _, field := range []string{"area", "stage", "severity", "retryable", "message", "hint"} {
		if _, exists := fields[field]; !exists {
			t.Fatalf("canonical field %q missing: %s", field, data)
		}
	}
}

func TestEmittedCodesAreKnown(t *testing.T) {
	root := ".."
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		base := filepath.Base(path)
		if filepath.Ext(path) != ".go" || len(base) >= len("_test.go") && base[len(base)-len("_test.go"):] == "_test.go" {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		check := func(code string) {
			if _, ok := diag.LookupCatalog(code); !ok {
				t.Errorf("%s emits code %s missing from the generated shared catalog", path, code)
			}
		}
		ast.Inspect(file, func(node ast.Node) bool {
			switch value := node.(type) {
			case *ast.ValueSpec:
				for index, name := range value.Names {
					if len(name.Name) <= len("Code") || name.Name[:len("Code")] != "Code" || index >= len(value.Values) {
						continue
					}
					literal, ok := value.Values[index].(*ast.BasicLit)
					if !ok || literal.Kind != token.STRING {
						continue
					}
					code, err := strconv.Unquote(literal.Value)
					if err == nil {
						check(code)
					}
				}
			case *ast.CallExpr:
				if len(value.Args) == 0 {
					break
				}
				name := ""
				switch function := value.Fun.(type) {
				case *ast.Ident:
					name = function.Name
				case *ast.SelectorExpr:
					name = function.Sel.Name
				}
				if name != "NewDiag" && name != "New" && name != "Wrap" && name != "hostError" {
					break
				}
				literal, ok := value.Args[0].(*ast.BasicLit)
				if !ok || literal.Kind != token.STRING {
					break
				}
				code, err := strconv.Unquote(literal.Value)
				if err == nil && len(code) > len("KB_") && code[:len("KB_")] == "KB_" {
					check(code)
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
