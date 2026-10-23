//go:build darwin || linux

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
	t.Setenv("KB_RUNNER_CODEX_HELPER", "1")
	t.Setenv("KB_RUNNER_TEST_BINARY", os.Args[0])
	fake := filepath.Join(t.TempDir(), "codex-app-server")
	script := "#!/bin/sh\nexec \"$KB_RUNNER_TEST_BINARY\" -test.run='^TestCodexAppServerHelperProcess$' -- \"$@\"\n"
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

func TestCodexAppServerHelperProcess(t *testing.T) {
	if os.Getenv("KB_RUNNER_CODEX_HELPER") != "1" {
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	for scanner.Scan() {
		var message map[string]json.RawMessage
		if json.Unmarshal(scanner.Bytes(), &message) != nil {
			os.Exit(90)
		}
		var id int
		if err := json.Unmarshal(message["id"], &id); err != nil && len(message["id"]) > 0 {
			os.Exit(93)
		}
		if id == 1 {
			if err := encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "result": map[string]any{}}); err != nil {
				os.Exit(94)
			}
			continue
		}
		if id != 2 {
			continue
		}
		open := make(chan bool, 1)
		go func() { open <- scanner.Scan() }()
		select {
		case stillOpen := <-open:
			if !stillOpen {
				os.Exit(91)
			}
		case <-time.After(30 * time.Millisecond):
		}
		for _, message := range []any{
			map[string]any{"jsonrpc": "2.0", "method": "remoteControl/status/changed", "params": map[string]any{}},
			map[string]any{"jsonrpc": "2.0", "method": "account/updated", "params": map[string]any{}},
			map[string]any{"jsonrpc": "2.0", "id": 2, "result": json.RawMessage(realCodexRateLimitsFixture)},
		} {
			if err := encoder.Encode(message); err != nil {
				os.Exit(94)
			}
		}
		return
	}
	os.Exit(92)
}

func TestClaudeCredentialExpiryIsReadWithoutRefreshing(t *testing.T) {
	expiredAt := time.Now().Add(-time.Hour).UnixMilli()
	data := []byte(fmt.Sprintf(`{"claudeAiOauth":{"accessToken":"opaque","refreshToken":"leave-alone","expiresAt":%d}}`, expiredAt))
	credential, err := decodeClaudeCredential(data)
	if err != nil || credential.AccessToken == "" || credential.ExpiresAt == nil || credential.ExpiresAt.After(time.Now()) {
		t.Fatalf("expired Claude credential metadata: expiry=%v err=%v", credential.ExpiresAt, err)
	}
	if !credential.Expired(time.Now()) {
		t.Fatal("expired access token was admitted to the usage endpoint")
	}
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	limits, _, err := readClaudeLimitsDetailed(context.Background(), server.URL, func(context.Context) (claudeCredential, error) {
		return credential, nil
	}, server.Client())
	if err == nil || err.Error() != "token expired (owner session refreshes it)" || called || len(limits) != 1 || limits[0].Error != err.Error() {
		t.Fatalf("expired token handling limits=%+v called=%t err=%v", limits, called, err)
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

func TestClaudeExpiredStructuredLimitFallsBackAndCapturesEveryAttempt(t *testing.T) {
	fake := fakeLimitsCLI(t, "claude")
	captureDir := t.TempDir()
	adapter := commandAdapter{
		vendor: "claude",
		binary: fake,
		claudeCredentialResolver: func(context.Context) (claudeCredential, error) {
			return claudeCredential{AccessToken: "opaque", ExpiresAt: timePtr(time.Now().Add(-time.Minute))}, nil
		},
	}
	ctx := withLimitsCapture(context.Background(), captureDir)
	limits, err := adapter.Limits(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !hasVendorLimits(limits) || limits[0].Source != "tty" {
		t.Fatalf("expired token did not fall back to TTY: %+v", limits)
	}
	if len(limits[0].Attempts) != 2 || limits[0].Attempts[0].Error != "token expired (owner session refreshes it)" || limits[0].Attempts[1].Outcome != "available" {
		t.Fatalf("attempt outcomes were dropped: %+v", limits[0].Attempts)
	}
	for _, source := range []string{"oauth-usage", "tty"} {
		path := limitCapturePath(captureDir, "claude", source)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("capture %s: %v", source, err)
		}
		if strings.Contains(string(data), "opaque") || strings.Contains(strings.ToLower(string(data)), "accesstoken") {
			t.Fatalf("capture %s contains credential material", source)
		}
	}
}

func TestClaudeLimitAdapterNeverResolvesCredentialsWithoutInjection(t *testing.T) {
	fake := fakeLimitsCLI(t, "claude")
	adapter := commandAdapter{vendor: "claude", binary: fake}
	limits, err := adapter.Limits(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !hasVendorLimits(limits) || limits[0].Source != "tty" || len(limits[0].Attempts) != 2 || limits[0].Attempts[0].Error != "Claude credential resolver is not configured" {
		t.Fatalf("Claude limits used an uninjected credential resolver: %+v", limits)
	}
}

func TestGeminiTTYReadinessUsesStableScreenWithoutPromptMarker(t *testing.T) {
	fake := fakeLimitsCLI(t, "gemini")
	limits := readTTYLimitsDetailed(context.Background(), "gemini", fake).Limits
	if len(limits) != 1 || limits[0].Window != "5h" || limits[0].UsedPercent != 40 || limits[0].Source != "tty" {
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
	fake := fakeLimitsCLI(t, "gemini")
	t.Setenv("KB_RUNNER_FAKE_EXIT", "7")
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
			return commandAdapter{vendor: vendor, binary: claude, claudeCredentialResolver: func(context.Context) (claudeCredential, error) {
				return claudeCredential{AccessToken: "fake-expired-token", ExpiresAt: timePtr(time.Now().Add(-time.Minute))}, nil
			}}, nil
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
	for _, pair := range [][2]string{{"codex", "app-server"}, {"claude", "oauth-usage"}, {"claude", "tty"}, {"gemini", "tty"}} {
		path := limitCapturePath(captureDir, pair[0], pair[1])
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("missing %s/%s capture: %v", pair[0], pair[1], err)
		}
		if strings.Contains(string(data), "private-account") || strings.Contains(string(data), "accountId") {
			t.Fatalf("capture %s/%s leaked account data", pair[0], pair[1])
		}
	}
}

func fakeLimitsCLI(t *testing.T, vendor string) string {
	return fakeLimitsCLIWithScreen(t, vendor, "\033[32m5-hour usage 40%\033[0m\nreset in 2h 10m")
}

func fakeLimitsCLIWithScreen(t *testing.T, vendor, screen string) string {
	t.Helper()
	t.Setenv("KB_RUNNER_FAKE_SCREEN", screen)
	path := filepath.Join(t.TempDir(), vendor+"-interactive")
	script := `#!/bin/sh
printf 'Initial rendered screen without an explicit prompt marker\n'
while IFS= read -r command; do
  case "$command" in
    /help) printf '/usage - display usage\n' ;;
    /usage|/stats) printf '%b\n' "$KB_RUNNER_FAKE_SCREEN" ;;
    *) printf 'Unknown command: %s\n' "$command" ;;
  esac
done
exit "${KB_RUNNER_FAKE_EXIT:-0}"
`
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func timePtr(value time.Time) *time.Time { return &value }
