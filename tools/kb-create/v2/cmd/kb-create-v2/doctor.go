package v2cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/kb-labs/create/v2/contracts"
	"github.com/kb-labs/create/v2/doctor"
	"github.com/kb-labs/create/v2/host"
	"github.com/kb-labs/create/v2/installed"
	"github.com/kb-labs/create/v2/lifecycle"
	"github.com/kb-labs/create/v2/receipt"
	"github.com/kb-labs/create/v2/runtime"
	"github.com/kb-labs/create/v2/shim"
	"github.com/kb-labs/create/v2/supervisor"
	"github.com/kb-labs/create/v2/swap"
)

// recoverRequest is `doctor --recover`: the flags the launcher hands to the
// platform's recovery tool. The launcher validates none of them; the tool owns
// what an administrator account is.
type recoverRequest struct {
	Enabled       bool
	Email, Tenant string
	PasswordStdin bool
	Generate      bool
	Yes           bool
	JSON          bool
}

func (r recoverRequest) args() []string {
	var args []string
	if r.Email != "" {
		args = append(args, "--email", r.Email)
	}
	if r.Tenant != "" {
		args = append(args, "--tenant", r.Tenant)
	}
	for _, flag := range []struct {
		name string
		set  bool
	}{{"--password-stdin", r.PasswordStdin}, {"--generate", r.Generate}, {"--yes", r.Yes}, {"--json", r.JSON}} {
		if flag.set {
			args = append(args, flag.name)
		}
	}
	return args
}

