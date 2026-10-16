//go:build (darwin || linux) && docker

package proc

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Run only with `go test -tags docker ./proc -run '^TestDockerComposeCleanupRemovesOnlyUniqueProject$'`.
func TestDockerComposeCleanupRemovesOnlyUniqueProject(t *testing.T) {
	id, err := newID()
	if err != nil {
		t.Fatal(err)
	}
	project := "x56-cleanup-" + strings.ToLower(strings.ReplaceAll(id, "_", "a"))
	prefix := resolveComposePrefix(t)
	composeFile := filepath.Join(t.TempDir(), "compose.yaml")
	compose := "services:\n  sleeper:\n    image: busybox:latest\n    command: [\"sh\", \"-c\", \"trap '' TERM; exec sleep 3600\"]\n"
	if err := os.WriteFile(composeFile, []byte(compose), 0o600); err != nil {
		t.Fatal(err)
	}
	resource := Resource{Kind: ResourceCompose, ID: project, Files: []string{composeFile}, ComposePrefix: prefix}
	cleaned := false
	t.Cleanup(func() {
		if !cleaned {
			if _, err := cleanupCompose(resource); err != nil {
				t.Errorf("clean isolated compose project after test: %v", err)
			}
		}
	})
	args := []string{"-p", project, "-f", composeFile, "up", "-d"}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	output, err := composeCommand(ctx, prefix, args...)
	cancel()
	if err != nil {
		t.Fatalf("start isolated compose project: %v: %s", err, strings.TrimSpace(string(output)))
	}
	steps, err := cleanupCompose(resource)
	if err != nil {
		t.Fatalf("clean isolated compose project: %v", err)
	}
	if len(steps) != 2 || steps[0].Name != "down" || steps[0].Outcome != ResourceRemoved || steps[1].Name != "label-sweep" || steps[1].Outcome != ResourceRemoved {
		t.Fatalf("real Compose cleanup outcomes = %+v", steps)
	}
	cleaned = true
	for _, args := range [][]string{
		{"ps", "-aq", "--filter", "label=com.docker.compose.project=" + project},
		{"network", "ls", "-q", "--filter", "label=com.docker.compose.project=" + project},
		{"volume", "ls", "-q", "--filter", "label=com.docker.compose.project=" + project},
	} {
		ctx, cancel = context.WithTimeout(context.Background(), 10*time.Second)
		remaining, err := dockerCommand(ctx, args...)
		cancel()
		if err != nil {
			t.Fatalf("verify isolated project cleanup (%s): %v: %s", strings.Join(args[:2], " "), err, strings.TrimSpace(string(remaining)))
		}
		if strings.TrimSpace(string(remaining)) != "" {
			t.Fatalf("isolated project left labeled resources (%s): %s", strings.Join(args[:2], " "), strings.TrimSpace(string(remaining)))
		}
	}
}

func resolveComposePrefix(t *testing.T) []string {
	t.Helper()
	const resolve = `if command docker compose version >/dev/null 2>&1; then printf 'docker\000compose'; elif command -v docker-compose >/dev/null 2>&1; then printf 'docker-compose'; else exit 127; fi`
	output, err := exec.Command("sh", "-c", resolve).Output()
	if err != nil {
		t.Fatalf("resolve Compose prefix using journey policy: %v", err)
	}
	var prefix []string
	for _, part := range strings.Split(string(output), "\x00") {
		if part != "" {
			prefix = append(prefix, part)
		}
	}
	if len(prefix) == 0 {
		t.Fatal("journey Compose policy resolved an empty command prefix")
	}
	return prefix
}
