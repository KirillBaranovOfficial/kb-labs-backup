package v2cli

import (
	"bytes"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/kb-labs/create/v2/contracts"
	"github.com/kb-labs/create/v2/secrets"
)

// case: S-222.11
func TestFirstAdminDisclosureMarkerFollowsSuccessfulResultWrite(t *testing.T) {
	root := t.TempDir()
	store := secrets.Store{PlatformRoot: root}
	const password = "handoff-test-password-sentinel"
	if err := store.Put(secrets.BootstrapCompleted, "provisioned"); err != nil {
		t.Fatal(err)
	}
	if err := store.Put("gateway.bootstrap.password", password); err != nil {
		t.Fatal(err)
	}
	value := applyCommandResult{
		OK:                  true,
		Operation:           "install",
		Receipt:             contracts.InstallReceipt{Plan: contracts.ResolvedInstallPlan{Request: contracts.InstallRequest{PlatformRoot: root}}},
		FirstAdmin:          &firstAdminCredential{State: firstAdminDisclosed, Email: "admin@example.test", Password: password},
		disclosureStoreRoot: root,
	}

	written, failure := emitFirstAdminResult(value, "install", func() error { return errors.New("closed output") })
	if written || failure == nil || failure.Code != contracts.CodeApplyFailed {
		t.Fatalf("failed output emission = written %v, diagnostic %#v; want typed output failure", written, failure)
	}
	if disclosed, err := store.Exists(secrets.BootstrapDisclosed); err != nil || disclosed {
		t.Fatalf("failed result write recorded disclosure = %v, err %v", disclosed, err)
	}

	var output bytes.Buffer
	written, failure = emitFirstAdminResult(value, "install", func() error {
		_, err := output.WriteString(password)
		return err
	})
	if !written || failure != nil || output.String() != password {
		t.Fatalf("successful output emission = written %v, failure %v, output %q", written, failure, output.String())
	}
	if disclosed, err := store.Exists(secrets.BootstrapDisclosed); err != nil || !disclosed {
		t.Fatalf("successful result write disclosure marker = %v, err %v; want recorded", disclosed, err)
	}
	for _, name := range []string{"gateway.bootstrap.password", secrets.BootstrapPasswordEnvironment} {
		if present, err := store.Exists(name); err != nil || present {
			t.Fatalf("disclosed bootstrap password entry %q present=%v, err=%v; want erased", name, present, err)
		}
	}
	assertSecretAbsentFromTree(t, root, password)
}

func assertSecretAbsentFromTree(t *testing.T, root, secret string) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.Contains(data, []byte(secret)) {
			t.Errorf("disclosed password remains in install-root file %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestBootstrapDisclosureMarkerHasOnePostWriteOwner(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	var writers, setterCallers []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Body == nil {
				continue
			}
			ast.Inspect(function.Body, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				if selector, ok := call.Fun.(*ast.SelectorExpr); ok && selector.Sel.Name == "MarkBootstrapDisclosed" {
					writers = append(writers, function.Name.Name)
				}
				if identifier, ok := call.Fun.(*ast.Ident); ok && identifier.Name == "setBootstrapDisclosed" {
					setterCallers = append(setterCallers, function.Name.Name)
				}
				return true
			})
		}
	}
	if !reflect.DeepEqual(writers, []string{"setBootstrapDisclosed"}) {
		t.Fatalf("atomic bootstrap disclosure owners = %v; want only setBootstrapDisclosed", writers)
	}
	if !reflect.DeepEqual(setterCallers, []string{"emitFirstAdminResult"}) {
		t.Fatalf("disclosure setter callers = %v; want only emitFirstAdminResult", setterCallers)
	}
}
