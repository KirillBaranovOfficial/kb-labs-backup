// Package child launches and reaps a command together with its owned process tree.
package child

import (
	"context"
	"os/exec"
	"time"
)

const pipeWaitDelay = 300 * time.Millisecond

// ConfigureCommand puts a command in its own process group on supported Unix
// platforms. It is used by the clikit supervisor for its managed runs.
func ConfigureCommand(command *exec.Cmd) { configureCommand(command) }

// Run starts a command, terminates its process tree when ctx is canceled, and
// bounds waiting for inherited output pipes after the tree exits.
func Run(ctx context.Context, command *exec.Cmd) error {
	return runCommand(ctx, command, pipeWaitDelay)
}
