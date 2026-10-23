//go:build darwin || linux

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

type EventKind string

type vendorSpec struct {
	agent         bool
	binaryEnv     string
	binaryDefault string
	unavailable   string
}

var vendorRegistry = map[string]vendorSpec{
	"codex":  {agent: true, binaryEnv: "CODEX_BIN", binaryDefault: "/Applications/ChatGPT.app/Contents/Resources/codex-cli/bin/codex"},
	"claude": {agent: true, binaryEnv: "CLAUDE_BIN", binaryDefault: "claude"},
	"gemini": {binaryEnv: "GEMINI_BIN", binaryDefault: "gemini", unavailable: "Gemini is available for limits only"},
}

func vendors() []string {
	names := make([]string, 0, len(vendorRegistry))
	for name := range vendorRegistry {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

const (
	EventStarted             EventKind = "started"
	EventActivity            EventKind = "activity"
	EventTool                EventKind = "tool"
	EventUsage               EventKind = "usage"
	EventFinal               EventKind = "final"
	EventVendorError         EventKind = "vendor_error"
	EventRateLimited         EventKind = "rate_limited"
	EventProviderUnreachable EventKind = "provider_unreachable"
	EventAuthExpired         EventKind = "auth_expired"
)

type VendorEvent struct {
	Kind         EventKind `json:"kind"`
	SessionID    string    `json:"sessionId,omitempty"`
	Message      string    `json:"message,omitempty"`
	FinalMessage string    `json:"finalMessage,omitempty"`
	Tool         string    `json:"tool,omitempty"`
	Tokens       uint64    `json:"tokens,omitempty"`
	RawType      string    `json:"rawType,omitempty"`
}

type healthTracker struct {
	threshold int
	mu        sync.Mutex
	failures  map[string]int
}

func newHealthTracker(threshold int) *healthTracker {
	if threshold < 1 {
		threshold = 1
	}
	return &healthTracker{threshold: threshold, failures: map[string]int{}}
}
func (h *healthTracker) Observe(vendor string, line []byte) (VendorEvent, error) {
	e, err := parseVendorEvent(vendor, line)
	if err != nil {
		return VendorEvent{}, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	lower := strings.ToLower(e.Message)
	network := e.Kind == EventVendorError && (strings.Contains(lower, "timed out") || strings.Contains(lower, "connection refused") || strings.Contains(lower, "network is unreachable") || strings.Contains(lower, "dns"))
	if network {
		h.failures[vendor]++
		if h.failures[vendor] >= h.threshold {
			e.Kind = EventProviderUnreachable
		}
	} else if e.Kind != EventActivity {
		h.failures[vendor] = 0
	}
	return e, nil
}

func quotaBlocked(limits []VendorLimit, fiveHour, week float64) bool {
	for _, limit := range limits {
		if limit.Confidence != "vendor" {
			continue
		}
		if (limit.Window == "5h" && limit.UsedPercent >= fiveHour) || (limit.Window == "week" && limit.UsedPercent >= week) {
			return true
		}
	}
	return false
}

type AgentRequest struct {
	Prompt       string
	Workdir      string
	Effort       string
	WritableDirs []string
	Model        string
	SessionID    string
	FinalPath    string
}
type AgentAdapter interface {
	Start(context.Context, AgentRequest) (*exec.Cmd, error)
	Resume(context.Context, AgentRequest) (*exec.Cmd, error)
	Interrupt(*os.Process) error
	Parse([]byte) (VendorEvent, error)
	Limits(context.Context) ([]VendorLimit, error)
}

type fakeLimitProvider struct {
	limits []VendorLimit
	err    error
}

func (f fakeLimitProvider) Limits(context.Context) ([]VendorLimit, error) { return f.limits, f.err }

func parseVendorEvent(vendor string, line []byte) (VendorEvent, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(line, &obj); err != nil {
		return VendorEvent{}, err
	}
	decodeString := func(field string) (string, error) {
		raw, ok := obj[field]
		if !ok {
			return "", nil
		}
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return "", fmt.Errorf("event field %q must be a string: %w", field, err)
		}
		return value, nil
	}
	typ, err := decodeString("type")
	if err != nil {
		return VendorEvent{}, err
	}
	if typ == "" {
		return VendorEvent{}, errors.New("vendor event is missing its type")
	}
	msg, err := decodeString("message")
	if err != nil {
		return VendorEvent{}, err
	}
	session, err := decodeString("session_id")
	if err != nil {
		return VendorEvent{}, err
	}
	if session == "" {
		session, err = decodeString("thread_id")
		if err != nil {
			return VendorEvent{}, err
		}
	}
	e := VendorEvent{RawType: typ, SessionID: session, Kind: EventActivity, Message: msg}
	switch typ {
	case "thread.started", "session_started":
		e.Kind = EventStarted
	case "token_count", "usage":
		e.Kind = EventUsage
		var payload struct {
			Total struct {
				Total uint64 `json:"total_tokens"`
			} `json:"total_token_usage"`
			Usage struct {
				Total uint64 `json:"total_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal(line, &payload); err != nil {
			return VendorEvent{}, err
		}
		e.Tokens = payload.Total.Total
		if e.Tokens == 0 {
			e.Tokens = payload.Usage.Total
		}
	case "tool_use", "tool.started":
		e.Kind = EventTool
		e.Tool, err = decodeString("name")
		if err != nil {
			return VendorEvent{}, err
		}
	case "turn.completed", "result", "final":
		e.Kind = EventFinal
		if typ == "result" {
			e.FinalMessage, err = decodeString("result")
			if err != nil {
				return VendorEvent{}, err
			}
		}
		if e.FinalMessage == "" {
			e.FinalMessage = msg
		}
	case "error", "turn.failed":
		low := strings.ToLower(msg)
		switch {
		case strings.Contains(low, "rate limit") || strings.Contains(low, "usage limit") || strings.Contains(low, "capacity"):
			e.Kind = EventRateLimited
		case strings.Contains(low, "authentication") || strings.Contains(low, "unauthorized") || strings.Contains(low, "token expired"):
			e.Kind = EventAuthExpired
		default:
			e.Kind = EventVendorError
		}
	}
	if vendor != "codex" && vendor != "claude" {
		return VendorEvent{}, fmt.Errorf("unsupported vendor %q", vendor)
	}
	return e, nil
}

type commandAdapter struct{ vendor, binary string }

func newAdapter(vendor string) (AgentAdapter, error) {
	spec, ok := vendorRegistry[vendor]
	if !ok {
		return nil, fmt.Errorf("unsupported vendor %q", vendor)
	}
	if !spec.agent {
		return nil, errors.New(spec.unavailable)
	}
	bin := os.Getenv(spec.binaryEnv)
	if bin == "" {
		bin = spec.binaryDefault
	}
	return commandAdapter{vendor: vendor, binary: bin}, nil
}
func newLimitsProvider(vendor string) (limitsProvider, error) {
	spec, ok := vendorRegistry[vendor]
	if !ok {
		return nil, fmt.Errorf("unsupported vendor %q", vendor)
	}
	bin := os.Getenv(spec.binaryEnv)
	if bin == "" {
		bin = spec.binaryDefault
	}
	return commandAdapter{vendor: vendor, binary: bin}, nil
}
func (a commandAdapter) Start(ctx context.Context, r AgentRequest) (*exec.Cmd, error) {
	var args []string
	if a.vendor == "codex" {
		args = []string{"exec", "--json", "-s", "workspace-write", "-c", "sandbox_workspace_write.network_access=true", "-c", fmt.Sprintf("model_reasoning_effort=%q", r.Effort), "-C", r.Workdir}
		for _, d := range r.WritableDirs {
			args = append(args, "--add-dir", d)
		}
		if r.Model != "" {
			args = append(args, "-m", r.Model)
		}
		if r.FinalPath != "" {
			args = append(args, "-o", r.FinalPath)
		}
		args = append(args, "-")
	} else {
		args = []string{"-p", r.Prompt, "--output-format", "stream-json"}
		if r.Model != "" {
			args = append(args, "--model", r.Model)
		}
	}
	cmd := exec.CommandContext(ctx, a.binary, args...)
	cmd.Dir = r.Workdir
	if a.vendor == "codex" {
		cmd.Stdin = strings.NewReader(r.Prompt)
	}
	return cmd, nil
}
func (a commandAdapter) Resume(ctx context.Context, r AgentRequest) (*exec.Cmd, error) {
	if a.vendor == "codex" {
		args := []string{"exec", "resume", "-s", "workspace-write", "-c", "sandbox_workspace_write.network_access=true"}
		for _, dir := range r.WritableDirs {
			args = append(args, "--add-dir", dir)
		}
		if r.FinalPath != "" {
			args = append(args, "-o", r.FinalPath)
		}
		args = append(args, "--json", r.SessionID, "-")
		cmd := exec.CommandContext(ctx, a.binary, args...)
		cmd.Dir = r.Workdir
		cmd.Stdin = strings.NewReader(r.Prompt)
		return cmd, nil
	}
	cmd := exec.CommandContext(ctx, a.binary, "-p", r.Prompt, "--output-format", "stream-json", "--resume", r.SessionID)
	cmd.Dir = r.Workdir
	return cmd, nil
}
func (a commandAdapter) Interrupt(p *os.Process) error { return p.Signal(os.Interrupt) }
func (a commandAdapter) Parse(line []byte) (VendorEvent, error) {
	return parseVendorEvent(a.vendor, line)
}
func (a commandAdapter) Limits(ctx context.Context) ([]VendorLimit, error) {
	switch a.vendor {
	case "codex":
		structured, err := readCodexLimits(ctx, a.binary)
		if hasVendorLimits(structured) {
			return structured, err
		}
		return preferTTYLimits(ctx, a.vendor, a.binary, structured)
	case "claude":
		structured, err := readClaudeLimits(ctx)
		if hasVendorLimits(structured) {
			return structured, err
		}
		return preferTTYLimits(ctx, a.vendor, a.binary, structured)
	default:
		return readGeminiLimits(ctx, a.binary)
	}
}

func hasVendorLimits(limits []VendorLimit) bool {
	for _, limit := range limits {
		if limit.Confidence == "vendor" {
			return true
		}
	}
	return false
}

func preferTTYLimits(ctx context.Context, vendor, binary string, structured []VendorLimit) ([]VendorLimit, error) {
	tty := readTTYLimits(ctx, vendor, binary)
	if hasVendorLimits(tty) {
		return tty, nil
	}
	if len(structured) > 0 {
		return structured, nil
	}
	return tty, nil
}

func readGeminiLimits(ctx context.Context, binary string) ([]VendorLimit, error) {
	return readTTYLimits(ctx, "gemini", binary), nil
}

type limitsProvider interface {
	Limits(context.Context) ([]VendorLimit, error)
}

func cachedLimits(path string, ttl time.Duration, p limitsProvider, now func() time.Time) ([]VendorLimit, error) {
	if data, err := os.ReadFile(path); err == nil {
		var current struct {
			At     time.Time     `json:"at"`
			Limits []VendorLimit `json:"limits"`
		}
		if json.Unmarshal(data, &current) == nil && now().Sub(current.At) < ttl {
			return current.Limits, nil
		}
	}
	limits, err := p.Limits(context.Background())
	if err != nil {
		return nil, err
	}
	current := struct {
		At     time.Time     `json:"at"`
		Limits []VendorLimit `json:"limits"`
	}{now().UTC(), limits}
	data, e := json.Marshal(current)
	if e != nil {
		return nil, e
	}
	if e = os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return nil, e
	}
	if e = os.WriteFile(path, data, 0600); e != nil {
		return nil, e
	}
	return limits, nil
}

func readCodexLimits(ctx context.Context, binary string) ([]VendorLimit, error) {
	cmd := exec.CommandContext(ctx, binary, "app-server")
	in, err := cmd.StdinPipe()
	if err != nil {
		return unavailableLimits("codex", "app-server", err), nil
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return unavailableLimits("codex", "app-server", err), nil
	}
	cmd.Stderr = io.Discard
	if err = cmd.Start(); err != nil {
		return unavailableLimits("codex", "app-server", err), nil
	}
	cleanup := func(limits []VendorLimit, prior error) ([]VendorLimit, error) {
		killErr := cmd.Process.Kill()
		if killErr != nil && !errors.Is(killErr, os.ErrProcessDone) {
			return unavailableLimits("codex", "app-server", killErr), nil
		}
		waitErr := cmd.Wait()
		var exitErr *exec.ExitError
		if waitErr != nil && !errors.As(waitErr, &exitErr) {
			return unavailableLimits("codex", "app-server", waitErr), nil
		}
		return limits, prior
	}
	enc := json.NewEncoder(in)
	if err = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{"clientInfo": map[string]string{"name": "kb-runner", "version": "1"}}}); err != nil {
		return cleanup(unavailableLimits("codex", "app-server", err), nil)
	}
	scan := bufio.NewScanner(out)
	initialized := false
	for scan.Scan() {
		var m map[string]json.RawMessage
		if err := json.Unmarshal(scan.Bytes(), &m); err != nil {
			return cleanup(unavailableLimits("codex", "app-server", fmt.Errorf("invalid JSON-RPC response: %w", err)), nil)
		}
		if m["id"] == nil {
			continue
		}
		var id int
		if err := json.Unmarshal(m["id"], &id); err != nil {
			return cleanup(unavailableLimits("codex", "app-server", fmt.Errorf("invalid JSON-RPC response id: %w", err)), nil)
		}
		if id == 1 && !initialized {
			if err = enc.Encode(map[string]any{"jsonrpc": "2.0", "method": "initialized", "params": map[string]any{}}); err != nil {
				return cleanup(unavailableLimits("codex", "app-server", err), nil)
			}
			if err = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "account/rateLimits/read", "params": map[string]any{}}); err != nil {
				return cleanup(unavailableLimits("codex", "app-server", err), nil)
			}
			if err = in.Close(); err != nil {
				return cleanup(unavailableLimits("codex", "app-server", err), nil)
			}
			initialized = true
			continue
		}
		if id != 2 || !initialized {
			continue
		}
		return cleanup(decodeCodexLimits(m["result"], time.Now), nil)
	}
	if scan.Err() != nil {
		return cleanup(unavailableLimits("codex", "app-server", scan.Err()), nil)
	}
	return cleanup(unavailableLimits("codex", "app-server", errors.New("app-server returned no rate limit result")), nil)
}
func decodeCodexLimits(data []byte, now func() time.Time) []VendorLimit {
	type window struct {
		Used  *float64 `json:"usedPercent"`
		Reset string   `json:"resetsAt"`
	}
	var r struct {
		RateLimits struct {
			Primary   window `json:"primary"`
			Secondary window `json:"secondary"`
		} `json:"rateLimits"`
	}
	if json.Unmarshal(data, &r) != nil || len(data) == 0 || r.RateLimits.Primary.Used == nil || r.RateLimits.Secondary.Used == nil || r.RateLimits.Primary.Reset == "" || r.RateLimits.Secondary.Reset == "" {
		return unavailableLimits("codex", "app-server", errors.New("unexpected app-server rate limit schema"))
	}
	parse := func(v window) (*time.Time, error) {
		t, e := time.Parse(time.RFC3339, v.Reset)
		if e != nil {
			return nil, e
		}
		return &t, nil
	}
	five, err := parse(r.RateLimits.Primary)
	if err != nil {
		return unavailableLimits("codex", "app-server", errors.New("invalid primary reset timestamp"))
	}
	week, err := parse(r.RateLimits.Secondary)
	if err != nil {
		return unavailableLimits("codex", "app-server", errors.New("invalid secondary reset timestamp"))
	}
	return []VendorLimit{{Vendor: "codex", Window: "5h", UsedPercent: *r.RateLimits.Primary.Used, ResetsAt: five, Confidence: "vendor", Source: "app-server", FetchedAt: now().UTC()}, {Vendor: "codex", Window: "week", UsedPercent: *r.RateLimits.Secondary.Used, ResetsAt: week, Confidence: "vendor", Source: "app-server", FetchedAt: now().UTC()}}
}
func unavailableLimits(vendor, source string, err error) []VendorLimit {
	return []VendorLimit{{Vendor: vendor, Window: "unknown", Confidence: "unavailable", Source: source, Error: err.Error(), FetchedAt: time.Now().UTC()}}
}
func readClaudeLimits(ctx context.Context) ([]VendorLimit, error) {
	token, err := claudeToken(ctx)
	if err != nil {
		return unavailableLimits("claude", "oauth-usage", err), nil
	}
	return fetchClaudeLimits(ctx, "https://api.anthropic.com/api/oauth/usage", token), nil
}
func fetchClaudeLimits(ctx context.Context, endpoint, token string) []VendorLimit {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return unavailableLimits("claude", "oauth-usage", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("anthropic-beta", "oauth-2025-04-20")
	resp, err := http.DefaultClient.Do(req)
	token = ""
	if err != nil {
		return unavailableLimits("claude", "oauth-usage", err)
	}
	if resp.StatusCode != http.StatusOK {
		if err := resp.Body.Close(); err != nil {
			return unavailableLimits("claude", "oauth-usage", fmt.Errorf("close usage response: %w", err))
		}
		return unavailableLimits("claude", "oauth-usage", fmt.Errorf("usage endpoint returned HTTP %d", resp.StatusCode))
	}
	var body struct {
		FiveHour struct {
			Utilization float64 `json:"utilization"`
			ResetsAt    string  `json:"resets_at"`
		} `json:"five_hour"`
		SevenDay struct {
			Utilization float64 `json:"utilization"`
			ResetsAt    string  `json:"resets_at"`
		} `json:"seven_day"`
	}
	bodyBytes, readErr := io.ReadAll(resp.Body)
	closeErr := resp.Body.Close()
	if readErr != nil {
		return unavailableLimits("claude", "oauth-usage", fmt.Errorf("read usage response: %w", readErr))
	}
	if closeErr != nil {
		return unavailableLimits("claude", "oauth-usage", fmt.Errorf("close usage response: %w", closeErr))
	}
	if err := json.Unmarshal(bodyBytes, &body); err != nil || body.FiveHour.ResetsAt == "" || body.SevenDay.ResetsAt == "" {
		return unavailableLimits("claude", "oauth-usage", errors.New("unrecognized usage schema"))
	}
	five, err := time.Parse(time.RFC3339, body.FiveHour.ResetsAt)
	if err != nil {
		return unavailableLimits("claude", "oauth-usage", errors.New("unrecognized 5h reset timestamp"))
	}
	week, err := time.Parse(time.RFC3339, body.SevenDay.ResetsAt)
	if err != nil {
		return unavailableLimits("claude", "oauth-usage", errors.New("unrecognized weekly reset timestamp"))
	}
	now := time.Now().UTC()
	return []VendorLimit{{Vendor: "claude", Window: "5h", UsedPercent: body.FiveHour.Utilization, ResetsAt: &five, Confidence: "vendor", Source: "oauth-usage", FetchedAt: now}, {Vendor: "claude", Window: "week", UsedPercent: body.SevenDay.Utilization, ResetsAt: &week, Confidence: "vendor", Source: "oauth-usage", FetchedAt: now}}
}
func claudeToken(ctx context.Context) (string, error) {
	return claudeTokenWith(ctx, execCommandOutput, runtime.GOOS, os.Getenv("HOME"), os.Getenv("USER"))
}

type commandOutput func(context.Context, string, ...string) ([]byte, error)

func execCommandOutput(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	return cmd.Output()
}

func claudeTokenWith(ctx context.Context, run commandOutput, goos, home, username string) (string, error) {
	if goos == "darwin" {
		return claudeKeychainToken(ctx, run, username)
	}
	if goos != "linux" {
		return "", fmt.Errorf("Claude OAuth credentials are unavailable on %s", goos)
	}
	path := filepath.Join(home, ".claude", ".credentials.json")
	b, err := os.ReadFile(path)
	if err == nil {
		var v struct {
			OAuth struct {
				AccessToken string `json:"accessToken"`
			} `json:"claudeAiOauth"`
		}
		if json.Unmarshal(b, &v) == nil && v.OAuth.AccessToken != "" {
			return v.OAuth.AccessToken, nil
		}
		return "", errors.New("Claude credentials file has an unsupported schema")
	}
	return "", fmt.Errorf("Claude OAuth credentials unavailable: %w", err)
}

func claudeKeychainToken(ctx context.Context, run commandOutput, username string) (string, error) {
	if username == "" {
		return "", errors.New("Claude Keychain lookup requires the current account name")
	}
	metadata, err := run(ctx, "security", "find-generic-password", "-a", username)
	if err != nil {
		return "", errors.New("Claude Keychain item discovery failed; check that Claude Code is signed in")
	}
	service, err := keychainService(metadata)
	for i := range metadata {
		metadata[i] = 0
	}
	if err != nil {
		return "", err
	}
	secret, err := run(ctx, "security", "find-generic-password", "-a", username, "-s", service, "-w")
	if err != nil {
		return "", errors.New("Claude Keychain credential read failed; sign in to Claude Code again")
	}
	token := strings.TrimSpace(string(secret))
	for i := range secret {
		secret[i] = 0
	}
	if token == "" {
		return "", errors.New("Claude Keychain item contained no OAuth credential")
	}
	return token, nil
}

func keychainService(metadata []byte) (string, error) {
	const marker = `"svce"<blob>="`
	_, rest, ok := strings.Cut(string(metadata), marker)
	if !ok {
		return "", errors.New("Claude Keychain item discovery found no generic password service")
	}
	service, _, ok := strings.Cut(rest, `"`)
	if !ok || strings.TrimSpace(service) == "" {
		return "", errors.New("Claude Keychain item discovery returned an invalid service name")
	}
	return service, nil
}
