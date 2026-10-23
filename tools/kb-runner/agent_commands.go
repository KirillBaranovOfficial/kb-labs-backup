//go:build darwin || linux

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

func statusCommand(root runnerRoot, args []string, out, errOut io.Writer) (int, error) {
	return agentStatusCommand(args, out)
}
func agentStatusCommand(args []string, out io.Writer) (int, error) {
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		_, err := fmt.Fprintln(out, "Usage: kb-runner status [--state <dir>] [--json]")
		return 0, err
	}
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	state := fs.String("state", defaultRunnerState(), "runner state directory")
	asJSON := fs.Bool("json", false, "write JSON")
	if err := fs.Parse(args); err != nil {
		return 2, err
	}
	store := newJobStore(*state)
	jobs, err := store.List()
	if err != nil {
		return 1, err
	}
	status := AgentStatus{Jobs: jobs, Limits: readStatusLimits(*state), DailyTokens: dailyTokenTotals(jobs)}
	if *asJSON {
		return 0, json.NewEncoder(out).Encode(status)
	}
	if len(jobs) > 10 {
		jobs = jobs[len(jobs)-10:]
	}
	for _, j := range jobs {
		age := "-"
		if !j.LastActivity.IsZero() {
			age = time.Since(j.LastActivity).Round(time.Second).String()
		}
		message := j.MessagePath
		if j.Final != nil && j.Final.MessagePath != "" {
			message = j.Final.MessagePath
		}
		if _, err := fmt.Fprintf(out, "%-20s %-10s %-8s %-10s %-12s activity=%-7s tokens=%-7d session=%-16s %s\n", j.ID, j.Stream, j.Vendor, j.State, j.Phase, age, j.Tokens, j.SessionID, message); err != nil {
			return 1, err
		}
	}
	for _, limit := range status.Limits {
		if _, err := fmt.Fprintf(out, "limit %-7s %-5s %5.1f%% %-12s reset=%v\n", limit.Vendor, limit.Window, limit.UsedPercent, limit.Confidence, limit.ResetsAt); err != nil {
			return 1, err
		}
	}
	for _, usage := range status.DailyTokens {
		if _, err := fmt.Fprintf(out, "tokens %-7s %-10s %d\n", usage.Vendor, usage.Day, usage.Tokens); err != nil {
			return 1, err
		}
	}
	return 0, nil
}

func readStatusLimits(state string) []VendorLimit {
	all := []VendorLimit{}
	for _, vendor := range vendors() {
		b, err := os.ReadFile(filepath.Join(state, "limits", vendor+".json"))
		if err == nil {
			var cache struct {
				At     time.Time     `json:"at"`
				Limits []VendorLimit `json:"limits"`
			}
			if json.Unmarshal(b, &cache) == nil && len(cache.Limits) > 0 {
				all = append(all, cache.Limits...)
				continue
			}
		}
		all = append(all, unavailableLimits(vendor, "cache", fmt.Errorf("run kb-runner limits to fetch current usage"))[0])
	}
	return all
}

type contextLimitProvider struct {
	ctx     context.Context
	adapter limitsProvider
}

func (p contextLimitProvider) Limits(context.Context) ([]VendorLimit, error) {
	return p.adapter.Limits(p.ctx)
}
func defaultRunnerState() string {
	if value := os.Getenv("KBLABS_RUNNER_STATE"); value != "" {
		return value
	}
	if value := os.Getenv("XDG_STATE_HOME"); value != "" {
		return filepath.Join(value, "kb-runner")
	}
	config, err := os.UserConfigDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "kb-runner-state")
	}
	return filepath.Join(config, "kb-runner")
}

func limitsCommand(root runnerRoot, args []string, out, errOut io.Writer) (int, error) {
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		_, err := fmt.Fprintf(out, "Usage: %s limits [--state <dir>] [--json]\n", root.Name())
		return 0, err
	}
	fs := flag.NewFlagSet("limits", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	state := fs.String("state", defaultRunnerState(), "runner state directory")
	asJSON := fs.Bool("json", false, "write JSON")
	ttl := fs.Duration("ttl", time.Minute, "cached result lifetime")
	if err := fs.Parse(args); err != nil {
		return 2, err
	}
	all := []VendorLimit{}
	for _, vendor := range vendors() {
		provider, e := newLimitsProvider(vendor)
		if e != nil {
			all = append(all, unavailableLimits(vendor, "adapter", e)...)
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		cached, e := cachedLimits(filepath.Join(*state, "limits", vendor+".json"), *ttl, contextLimitProvider{ctx: ctx, adapter: provider}, time.Now)
		cancel()
		if e != nil {
			all = append(all, unavailableLimits(vendor, "provider", e)...)
		} else {
			all = append(all, cached...)
		}
	}
	if *asJSON {
		return 0, json.NewEncoder(out).Encode(all)
	}
	for _, l := range all {
		if _, err := fmt.Fprintf(out, "%-8s %-8s %6.1f%% %-12s %s %s\n", l.Vendor, l.Window, l.UsedPercent, l.Confidence, l.Source, l.Error); err != nil {
			return 1, err
		}
	}
	return 0, nil
}

func agentWaitCommand(root runnerRoot, args []string, out, errOut io.Writer) (int, error) {
	fs := flag.NewFlagSet("wait", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	any := fs.Bool("any", false, "wait for first selected job")
	all := fs.Bool("all", false, "wait for every selected job")
	state := fs.String("state", defaultRunnerState(), "runner state directory")
	since := fs.String("since", "", "acknowledgement cursor returned by a previous wait")
	asJSON := fs.Bool("json", false, "write JSON")
	if err := fs.Parse(args); err != nil {
		return 2, err
	}
	if *any == *all || len(fs.Args()) == 0 {
		return 2, fmt.Errorf("%s wait: choose --any or --all and name at least one job or stream; next: %s wait --help", root.Name(), root.InvocationCommand())
	}
	store := newJobStore(*state)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	for {
		jobs, cursor, err := store.Wait(fs.Args(), *all, *since)
		if err != nil {
			return 1, err
		}
		if len(jobs) > 0 {
			if *asJSON {
				return 0, json.NewEncoder(out).Encode(struct {
					Jobs   []Job  `json:"jobs"`
					Cursor string `json:"cursor"`
				}{jobs, cursor})
			}
			for _, j := range jobs {
				if _, err := fmt.Fprintf(out, "%s\t%s\t%s\t%s\t%s\n", j.ID, j.State, j.Phase, j.Reason, cursor); err != nil {
					return 1, err
				}
			}
			return 0, nil
		}
		select {
		case <-ctx.Done():
			return 1, ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
}
