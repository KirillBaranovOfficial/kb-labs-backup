package v2cli

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func artifactExecutorConstructionViolations(sources map[string]string) []string {
	var violations []string
	owners := make([]string, 0, 1)
	for file, source := range sources {
		if strings.Contains(source, "func newArtifactExecutor(") {
			owners = append(owners, file)
		}
	}
	if len(owners) != 1 {
		violations = append(violations, "package sources must define one newArtifactExecutor owner")
	}
	owner := ""
	if len(owners) == 1 {
		owner = owners[0]
	}
	for file, source := range sources {
		if strings.Contains(source, "artifacts.Composite{") && file != owner {
			violations = append(violations, file+": constructs artifacts.Composite outside newArtifactExecutor")
		}
		if strings.Contains(source, "artifacts.Binaries{") && file != owner {
			violations = append(violations, file+": constructs artifacts.Binaries outside newArtifactExecutor")
		}
	}
	return violations
}

func TestArtifactExecutorConstructionHasOneProductionOwner(t *testing.T) {
	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not locate command package source directory")
	}
	entries, err := os.ReadDir(filepath.Dir(testFile))
	if err != nil {
		t.Fatal(err)
	}
	sources := make(map[string]string)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		content, readErr := os.ReadFile(filepath.Join(filepath.Dir(testFile), entry.Name()))
		if readErr != nil {
			t.Fatal(readErr)
		}
		sources[entry.Name()] = string(content)
	}
	if violations := artifactExecutorConstructionViolations(sources); len(violations) != 0 {
		t.Fatalf("artifact executor construction escaped its owner: %v", violations)
	}

	mutated := make(map[string]string, len(sources)+1)
	for file, source := range sources {
		mutated[file] = source
	}
	mutated["another_command.go"] = "installer := artifacts.Composite{Packages: packages, Binaries: artifacts.Binaries{}}"
	if violations := artifactExecutorConstructionViolations(mutated); len(violations) == 0 {
		t.Fatal("a second artifacts.Composite/Binaries construction mutation was not detected")
	}
}
