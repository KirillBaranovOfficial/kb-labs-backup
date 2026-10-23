//go:build darwin || linux

package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

const claudeUsageRedrawFixture = "\x1b[?1049h\x1b[2J\x1b[H" +
	"Claude Code\r\nCurrent session\r\n99% used\r\nResets in 9h 00m\r\n" +
	"\x1b[2J\x1b[HClaude Code\r\nCurrent session\r\n47% used\r\nResets in 3h 12m\r\n" +
	"Current week (all models)\r\n12% used\r\nResets at Oct 23, 2026 8:00 PM\r\n"

const geminiStatsRedrawFixture = "\x1b[?1049h\x1b[2J\x1b[H" +
	"Gemini CLI stats\r\nPer-model: Gemini 2.5 Pro\r\n5/50 requests used\r\nResets in 3h 12m\r\n" +
	"Daily quota\r\n25% left\r\nResets at Oct 24, 2026 12:30 AM\r\n" +
	"\x1b[?1049l\x1b[?1049h\x1b[2J\x1b[HGemini CLI stats\r\n" +
	"Per-model: Gemini 2.5 Pro\r\n15/50 requests used\r\nResets in 3h 12m\r\n" +
	"Daily quota\r\n25% left\r\nResets at Oct 24, 2026 12:30 AM\r\n"

func TestTerminalScreenAppliesCursorEraseWrapAndAlternateScreen(t *testing.T) {
	var cleared terminalScreen
	cleared.WriteString("stale top\r\nstale bottom")
	cleared.WriteString("\x1b[2J\x1b[Hnew screen")
	if cleared.String() != "new screen" {
		t.Fatalf("clear-screen sequence left stale content: %q", cleared.String())
	}

	var screen terminalScreen
	screen.WriteString("primary screen")
	screen.WriteString("\x1b[?1049h\x1b[2J\x1b[H")
	screen.WriteString("stale quota: 99%\r\n")
	screen.WriteString("\x1b[1A\x1b[1G\x1b[2Kdone\r\n")
	screen.WriteString(strings.Repeat("x", 125))
	active := screen.String()
	if firstLine := strings.SplitN(active, "\n", 2)[0]; firstLine != "done" || !strings.Contains(active, "xxxxx") {
		t.Fatalf("alternate rendered screen has stale or missing content: %q", active)
	}
	screen.WriteString("\x1b[?1049l")

	got := screen.String()
	if got != "primary screen" {
		t.Fatalf("leaving alternate screen did not restore primary screen: %q", got)
	}
}

func TestTerminalQueryResponderAnswersStandardQueriesAcrossChunks(t *testing.T) {
	var responder terminalQueryResponder
	first, replies := responder.Feed([]byte("screen\x1b[6"))
	if string(first) != "screen" || len(replies) != 0 {
		t.Fatalf("partial DSR handling visible=%q replies=%q", first, replies)
	}
	visible, replies := responder.Feed([]byte("n\x1b[c\x1b]10;?\x07\x1b]11;?\x07"))
	if len(visible) != 0 {
		t.Fatalf("terminal query bytes leaked to rendered screen: %q", visible)
	}
	want := []string{"\x1b[1;1R", "\x1b[?1;2c", "\x1b]10;rgb:ffff/ffff/ffff\x07", "\x1b]11;rgb:0000/0000/0000\x07"}
	if len(replies) != len(want) {
		t.Fatalf("terminal query reply count=%d want=%d", len(replies), len(want))
	}
	for index := range want {
		if string(replies[index]) != want[index] {
			t.Fatalf("terminal query reply %d=%q want=%q", index, replies[index], want[index])
		}
	}
}

func TestTTYReadinessAcceptsStableBlankRenderedScreen(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	chunks := make(chan []byte, 1)
	chunks <- []byte("\n")
	var screen terminalScreen
	if err := readUntilStableScreen(ctx, chunks, make(chan error), &screen); err != nil {
		t.Fatalf("stable blank screen was not ready: %v", err)
	}
	if screen.String() != "" {
		t.Fatalf("blank screen rendered unexpected content %q", screen.String())
	}
}

func TestQuotaExtractorReadsRedrawnClaudeUsageScreen(t *testing.T) {
	var screen terminalScreen
	screen.WriteString(claudeUsageRedrawFixture)
	got := parseQuotaScreen("claude", screen.String(), func() time.Time { return time.Date(2026, 10, 23, 10, 0, 0, 0, time.UTC) })
	if len(got) != 2 {
		t.Fatalf("Claude screen produced %d limits: %#v; screen=%q", len(got), got, screen.String())
	}
	if got[0].Window != "5h" || got[0].UsedPercent != 47 || got[0].ResetsAt == nil || !got[0].ResetsAt.Equal(time.Date(2026, 10, 23, 13, 12, 0, 0, time.UTC)) {
		t.Fatalf("unexpected Claude session quota: %#v", got[0])
	}
	if got[1].Window != "week" || got[1].UsedPercent != 12 || got[1].ResetsAt == nil || !got[1].ResetsAt.Equal(time.Date(2026, 10, 23, 20, 0, 0, 0, time.UTC)) {
		t.Fatalf("unexpected Claude weekly quota: %#v", got[1])
	}
}

func TestQuotaExtractorReadsRedrawnGeminiStatsScreen(t *testing.T) {
	var screen terminalScreen
	screen.WriteString(geminiStatsRedrawFixture)
	got := parseQuotaScreen("gemini", screen.String(), func() time.Time { return time.Date(2026, 10, 23, 10, 0, 0, 0, time.UTC) })
	if len(got) != 2 {
		t.Fatalf("Gemini screen produced %d limits: %#v; screen=%q", len(got), got, screen.String())
	}
	if got[0].Window != "model:gemini 2.5 pro" || got[0].UsedPercent != 30 || got[0].ResetsAt == nil || !got[0].ResetsAt.Equal(time.Date(2026, 10, 23, 13, 12, 0, 0, time.UTC)) {
		t.Fatalf("unexpected Gemini model quota: %#v", got[0])
	}
	if got[1].Window != "daily" || got[1].UsedPercent != 75 || got[1].ResetsAt == nil || !got[1].ResetsAt.Equal(time.Date(2026, 10, 24, 0, 30, 0, 0, time.UTC)) {
		t.Fatalf("unexpected Gemini daily quota: %#v", got[1])
	}
}

func TestQuotaExtractorRejectsUnknownRenderedScreen(t *testing.T) {
	if got := parseQuotaScreen("gemini", "Tasks complete: 42%\n", time.Now); len(got) != 0 {
		t.Fatalf("unrecognized layout created quotas: %#v", got)
	}
}

func TestQuotaResetParsesLongRelativeAndAbsoluteDates(t *testing.T) {
	now := time.Date(2026, 10, 23, 10, 0, 0, 0, time.UTC)
	relative := parseQuotaReset("Resets in 3 hours 12 minutes", now)
	if relative == nil || !relative.Equal(now.Add(3*time.Hour+12*time.Minute)) {
		t.Fatalf("long relative reset=%v", relative)
	}
	absolute := parseQuotaReset("Reset: Oct 24, 2026 12:30 AM", now)
	if absolute == nil || !absolute.Equal(time.Date(2026, 10, 24, 0, 30, 0, 0, time.UTC)) {
		t.Fatalf("absolute reset=%v", absolute)
	}
}
