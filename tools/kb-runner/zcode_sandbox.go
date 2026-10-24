//go:build darwin || linux

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/kb-labs/clikit/diag"
)

const zcodeSandboxDiagnosticCode = "KB_RUNNER_ZCODE_SANDBOX_UNAVAILABLE"
const zcodeSandboxBinary = "/usr/bin/sandbox-exec"

type zcodeSandboxError struct{ reason string }

func (e zcodeSandboxError) Error() string { return e.reason }

type zcodeSandboxInputs struct {
	Worktree   string
	JobTemp    string
	ZcodeData  string
	NodeCaches []string
}

// canonicalSandboxWriteRoot resolves every existing path component through
// EvalSymlinks. If the requested root does not exist yet, resolve the nearest
// existing ancestor and append the missing suffix; callers do not create roots.
func canonicalSandboxWriteRoot(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve ZCode sandbox directory %q: %w", path, err)
	}
	candidate := filepath.Clean(absolute)
	missing := make([]string, 0)
	for {
		resolved, err := filepath.EvalSymlinks(candidate)
		if err == nil {
			for index := len(missing) - 1; index >= 0; index-- {
				resolved = filepath.Join(resolved, missing[index])
			}
			return filepath.Clean(resolved), nil
		}
		if !os.IsNotExist(err) {
			return "", fmt.Errorf("canonicalize ZCode sandbox directory %q: %w", path, err)
		}
		if _, statErr := os.Lstat(candidate); statErr == nil {
			return "", fmt.Errorf("canonicalize existing ZCode sandbox path %q: %w", candidate, err)
		} else if !os.IsNotExist(statErr) {
			return "", fmt.Errorf("inspect ZCode sandbox directory %q: %w", candidate, statErr)
		}
		parent := filepath.Dir(candidate)
		if parent == candidate {
			return "", fmt.Errorf("canonicalize ZCode sandbox directory %q: no existing ancestor", path)
		}
		missing = append(missing, filepath.Base(candidate))
		candidate = parent
	}
}

func zcodeSandboxProfile(input zcodeSandboxInputs) (string, error) {
	if runtime.GOOS != "darwin" {
		return "", zcodeSandboxError{reason: "ZCode requires the macOS sandbox-exec boundary"}
	}
	paths := append([]string{input.Worktree, input.JobTemp, input.ZcodeData}, input.NodeCaches...)
	allowed := make([]string, 0, len(paths))
	seen := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		if path == "" {
			return "", errors.New("ZCode sandbox directory is empty")
		}
		canonical, err := canonicalSandboxWriteRoot(path)
		if err != nil {
			return "", err
		}
		if _, exists := seen[canonical]; exists {
			continue
		}
		seen[canonical] = struct{}{}
		allowed = append(allowed, canonical)
	}
	if len(allowed) < 3 {
		return "", errors.New("ZCode sandbox requires worktree, per-job temp, and ZCode data directories")
	}
	var profile strings.Builder
	profile.WriteString("(version 1)\n(deny default)\n")
	profile.WriteString("(allow file-read*)\n(allow process-exec)\n(allow process-fork)\n(allow process-info*)\n(allow sysctl-read)\n(allow mach-lookup)\n(allow network*)\n")
	for _, path := range allowed {
		fmt.Fprintf(&profile, "(allow file-write* (subpath %s))\n", strconv.Quote(path))
	}
	return profile.String(), nil
}

func zcodeSandboxCommand(ctx context.Context, sandboxExec, profile, executable string, args ...string) (*exec.Cmd, error) {
	if sandboxExec == "" {
		return nil, zcodeSandboxError{reason: "sandbox-exec is unavailable"}
	}
	if profile == "" || executable == "" {
		return nil, errors.New("ZCode sandbox command requires a generated profile and executable")
	}
	commandArgs := append([]string{"-p", profile, executable}, args...)
	return exec.CommandContext(ctx, sandboxExec, commandArgs...), nil
}

