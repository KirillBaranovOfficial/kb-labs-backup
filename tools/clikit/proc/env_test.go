//go:build darwin || linux

package proc

import (
	"reflect"
	"strings"
	"testing"
)

func TestCleanEnvironmentUsesOnlyConfiguredEntries(t *testing.T) {
	configured := []string{"HOME=/tmp/runner-home", "PATH=/usr/bin", "TERM=xterm-256color"}
	parent := []string{"CLAUDECODE=parent", "CLAUDE_CODE_MESSAGING_TOKEN=parent-secret", "PATH=/usr/local/bin", "NPM_CONFIG_TOKEN=private", keeperDispatchEnv + "=" + keeperDispatchValue}
	got := environmentFor(Config{Env: configured, CleanEnv: true}, parent)
	if !reflect.DeepEqual(got, configured) {
		t.Fatalf("clean child environment=%q want=%q", got, configured)
	}
}

func TestKeeperDispatchMarkerNeverReachesSupervisedEnvironment(t *testing.T) {
	configured := []string{"PATH=/usr/bin", keeperDispatchEnv + "=" + keeperDispatchValue}
	for _, cfg := range []Config{
		{Env: configured},
		{Env: configured, CleanEnv: true},
	} {
		got := environmentFor(cfg, []string{"PATH=/bin", keeperDispatchEnv + "=" + keeperDispatchValue})
		for _, entry := range got {
			if strings.HasPrefix(entry, keeperDispatchEnv+"=") {
				t.Fatalf("keeper dispatch marker leaked into supervised environment: %q", got)
			}
		}
	}
}
