package checks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/kb-labs/clikit/diag"
	"github.com/kb-labs/clikit/toolchain"
	"github.com/kb-labs/devkit/internal/config"
	"github.com/kb-labs/devkit/internal/workspace"
)

type externalRule struct {
	name          string
	run           string
	fix           string
	phase         string
	language      string
	categories    []string
	scope         string
	workspaceRoot string
	checkConfig   config.CheckPackConfig
	graphTasks    []string
	timeout       time.Duration
}

type externalInput struct {
	Package       workspace.Package  `json:"package"`
	Workspace     *externalWorkspace `json:"workspace,omitempty"`
	Preset        config.Preset      `json:"preset"`
	WorkspaceRoot string             `json:"workspaceRoot"`
	Check         string             `json:"check"`
	Phase         string             `json:"phase"`
	DryRun        bool               `json:"dryRun,omitempty"`
	Config        map[string]any     `json:"config,omitempty"`
	GraphTasks    []string           `json:"graphTasks"`
	Issues        []Issue            `json:"issues,omitempty"`
}

type externalWorkspace struct {
	Root     string                     `json:"root"`
	Packages []externalWorkspacePackage `json:"packages"`
}

type externalWorkspacePackage struct {
	Name        string `json:"name"`
	Dir         string `json:"dir"`
	RelPath     string `json:"relPath"`
	Category    string `json:"category"`
	Preset      string `json:"preset"`
	Language    string `json:"language"`
	BuildTarget bool   `json:"buildTarget"`
}

type externalCheckOutput struct {
	Issues []Issue `json:"issues"`
}

// WriteExternalCheckOutput writes the shared JSON contract consumed from
// workspace-scope custom checks.
func WriteExternalCheckOutput(writer io.Writer, issues []Issue) error {
	return json.NewEncoder(writer).Encode(externalCheckOutput{Issues: issues})
}

type externalFixOutput struct {
	Actions []string `json:"actions"`
}

func (r *externalRule) Name() string  { return r.name }
func (r *externalRule) Scope() string { return r.scope }

func (r *externalRule) appliesToCategory(category string) bool {
	if len(r.categories) == 0 {
		return true
	}
	for _, candidate := range r.categories {
		if candidate == category {
			return true
		}
	}
	return false
}

// AppliesToPackage is the package-level category predicate used by execution,
// selection validation, and reporting through Registry.ApplicableRulesFor.
func (r *externalRule) AppliesToPackage(pkg workspace.Package) bool {
	return r.scope == "workspace" || r.appliesToCategory(pkg.Category)
}

// ApplicableCategories describes a package-scoped external check's category filter.
func (r *externalRule) ApplicableCategories() []string {
	return append([]string(nil), r.categories...)
}

func (r *externalRule) Check(pkg workspace.Package, preset config.Preset) []Issue {
	if r.scope == "workspace" {
		return nil
	}
	out, err := r.runCommand(r.run, pkg, preset, nil, false)
	if err == nil {
		return out.Issues
	}
	if len(out.Issues) > 0 {
		return out.Issues
	}
	return []Issue{{
		Check:             r.name,
		Severity:          SeverityError,
		Message:           "External check could not complete.",
		CheckFailure:      true,
		Cause:             safeExternalCause(err),
		File:              pkg.Dir,
		FailureDiagnostic: externalDiagnostic(err),
		FailureExitCode:   externalExitCode(err),
		FailureStderrTail: externalStderrTail(err),
	}}
}

func (r *externalRule) CheckWorkspace(ws *workspace.Workspace) []Issue {
	packages := make([]externalWorkspacePackage, 0, len(ws.Packages))
	for _, pkg := range ws.Packages {
		packages = append(packages, externalWorkspacePackage{
			Name: pkg.Name, Dir: pkg.Dir, RelPath: pkg.RelPath,
			Category: pkg.Category, Preset: pkg.Preset, Language: pkg.Language, BuildTarget: pkg.BuildTarget,
		})
	}
	out, err := r.runCommandWithInput(r.run, externalInput{
		WorkspaceRoot: r.workspaceRoot,
		Check:         r.name,
		Phase:         r.phase,
		Workspace:     &externalWorkspace{Root: ws.Root, Packages: packages},
		Config:        r.checkConfig.Config,
		GraphTasks:    r.graphTasks,
	})
	if err == nil {
		return out.Issues
	}
	if len(out.Issues) > 0 {
		return out.Issues
	}
	return []Issue{{Check: r.name, Severity: SeverityError, Message: "External check could not complete.", CheckFailure: true, Cause: safeExternalCause(err), File: ws.Root, FailureDiagnostic: externalDiagnostic(err), FailureExitCode: externalExitCode(err), FailureStderrTail: externalStderrTail(err)}}
}