func probeZcodeSandbox(ctx context.Context, sandboxExec, profile string) error {
	cmd, err := zcodeSandboxCommand(ctx, sandboxExec, profile, "/usr/bin/true")
	if err != nil {
		return err
	}
	cmd.Env, err = childEnvironment(nil)
	if err != nil {
		return fmt.Errorf("build sandbox probe environment: %w", err)
	}
	if output, err := cmd.CombinedOutput(); err != nil {
		return zcodeSandboxError{reason: "macOS could not apply the generated ZCode sandbox policy: " + strings.TrimSpace(string(output))}
	}
	return nil
}

func (a commandAdapter) zcodeCommand(ctx context.Context, request AgentRequest, args ...string) (*exec.Cmd, error) {
	if request.Model != "" {
		return nil, errors.New("the installed ZCode CLI does not accept --model; set the model in its account configuration")
	}
	if len(request.WritableDirs) > 0 {
		return nil, errors.New("ZCode sandbox writes are limited to the agent worktree; remove --add-dir")
	}
	sandboxExec, err := exec.LookPath(zcodeSandboxBinary)
	if err != nil {
		return nil, zcodeSandboxError{reason: "sandbox-exec is unavailable"}
	}
	home, ok := os.LookupEnv("HOME")
	if !ok || home == "" {
		return nil, errors.New("ZCode sandbox requires the account HOME for its wrapper and login data")
	}
	jobTemp := filepath.Join(request.StateDir, "jobs", request.JobID, "tmp")
	if err := os.MkdirAll(jobTemp, 0o700); err != nil {
		return nil, fmt.Errorf("create ZCode per-job temp directory: %w", err)
	}
	input := zcodeSandboxInputs{
		Worktree:  request.Workdir,
		JobTemp:   jobTemp,
		ZcodeData: filepath.Join(home, ".zcode-kb-runner"),
		NodeCaches: []string{
			filepath.Join(home, ".npm"),
			filepath.Join(home, ".cache", "node"),
			filepath.Join(home, ".cache", "node", "corepack"),
			filepath.Join(home, ".cache", "corepack"),
			filepath.Join(home, ".cache", "pnpm"),
			filepath.Join(home, "Library", "Caches", "pnpm"),
			filepath.Join(home, "Library", "Caches", "node", "corepack"),
			filepath.Join(home, "Library", "pnpm", "store"),
		},
	}
	profile, err := zcodeSandboxProfile(input)
	if err != nil {
		return nil, err
	}
	if err := probeZcodeSandbox(ctx, sandboxExec, profile); err != nil {
		return nil, err
	}
	return zcodeSandboxCommand(ctx, sandboxExec, profile, a.binary, args...)
}

func zcodeSandboxDiagnostic(root runnerRoot, err error) error {
	var sandboxErr zcodeSandboxError
	if !errors.As(err, &sandboxErr) {
		return err
	}
	entry, ok := diag.LookupCatalog(zcodeSandboxDiagnosticCode)
	if !ok {
		return fmt.Errorf("ZCode sandbox diagnostic is missing from the platform catalog: %w", err)
	}
	return diag.New(entry.Code, entry.Message,
		diag.WithReason(sandboxErr.Error()),
		diag.WithBinary(root.InvocationCommand()),
	)
}

func failZcodeSandboxJob(store *jobStore, id string, root runnerRoot, messagePath string, cause error) (int, error) {
	diagnosticErr := zcodeSandboxDiagnostic(root, cause)
	var diagnostic *diag.Diag
	if !errors.As(diagnosticErr, &diagnostic) {
		return 1, diagnosticErr
	}
	if err := store.Terminate(id, JobTerminal{
		State: JobFailed, Phase: PhaseRun, Reason: ReasonSetup,
		Error:       renderDiagnostic(diagnostic),
		Next:        root.InvocationCommand() + " status --state " + shellQuote(filepath.Dir(store.dir)) + " --json",
		MessagePath: messagePath,
	}); err != nil {
		return 1, fmt.Errorf("ZCode sandbox refusal could not be persisted: %w; next: %s status --state %s --json", err, root.InvocationCommand(), shellQuote(filepath.Dir(store.dir)))
	}
	return 1, diagnosticErr
}
