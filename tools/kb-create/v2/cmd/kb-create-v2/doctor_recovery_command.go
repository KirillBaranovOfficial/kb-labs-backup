package v2cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/charmbracelet/x/term"
	"github.com/kb-labs/clikit/diag"
	"github.com/kb-labs/create/v2/contracts"
	"github.com/kb-labs/create/v2/host"
	"github.com/kb-labs/create/v2/supervisor"
)

type doctorRecoveryResult struct {
	OK        bool   `json:"ok"`
	Operation string `json:"operation"`
	Recovered bool   `json:"recovered"`
	Output    string `json:"output"`
}

func (doctorRecoveryResult) doctorOperationResult() {}

func executeRecover(inv doctorCommandInput) (doctorRecoveryResult, *diag.Diag) {
	command := hostCommand{PlatformRoot: inv.PlatformRoot, Foreground: inv.Foreground, Executable: inv.Executable, Version: buildVersion}
	runner, state, failure := command.setup("recover")
	if failure != nil {
		failure.ProcessExitCode = diag.ExitInput
		return doctorRecoveryResult{}, failure
	}
	if !inv.Recover.Yes {
		if inv.Recover.JSON || !term.IsTerminal(os.Stdin.Fd()) {
			failure := contracts.NewDiag("KB_INSTALL_CONFIRMATION_REQUIRED", "This destructive operation requires explicit confirmation.",
				fmt.Sprintf("rerun `kb-create doctor --platform-root %q --recover --yes`", inv.PlatformRoot), fmt.Errorf("confirmation unavailable without a terminal"))
			failure.ProcessExitCode = diag.ExitInput
			return doctorRecoveryResult{}, failure
		}
		fmt.Fprintf(os.Stdout, "This will run the platform's administrator recovery tool for %s. Continue? [y/N] ", inv.PlatformRoot)
		confirmed, err := readConfirmation(os.Stdin)
		if err != nil {
			failure := contracts.NewDiag("KB_INSTALL_CONFIRMATION_REQUIRED", "This destructive operation requires explicit confirmation.", "rerun the command with `--yes`", err)
			failure.ProcessExitCode = diag.ExitInput
			return doctorRecoveryResult{}, failure
		}
		if !confirmed {
			return doctorRecoveryResult{}, contracts.NewDiag("KB_INSTALL_OPERATION_CANCELLED", "The operation was cancelled.", "rerun the command and confirm when prompted", nil)
		}
	}
	release, err := state.AcquireOperation("recover")
	if err != nil {
		return doctorRecoveryResult{}, asDiag(err)
	}
	defer release()
	if runner.Spec.Recover == nil {
		return doctorRecoveryResult{}, contracts.NewDiag(contracts.CodeRecoveryUnavailable, "The installed platform does not ship a recovery tool.", "Update the platform with \"kb-create update\", then retry.", nil)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	status, _ := runner.Status(ctx)
	if _, supervised := supervisor.LivePID(state, inv.PlatformRoot); supervised || status.State != host.StateStopped {
		return doctorRecoveryResult{}, contracts.NewDiag(contracts.CodeRecoveryHostRunning, "Recovery needs the host to be stopped, but it is running.", "Run \"kb-create stop\", recover, then \"kb-create start\".", nil)
	}
	var toolOutput bytes.Buffer
	code, err := runner.RunTool(ctx, *runner.Spec.Recover, inv.Recover.args(), os.Stdin, &toolOutput, os.Stderr)
	if err != nil {
		return doctorRecoveryResult{}, contracts.NewDiag(contracts.CodeRecoveryFailed, "The platform's recovery tool could not be run.", "Inspect the platform installation with \"kb-create doctor\".", err)
	}
	if code != 0 {
		failure := contracts.NewDiag(contracts.CodeRecoveryFailed, "The platform recovery tool reported failure.", "Inspect the platform installation with `kb-create doctor`.", nil)
		failure.ProcessExitCode = code
		return doctorRecoveryResult{}, failure
	}
	return doctorRecoveryResult{OK: true, Operation: "recover", Recovered: true, Output: strings.TrimSpace(toolOutput.String())}, nil
}
