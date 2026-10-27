package checks

import (
	"sync"
	"time"

	"github.com/kb-labs/devkit/internal/config"
	"github.com/kb-labs/devkit/internal/workspace"
)

// ProgressEvent describes one rule invocation while a package check is running.
type ProgressEvent struct {
	Package string
	Check   string
	Elapsed time.Duration
	Running bool
}

// PackageResult holds all issues for a single package.
type PackageResult struct {
	Package   workspace.Package
	Issues    []Issue
	Skipped   bool
	ChecksRun int `json:"-"`
}

// RunAll runs all rules from the registry against all packages in parallel.
// Circular detection runs once on the full graph and is merged in.
func RunAll(ws *workspace.Workspace, cfg *config.DevkitConfig, registry *Registry, only []string, progress func(ProgressEvent)) map[string]PackageResult {
	for _, rule := range registry.All() {
		if aware, ok := rule.(interface{ SetWorkspace(*workspace.Workspace) }); ok {
			aware.SetWorkspace(ws)
		}
	}

	onlySet := make(map[string]bool, len(only))
	for _, name := range only {
		onlySet[name] = true
	}

	// Run circular detection once on the full workspace graph.
	var circularResult CircularResult
	if CircularEnabled(cfg) {
		circularResult = DetectCircular(ws.Packages)
	}
	circularOnly := len(onlySet) == 1 && onlySet["circular"]

	results := make(map[string]PackageResult, len(ws.Packages))
	workspaceIssues := make([]Issue, 0)
	workspaceRules := registry.WorkspaceRules(only)
	for _, rule := range workspaceRules {
		workspaceRule := rule.(interface {
			CheckWorkspace(*workspace.Workspace) []Issue
		})
		workspaceIssues = append(workspaceIssues, runRule(rule, workspace.Package{Name: "workspace"}, progress, func() []Issue {
			return workspaceRule.CheckWorkspace(ws)
		})...)
	}
	if len(workspaceRules) > 0 {
		results["workspace"] = PackageResult{
			Package:   workspace.Package{Name: "workspace", Dir: ws.Root, RelPath: "."},
			Issues:    workspaceIssues,
			ChecksRun: len(workspaceRules),
		}
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8) // max 8 goroutines

	for _, pkg := range ws.Packages {
		pkg := pkg
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			preset, err := config.ResolvePreset(pkg.Preset, cfg)
			if err != nil {
				mu.Lock()
				results[pkg.Name] = PackageResult{Package: pkg, Skipped: true}
				mu.Unlock()
				return
			}

			rules := registry.ApplicableRulesFor(pkg, preset, only)
			if len(rules) == 0 && !circularOnly {
				return
			}

			var issues []Issue
			for _, rule := range rules {
				issues = append(issues, runRule(rule, pkg, progress, func() []Issue {
					return rule.Check(pkg, preset)
				})...)
			}

			// Merge circular results.
			if circularIssues, ok := circularResult[pkg.Dir]; ok {
				if len(onlySet) == 0 || onlySet["circular"] {
					issues = append(issues, circularIssues...)
				}
			}

			mu.Lock()
			checksRun := len(rules)
			if circularOnly {
				checksRun++
			}
			results[pkg.Name] = PackageResult{Package: pkg, Issues: issues, ChecksRun: checksRun}
			mu.Unlock()
		}()
	}

	wg.Wait()
	return results
}

func runRule(rule Rule, pkg workspace.Package, progress func(ProgressEvent), check func() []Issue) []Issue {
	started := time.Now()
	report := func(running bool) {
		if progress != nil {
			progress(ProgressEvent{Package: pkg.Name, Check: rule.Name(), Elapsed: time.Since(started), Running: running})
		}
	}
	report(true)
	stop := make(chan struct{})
	monitorDone := make(chan struct{})
	if progress != nil {
		go func() {
			defer close(monitorDone)
			ticker := time.NewTicker(10 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					report(true)
				case <-stop:
					return
				}
			}
		}()
	}
	issues := check()
	if progress != nil {
		close(stop)
		<-monitorDone
	}
	report(false)
	return issues
}
