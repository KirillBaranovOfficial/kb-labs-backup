//go:build darwin || linux

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

const realCodexRateLimitsFixture = `{"ordinaryUsageAllowed":true,"rateLimits":{"limitId":"codex","primary":{"usedPercent":82,"windowDurationMins":300,"resetsAt":1792775628},"secondary":{"usedPercent":24,"windowDurationMins":10080,"resetsAt":1793298749},"credits":{"hasCredits":true},"planType":"plus","rateLimitReachedType":null,"spendControlReached":false},"rateLimitsByLimitId":{"codex":{"limitId":"codex"}},"accountId":"private-account"}`

func TestDecodeCodexAppServerRealRateLimitShape(t *testing.T) {
	limits := decodeCodexLimits([]byte(realCodexRateLimitsFixture), time.Now)
	byWindow := map[string]VendorLimit{}
	for _, limit := range limits {
		byWindow[limit.Window] = limit
	}
	five, hasFive := byWindow["5h"]
	week, hasWeek := byWindow["week"]
	if !hasFive || !hasWeek || five.UsedPercent != 82 || week.UsedPercent != 24 {
		t.Fatalf("unexpected decoded Codex windows: %+v", limits)
	}
	if five.ResetsAt == nil || five.ResetsAt.Unix() != 1792775628 || week.ResetsAt == nil || week.ResetsAt.Unix() != 1793298749 {
		t.Fatalf("epoch reset values were not preserved: %+v", limits)
	}
	blocked := decodeCodexLimits([]byte(strings.Replace(realCodexRateLimitsFixture, `"rateLimitReachedType":null`, `"rateLimitReachedType":"primary"`, 1)), time.Now)
	if !quotaBlocked(blocked, 95, 98) {
		t.Fatalf("rateLimitReachedType did not block admission: %+v", blocked)
	}
	if nextBlockedReset(blocked, 95, 98) == nil {
		t.Fatalf("rateLimitReachedType block had no reset to await: %+v", blocked)
	}
	spendBlocked := decodeCodexLimits([]byte(strings.Replace(realCodexRateLimitsFixture, `"spendControlReached":false`, `"spendControlReached":true`, 1)), time.Now)
	if !quotaBlocked(spendBlocked, 95, 98) {
		t.Fatalf("spendControlReached did not block admission: %+v", spendBlocked)
	}
}

func TestCodexAppServerKeepsStdinOpenAndIgnoresNotifications(t *testing.T) {
	fake := filepath.Join(t.TempDir(), "codex-app-server")
	script := "#!/bin/sh\nwhile IFS= read -r line; do\ncase \"$line\" in\n*'\"id\":1'*) printf '%s\\n' '{\"id\":1,\"result\":{}}' ;;\n*'account/rateLimits/read'*) printf '%s\\n' '{\"method\":\"remoteControl/status/changed\"}' '{\"method\":\"account/updated\"}' '{\"id\":2,\"result\":" + realCodexRateLimitsFixture + "}'; exit 0 ;;\nesac\ndone\n"
	if err := os.WriteFile(fake, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	limits, _, err := readCodexLimitsDetailed(context.Background(), fake)
	if err != nil {
		t.Fatal(err)
	}
	if !hasVendorLimits(limits) {
		t.Fatalf("app-server response after notifications was lost: %+v", limits)
	}
}

func TestLimitCaptureRedactsSensitiveStructuredFields(t *testing.T) {
	input := json.RawMessage(`{"accountId":"private","email":"person@example.test","nested":{"accessToken":"opaque","apiKey":"opaque","secretValue":"opaque","usedPercent":42}}`)
	got, err := redactLimitPayload(input)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"accountId", "email", "person@example.test", "accessToken", "apiKey", "secretValue", "opaque"} {
		if strings.Contains(string(got), forbidden) {
			t.Fatalf("capture retained sensitive field %q: %s", forbidden, got)
		}
	}
	if !strings.Contains(string(got), `"usedPercent":42`) {
		t.Fatalf("capture discarded non-sensitive response fields: %s", got)
	}
}

