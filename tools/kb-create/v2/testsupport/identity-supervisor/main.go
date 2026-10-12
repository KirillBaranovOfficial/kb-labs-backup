//go:build kb_create_test_support

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"sync"

	"github.com/kb-labs/create/v2/host"
	"github.com/kb-labs/create/v2/supervisor"
)

const hostPIDFileEnvironment = "KB_CREATE_TEST_HOST_PID_FILE"

func main() {
	root := ""
	for index, arg := range os.Args {
		if arg == "--platform-root" && index+1 < len(os.Args) {
			root = os.Args[index+1]
			break
		}
	}
	if root == "" {
		emitFailure(errors.New("test supervisor did not receive platform root"))
		return
	}
	deadline, handedDown, err := host.ConsumeStartupDeadlineEnvironment()
	if err != nil {
		emitFailure(err)
		return
	}
	if !handedDown {
		emitFailure(errors.New("test supervisor did not receive startup deadline"))
		return
	}
	runner, err := host.NewRunner(root)
	if err != nil {
		emitFailure(err)
		return
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
		return "", errors.New("test injected unavailable host process identity")
	})
	err = (&supervisor.Supervisor{Runner: runner, State: runner.State, PlatformRoot: runner.InstallRoot}).Run(context.Background())
	if err == nil {
		err = errors.New("test injected unavailable host process identity unexpectedly matched")
	}
	emitFailure(err)
}

func emitFailure(err error) {
	if encodeErr := json.NewEncoder(os.Stdout).Encode(map[string]any{"error": err}); encodeErr != nil {
		fmt.Fprintln(os.Stderr, "encode test supervisor failure:", encodeErr)
		os.Exit(1)
	}
}