// runDoctor reports two separate things: whether the installation on disk is
// complete and runnable, and whether the platform it runs is healthy. With
// --fix it repairs safe manifest defaults and restarts the host; with --recover
// it hands the administrator account to the platform's recovery tool.
func (i invocation) runDoctor(output *os.File) int {
	if i.Recover.Enabled {
		return i.runRecover(output)
	}
	if i.PlatformRoot == "" {
		i.PlatformRoot = defaultPlatformRoot()
	}
	if i.DoctorInput == "" {
		if _, err := receipt.Read(i.PlatformRoot); err != nil {
			write(output, failure(contracts.CodeReceiptUnavailable, "No active installation receipt was found.", "Run `kb-create install` to create the platform, then retry `kb-create doctor`.", err))
			return 1
		}
	}
	var input doctor.Input
	var err error
	var manifestFindings []doctor.Finding
	if i.DoctorInput != "" {
		data, readErr := os.ReadFile(i.DoctorInput)
		if readErr != nil {
			write(output, failure("KB_INSTALL_INPUT_REQUIRED", "doctor input could not be read", "supply a readable V2 doctor input JSON file", readErr))
			return 2
		}
		input, err = doctor.Decode(data)
	} else {
		if i.PlatformRoot == "" {
			write(output, failure("KB_INSTALL_INPUT_REQUIRED", "--platform-root is required for automatic doctor", "pass the V2 platform root or a manifest-derived --doctor-input", nil))
			return 2
		}
		active, readErr := receipt.Read(i.PlatformRoot)
		if readErr != nil {
			err = fmt.Errorf("read active V2 receipt: %w", readErr)
		} else {
			issues, inspectErr := installed.InspectProjections(i.PlatformRoot, active.Plan.Artifacts)
			if inspectErr != nil {
				err = inspectErr
			} else if len(issues) > 0 {
				manifestFindings = projectionFindings(issues)
			} else {
				input, err = automaticDoctorInput(i.PlatformRoot)
			}
		}
	}
	if err != nil {
		write(output, failure("KB_INSTALL_DOCTOR_INPUT_INVALID", "installed manifest diagnostics could not be prepared", "install exact V2 artifacts that ship matching V2 manifests", err))
		return 2
	}
	findings := doctor.Diagnose(input.Manifests, input.Configured)
	findings = append(findings, manifestFindings...)
	response := doctor.Response{Findings: findings, Repair: doctor.PlanRepair(findings)}
	if i.PlatformRoot != "" {
		if active, readErr := receipt.Read(i.PlatformRoot); readErr == nil {
			response.PlatformChannel = active.Plan.PlatformChannel
			response.PlatformVersion = active.Plan.ServiceGraph.PlatformVersion
		}
		state, stateErr := host.StateHome()
		if stateErr != nil {
			write(output, map[string]any{"ok": false, "error": asDiag(stateErr)})
			return 2
		}
		installation := doctor.InspectInstallation(i.PlatformRoot, state)
		if i.Executable != "" && !shim.Correct(i.Executable, i.PlatformRoot) {
			installation.Checks = append(installation.Checks, doctor.Check{ID: "kb-command-shim", Detail: "The `kb` command shim is missing or points to another platform.", Hint: "Run `kb-create install` to repair the command shim."})
			installation.OK = false
		}
		platform := i.inspectPlatform(state)
		for index := range platform.Checks {
			if platform.Checks[index].ID == "adapters" && strings.Contains(platform.Checks[index].Detail, "cannot be checked") {
				platform.Checks[index].Hint = fmt.Sprintf("Run `kb-create start --platform-root %q`.", i.PlatformRoot)
			}
			if platform.Checks[index].ID == "host-process" && strings.Contains(platform.Checks[index].Hint, "kb-create start") {
				platform.Checks[index].Hint = fmt.Sprintf("Run `kb-create start --platform-root %q`.", i.PlatformRoot)
			}
		}
		response.Installation, response.Platform = &installation, &platform
	}
	response.OK = !doctor.HasErrors(findings) && (response.Installation == nil || response.Installation.OK) && (response.Platform == nil || response.Platform.OK)
	response.Installed = response.Installation != nil && response.Installation.OK
	if response.Platform != nil {
		for _, check := range response.Platform.Checks {
			if check.ID == "host-process" && check.OK {
				response.Running, response.Reachable = true, true
			}
		}
	}
	response.Next = "kb-create install"
	if response.Installed {
		response.Next = "kb-create start"
		if response.Running {
			response.Next = "cd ~/any-project && kb commit"
		}
	}
	if len(manifestFindings) > 0 {
		response.Next = "kb-create doctor --fix"
	}
	if i.DoctorFix {
		if i.PlatformRoot == "" {
			write(output, failure("KB_INSTALL_INPUT_REQUIRED", "--platform-root is required with --fix", "pass the V2 platform root that owns the active receipt", nil))
			return 2
		}
		return i.runDoctorFix(output, response)
	}
	if i.JSON {
		write(output, response)
	} else {
		installed, running, reachable := "no", "no", "no"
		if response.Installed {
			installed = "yes"
		}
		if response.Running {
			running = "yes"
		}
		if response.Reachable {
			reachable = "yes"
		}
		next := "run `" + response.Next + "`"
		if response.PlatformChannel != "" {
			fmt.Fprintf(output, "Platform channel: %s\n", response.PlatformChannel)
		}
		if response.PlatformVersion != "" {
			fmt.Fprintf(output, "Platform version: %s\n", response.PlatformVersion)
		}
		fmt.Fprintf(output, "Installed: %s\nRunning: %s\nReachable: %s\nNext: %s\n", installed, running, reachable, next)
		var warnings []doctor.Finding
		for _, finding := range response.Findings {
			if finding.Severity == doctor.SeverityWarning {
				warnings = append(warnings, finding)
				continue
			}
			fmt.Fprintf(output, "Finding %s: package=%s path=%s\nHint: %s\n", finding.Code, finding.Component, finding.Path, finding.Hint)
		}
		if len(warnings) > 0 {
			fmt.Fprintln(output, "Warnings:")
			for _, warning := range warnings {
				action := warning.Action
				if action == "" {
					action = warning.Hint
				}
				fmt.Fprintf(output, "Warning %s: %s\n", warning.Code, action)
			}
		}
		if response.Platform != nil {
			writeDoctorPlatformChecks(output, response.Platform.Checks)
		}
	}
	if !response.OK {
		return 1
	}
	return 0
}

