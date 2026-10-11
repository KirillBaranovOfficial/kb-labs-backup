package v2cli

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/kb-labs/clikit/diag"
	"github.com/kb-labs/create/v2/contracts"
	"github.com/kb-labs/create/v2/doctor"
	"github.com/kb-labs/create/v2/host"
	"github.com/kb-labs/create/v2/installed"
	"github.com/kb-labs/create/v2/receipt"
	"github.com/kb-labs/create/v2/shim"
	"github.com/kb-labs/create/v2/versionroot"
	"github.com/spf13/cobra"
)

type doctorFixResult struct {
	OK            bool              `json:"ok"`
	Operation     string            `json:"operation"`
	Findings      []doctor.Finding  `json:"findings"`
	Repair        doctor.RepairPlan `json:"repair"`
	Snapshot      map[string]any    `json:"snapshot"`
	HostRestarted bool              `json:"hostRestarted"`
}

func (doctorFixResult) doctorOperationResult() {}

type doctorOperationResult interface {
	doctorOperationResult()
}

type doctorCommandInput struct {
	Command      *cobra.Command
	PlatformRoot string
	Executable   string
	InputFile    string
	Fix          bool
	Recover      recoverRequest
	Foreground   bool
}

func executeDoctor(_ context.Context, input doctorCommandInput) (doctorOperationResult, *diag.Diag) {
	in := input
	if in.Recover.Enabled {
		return executeRecover(input)
	}
	root := in.PlatformRoot
	if root == "" {
		root = defaultPlatformRoot()
	}
	layout, layoutErr := versionroot.New(root)
	if layoutErr != nil {
		return nil, unsupportedPlatformLayout(root, layoutErr)
	}
	platformRoot := layout.PlatformRoot()
	in.PlatformRoot = root
	if in.InputFile == "" {
		if _, err := receipt.Read(platformRoot); err != nil {
			return nil, contracts.NewDiag(contracts.CodeReceiptUnavailable, "No active installation receipt was found.", "Run `kb-create install` to create the platform, then retry `kb-create doctor`.", err)
		}
	}
	var doctorInput doctor.Input
	var err error
	var manifestFindings []doctor.Finding
	if in.InputFile != "" {
		data, readErr := os.ReadFile(in.InputFile)
		if readErr != nil {
			return nil, contracts.NewDiag("KB_INSTALL_INPUT_REQUIRED", "doctor input could not be read", "supply a readable V2 doctor input JSON file", readErr)
		}
		doctorInput, err = doctor.Decode(data)
	} else {
		active, readErr := receipt.Read(platformRoot)
		if readErr != nil {
			err = fmt.Errorf("read active V2 receipt: %w", readErr)
		} else if active.Plan.ServiceGraph.Profile == "client" {
			doctorInput = doctor.Input{Configured: map[string]bool{}}
		} else {
			issues, inspectErr := installed.InspectProjections(platformRoot, active.Plan.Artifacts)
			if inspectErr != nil {
				err = inspectErr
			} else if len(issues) > 0 {
				manifestFindings = projectionFindings(issues)
			} else {
				doctorInput, err = automaticDoctorInput(platformRoot)
			}
		}
	}
	if err != nil {
		return nil, contracts.NewDiag("KB_INSTALL_DOCTOR_INPUT_INVALID", "installed manifest diagnostics could not be prepared", "install exact V2 artifacts that ship matching V2 manifests", err)
	}
	findings := append(doctor.Diagnose(doctorInput.Manifests, doctorInput.Configured), manifestFindings...)
	response := doctor.Response{Findings: findings, Repair: doctor.PlanRepair(findings)}
	state, stateErr := host.StateForRoot(root)
	if stateErr != nil {
		return nil, asDiag(stateErr)
	}
	if in.Executable != "" {
		if _, dangling := shim.Dangling(in.Executable); dangling {
			response.Findings = append(response.Findings, doctor.Finding{Code: "KB_INSTALL_SHIM_DANGLING", Severity: doctor.SeverityError,
				Component: "kb-create", Path: shim.Path(in.Executable), Hint: "Run `" + contracts.ShellCommand("kb-create", "doctor", "--fix", "--platform-root", root) + "`."})
		}
	}
	if active, readErr := receipt.Read(platformRoot); readErr == nil {
		response.PlatformChannel, response.PlatformVersion = active.Plan.PlatformChannel, active.Plan.ServiceGraph.PlatformVersion
		response.InstalledVersion = active.Plan.ServiceGraph.PlatformVersion
		response.Profile = active.Plan.ServiceGraph.Profile
		runningRoot := ""
		if runner, runnerErr := host.NewRecordedRunner(root); runnerErr == nil {
			if running, statusErr := runner.Status(context.Background()); statusErr == nil {
				response.RunningVersion = running.PlatformVersion
				response.PendingRestart = pendingRestart(active, running, root)
				if running.PID > 0 {
					runningRoot = running.TreeRoot
				}
			}
		}
		if layoutErr := layout.Validate(); layoutErr != nil {
			return nil, unsupportedPlatformLayout(root, layoutErr)
		}
		versions, retentionErr := layout.Retained(runningRoot)
		if retentionErr != nil {
			return nil, contracts.NewDiag(contracts.CodeOperationFailed, "retained platform versions could not be inspected", "check that the installed root and its version directory are readable, then retry `kb-create doctor`", retentionErr)
		}
		snapshots, retentionErr := receipt.ListSnapshots(layout.SnapshotsDir())
		if retentionErr != nil {
			return nil, contracts.NewDiag(contracts.CodeOperationFailed, "recovery snapshots could not be inspected", "check the V2 receipt directory, then retry `kb-create doctor`", retentionErr)
		}
		response.Retention = &doctor.RetentionReport{InstallRoot: root, PlatformRoot: platformRoot,
			VersionsDirectory: layout.VersionsDir(), SharedState: layout.SharedStateDir(), InstallerState: layout.InstallerStateDir(),
			HostState: layout.HostStateDir(),
			Versions:  versions, Snapshots: snapshots}
	}
	installation := doctor.InspectInstallation(platformRoot, state)
	if in.Executable != "" && !shim.Correct(in.Executable, platformRoot) {
		installation.Checks = append(installation.Checks, doctor.Check{ID: "kb-command-shim", Detail: "The `kb` command shim is missing or points to another platform.", Hint: "Run `" + contracts.ShellCommand("kb-create", "doctor", "--fix", "--platform-root", root) + "` to repair the command shim when its current owner has been removed."})
		installation.OK = false
	}
	platform := in.inspectPlatform()
	response.Installation, response.Platform = &installation, &platform
	response.OK = !doctor.HasErrors(response.Findings) && installation.OK && platform.OK
	response.Installed = installation.OK
	for _, check := range platform.Checks {
		if check.ID == "host-process" && check.OK {
			response.Running, response.Reachable = true, true
		}
	}
	response.Next = "kb-create install"
	if response.Installed {
		response.Next = "kb-create start"
		if response.Profile == "client" {
			response.Next = "kb auth login --gateway-url <url> --email <member>"
		}
		if response.Running {
			response.Next = "cd ~/any-project && kb commit"
		}
	}
	if len(manifestFindings) > 0 {
		response.Next = "kb-create doctor --fix"
	}
	if in.Fix {
		if in.PlatformRoot == "" {
			return nil, contracts.NewDiag(contracts.CodeInputRequired, "--platform-root is required with --fix", "pass the V2 platform root that owns the active receipt", nil)
		}
		return executeDoctorFix(in, response)
	}
	status := "healthy"
	if !response.OK {
		status = "unhealthy"
	}
	return doctorCommandResult{Operation: "doctor", Status: status, Response: response}, nil
}

func executeDoctorFix(input doctorCommandInput, response doctor.Response) (doctorFixResult, *diag.Diag) {
	return input.fixDoctor(response)
}

func renderDoctorPlatformChecks(checks []doctor.Check) string {
	var lines []string
	for _, check := range checks {
		if check.Slot != "" {
			lines = append(lines, fmt.Sprintf("Adapter %s: mode=%s implementation=%s", check.Slot, check.Mode, check.Implementation))
			if !check.OK {
				if check.Detail != "" {
					lines = append(lines, "Problem: "+check.Detail)
				}
				if check.Hint != "" {
					lines = append(lines, "Next: "+check.Hint)
				}
			}
		} else if check.ID == "adapters" {
			lines = append(lines, "Adapters: "+check.Detail)
			if check.Hint != "" {
				lines = append(lines, "Next: "+check.Hint)
			}
		}
	}
	return strings.Join(lines, "\n")
}
