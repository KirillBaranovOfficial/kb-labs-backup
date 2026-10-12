package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	clikitcommand "github.com/kb-labs/clikit/command"
)

// case: S-179.18
func TestRunJSONReportsCachedFailureAsFailure(t *testing.T) {
	root := t.TempDir()
	writeRunFixture(t, filepath.Join(root, "package.json"), `{"workspaces":["packages/*"]}`)
	writeRunFixture(t, filepath.Join(root, "packages", "app", "package.json"), `{"name":"@kb/app"}`)
	config := "workspace:\n" +
		"  categories:\n" +
		"    libraries:\n" +
		"      match: [\"packages/*\"]\n" +
		"      preset: node-lib\n" +
		"tasks:\n" +
		"  build:\n" +
		"    - command: 'sh -c \"test -f ../dependency.ready\"'\n" +
		"      inputs: [\"src/**\"]\n" +
		"      outputs: [\"dist/**\"]\n"
	writeRunFixture(t, filepath.Join(root, "devkit.yaml"), config)

	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatalf("change working directory: %v", err)
	}
	originalRunPackages := runPackages
	originalRunAffected := runAffected
	originalRunDiffOnly := runDiffOnly
	originalConfigPath := configPath
	originalJSONMode := jsonMode
	t.Cleanup(func() {
		if err := os.Chdir(originalDir); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
		runPackages = originalRunPackages
		runAffected = originalRunAffected
		runDiffOnly = originalRunDiffOnly
		configPath = originalConfigPath
		jsonMode = originalJSONMode
	})

	invoke := func() (int, []byte) {
		t.Helper()
		runPackages = nil
		var stdout, stderr bytes.Buffer
		code := clikitcommand.Execute(rootCmd, []string{"run", "build", "--packages", "@kb/app", "--json"}, &stdout, &stderr)
		if stderr.Len() != 0 {
			t.Fatalf("unexpected stderr: %s", stderr.String())
		}
		return code, stdout.Bytes()
	}
	assertFailure := func() {
		t.Helper()
		code, output := invoke()
		var payload struct {
			OK    bool `json:"ok"`
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal(output, &payload); err != nil {
			t.Fatalf("decode failure JSON %q: %v", output, err)
		}
		if code != 1 || payload.OK || payload.Error.Code != "KB_RUNTIME_COMMAND_FAILED" {
			t.Fatalf("failure result: exit=%d JSON=%s", code, output)
		}
	}

	assertFailure()
}

