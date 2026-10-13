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

func TestRecordCommitRejectsAStalePreDisclosureSnapshot(t *testing.T) {
	base := NewRecord()
	base.SetBootstrapPassword("lifecycle-test-value", OriginGenerated)
	encoded, err := Encode(base)
	if err != nil {
		t.Fatal("encode starting record")
	}
	stale, err := Decode(encoded)
	if err != nil {
		t.Fatal("decode stale snapshot")
	}
	current, err := Decode(encoded)
	if err != nil {
		t.Fatal("decode current snapshot")
	}
	current.MarkBootstrapDisclosed()
	if !current.AdvanceFrom(base) {
		t.Fatal("current disclosure transition was refused")
	}
	stale.SetRequirement("unrelated.requirement", "unrelated-value")
	if stale.AdvanceFrom(current) {
		t.Fatal("stale pre-disclosure snapshot was allowed to overwrite the current record")
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

func TestSecretStateAccessHasOneRepositoryOwner(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate repository root for access rail")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", "..", ".."))
	violations, err := privateRecordReferences(repoRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) != 0 {
		t.Fatalf("private secret record path may only be named by its layout and shared owner; found references in %v", violations)
	}
}

func TestSecretStateAccessRailRejectsEverySourceLanguage(t *testing.T) {
	for relative, source := range map[string]string{
		"tools/kb-create/v2/other/map.go":          `json.Unmarshal(data, &map[string]any{}); open(".kb/v2/secret-state.json")`,
		"tools/kb-monitor/main.go":                 `json.Unmarshal(data, &map[string]any{}); open(".kb/v2/secret-state.json")`,
		"tools/kb-create/v2/other/raw.go":          `json.Unmarshal(data, &json.RawMessage{}); open(".kb/v2/secret-state.json")`,
		"e2e/journey/layout_reader.py":             `layout["sharedRuntimePaths"] and entry["id"] == "secretState"`,
		"services/gateway/app/src/private.test.ts": `readFileSync('.kb/v2/secret-state.json')`,
		"e2e/journey/private_steps.py":             `open('.kb/v2/secret-state.json')`,
		"scripts/private-check.sh":                 `cat .kb/v2/secret-state.json | jq .`,
	} {
		t.Run(relative, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, filepath.FromSlash(relative))
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
				t.Fatal(err)
			}
			violations, err := privateRecordReferences(root)
			if err != nil {
				t.Fatal(err)
			}
			if len(violations) != 1 || violations[0] != filepath.ToSlash(relative) {
				t.Fatalf("access rail violations = %v; want only %s", violations, relative)
			}
		})
	}
}

func privateRecordReferences(root string) ([]string, error) {
	var violations []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == ".git" || entry.Name() == "node_modules" || entry.Name() == "dist" {
				return filepath.SkipDir
			}
			return nil
		}
		extension := filepath.Ext(path)
		switch extension {
		case ".go", ".ts", ".tsx", ".mts", ".cts", ".js", ".jsx", ".mjs", ".cjs", ".py", ".sh", ".bash", ".zsh":
		default:
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if strings.HasPrefix(relative, "tools/clikit/secretstate/") || strings.HasPrefix(relative, "tools/kb-create/v2/contracts/") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(data), "secret-state.json") || strings.Contains(string(data), "secretState") {
			violations = append(violations, relative)
		}
		return nil
	})
	return violations, err
}
