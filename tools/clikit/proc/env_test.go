//go:build darwin || linux

package proc

import (
	"reflect"
	"testing"
)

func TestCleanEnvironmentUsesOnlyConfiguredEntries(t *testing.T) {
	configured := []string{"HOME=/tmp/runner-home", "PATH=/usr/bin", "TERM=xterm-256color"}
	parent := []string{"CLAUDECODE=parent", "CLAUDE_CODE_MESSAGING_TOKEN=parent-secret", "PATH=/usr/local/bin", "NPM_CONFIG_TOKEN=private"}
	got := environmentFor(Config{Env: configured, CleanEnv: true}, parent)
	if !reflect.DeepEqual(got, configured) {
		t.Fatalf("clean child environment=%q want=%q", got, configured)
	}
}
