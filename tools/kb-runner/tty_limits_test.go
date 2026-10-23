//go:build darwin || linux

package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
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
		limits := readTTYLimitsDetailed(context.Background(), "gemini", binary).Limits
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
		limits := readTTYLimitsDetailed(context.Background(), "gemini", binary).Limits
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

func TestPTYTerminalHandshakeAnswersDSRAndSetsWindowSize(t *testing.T) {
	t.Setenv("CLAUDE_CODE_MESSAGING_TOKEN", "parent-session-secret")
	t.Setenv("CLAUDECODE", "parent-session-secret")
	master, slave, err := openPTY()
	if err != nil {
		t.Fatal(err)
	}
	defer master.Close()
	defer slave.Close()
	size, err := unix.IoctlGetWinsize(int(slave.Fd()), unix.TIOCGWINSZ)
	if err != nil {
		t.Fatal(err)
	}
	if size.Row != ttyScreenRows || size.Col != ttyScreenCols {
		t.Fatalf("PTY size=%dx%d want=%dx%d", size.Row, size.Col, ttyScreenRows, ttyScreenCols)
	}

	responsePath := filepath.Join(t.TempDir(), "dsr-response")
	quotedPath := "'" + strings.ReplaceAll(responsePath, "'", "'\\''") + "'"
	envPath := filepath.Join(t.TempDir(), "gemini-environment")
	quotedEnvPath := "'" + strings.ReplaceAll(envPath, "'", "'\\''") + "'"
	binary := fakeInteractiveCLI(t, `
/usr/bin/env > `+quotedEnvPath+`
stty raw -echo
printf '\033[6n'
response=$(dd bs=1 count=6 2>/dev/null)
stty sane
printf '%s' "$response" > `+quotedPath+`
printf '> '
IFS= read -r command || exit 0
printf '/usage - show usage\n> '
IFS= read -r command || exit 0
printf '5h usage: 25%% reset in 2h\n> '
IFS= read -r command || exit 0
exit 0
`)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	result := probeTTY(ctx, "gemini", binary)
	if result.Err != nil {
		t.Fatalf("TTY handshake failed: %v screen=%q steps=%+v", result.Err, result.Screen, result.Steps)
	}
	response, err := os.ReadFile(responsePath)
	if err != nil || string(response) != "\x1b[1;1R" {
		t.Fatalf("fake CLI did not receive the DSR response: bytes=%q err=%v", response, err)
	}
	childEnv, err := os.ReadFile(envPath)
	if err != nil || strings.Contains(string(childEnv), "CLAUDE") || strings.Contains(string(childEnv), "parent-session-secret") {
		t.Fatalf("Gemini terminal child inherited parent session environment: err=%v", err)
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
