package cmd

import (
	"context"
	"errors"
	"strings"

	clikitcommand "github.com/kb-labs/clikit/command"
	"github.com/kb-labs/clikit/diag"
	sharedtoolchain "github.com/kb-labs/clikit/toolchain"
	"github.com/kb-labs/dev/internal/manager"
	"github.com/spf13/cobra"
)

// ensureSupportedRuntime validates before any lifecycle mutation. Interactive
// human commands may opt into the shared Node update flow; JSON/agent calls
// remain deterministic and never prompt or mutate the machine.
func ensureSupportedRuntime(cmd *cobra.Command, mgr *manager.Manager) error {
	err := mgr.ValidateRuntime()
	if err == nil || jsonMode {
		return err
	}
	var issue *diag.Diag
	if !errors.As(err, &issue) || issue.Code != "KB_NODE_VERSION_UNSUPPORTED" {
		return err
	}
	interaction := clikitcommand.FromCommand(cmd)
	answer, refusal := interaction.Ask(issue.Error()+"\nWould you like to update now? [y/N] ", issue.Hint)
	if refusal != nil {
		// Nobody to ask (script, hook, pipe): the unsupported runtime is the failure, not the missing terminal.
		return err
	}
	if !strings.EqualFold(answer, "y") && !strings.EqualFold(answer, "yes") {
		return err
	}
	nodePath, err := sharedtoolchain.UpdateNode(context.Background())
	if err != nil {
		return err
	}
	return mgr.UseNode(nodePath)
}
