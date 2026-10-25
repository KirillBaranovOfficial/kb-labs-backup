package cmd

import (
	"fmt"
	clikitcommand "github.com/kb-labs/clikit/command"
	"github.com/kb-labs/clikit/diag"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/kb-labs/devkit/internal/checks"
	"github.com/kb-labs/devkit/internal/config"
	"github.com/kb-labs/devkit/internal/workspace"
	"github.com/spf13/cobra"
)

var (
	checkPackage string
	checkOnly    []string
	debugMode    bool
)

var checkCmd = &cobra.Command{
	Use:   "check",
	Short: "Check workspace packages against devkit rules",
	Long: `Runs all configured rules against packages in the workspace.

Checks include: package_json, tsconfig, tsup, eslint, structure, deps, circular.

Use --only to run a subset of checks.
Use --package to check a single package.`,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		ws, cfg, err := loadWorkspace()
		if err != nil {
			return err
		}

		registry := checks.Build(cfg, ws.Root, "check")
		if err := validateCheckSelection(ws, cfg, registry, checkOnly, checkPackage); err != nil {
			return err
		}
		checksByCategory := selectedCheckCounts(ws, cfg, registry, checkOnly, checkPackage)
		if checkPackage != "" {
			ws.Packages = ws.FilterByName([]string{checkPackage})
		}
		progressWriter := clikitcommand.FromCommand(cmd).ErrorWriter()
		var progressMu sync.Mutex
		results := checks.RunAll(ws, cfg, registry, checkOnly, func(event checks.ProgressEvent) {
			progressMu.Lock()
			defer progressMu.Unlock()
			state := "DONE"
			if event.Running {
				state = "RUN"
			}
			fmt.Fprintf(progressWriter, "[%s] %s:%s elapsed %s\n", state, event.Package, event.Check, event.Elapsed.Round(time.Second))
		})
		packagesChecked := checkedPackageCount(results)
		if failures := checkExecutionFailures(results); len(failures) > 0 {
			return externalCheckFailureError(failures, results)
		}

		if jsonMode {
			return outputCheckJSON(results, cfg, checksByCategory, packagesChecked)
		}

		return outputCheckHuman(results, cfg, checksByCategory, packagesChecked)
	},
}

func init() {
	checkCmd.Flags().StringVar(&checkPackage, "package", "", "check a single package by name")
	checkCmd.Flags().StringSliceVar(&checkOnly, "only", nil, "run only specific checks (comma-separated)")
	checkCmd.Flags().BoolVar(&debugMode, "debug", false, "include check execution details")
	rootCmd.AddCommand(checkCmd)
}

// ─── JSON output ─────────────────────────────────────────────────────────────

type checkJSONResult struct {
	OK               bool                         `json:"ok"`
	Packages         map[string]packageJSONResult `json:"packages"`
	Results          []checkJSONPackage           `json:"results"`
	Groups           map[string]int               `json:"groups,omitempty"`
	Capabilities     map[string]int               `json:"capabilities,omitempty"`
	Summary          checkSummary                 `json:"summary"`
	ChecksByCategory map[string]int               `json:"checksByCategory"`
	Debug            bool                         `json:"debug,omitempty"`
	Hint             string                       `json:"hint,omitempty"`
}

type packageJSONResult struct {
	OK     bool           `json:"ok"`
	Issues []checks.Issue `json:"issues"`
}

type checkJSONPackage struct {
	Package string         `json:"package"`
	OK      bool           `json:"ok"`
	Issues  []checks.Issue `json:"issues"`
}

type checkSummary struct {
	Total           int `json:"total"`
	Passed          int `json:"passed"`
	Failed          int `json:"failed"`
	Errors          int `json:"errors"`
	Warnings        int `json:"warnings"`
	ChecksRun       int `json:"checksRun"`
	PackagesChecked int `json:"packagesChecked"`
}

func outputCheckJSON(results map[string]checks.PackageResult, cfg *config.DevkitConfig, checksByCategory map[string]int, packagesChecked int) error {
	out := checkJSONResult{
		OK:               true,
		Packages:         make(map[string]packageJSONResult, len(results)),
		Groups:           map[string]int{},
		Capabilities:     map[string]int{},
		ChecksByCategory: checksByCategory,
		Debug:            debugMode,
	}
	out.Summary.ChecksRun = sumCheckCounts(out.ChecksByCategory)
	out.Summary.PackagesChecked = packagesChecked

	for name, r := range results {
		hasErrors := false
		for _, issue := range r.Issues {
			if issue.Severity == checks.SeverityError {
				hasErrors = true
				out.Summary.Errors++
			} else if issue.Severity == checks.SeverityWarning {
				out.Summary.Warnings++
			}
		}

		ok := !hasErrors
		if !ok {
			out.OK = false
		}
		out.Packages[name] = packageJSONResult{
			OK:     ok,
			Issues: r.Issues,
		}
		out.Results = append(out.Results, checkJSONPackage{
			Package: name,
			OK:      ok,
			Issues:  r.Issues,
		})
		category := r.Package.Category
		if r.Package.Name == "workspace" {
			category = "workspace"
		}
		out.Groups[category]++
		for _, issue := range r.Issues {
			cap := string(issue.Capability)
			if cap == "" {
				cap = string(checks.CapabilityManual)
			}
			out.Capabilities[cap]++
		}

		if r.Package.Name != "workspace" {
			out.Summary.Total++
			if ok {
				out.Summary.Passed++
			} else {
				out.Summary.Failed++
			}
		}
	}

	if !out.OK {
		out.Hint = "run 'kb-devkit fix' to auto-fix issues where possible"
	}

	if !out.OK {
		return diag.New("KB_DEVKIT_STANDARDS_VIOLATIONS", "Workspace standards violations were found.",
			diag.WithHint("Review the findings, correct them, then rerun `kb-devkit check`."),
			diag.WithData(out))
	}
	return JSONOut(out)
}

