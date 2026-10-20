// Package cmd implements the kb-monitor CLI commands.
package cmd

import (
	"fmt"
	"os"

	clikitcommand "github.com/kb-labs/clikit/command"
	"github.com/kb-labs/clikit/diag"
	"github.com/kb-labs/clikit/result"
	"github.com/spf13/cobra"
)

var (
	jsonMode   bool
	configPath string
)

// SetVersionInfo is called from main.go with values injected at build time via -ldflags.
func SetVersionInfo(version, commit, date string) {
	rootCmd.SetVersionTemplate(fmt.Sprintf(
		"kb-monitor %s (commit %s, built %s)\n", version, commit, date,
	))
	rootCmd.Version = version
}

var rootCmd = &cobra.Command{
	Use:   "kb-monitor",
	Short: "Remote observability for deployed services",
	Long: `kb-monitor observes services deployed via kb-deploy.
Reads the same .kb/deploy.yaml configuration.

Commands:
  health   Check service health status
  status   Show running state, uptime, and image SHA
  logs     Fetch or stream service logs
  exec     Execute a command inside a container

Examples:
  kb-monitor health
  kb-monitor health kb-labs-web
  kb-monitor status --json
  kb-monitor logs kb-labs-web --lines 100
  kb-monitor logs kb-labs-web --follow
  kb-monitor exec kb-labs-web -- df -h`,
	SilenceUsage:  true,
	SilenceErrors: true,
}

// Execute is the main entry point called from main.go.
func Execute() diag.ExitStatus {
	args := os.Args[1:]
	if clikitcommand.HandleCobraHelp(rootCmd, args, os.Stdout) {
		return diag.SuccessStatus()
	}
	_, _, findErr := rootCmd.Find(args)
	if status, handled := clikitcommand.HandleUnknownCommand(rootCmd, args, os.Stdout, os.Stderr, result.ResolveMode(jsonMode, false, ""), findErr == nil); handled {
		return status
	}
	err := rootCmd.Execute()
	if err == nil {
		return diag.SuccessStatus()
	}
	diagnostic := diag.FromError(err)
	diagnostic.Binary = rootCmd.Name()
	if jsonMode {
		if diagnostic.Error() != "" {
			_ = JSONOut(map[string]any{"ok": false, "hint": diagnostic.Error()})
		}
	} else {
		out := newOutput()
		out.Err(diagnostic.Error())
	}
	return diag.ExitCode(diagnostic)
}

func init() {
	rootCmd.PersistentFlags().BoolVar(&jsonMode, "json", false, "output as structured JSON")
	rootCmd.PersistentFlags().StringVar(&configPath, "config", "", "path to deploy config file (default: .kb/deploy.yaml)")
}
