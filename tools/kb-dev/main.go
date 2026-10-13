// kb-dev is a local service manager for the KB Labs platform.
package main

import "github.com/kb-labs/dev/cmd"

import "os"

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	cmd.SetVersionInfo(version, commit, date)
	os.Exit(cmd.Execute().Code())
}
