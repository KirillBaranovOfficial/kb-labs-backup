package secretstate

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/kb-labs/clikit/diag"
)

func TestHostProjectionSuppressesCompletedBootstrapPassword(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret-state.json")
	record := NewRecord()
	record.SetRequirement(BootstrapPasswordEnvironment, "private-test-value")
	record.SetRequirement("APP_TOKEN", "other-private-test-value")
	record.MarkBootstrapCompleted("provisioned")
	data, err := Encode(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	projection, err := HostEnvironment(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, found := projection.Values[BootstrapPasswordEnvironment]; found {
		t.Fatal("completed bootstrap password was projected to the host")
	}
	if projection.Values["APP_TOKEN"] != "other-private-test-value" {
		t.Fatal("unrelated declared environment secret was not projected")
	}
	if len(projection.Suppressed) != 1 || projection.Suppressed[0] != BootstrapPasswordEnvironment {
		t.Fatalf("suppressed values = %v", projection.Suppressed)
	}
}

func TestUnknownAndMalformedRecordsReturnCataloguedDiagnosis(t *testing.T) {
	for name, data := range map[string][]byte{
		"unknown schema": []byte(`{"schema":99,"requirements":{},"bootstrap":{}}`),
		"malformed":      []byte(`{"schema":`),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Decode(data)
			if !errors.Is(err, ErrUnsupportedFormat) {
				t.Fatalf("decode error does not identify unsupported secret-state format: %v", err)
			}
			var failure *diag.Diag
			if !errors.As(err, &failure) {
				t.Fatalf("decode error = %T; want catalogued diagnostic", err)
			}
			catalog, ok := diag.LookupCatalog(UnsupportedCode)
			if !ok || failure.Code != UnsupportedCode || failure.Message != catalog.Message || failure.Hint != catalog.Hint {
				t.Fatalf("diagnostic = %#v; catalog entry = %#v", failure, catalog)
			}
		})
	}
}

func TestSecretStateSerializedSchemaHasOneGoOwner(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate source owner for schema rail")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", "..", ".."))
	ownerDir := filepath.Join(repoRoot, "tools", "clikit", "secretstate")
	var violations []string
	err := filepath.WalkDir(repoRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != repoRoot && (entry.Name() == ".git" || entry.Name() == "node_modules" || entry.Name() == "dist" || entry.Name() == "vendor") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasPrefix(filepath.Clean(path)+string(filepath.Separator), filepath.Clean(ownerDir)+string(filepath.Separator)) {
			return nil
		}
		parsed, parseErr := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if parseErr != nil {
			return parseErr
		}
		ast.Inspect(parsed, func(node ast.Node) bool {
			switch value := node.(type) {
			case *ast.StructType:
				fields := map[string]bool{}
				for _, field := range value.Fields.List {
					if field.Tag == nil {
						continue
					}
					tagText, unquoteErr := strconv.Unquote(field.Tag.Value)
					if unquoteErr != nil {
						continue
					}
					fieldName := strings.Split(reflect.StructTag(tagText).Get("json"), ",")[0]
					fields[fieldName] = true
				}
				lifecycleRecord := fields["bootstrapCompleted"] || fields["bootstrapPasswordOrigin"] || fields["bootstrapPasswordDisclosed"]
				secretStateShape := fields["schema"] && fields["requirements"] && fields["bootstrap"]
				if lifecycleRecord || secretStateShape {
					violations = append(violations, path+" declares serialized secret-state fields")
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) != 0 {
		t.Fatalf("secret-state serialized schema has non-owner declarations: %v", violations)
	}
}
