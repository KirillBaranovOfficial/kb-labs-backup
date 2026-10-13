package cmd

import (
	"bytes"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"

	clikitcommand "github.com/kb-labs/clikit/command"
	"github.com/kb-labs/clikit/result"
	"github.com/spf13/cobra"
)

// case: S-203.1, S-203.2, S-203.6
func TestRegisteredCommandsUseSharedResultBoundary(t *testing.T) {
	commands := registeredCommands(rootCmd)
	if len(commands) == 0 {
		t.Fatal("kb-devkit command registry is empty")
	}

	safe := findCommand(rootCmd, "kb-devkit status")
	if safe == nil || safe.RunE == nil {
		t.Fatal("status must be registered as the safe read-only boundary fixture")
	}
	original := safe.RunE
	safe.RunE = func(*cobra.Command, []string) error {
		return result.Return(map[string]string{"fixture": "status"})
	}
	var success bytes.Buffer
	if code := clikitcommand.Execute(rootCmd, []string{"status", "--json"}, &success, &bytes.Buffer{}); code.Code() != 0 {
		t.Fatalf("safe read-only fixture exit = %d", code.Code())
	}
	if err := result.ValidateMachineResult(success.Bytes()); err != nil {
		t.Fatalf("safe read-only fixture envelope: %v; output=%s", err, success.String())
	}
	safe.RunE = original
	delete(safe.Annotations, "clikit.sharedResultBoundary")
	clikitcommand.InstallInputBoundary(rootCmd)

	for _, command := range commands {
		if !clikitcommand.BoundaryInstalled(command) {
			t.Errorf("registered command %q is not wrapped by the shared boundary", command.CommandPath())
			continue
		}
		path := strings.Fields(strings.TrimPrefix(command.CommandPath(), rootCmd.CommandPath()))
		var help bytes.Buffer
		if code := clikitcommand.Execute(rootCmd, append(path, "--help"), &help, &bytes.Buffer{}); code.Code() != 0 {
			t.Errorf("%s --help exit = %d", command.CommandPath(), code.Code())
		}

		var invalid bytes.Buffer
		args := append(append([]string{}, path...), "--json", "--kb-contract-invalid")
		if code := clikitcommand.Execute(rootCmd, args, &invalid, &bytes.Buffer{}); code.Code() != 2 {
			t.Errorf("%s invalid flag exit = %d, want 2", command.CommandPath(), code.Code())
		}
		if err := result.ValidateMachineResult(invalid.Bytes()); err != nil {
			t.Errorf("%s invalid flag envelope: %v; output=%s", command.CommandPath(), err, invalid.String())
		} else {
			var failure struct {
				OK    bool `json:"ok"`
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if err := json.Unmarshal(invalid.Bytes(), &failure); err != nil || failure.OK || failure.Error.Code != "KB_RUNTIME_INPUT_INVALID" {
				t.Errorf("%s invalid flag result = %s, want typed KB_RUNTIME_INPUT_INVALID", command.CommandPath(), invalid.String())
			}
		}
	}
}

// case: S-203.1
func TestCommandSourcesDoNotWriteTerminalOutputDirectly(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, filename := range files {
		if strings.HasSuffix(filename, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), filename, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := selector.X.(*ast.Ident)
			if ok && pkg.Name == "fmt" && strings.Contains(selector.Sel.Name, "Print") {
				t.Errorf("%s calls fmt.%s; route text through the shared result boundary", filename, selector.Sel.Name)
			}
			if ok && pkg.Name == "os" && selector.Sel.Name == "Exit" && filename != "root.go" {
				t.Errorf("%s exits outside the shared result boundary", filename)
			}
			if ok && pkg.Name == "json" && selector.Sel.Name == "NewEncoder" && len(call.Args) > 0 {
				if output, ok := call.Args[0].(*ast.SelectorExpr); ok {
					if owner, ok := output.X.(*ast.Ident); ok && owner.Name == "os" && output.Sel.Name == "Stdout" {
						t.Errorf("%s encodes JSON directly to os.Stdout", filename)
					}
				}
			}
			return true
		})
	}
}

func registeredCommands(root *cobra.Command) []*cobra.Command {
	commands := make([]*cobra.Command, 0)
	var visit func(*cobra.Command)
	visit = func(parent *cobra.Command) {
		for _, child := range parent.Commands() {
			if child.Run != nil || child.RunE != nil {
				commands = append(commands, child)
			}
			visit(child)
		}
	}
	visit(root)
	return commands
}

func findCommand(root *cobra.Command, path string) *cobra.Command {
	for _, command := range registeredCommands(root) {
		if command.CommandPath() == path {
			return command
		}
	}
	return nil
}
