//go:build darwin || linux

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type EventKind string

type vendorLimitsMode uint8

const (
	vendorLimitsAppServer vendorLimitsMode = iota + 1
	vendorLimitsRecorded
)

type vendorSpec struct {
	agent         bool
	binaryEnv     string
	binaryDefault string
	childEnv      []string
	limitsMode    vendorLimitsMode
}

var vendorRegistry = map[string]vendorSpec{
	"codex":  {agent: true, binaryEnv: "CODEX_BIN", binaryDefault: "/Applications/ChatGPT.app/Contents/Resources/codex-cli/bin/codex", limitsMode: vendorLimitsAppServer},
	"claude": {agent: true, binaryEnv: "CLAUDE_BIN", binaryDefault: "claude", limitsMode: vendorLimitsRecorded},
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
		if limit.Blocked || (limit.Window == "5h" && limit.UsedPercent >= fiveHour) || (limit.Window == "week" && limit.UsedPercent >= week) {
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

type commandAdapter struct {
	vendor   string
	binary   string
	childEnv []string
}

func newAdapter(vendor string) (AgentAdapter, error) {
	spec, ok := vendorRegistry[vendor]
	if !ok {
		return nil, fmt.Errorf("unsupported vendor %q", vendor)
	}
	if !spec.agent {
		return nil, fmt.Errorf("vendor %q is not available for agent runs", vendor)
	}
	bin := os.Getenv(spec.binaryEnv)
	if bin == "" {
		bin = spec.binaryDefault
	}
	return commandAdapter{vendor: vendor, binary: bin, childEnv: spec.childEnv}, nil
}
func newLimitsProvider(vendor string) (limitsProvider, error) {
	spec, ok := vendorRegistry[vendor]
	if !ok {
		return nil, fmt.Errorf("unsupported vendor %q", vendor)
	}
	if spec.limitsMode != vendorLimitsAppServer {
		return nil, fmt.Errorf("vendor %q has no automatic limits provider", vendor)
	}
	bin := os.Getenv(spec.binaryEnv)
	if bin == "" {
		bin = spec.binaryDefault
	}
	return commandAdapter{vendor: vendor, binary: bin, childEnv: spec.childEnv}, nil
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
	var err error
	cmd.Env, err = childEnvironment(a.childEnv)
	if err != nil {
		return nil, err
	}
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
		var err error
		cmd.Env, err = childEnvironment(a.childEnv)
		if err != nil {
			return nil, err
		}
		cmd.Stdin = strings.NewReader(r.Prompt)
		return cmd, nil
	}
	cmd := exec.CommandContext(ctx, a.binary, "-p", r.Prompt, "--output-format", "stream-json", "--resume", r.SessionID)
	cmd.Dir = r.Workdir
	var err error
	cmd.Env, err = childEnvironment(a.childEnv)
	if err != nil {
		return nil, err
	}
	return cmd, nil
}
func (a commandAdapter) Interrupt(p *os.Process) error { return p.Signal(os.Interrupt) }
func (a commandAdapter) Parse(line []byte) (VendorEvent, error) {
	return parseVendorEvent(a.vendor, line)
}
func (a commandAdapter) Limits(ctx context.Context) ([]VendorLimit, error) {
	spec, ok := vendorRegistry[a.vendor]
	if !ok {
		return nil, fmt.Errorf("unsupported vendor %q", a.vendor)
	}
	switch spec.limitsMode {
	case vendorLimitsAppServer:
		return collectVendorLimits(ctx, a.vendor,
			limitSource{name: "app-server", read: func(ctx context.Context) limitSourceOutput {
				limits, response, err := readCodexLimitsDetailed(ctx, a.binary)
				return limitSourceOutput{Limits: limits, Structured: response, Err: err}
			}},
		)
	case vendorLimitsRecorded:
		return []VendorLimit{}, nil
	default:
		return nil, fmt.Errorf("vendor %q has no limits source", a.vendor)
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

func readCodexLimitsDetailed(ctx context.Context, binary string) ([]VendorLimit, json.RawMessage, error) {
	cmd := exec.CommandContext(ctx, binary, "app-server")
	env, err := childEnvironment(vendorRegistry["codex"].childEnv)
	if err != nil {
		return unavailableLimits("codex", "app-server", err), nil, err
	}
	cmd.Env = env
	in, err := cmd.StdinPipe()
	if err != nil {
		return unavailableLimits("codex", "app-server", err), nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return unavailableLimits("codex", "app-server", err), nil, err
	}
	cmd.Stderr = io.Discard
	if err = cmd.Start(); err != nil {
		return unavailableLimits("codex", "app-server", err), nil, err
	}
	cleanup := func(limits []VendorLimit, response json.RawMessage, prior error) ([]VendorLimit, json.RawMessage, error) {
		if closeErr := in.Close(); closeErr != nil {
			prior = errors.Join(prior, fmt.Errorf("close app-server stdin: %w", closeErr))
		}
		killErr := cmd.Process.Kill()
		if killErr != nil && !errors.Is(killErr, os.ErrProcessDone) {
			killErr = fmt.Errorf("terminate app-server: %w", killErr)
			return unavailableLimits("codex", "app-server", killErr), response, errors.Join(prior, killErr)
		}
		waitErr := cmd.Wait()
		var exitErr *exec.ExitError
		if waitErr != nil && !errors.As(waitErr, &exitErr) {
			return unavailableLimits("codex", "app-server", waitErr), response, errors.Join(prior, waitErr)
		}
		return limits, response, prior
	}
	enc := json.NewEncoder(in)
	if err = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{"clientInfo": map[string]string{"name": "kb-runner", "version": "1"}}}); err != nil {
		return cleanup(unavailableLimits("codex", "app-server", err), nil, err)
	}
	scan := bufio.NewScanner(out)
	initialized := false
	for scan.Scan() {
		var m map[string]json.RawMessage
		if err := json.Unmarshal(scan.Bytes(), &m); err != nil {
			return cleanup(unavailableLimits("codex", "app-server", fmt.Errorf("invalid JSON-RPC response: %w", err)), nil, err)
		}
		if m["id"] == nil {
			continue
		}
		var id int
		if err := json.Unmarshal(m["id"], &id); err != nil {
			return cleanup(unavailableLimits("codex", "app-server", fmt.Errorf("invalid JSON-RPC response id: %w", err)), nil, err)
		}
		if id == 1 && !initialized {
			if err = enc.Encode(map[string]any{"jsonrpc": "2.0", "method": "initialized", "params": map[string]any{}}); err != nil {
				return cleanup(unavailableLimits("codex", "app-server", err), nil, err)
			}
			if err = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "account/rateLimits/read", "params": map[string]any{}}); err != nil {
				return cleanup(unavailableLimits("codex", "app-server", err), nil, err)
			}
			initialized = true
			continue
		}
		if id != 2 || !initialized {
			continue
		}
		if len(m["error"]) > 0 {
			return cleanup(unavailableLimits("codex", "app-server", errors.New("app-server rate limit request failed")), m["error"], errors.New("app-server rate limit request failed"))
		}
		return cleanup(decodeCodexLimits(m["result"], time.Now), m["result"], nil)
	}
	if scan.Err() != nil {
		return cleanup(unavailableLimits("codex", "app-server", scan.Err()), nil, scan.Err())
	}
	err = errors.New("app-server returned no rate limit result")
	return cleanup(unavailableLimits("codex", "app-server", err), nil, err)
}
func decodeCodexLimits(data []byte, now func() time.Time) []VendorLimit {
	type window struct {
		Used     *float64 `json:"usedPercent"`
		Duration int      `json:"windowDurationMins"`
		Reset    *int64   `json:"resetsAt"`
	}
	var r struct {
		RateLimits struct {
			RateLimitReachedType json.RawMessage `json:"rateLimitReachedType"`
			SpendControlReached  bool            `json:"spendControlReached"`
		} `json:"rateLimits"`
	}
	if len(data) == 0 || json.Unmarshal(data, &r) != nil {
		return unavailableLimits("codex", "app-server", errors.New("unexpected app-server rate limit schema"))
	}
	var envelope struct {
		RateLimits map[string]json.RawMessage `json:"rateLimits"`
	}
	if json.Unmarshal(data, &envelope) != nil || len(envelope.RateLimits) == 0 {
		return unavailableLimits("codex", "app-server", errors.New("unexpected app-server rate limit schema"))
	}
	blocked := r.RateLimits.SpendControlReached || len(r.RateLimits.RateLimitReachedType) > 0 && string(r.RateLimits.RateLimitReachedType) != "null"
	limits := make([]VendorLimit, 0, len(envelope.RateLimits))
	for _, raw := range envelope.RateLimits {
		var value window
		if err := json.Unmarshal(raw, &value); err != nil || value.Used == nil || value.Duration <= 0 || value.Reset == nil {
			continue
		}
		windowName := fmt.Sprintf("%dm", value.Duration)
		switch value.Duration {
		case 300:
			windowName = "5h"
		case 10080:
			windowName = "week"
		}
		reset := time.Unix(*value.Reset, 0).UTC()
		limits = append(limits, VendorLimit{Vendor: "codex", Window: windowName, UsedPercent: *value.Used, Blocked: blocked, ResetsAt: &reset, Confidence: "vendor", Source: "app-server", FetchedAt: now().UTC()})
	}
	if len(limits) != 2 {
		return unavailableLimits("codex", "app-server", errors.New("unexpected app-server rate limit schema"))
	}
	sort.Slice(limits, func(i, j int) bool { return limits[i].Window < limits[j].Window })
	return limits
}
func unavailableLimits(vendor, source string, err error) []VendorLimit {
	return []VendorLimit{{Vendor: vendor, Window: "unknown", Confidence: "unavailable", Source: source, Error: err.Error(), FetchedAt: time.Now().UTC()}}
}