func writeDoctorPlatformChecks(output *os.File, checks []doctor.Check) {
	for _, check := range checks {
		if check.Slot != "" {
			fmt.Fprintf(output, "Adapter %s: mode=%s implementation=%s\n", check.Slot, check.Mode, check.Implementation)
			if !check.OK {
				if check.Detail != "" {
					fmt.Fprintf(output, "Problem: %s\n", check.Detail)
				}
				if check.Hint != "" {
					fmt.Fprintf(output, "Next: %s\n", check.Hint)
				}
			}
		} else if check.ID == "adapters" {
			fmt.Fprintf(output, "Adapters: %s\n", check.Detail)
			if check.Hint != "" {
				fmt.Fprintf(output, "Next: %s\n", check.Hint)
			}
		}
	}
}

// inspectPlatform observes the running host. An installation whose host cannot
// even be built is reported as such rather than aborting the whole report.
func (i invocation) inspectPlatform(state host.State) doctor.Section {
	runner, err := host.NewRunner(i.PlatformRoot, state)
	if err != nil {
		return doctor.NewSection([]doctor.Check{{ID: "host-process", Detail: "The host cannot be inspected: " + err.Error(), Hint: "Repair the installation first."}})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	status, err := runner.Status(ctx)
	if err != nil {
		return doctor.NewSection([]doctor.Check{{ID: "host-process", Detail: "The host status cannot be read: " + err.Error()}})
	}
	active, err := receipt.Read(i.PlatformRoot)
	if err != nil {
		return doctor.NewSection([]doctor.Check{{ID: "host-process", Detail: "The active install receipt cannot be read while checking adapter bindings."}})
	}
	return doctor.InspectPlatform(ctx, status, nil, active.Plan.ProviderBindings)
}

// runDoctorFix applies manifest-declared safe defaults under a snapshot and
// restarts the host so it reads them; a host that no longer starts is rolled
// back like a failed update.
func (i invocation) runDoctorFix(output *os.File, response doctor.Response) int {
	root := i.PlatformRoot
	projectionRepairRequested := false
	for _, finding := range response.Findings {
		if finding.Code == "KB_INSTALL_MANIFEST_PROJECTION_INVALID" {
			projectionRepairRequested = true
			break
		}
	}
	unresolvedRequiredInput := false
	state, err := host.StateHome()
	if err != nil {
		write(output, map[string]any{"ok": false, "error": asDiag(err)})
		return 2
	}
	release, err := state.AcquireOperation("doctor-fix")
	if err != nil {
		write(output, map[string]any{"ok": false, "error": asDiag(err)})
		return 2
	}
	defer release()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	correlationID := fmt.Sprintf("doctor-fix-%d", time.Now().UTC().UnixNano())
	command := hostCommand{PlatformRoot: root, Executable: i.Executable, Version: buildVersion}
	var snapshot contracts.Snapshot
	result, err := swap.Run(ctx, lifecycleHost{command: command, state: state}, swap.Operation{
		Name:            "repair",
		PreviousVersion: installedVersion(root),
		Change: func(context.Context, bool) (string, error) {
			active, receiptErr := receipt.Read(root)
			if receiptErr != nil {
				return "", receiptErr
			}
			if err := installed.EnsureProjections(root, active.Plan.Artifacts); err != nil {
				return "", err
			}
			input, inputErr := automaticDoctorInput(root)
			if inputErr != nil {
				return "", inputErr
			}
			response.Findings = doctor.Diagnose(input.Manifests, input.Configured)
			response.Repair = doctor.PlanRepair(response.Findings)
			if projectionRepairRequested {
				// Restoring a missing projection can reveal unrelated required
				// credentials. Those cannot block the explicitly requested file
				// repair, and remain visible in the resulting findings.
				unresolvedRequiredInput = len(response.Repair.RequiredInput) > 0
				response.Repair.RequiredInput = nil
			}
			var fixErr error
			snapshot, fixErr = runtime.DoctorFix(root, response.Repair, runtime.Dependencies{})
			return snapshot.ID, fixErr
		},
		Restore: func(_ context.Context, id string) error {
			_, restoreErr := lifecycle.Rollback(root, id, nil)
			return restoreErr
		},
	})
	if err != nil {
		writeDiagnosticFailure(output, root, correlationID, "", asDiag(err), nil)
		return 1
	}
	fixOK := !unresolvedRequiredInput
	write(output, map[string]any{"ok": fixOK, "operation": "doctor-fix", "findings": response.Findings, "snapshot": snapshotSummary(snapshot), "hostRestarted": result.WasRunning})
	if !fixOK {
		return 1
	}
	return 0
}

// runRecover runs the platform's recovery tool with the host stopped, so the
// tool is the only writer of the platform database. The launcher supplies the
// host's environment and working directory, and the terminal (a password may
// arrive on stdin); it does not interpret the tool's result beyond its status.
func (i invocation) runRecover(output *os.File) int {
	command := i.hostCommand(output)
	command.JSON = i.Recover.JSON
	command.Err = os.Stderr
	runner, state, setupErr := command.setup()
	if setupErr != nil {
		return command.fail(2, setupErr)
	}
	release, err := state.AcquireOperation("recover")
	if err != nil {
		return command.fail(1, asDiag(err))
	}
	defer release()
	if runner.Spec.Recover == nil {
		return command.fail(1, contracts.NewDiag(contracts.CodeRecoveryUnavailable, "The installed platform does not ship a recovery tool.", "Update the platform with \"kb-create update\", then retry.", nil))
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	status, _ := runner.Status(ctx)
	if _, supervised := supervisor.LivePID(state); supervised || status.State != host.StateStopped {
		return command.fail(1, contracts.NewDiag(contracts.CodeRecoveryHostRunning, "Recovery needs the host to be stopped, but it is running.", "Run \"kb-create stop\", recover, then \"kb-create start\".", nil))
	}
	code, err := runner.RunTool(ctx, *runner.Spec.Recover, i.Recover.args(), os.Stdin, output, os.Stderr)
	if err != nil {
		return command.fail(1, contracts.NewDiag(contracts.CodeRecoveryFailed, "The platform's recovery tool could not be run.", "Inspect the platform installation with \"kb-create doctor\".", err))
	}
	return code
}

func automaticDoctorInput(platformRoot string) (doctor.Input, error) {
	active, err := receipt.Read(platformRoot)
	if err != nil {
		return doctor.Input{}, fmt.Errorf("read active V2 receipt: %w", err)
	}
	manifests, err := installed.LoadAll(platformRoot, active.Plan.Artifacts)
	if err != nil {
		return doctor.Input{}, err
	}
	paths := make([]string, 0)
	for _, manifest := range manifests {
		for _, requirement := range manifest.Requirements {
			paths = append(paths, requirement.Path)
		}
	}
	configured, err := installed.ConfiguredPaths(platformRoot, paths)
	if err != nil {
		return doctor.Input{}, err
	}
	return installed.DoctorInput(platformRoot, active.Plan.Artifacts, configured)
}

func projectionFindings(issues []installed.ProjectionIssue) []doctor.Finding {
	findings := make([]doctor.Finding, 0, len(issues))
	for _, issue := range issues {
		findings = append(findings, doctor.Finding{
			Code:      "KB_INSTALL_MANIFEST_PROJECTION_INVALID",
			Severity:  doctor.SeverityError,
			Component: issue.Package,
			Path:      filepath.Join("node_modules", issue.Package, "kb-create.manifest.json"),
			Hint:      issue.Reason + ". Run `kb-create doctor --fix`.",
		})
	}
	return findings
}