// ─── Human output ─────────────────────────────────────────────────────────────

func outputCheckHuman(results map[string]checks.PackageResult, cfg *config.DevkitConfig, countsByCategory map[string]int, packagesChecked int) error {
	o := newOutput()

	checksRun := sumCheckCounts(countsByCategory)
	clikitcommand.Printf("Workspace check: %d package(s), %d check(s)\n", packagesChecked, checksRun)
	categories := make([]string, 0, len(countsByCategory))
	for category := range countsByCategory {
		categories = append(categories, category)
	}
	sort.Strings(categories)
	for _, category := range categories {
		count := countsByCategory[category]
		clikitcommand.Printf("   %s: %d check(s)\n", category, count)
	}
	totalErrors := 0
	totalWarnings := 0
	totalPassed := 0
	packagesWithFindings := 0

	for _, r := range results {
		if r.Skipped {
			continue
		}

		errCount := 0
		warnCount := 0
		for _, issue := range r.Issues {
			if issue.Severity == checks.SeverityError {
				errCount++
			} else {
				warnCount++
			}
		}
		totalErrors += errCount
		totalWarnings += warnCount
		if (errCount > 0 || warnCount > 0) && r.Package.Name != "workspace" {
			packagesWithFindings++
		}

		if errCount == 0 && warnCount == 0 {
			if r.Package.Name != "workspace" {
				totalPassed++
			}
			continue
		}

		state := "warning"
		if errCount > 0 {
			state = "error"
		}

		clikitcommand.Printf("\n%s %s\n", o.StatusIcon(state), o.label.Render(r.Package.Name))
		clikitcommand.Printf("   %s\n", o.dim.Render(r.Package.RelPath))

		for _, issue := range r.Issues {
			prefix := o.SeverityColor(string(issue.Severity))
			msg := issue.Message
			if issue.File != "" {
				msg += o.dim.Render(" — " + issue.File)
			}
			clikitcommand.Printf("   %s  %s\n", prefix, msg)
			if issue.Fix != "" {
				clikitcommand.Printf("          %s\n", o.dim.Render("fix: "+issue.Fix))
			}
		}
	}

	clikitcommand.Println()
	if totalErrors == 0 && totalWarnings == 0 {
		o.OK(fmt.Sprintf("All %d packages passed", totalPassed))
		return nil
	}

	o.Err(fmt.Sprintf("%d error(s), %d warning(s) across %d packages", totalErrors, totalWarnings, packagesWithFindings))
	return diag.New("KB_DEVKIT_STANDARDS_VIOLATIONS", "Workspace standards violations were found.",
		diag.WithHint("Review the findings, correct them, then rerun `kb-devkit check`."))
}

type externalCheckFailure struct {
	Check      string
	Cause      string
	Diagnostic *diag.Diag
}

func externalCheckFailureError(failures []externalCheckFailure, results map[string]checks.PackageResult) error {
	for _, failure := range failures {
		if failure.Diagnostic != nil {
			return failure.Diagnostic
		}
	}
	checks := make(map[string]bool, len(failures))
	causes := make([]string, 0, len(failures))
	for _, failure := range failures {
		checks[failure.Check] = true
		causes = append(causes, fmt.Sprintf("%s: %s", failure.Check, failure.Cause))
	}
	checkNames := sortedKeys(checks)
	return diag.New("KB_DEVKIT_CHECK_EXECUTION_FAILED", "",
		diag.WithMeta(map[string]any{"check": strings.Join(checkNames, ", ")}),
		diag.WithCause(strings.Join(causes, "; ")), diag.WithData(map[string]any{"checks": failures, "packages": results}))
}

