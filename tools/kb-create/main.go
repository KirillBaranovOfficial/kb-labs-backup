package main

import (
	"github.com/kb-labs/clikit/command"
	v2cli "github.com/kb-labs/create/v2/cmd/kb-create-v2"
)

// Build-time variables injected by goreleaser / go build -ldflags.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	v2cli.SetVersionInfo(version, commit, date)
	command.Exit(v2cli.Execute())
}