// case: S-179.20, S-179.21, S-179.22
func TestBuiltBinaryRunsUnfilteredBuildAcrossWorkspace(t *testing.T) {
	root := t.TempDir()
	writeRunFixture(t, filepath.Join(root, "package.json"), `{"workspaces":["packages/*"]}`)
	writeRunFixture(t, filepath.Join(root, "packages", "one", "package.json"), `{"name":"@fixture/one"}`)
	writeRunFixture(t, filepath.Join(root, "packages", "two", "package.json"), `{"name":"@fixture/two"}`)
	writeRunFixture(t, filepath.Join(root, "devkit.yaml"), "workspace:\n"+
		"  categories:\n"+
		"    fixture:\n"+
		"      match: [\"packages/*\"]\n"+
		"tasks:\n"+
		"  build:\n"+
		"    - command: 'true'\n")
	writeRunFixture(t, filepath.Join(root, ".gitignore"), ".kb/\n")
	for _, args := range [][]string{
		{"init", "--quiet"},
		{"add", "--all"},
		{"-c", "user.name=Fixture", "-c", "user.email=fixture@example.test", "commit", "--quiet", "-m", "fixture"},
	} {
		git := exec.Command("git", args...)
		git.Dir = root
		if output, err := git.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}

	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate command test source")
	}
	moduleRoot := filepath.Dir(filepath.Dir(source))
	binary := filepath.Join(t.TempDir(), "kb-devkit")
	build := exec.Command("go", "build", "-o", binary, ".")
	build.Dir = moduleRoot
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build kb-devkit: %v\n%s", err, output)
	}

	isolatedHome := filepath.Join(t.TempDir(), "home")
	if err := os.MkdirAll(isolatedHome, 0o755); err != nil {
		t.Fatalf("create isolated HOME: %v", err)
	}
	command := exec.Command(binary, "run", "build", "--no-cache", "--json")
	command.Dir = root
	command.Env = append(os.Environ(), "HOME="+isolatedHome)
	output, err := command.Output()
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			t.Fatalf("built binary failed: %v\nstdout: %s\nstderr: %s", err, output, exit.Stderr)
		}
		t.Fatalf("run built binary: %v", err)
	}

	var payload struct {
		Data struct {
			Results []struct {
				Package string `json:"package"`
				OK      bool   `json:"ok"`
			} `json:"results"`
			Summary struct {
				Total  int `json:"total"`
				Failed int `json:"failed"`
			} `json:"summary"`
		} `json:"data"`
	}
	if err := json.Unmarshal(output, &payload); err != nil {
		t.Fatalf("decode built binary output %q: %v", output, err)
	}
	if payload.Data.Summary.Total != 2 || payload.Data.Summary.Failed != 0 || len(payload.Data.Results) != 2 {
		t.Fatalf("unfiltered build summary/results = %+v, want two successful package results", payload.Data)
	}
	results := make(map[string]bool, len(payload.Data.Results))
	for _, result := range payload.Data.Results {
		results[result.Package] = result.OK
	}
	if len(results) != 2 || !results["@fixture/one"] || !results["@fixture/two"] {
		t.Fatalf("unfiltered build results = %+v, want successful @fixture/one and @fixture/two", payload.Data.Results)
	}

	noChanges := exec.Command(binary, "run", "build", "--affected", "--diff-only", "--json")
	noChanges.Dir = root
	noChanges.Env = append(os.Environ(), "HOME="+isolatedHome)
	noChangesOutput, err := noChanges.Output()
	if err != nil {
		t.Fatalf("built binary --affected with empty change set: %v\n%s", err, noChangesOutput)
	}
	if !strings.Contains(string(noChangesOutput), "No affected or dirty packages") {
		t.Fatalf("empty affected result = %s, want documented no-op", noChangesOutput)
	}

	for _, input := range []struct {
		args    []string
		message string
		hint    string
	}{
		{
			args:    []string{"run", "build", "--packages", "@missing/package", "--json"},
			message: "No workspace packages match",
			hint:    "kb-devkit deps",
		},
		{
			args:    []string{"run", "missing-task", "--json"},
			message: "Task \"missing-task\" is not declared in devkit.yaml.",
			hint:    "kb-devkit run build",
		},
	} {
		command := exec.Command(binary, input.args...)
		command.Dir = root
		command.Env = append(os.Environ(), "HOME="+isolatedHome)
		output, err := command.Output()
		exit, ok := err.(*exec.ExitError)
		if !ok || exit.ExitCode() != 2 {
			t.Fatalf("built binary %v exit = %v; stdout=%s", input.args, err, output)
		}
		if strings.Contains(string(exit.Stderr), "stack") {
			t.Fatalf("built binary %v printed a stack: %s", input.args, exit.Stderr)
		}
		var failure struct {
			OK    bool `json:"ok"`
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
				Hint    string `json:"hint"`
			} `json:"error"`
		}
		if err := json.Unmarshal(output, &failure); err != nil {
			t.Fatalf("decode %v output %q: %v", input.args, output, err)
		}
		if failure.OK || failure.Error.Code != "KB_RUNTIME_INPUT_INVALID" ||
			!strings.Contains(failure.Error.Message, input.message) || !strings.Contains(failure.Error.Hint, input.hint) {
			t.Fatalf("built binary %v diagnosis = %+v, want catalogued actionable input error", input.args, failure)
		}
	}
}

func writeRunFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
