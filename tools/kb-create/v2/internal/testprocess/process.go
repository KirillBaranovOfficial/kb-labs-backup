package testprocess

import (
	"errors"
	"os"
	"os/exec"

	"github.com/kb-labs/clikit/proc/child"
)

type processTest interface {
	Helper()
	Cleanup(func())
	Errorf(format string, args ...any)
}

// Start owns a test child process group from the moment it starts until test cleanup.
func Start(t processTest, command *exec.Cmd) error {
	t.Helper()
	if err := child.Start(command, child.ProcessGroup); err != nil {
		return err
	}
	t.Cleanup(func() {
		if err := child.KillTree(command.Process.Pid); err != nil && !errors.Is(err, os.ErrProcessDone) {
			t.Errorf("kill test process group %d: %v", command.Process.Pid, err)
		}
		if command.ProcessState == nil {
			if err := command.Wait(); err != nil {
				var exit *exec.ExitError
				if !errors.As(err, &exit) {
					t.Errorf("wait for test process group %d: %v", command.Process.Pid, err)
				}
			}
		}
	})
	return nil
}
