// Package child launches and reaps a command together with its owned process tree.
package child

import (
	"context"
	"fmt"
	"os/exec"
	"time"
)

const pipeWaitDelay = 300 * time.Millisecond

// Mode describes the lifetime and ownership relationship for a started child.
type Mode uint8

const (
	// ProcessGroup isolates a command so its whole tree can be signaled together.
	ProcessGroup Mode = iota + 1
	// Detached lets a long-lived service outlive the process that starts it.
	Detached
)

// Start configures and starts a child using the requested process ownership mode.
// Callers own waiting and process-tree termination after a successful start.
func Start(command *exec.Cmd, mode Mode) error {
	if command == nil {
		return fmt.Errorf("start child: command is nil")
	}
	if err := Configure(command, mode); err != nil {
		return err
	}
	return command.Start()
}

// Configure applies the OS process attributes for a mode without starting the
// command. This is used when a caller must add platform attributes before start.
func Configure(command *exec.Cmd, mode Mode) error {
	if command == nil {
		return fmt.Errorf("configure child: command is nil")
	}
	return configureCommand(command, mode)
}

// TerminateTree requests graceful termination of the process tree rooted at pid.
func TerminateTree(pid int) error { return terminateTree(pid) }

// KillTree forcefully terminates the process tree rooted at pid.
func KillTree(pid int) error { return killTree(pid) }

// Run starts a command, terminates its process tree when ctx is canceled, and
// bounds waiting for inherited output pipes after the tree exits.
func Run(ctx context.Context, command *exec.Cmd) error {
	return runCommand(ctx, command, pipeWaitDelay)
}
