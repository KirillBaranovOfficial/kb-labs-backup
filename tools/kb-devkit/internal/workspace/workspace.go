// Package workspace discovers and classifies packages in the monorepo.
package workspace

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kb-labs/devkit/internal/config"
)

// Package represents a single package in the monorepo.
type Package struct {
	Name        string // @acme/core-runtime
	Dir         string // absolute path
	Category    string // ts-lib, go-binary, site, ...
	Preset      string // node-lib, go-binary, ...
	Language    string // typescript, go
	RelPath     string // relative to workspace root
	BuildTarget bool   // whether the resolved devkit task graph defines a build task
	PnpmMember  bool   // whether pnpm-workspace.yaml declares this package
}

// Workspace holds all discovered packages and the root path.
type Workspace struct {
	Root       string
	Packages   []Package
	aggregates map[string][]string
	// byAbsPath is an O(1) lookup map: absolute file path → package.
	byAbsPath map[string]*Package
}

// New creates a Workspace by discovering and classifying packages.
func New(root string, cfg *config.DevkitConfig) (*Workspace, error) {
	pkgs, err := discoverPackages(root, cfg)
	if err != nil {
		return nil, fmt.Errorf("discover packages: %w", err)
	}

	ws := &Workspace{
		Root:       root,
		Packages:   pkgs,
		aggregates: discoverAggregates(pkgs),
		byAbsPath:  make(map[string]*Package),
	}

	// Resolve the build task through the same category/task model used by the scheduler.
	if cfg != nil {
		for i := range ws.Packages {
			if build, ok := cfg.Tasks["build"]; ok {
				ws.Packages[i].BuildTarget = !ws.IsAggregatePackage(ws.Packages[i].Name) && build.ResolveVariant(ws.Packages[i].Category) != nil
			}
		}
	}

	// Build reverse index: every file under a package dir maps to that package.
	// We only store the package dir itself as the key; callers use PackageByPath
	// which walks up the path to find the matching entry.
	for i := range ws.Packages {
		ws.byAbsPath[ws.Packages[i].Dir] = &ws.Packages[i]
	}

	return ws, nil
}

// AggregateChildren returns the workspace package names nested below packageName.
// The topology is derived once from the same discovered package list used by
// the scheduler, so checks and task resolution share one owner.
func (ws *Workspace) AggregateChildren(packageName string) []string {
	return append([]string(nil), ws.aggregates[packageName]...)
}

// IsAggregatePackage reports whether the package directory contains another
// discovered workspace package.
func (ws *Workspace) IsAggregatePackage(packageName string) bool {
	return len(ws.aggregates[packageName]) > 0
}

func discoverAggregates(packages []Package) map[string][]string {
	aggregates := make(map[string][]string)
	for _, parent := range packages {
		for _, child := range packages {
			if parent.Name == child.Name {
				continue
			}
			rel, err := filepath.Rel(parent.Dir, child.Dir)
			if err != nil || rel == "." || filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				continue
			}
			aggregates[parent.Name] = append(aggregates[parent.Name], child.Name)
		}
	}
	for name := range aggregates {
		sort.Strings(aggregates[name])
	}
	return aggregates
}

// FilterByName returns packages matching any of the given names.
func (ws *Workspace) FilterByName(names []string) []Package {
	set := make(map[string]bool, len(names))
	for _, n := range names {
		set[n] = true
	}
	var result []Package
	for _, p := range ws.Packages {
		if set[p.Name] {
			result = append(result, p)
		}
	}
	return result
}

// FilterByCategory returns packages in the given category.
func (ws *Workspace) FilterByCategory(category string) []Package {
	var result []Package
	for _, p := range ws.Packages {
		if p.Category == category {
			result = append(result, p)
		}
	}
	return result
}

// PackageByPath returns the package that owns the given absolute file path.
// It walks upward from the path until it finds a known package dir.
func (ws *Workspace) PackageByPath(absPath string) (*Package, bool) {
	cur := absPath
	for {
		if pkg, ok := ws.byAbsPath[cur]; ok {
			return pkg, true
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		cur = parent
	}
	return nil, false
}

// TrackedFiles returns Git-tracked paths and the subset that have no discovered
// package owner. PackageByPath is the same ownership rule used by affected
// selection; paths are returned even when deleted from the working tree.
func (ws *Workspace) TrackedFiles() (all, unowned []string, err error) {
	command := exec.Command("git", "-C", ws.Root, "ls-files", "--cached", "-z")
	output, err := command.Output()
	if err != nil {
		return nil, nil, fmt.Errorf("list tracked workspace files: %w", err)
	}

	for _, rel := range strings.Split(string(output), "\x00") {
		if rel == "" {
			continue
		}
		absPath := filepath.Join(ws.Root, filepath.FromSlash(rel))
		all = append(all, absPath)
		if _, owned := ws.PackageByPath(absPath); !owned {
			unowned = append(unowned, absPath)
		}
	}
	sort.Strings(all)
	sort.Strings(unowned)
	return all, unowned, nil
}