func TestClaudeLimitsUseOnlyTerminalSource(t *testing.T) {
	fake := fakeLimitsCLI(t, "claude")
	adapter := commandAdapter{vendor: "claude", binary: fake}
	limits, err := adapter.Limits(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !hasVendorLimits(limits) || limits[0].Source != "tty" || len(limits[0].Attempts) != 1 || limits[0].Attempts[0].Source != "tty" {
		t.Fatalf("Claude limits did not use the terminal-only source: %+v", limits)
	}
}

func TestGeminiTTYReadinessUsesStableScreenWithoutPromptMarker(t *testing.T) {
	fake := fakeLimitsCLI(t, "gemini")
	limits := readTTYLimitsDetailed(context.Background(), "gemini", fake).Limits
	if len(limits) != 2 || limits[0].Window != "model:gemini 2.5 pro" || limits[0].UsedPercent != 30 || limits[0].Source != "tty" || limits[1].Window != "daily" || limits[1].UsedPercent != 75 {
		t.Fatalf("Gemini stable-screen probe did not read quota: %+v", limits)
	}
}

func TestTTYUnrecognizedRenderedScreenIsUnavailableWithScreenReason(t *testing.T) {
	fake := fakeLimitsCLIWithScreen(t, "gemini", "Quota display has changed\nNo usage windows are listed")
	limits := readTTYLimitsDetailed(context.Background(), "gemini", fake).Limits
	if len(limits) != 1 || limits[0].Confidence != "unavailable" || !strings.Contains(limits[0].Error, "Quota display has changed") {
		t.Fatalf("unrecognized TTY screen reason=%+v", limits)
	}
}

func TestTTYNonzeroCLIExitIsUnavailable(t *testing.T) {
	fake := fakeLimitsCLIWithScreenExit(t, "gemini", "5-hour usage 40%\nreset in 2h 10m", 7)
	limits := readTTYLimitsDetailed(context.Background(), "gemini", fake).Limits
	if len(limits) != 1 || limits[0].Confidence != "unavailable" || !strings.Contains(limits[0].Error, "exit gemini limits command") {
		t.Fatalf("nonzero CLI exit was accepted: %+v", limits)
	}
}

func TestLimitsCaptureCLIRecordsEveryVendorSource(t *testing.T) {
	codex := filepath.Join(t.TempDir(), "codex-app-server")
	codexScript := "#!/bin/sh\nwhile IFS= read -r line; do case \"$line\" in *'\"id\":1'*) printf '%s\\n' '{\"id\":1,\"result\":{}}';; *'\"id\":2'*) printf '%s\\n' '{\"id\":2,\"result\":" + realCodexRateLimitsFixture + "}'; exit 0;; esac; done\n"
	if err := os.WriteFile(codex, []byte(codexScript), 0o700); err != nil {
		t.Fatal(err)
	}
	claude := fakeLimitsCLI(t, "claude")
	gemini := fakeLimitsCLI(t, "gemini")
	captureDir, state := t.TempDir(), t.TempDir()
	var stdout, stderr strings.Builder
	factory := func(vendor string) (limitsProvider, error) {
		switch vendor {
		case "codex":
			return commandAdapter{vendor: vendor, binary: codex}, nil
		case "claude":
			return commandAdapter{vendor: vendor, binary: claude}, nil
		case "gemini":
			return commandAdapter{vendor: vendor, binary: gemini}, nil
		default:
			return nil, fmt.Errorf("unexpected vendor %q", vendor)
		}
	}
	code, err := limitsCommandWithProviderFactory(runnerRoot{invocation: "kb-runner"}, []string{"--state", state, "--capture", captureDir, "--json"}, &stdout, &stderr, factory)
	if err != nil || code != 0 {
		t.Fatalf("limits --capture: exit=%d err=%v stdout=%s stderr=%s", code, err, stdout.String(), stderr.String())
	}
	var limits []VendorLimit
	if err := json.Unmarshal([]byte(stdout.String()), &limits); err != nil {
		t.Fatal(err)
	}
	byVendor := map[string][]VendorLimit{}
	for _, limit := range limits {
		byVendor[limit.Vendor] = append(byVendor[limit.Vendor], limit)
	}
	if len(byVendor["codex"]) != 2 || byVendor["codex"][0].Source != "app-server" || len(byVendor["claude"]) == 0 || byVendor["claude"][0].Source != "tty" || len(byVendor["gemini"]) == 0 || byVendor["gemini"][0].Source != "tty" {
		t.Fatalf("captured CLI sources=%+v", byVendor)
	}
	for _, pair := range [][2]string{{"codex", "app-server"}, {"claude", "tty"}, {"gemini", "tty"}} {
		path := limitCapturePath(captureDir, pair[0], pair[1])
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("missing %s/%s capture: %v", pair[0], pair[1], err)
		}
		if strings.Contains(string(data), "private-account") || strings.Contains(string(data), "accountId") {
			t.Fatalf("capture %s/%s leaked account data", pair[0], pair[1])
		}
		if pair[1] == "tty" {
			var capture limitCaptureFile
			if err := json.Unmarshal(data, &capture); err != nil {
				t.Fatalf("decode %s/%s capture: %v", pair[0], pair[1], err)
			}
			if capture.TTYScreen == "" || strings.Contains(capture.TTYScreen, "\x1b") || !strings.Contains(capture.TTYScreen, "47% used") && !strings.Contains(capture.TTYScreen, "15/50 requests used") {
				t.Fatalf("%s/%s capture did not retain the rendered terminal screen: %q", pair[0], pair[1], capture.TTYScreen)
			}
		}
	}
}

func fakeLimitsCLI(t *testing.T, vendor string) string {
	switch vendor {
	case "claude":
		return fakeLimitsCLIWithScreen(t, vendor, claudeUsageRedrawFixture)
	case "gemini":
		return fakeLimitsCLIWithScreen(t, vendor, geminiStatsRedrawFixture)
	default:
		return fakeLimitsCLIWithScreen(t, vendor, "\033[32m5-hour usage 40%\033[0m\nreset in 2h 10m")
	}
}

func fakeLimitsCLIWithScreen(t *testing.T, vendor, screen string) string {
	return fakeLimitsCLIWithScreenExit(t, vendor, screen, 0)
}

func fakeLimitsCLIWithScreenExit(t *testing.T, vendor, screen string, exitCode int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), vendor+"-interactive")
	quotedScreen := "'" + strings.ReplaceAll(screen, "'", "'\\''") + "'"
	helpLine := "/usage - display usage"
	if vendor == "gemini" {
		helpLine = "/stats - show usage and quota"
	}
	quotedHelp := "'" + strings.ReplaceAll(helpLine, "'", "'\\''") + "'"
	script := `#!/bin/sh
printf 'Initial rendered screen without an explicit prompt marker\n'
while IFS= read -r command; do
  case "$command" in
    /help) printf '%s\n' ` + quotedHelp + ` ;;
    /usage|/stats) printf '%s\n' ` + quotedScreen + ` ;;
    *) printf 'Unknown command: %s\n' "$command" ;;
  esac
done
exit ` + strconv.Itoa(exitCode) + `
`
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}
