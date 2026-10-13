// kb-monitor is a remote observability tool for deployed services.
package main

import (
	"os"

	"github.com/kb-labs/kb-monitor/cmd"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	cmd.SetVersionInfo(version, commit, date)
	os.Exit(cmd.Execute().Code())
}