func (r *externalRule) Apply(pkg workspace.Package, issues []Issue, dryRun bool) error {
	if strings.TrimSpace(r.fix) == "" {
		return nil
	}
	_, err := r.runCommand(r.fix, pkg, config.Preset{}, issues, dryRun)
	return err
}

func (r *externalRule) runCommand(command string, pkg workspace.Package, preset config.Preset, issues []Issue, dryRun bool) (externalCheckOutput, error) {
	input := externalInput{
		Package:       pkg,
		Preset:        preset,
		WorkspaceRoot: r.workspaceRoot,
		Check:         r.name,
		Phase:         r.phase,
		DryRun:        dryRun,
		Config:        r.checkConfig.Config,
		GraphTasks:    r.graphTasks,
		Issues:        issues,
	}
	return r.runCommandWithInput(command, input)
}

func (r *externalRule) runCommandWithInput(command string, input externalInput) (externalCheckOutput, error) {
	payload, err := json.Marshal(input)
	if err != nil {
		return externalCheckOutput{}, fmt.Errorf("marshal external check input for %s: %w", r.name, err)
	}

	env := append(os.Environ(),
		"KB_DEVKIT_MODE="+modeForCommand(input.Issues),
		"KB_DEVKIT_CHECK_NAME="+r.name,
		"KB_DEVKIT_WORKSPACE_ROOT="+r.workspaceRoot,
		"KB_DEVKIT_PACKAGE_NAME="+input.Package.Name,
		"KB_DEVKIT_PACKAGE_DIR="+input.Package.Dir,
		"KB_DEVKIT_PACKAGE_REL="+input.Package.RelPath,
		"KB_DEVKIT_PACKAGE_CATEGORY="+input.Package.Category,
		"KB_DEVKIT_PACKAGE_PRESET="+input.Package.Preset,
		"KB_DEVKIT_PACKAGE_LANGUAGE="+input.Package.Language,
		"KB_DEVKIT_DRY_RUN="+fmt.Sprintf("%t", input.DryRun),
	)

	ctx := context.Background()
	cancel := func() {}
	if r.timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, r.timeout)
	}
	defer cancel()
	result, runErr := toolchain.Run(ctx, toolchain.Invocation{
		Tool: "sh", Executable: "sh", Args: []string{"-c", command}, Dir: r.workspaceRoot,
		Env: env, Stdin: bytes.NewReader(payload), Operation: toolchain.OperationWorkspaceTask,
	})

	var parsed externalCheckOutput
	if strings.TrimSpace(result.Stdout) != "" {
		if err := json.Unmarshal([]byte(result.Stdout), &parsed); err != nil && runErr == nil {
			return externalCheckOutput{}, fmt.Errorf("decode external check %s output: %w", r.name, err)
		}
	}

	if runErr != nil {
		return parsed, fmt.Errorf("external command exited unsuccessfully: %w", runErr)
	}

	return parsed, nil
}

func safeExternalCause(err error) string {
	var timeoutErr *toolchain.TimeoutError
	if errors.As(err, &timeoutErr) && timeoutErr.Bound == toolchain.BoundCaller {
		return fmt.Sprintf("external check exceeded its configured %s execution budget", timeoutErr.Budget.Round(time.Millisecond))
	}
	var exitErr *toolchain.CommandError
	if errors.As(err, &exitErr) {
		return fmt.Sprintf("external command exited with status %d", exitErr.ExitCode)
	}
	return "external check output could not be read or decoded"
}

func externalDiagnostic(err error) *diag.Diag {
	var diagnostic *diag.Diag
	if errors.As(err, &diagnostic) {
		return diagnostic
	}
	return nil
}

func externalExitCode(err error) int {
	var commandErr *toolchain.CommandError
	if errors.As(err, &commandErr) {
		return commandErr.ExitCode
	}
	return 0
}

func externalStderrTail(err error) string {
	var commandErr *toolchain.CommandError
	if errors.As(err, &commandErr) {
		return commandErr.StderrTail
	}
	return ""
}

func modeForCommand(issues []Issue) string {
	if len(issues) > 0 {
		return "fix"
	}
	return "check"
}

func matchesPhase(check config.CustomCheck, phase string) bool {
	if len(check.On) == 0 {
		return phase == "check"
	}
	for _, on := range check.On {
		if on == phase {
			return true
		}
	}
	return false
}

func matchesLanguage(check config.CustomCheck, language string) bool {
	if check.Language == "" || check.Language == "any" {
		return true
	}
	return check.Language == language
}
