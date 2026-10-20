package cmd

import (
	"fmt"
	clikitcommand "github.com/kb-labs/clikit/command"

	"github.com/kb-labs/devkit/internal/engine"
	"github.com/spf13/cobra"
)

var affectedCmd = &cobra.Command{
	Use:   "affected",
	Short: "List packages affected by the configured diff",
	Long:  "Resolves the workspace dependency graph without running any task.\nThis is intended for fast CI discovery; use `run --affected` to execute checks.",
	RunE: func(cmd *cobra.Command, args []string) error {
		ws, cfg, err := loadWorkspace()
		if err != nil {
			return err
		}

		pkgs, err := engine.AffectedPackages(ws, cfg)
		if err != nil {
			return fmt.Errorf("affected: %w", err)
		}

		if jsonMode {
			packages := make([]string, 0, len(pkgs))
			for _, pkg := range pkgs {
				packages = append(packages, pkg.Name)
			}
			return JSONOut(map[string]any{"packages": packages})
		}

		if len(pkgs) == 0 {
			newOutput().OK("No affected packages")
			return nil
		}
		for _, pkg := range pkgs {
			clikitcommand.Println(pkg.Name)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(affectedCmd)
}
