//go:build kb_create_test_support

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kb-labs/clikit/command"
	"github.com/kb-labs/clikit/diag"
	"github.com/kb-labs/create/v2/host"
	"github.com/kb-labs/create/v2/supervisor"
)

const hostPIDFileEnvironment = "TEST_HOST_PID_FILE"

func main() {
	command.Exit(run())
}

func run() diag.ExitStatus {
	root := ""
	for index, arg := range os.Args {
		if arg == "--platform-root" && index+1 < len(os.Args) {
			root = os.Args[index+1]
			break
		}
	}
	if root == "" {
		return emitFailure(errors.New("test supervisor did not receive platform root"))
	}
	deadline, handedDown, err := host.ConsumeStartupDeadlineEnvironment()
	if err != nil {
		return emitFailure(err)
	}
	if !handedDown {
		return emitFailure(errors.New("test supervisor did not receive startup deadline"))
	}
	runner, err := host.NewRunner(root)
	if err != nil {
		return emitFailure(err)
	}
	runner.SetStartupDeadline(deadline)
	pidFile := os.Getenv(hostPIDFileEnvironment)
	var once sync.Once
	var pidFileErr error
	host.SetProcessIdentityReaderForTest(runner, func(pid int) (string, error) {
		once.Do(func() { pidFileErr = os.WriteFile(pidFile, []byte(strconv.Itoa(pid)), 0o600) })
		if pidFileErr != nil {
			return "", pidFileErr
		}
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		timer := time.NewTimer(time.Until(deadline))
		defer timer.Stop()
		for {
			log, err := os.ReadFile(runner.State.HostLog())
			if err == nil && strings.Contains(string(log), "fakehost: ready") {
				return "", errors.New("test injected unavailable host process identity")
			}
			select {
			case <-ticker.C:
			case <-timer.C:
				return "", errors.New("test host did not become ready before the startup deadline")
			}
		}
	})
	err = (&supervisor.Supervisor{Runner: runner, State: runner.State, PlatformRoot: runner.InstallRoot}).Run(context.Background())
	if err == nil {
		err = errors.New("test injected unavailable host process identity unexpectedly matched")
	}
	return emitFailure(err)
}

func emitFailure(err error) diag.ExitStatus {
	if encodeErr := json.NewEncoder(os.Stdout).Encode(map[string]any{"error": err}); encodeErr != nil {
		fmt.Fprintln(os.Stderr, "encode test supervisor failure:", encodeErr)
	}
	return diag.ExitCode(diag.New("KB_RUNTIME_UNEXPECTED", "The test supervisor failed.", diag.WithCauseAndReason(err.Error()), diag.WithHint("Rerun the identity supervisor test.")))
}
