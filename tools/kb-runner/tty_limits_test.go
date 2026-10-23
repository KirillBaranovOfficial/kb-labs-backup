//go:build darwin || linux

package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTTYLimitsFakeCLIReadsQuotaAndRejectsUnknownLayout(t *testing.T) {
	t.Run("colored wrapped quota screen", func(t *testing.T) {
		binary := fakeInteractiveCLI(t, `
printf '> '
IFS= read -r command || exit 0
printf '/stats - show model usage and quota\\n> '
IFS= read -r command || exit 0
printf '\033[32mWeekly\\nusage: 42%% reset in 3h 15m\\033[0m\\n> '
IFS= read -r command || exit 0
`)
		limits := readTTYLimits(context.Background(), "gemini", binary)
		if len(limits) != 1 {
			t.Fatalf("got %d limits: %#v", len(limits), limits)
		}
		if limits[0].Window != "week" || limits[0].UsedPercent != 42 || limits[0].Confidence != "vendor" || limits[0].Source != "tty" {
			t.Fatalf("unexpected limit: %#v", limits[0])
		}
		if limits[0].ResetsAt == nil || time.Until(*limits[0].ResetsAt) < 3*time.Hour {
			t.Fatalf("reset time missing or too early: %#v", limits[0].ResetsAt)
		}
	})
	t.Run("unrecognized layout is unavailable", func(t *testing.T) {
		binary := fakeInteractiveCLI(t, `
printf '> '
IFS= read -r command || exit 0
printf '/stats - show usage\\n> '
IFS= read -r command || exit 0
printf 'Background task progress: 42%% complete\\n> '
IFS= read -r command || exit 0
`)
		limits := readTTYLimits(context.Background(), "gemini", binary)
		if len(limits) != 1 || limits[0].Confidence != "unavailable" || limits[0].Error == "" {
			t.Fatalf("unknown layout was accepted: %#v", limits)
		}
	})
}

func TestDiscoverLimitCommandRequiresInSessionHelp(t *testing.T) {
	command, err := discoverLimitCommand("/help - show commands\n/stats - show model usage and quota")
	if err != nil || command != "/stats model" {
		t.Fatalf("discoverLimitCommand() = %q, %v", command, err)
	}
	if _, err := discoverLimitCommand("/help - show commands\n/clear - clear screen"); err == nil {
		t.Fatal("accepted help output with no quota command")
	}
}

func fakeInteractiveCLI(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-interactive-cli")
	script := "#!/bin/sh\n" + body + "\n"
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}
