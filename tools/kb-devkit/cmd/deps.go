package cmd

import (
	"fmt"
	clikitcommand "github.com/kb-labs/clikit/command"

	"github.com/kb-labs/devkit/internal/checks"
	"github.com/spf13/cobra"
)

var (
	depsCircular bool
	depsWhy      string
)

var depsCmd = &cobra.Command{
	Use:   "deps",
	Short: "Analyze workspace dependencies",
	Long: `Analyzes dependencies across the workspace.

--circular  detect circular dependencies using Tarjan SCC
--why       explain why package X is in the dependency graph`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ws, cfg, err := loadWorkspace()
		if err != nil {
			return err
		}
		_ = cfg

		if depsCircular {
			result := checks.DetectCircular(ws.Packages)

			if jsonMode {
				if len(result) > 0 {
					return commandFailure("kb-devkit deps")
				}
				return JSONOut(map[string]any{
					"circles": result,
				})
			}

			o := newOutput()
			if len(result) == 0 {
				o.OK("No circular dependencies detected")
				return nil
			}

			o.Err(fmt.Sprintf("%d package(s) involved in circular dependencies:", len(result)))
			for pkgDir, issues := range result {
				// Find package name.
				name := pkgDir
				for _, p := range ws.Packages {
					if p.Dir == pkgDir {
						name = p.Name
						break
					}
				}
				clikitcommand.Printf("\n  %s %s\n", o.StatusIcon("error"), o.label.Render(name))
				for _, issue := range issues {
					clikitcommand.Printf("     %s\n", o.dim.Render("↳ "+issue.Message))
				}
			}
			clikitcommand.Println()
			return nil
		}

		// Default: show dependency summary.
		o := newOutput()
		o.Info(fmt.Sprintf("Workspace: %d packages discovered", len(ws.Packages)))
		clikitcommand.Println()
		o.Info("Use --circular to detect circular dependencies")
		o.Info("Use --why @scope/package to trace dependency chains")

		return nil
	},
}

func init() {
	depsCmd.Flags().BoolVar(&depsCircular, "circular", false, "detect circular dependencies")
	depsCmd.Flags().StringVar(&depsWhy, "why", "", "explain why a package is in the graph")
	rootCmd.AddCommand(depsCmd)
}