func checkExecutionFailures(results map[string]checks.PackageResult) []externalCheckFailure {
	failures := make(map[string]map[string]*diag.Diag)
	for _, result := range results {
		for _, issue := range result.Issues {
			if issue.CheckFailure {
				if failures[issue.Check] == nil {
					failures[issue.Check] = make(map[string]*diag.Diag)
				}
				if diagnostic, exists := failures[issue.Check][issue.Cause]; !exists || diagnostic == nil {
					failures[issue.Check][issue.Cause] = issue.FailureDiagnostic
				}
			}
		}
	}
	checkNames := make([]string, 0, len(failures))
	for checkName := range failures {
		checkNames = append(checkNames, checkName)
	}
	sort.Strings(checkNames)
	result := make([]externalCheckFailure, 0, len(checkNames))
	for _, checkName := range checkNames {
		causes := make([]string, 0, len(failures[checkName]))
		for cause := range failures[checkName] {
			causes = append(causes, cause)
		}
		sort.Strings(causes)
		for _, cause := range causes {
			result = append(result, externalCheckFailure{Check: checkName, Cause: cause, Diagnostic: failures[checkName][cause]})
		}
	}
	return result
}

func validateCheckSelection(ws *workspace.Workspace, cfg *config.DevkitConfig, registry *checks.Registry, only []string, packageName string) error {
	known := make(map[string]bool)
	for _, rule := range registry.All() {
		known[rule.Name()] = true
	}
	if checks.CircularEnabled(cfg) {
		known["circular"] = true
	}
	for _, name := range only {
		if !known[name] {
			return diag.New("KB_RUNTIME_INPUT_INVALID", fmt.Sprintf("Unknown check %q in --only.", name),
				diag.WithHint(fmt.Sprintf("Choose a check from: %s.", strings.Join(sortedKeys(known), ", "))))
		}
	}
	for _, name := range only {
		if name == "circular" && packageName != "" {
			return diag.New("KB_RUNTIME_INPUT_INVALID", "The circular check analyzes the full workspace graph and cannot be limited to one package.",
				diag.WithHint("Rerun without --package: kb-devkit check --only circular"))
		}
	}
	pkgs := ws.Packages
	if packageName != "" {
		pkgs = ws.FilterByName([]string{packageName})
		if len(pkgs) == 0 {
			return diag.New("KB_RUNTIME_INPUT_INVALID", fmt.Sprintf("Package %q was not found in the workspace.", packageName),
				diag.WithHint("Run `kb-devkit deps` to list discovered packages, then rerun with one of those package names."))
		}
	}
	count := 0
	for _, pkg := range pkgs {
		preset, err := config.ResolvePreset(pkg.Preset, cfg)
		if err != nil {
			continue
		}
		count += len(registry.ApplicableRulesFor(pkg, preset, only))
	}
	count += len(registry.WorkspaceRules(only))
	if len(only) == 1 && only[0] == "circular" && checks.CircularEnabled(cfg) {
		count++
	}
	if count == 0 {
		if packageName != "" && len(pkgs) == 1 {
			pkg := pkgs[0]
			if preset, err := config.ResolvePreset(pkg.Preset, cfg); err == nil {
				if checkName, categories := registry.PackageApplicabilityMismatch(pkg, preset, only); checkName != "" {
					return diag.New("KB_RUNTIME_INPUT_INVALID",
						fmt.Sprintf("Check %q does not apply to package %q because its category is %q.", checkName, pkg.Name, pkg.Category),
						diag.WithHint(fmt.Sprintf("%s applies to these categories: %s. Select a package in one of those categories, or run `kb-devkit check` without --package.", checkName, strings.Join(categories, ", "))))
				}
			}
		}
		return diag.New("KB_RUNTIME_INPUT_INVALID", "The check selection matched no workspace packages or checks.",
			diag.WithHint("Run `kb-devkit check --help` to review filters, then select a package and check that exist in this workspace."))
	}
	return nil
}

func selectedCheckCounts(ws *workspace.Workspace, cfg *config.DevkitConfig, registry *checks.Registry, only []string, packageName string) map[string]int {
	counts := make(map[string]int)
	pkgs := ws.Packages
	if packageName != "" {
		pkgs = ws.FilterByName([]string{packageName})
	}
	for _, pkg := range pkgs {
		preset, err := config.ResolvePreset(pkg.Preset, cfg)
		if err != nil {
			continue
		}
		applicableCount := len(registry.ApplicableRulesFor(pkg, preset, only))
		if applicableCount > 0 {
			counts[pkg.Category] += applicableCount
		}
	}
	if workspaceRules := len(registry.WorkspaceRules(only)); workspaceRules > 0 {
		counts["workspace"] = workspaceRules
	}
	if len(only) == 1 && only[0] == "circular" && checks.CircularEnabled(cfg) {
		counts["circular"] = 1
	}
	return counts
}

func checkedPackageCount(results map[string]checks.PackageResult) int {
	count := 0
	for _, result := range results {
		if result.Package.Name != "workspace" && !result.Skipped && result.ChecksRun > 0 {
			count++
		}
	}
	return count
}

func sumCheckCounts(counts map[string]int) int {
	total := 0
	for _, count := range counts {
		total += count
	}
	return total
}

func sortedKeys(values map[string]bool) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
