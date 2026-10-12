package v2cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	clikitcommand "github.com/kb-labs/clikit/command"
	"github.com/kb-labs/clikit/diag"
	"github.com/kb-labs/clikit/stream"
	"github.com/kb-labs/create/v2/catalog"
	"github.com/kb-labs/create/v2/contracts"
	"github.com/kb-labs/create/v2/preflight"
	"github.com/kb-labs/create/v2/receipt"
	"github.com/kb-labs/create/v2/shim"
	"github.com/kb-labs/create/v2/versionroot"
	"github.com/kb-labs/create/v2/wizard"
)

type installCommandInput struct {
	Index, RequestFile, ScenarioID, Answers, SecretEnv, Registry, Executable string
	TelemetryEndpoint                                                        string
	Direct                                                                   directRequest
	Resume, JSON, TelemetryConsent                                           bool
}

func executeInstall(ctx context.Context, installInput installCommandInput, deps commandDependencies) (applyCommandResult, *diag.Diag) {
	installInput.JSON = false
	return install(ctx, installInput, deps)
}

func executeInstallStream(ctx context.Context, installInput installCommandInput, deps commandDependencies) (streamCompletion, *diag.Diag) {
	completion, failure := installJSON(ctx, installInput, deps)
	return completion, failure
}

func install(ctx context.Context, input installCommandInput, deps commandDependencies) (applyCommandResult, *diag.Diag) {
	out := deps.InstallOutput
	if out == nil {
		out = clikitcommand.FromCommand(deps.Command).ProgressWriter()
	}
	if err := deps.NodeCheck(); err != nil {
		failure := contracts.NewDiag(contracts.CodeNodeVersion, "Node.js 24.x is required before installation.",
			"Install Node.js 24, then run `kb-create install` (for nvm: `nvm install 24 && nvm use 24`).", err)
		failure.ProcessExitCode = diag.ExitInput
		return applyCommandResult{}, failure
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return applyCommandResult{}, contracts.NewDiag(contracts.CodeInputRequired, "The home directory could not be determined.", "Set HOME to a writable directory, then rerun `kb-create install`.", err)
	}
	index := input.Index
	if index == "" {
		index = filepath.Join(home, ".local", "share", "kb-create", "release-index.json")
	}
	root := input.Direct.PlatformRoot
	if root == "" {
		root = filepath.Join(home, "kb-platform")
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return applyCommandResult{}, contracts.NewDiag(contracts.CodeInputRequired, "The platform path is invalid.", "Pass a valid path with `"+contracts.ShellCommand("kb-create", "install", "--platform-root", "PATH")+"`.", err)
	}
	if failure := validateInstallTarget(root); failure != nil {
		return applyCommandResult{}, failure
	}
	if err := preflight.CheckInstallResources(root); err != nil {
		return applyCommandResult{}, asDiag(err)
	}
	source, err := catalog.LoadFile(index)
	if err != nil {
		return applyCommandResult{}, indexFailure(err, "supply a valid immutable V2 release index")
	}
	if _, err := fmt.Fprintln(out, "Set up KB Labs. Local access is enabled by default; it only accepts connections from this computer."); err != nil {
		return applyCommandResult{}, progressWriteFailure(err, "install")
	}
	inputReader := deps.InstallInput
	if inputReader == nil {
		inputReader = clikitcommand.FromCommand(deps.Command).Input()
	}
	generatedSecrets := map[string]string{}
	request, err := wizard.RequestScenario(source, root, "explore", input.Direct.ProjectRoot, wizard.IO{In: inputReader, Out: out, Generated: generatedSecrets})
	if err != nil {
		var validation *contracts.RequestValidationError
		if errors.As(err, &validation) {
			return applyCommandResult{}, validation.Diagnostic()
		}
		return applyCommandResult{}, contracts.NewDiag("KB_INSTALL_SCENARIO_INVALID", "The install answers could not be compiled.", "Correct the answers and rerun `kb-create install`.", err)
	}
	request, err = mergeDirectRequest(request, input.Direct)
	if err != nil {
		var validation *contracts.RequestValidationError
		if errors.As(err, &validation) {
			return applyCommandResult{}, validation.Diagnostic()
		}
		return applyCommandResult{}, contracts.NewDiag("KB_INSTALL_SCENARIO_INVALID", "The install answers could not be compiled.", "Correct the direct install fields and rerun `kb-create install`.", err)
	}
	started := time.Now()
	resolved, failure := resolvePlan(planInput{Operation: "install", Index: index, Request: &request, Executable: input.Executable, KnownGateways: knownGatewayRoots(input.Executable)})
	if failure != nil {
		return applyCommandResult{}, failure
	}
	plan := resolved.Plan
	existingSelection, selectionFailure := existingProjectSelection(root, plan.Request.ProjectRoot)
	if selectionFailure != nil {
		return applyCommandResult{}, selectionFailure
	}
	if _, err := fmt.Fprintf(out, "  ✓ Resolve (%s)\n", time.Since(started).Round(time.Millisecond)); err != nil {
		return applyCommandResult{}, progressWriteFailure(err, "install")
	}
	if _, err := fmt.Fprintf(out, "\nPlan: platform %s at %s, %d artifacts", plan.ServiceGraph.PlatformVersion, root, len(plan.Artifacts)); err != nil {
		return applyCommandResult{}, progressWriteFailure(err, "install")
	}
	if plan.Request.ProjectRoot != "" {
		if _, err := fmt.Fprintf(out, "; project %s", plan.Request.ProjectRoot); err != nil {
			return applyCommandResult{}, progressWriteFailure(err, "install")
		}
	}
	if _, err := fmt.Fprintln(out); err != nil {
		return applyCommandResult{}, progressWriteFailure(err, "install")
	}
	for _, artifact := range plan.Artifacts {
		if _, err := fmt.Fprintf(out, "  - %s %s (%s)\n", artifact.ID, artifact.Version, artifact.Kind); err != nil {
			return applyCommandResult{}, progressWriteFailure(err, "install")
		}
	}
	if _, err := fmt.Fprintln(out, "\nApplying installation…"); err != nil {
		return applyCommandResult{}, progressWriteFailure(err, "install")
	}
	deps.Runtime.Progress = &humanProgress{out: out, started: map[string]time.Time{}}
	result, failure := executeApply(ctx, applyCommandInput{Resolved: &resolved, Registry: input.Registry, SecretEnv: input.SecretEnv,
		Executable: input.Executable, Progress: deps.Runtime.Progress, TelemetryEndpoint: input.TelemetryEndpoint, TelemetryConsent: input.TelemetryConsent,
		AdminPasswordGenerated: generatedSecrets["gateway.bootstrap.password"] != ""}, deps)
	if failure != nil {
		return applyCommandResult{}, failure
	}
	if result.Shim != nil && !result.Shim.Linked {
		if _, err := fmt.Fprintf(out, "The kb command is owned by %s; run %s to use this platform.\n", result.Shim.Owner, shim.Target(root)); err != nil {
			return applyCommandResult{}, progressWriteFailure(err, "install")
		}
	}
	if plan.Request.ProjectRoot == "" {
		if _, err := fmt.Fprintf(out, "\nKB Labs is installed at %s.\nNext: register a project with `%s`.\n", root, contracts.ShellCommand("kb", "project", "add", "<path>")); err != nil {
			return applyCommandResult{}, progressWriteFailure(err, "install")
		}
		return result, nil
	}
	if !projectRegistrationRequired(plan.Request.ProjectRoot, existingSelection) {
		if _, err := fmt.Fprintf(out, "\nVerification: platform installed at %s; project %s remains registered at %s.\nNext: run `%s` and `%s`.\n", root, result.Receipt.ProjectID, result.Receipt.ProjectCanonicalPath,
			contracts.ShellCommand("kb-create", "status", "--platform-root", root), contracts.ShellCommand("kb", "project", "show", result.Receipt.ProjectCanonicalPath)); err != nil {
			return applyCommandResult{}, progressWriteFailure(err, "install")
		}
		return result, nil
	}
	registered, err := deps.ProjectRegistrar(root, plan.Request.ProjectRoot)
	if err != nil {
		return applyCommandResult{}, projectRegistrationFailure(root, plan.Request.ProjectRoot, err)
	}
	result.Receipt, err = recordProjectIdentity(root, plan.Request.ProjectRoot, registered)
	if err != nil {
		return applyCommandResult{}, contracts.NewDiag(contracts.CodeReceiptUnavailable, "The project is registered, but its identity could not be recorded in the active receipt.", "Run `"+contracts.ShellCommand("kb-create", "doctor", "--platform-root", root)+"` and inspect the installation before retrying.", err)
	}
	if _, err := fmt.Fprintf(out, "\nVerification: platform installed at %s; project %s registered at %s.\nNext: run `%s` and `%s`.\n", root, registered.ID, registered.CanonicalPath,
		contracts.ShellCommand("kb-create", "status", "--platform-root", root), contracts.ShellCommand("kb", "project", "show", registered.CanonicalPath)); err != nil {
		return applyCommandResult{}, progressWriteFailure(err, "install")
	}
	return result, nil
}

func validateInstallTarget(root string) *diag.Diag {
	layout, err := versionroot.New(root)
	if err != nil {
		return unsupportedPlatformLayout(root, err)
	}
	if err := layout.ValidateInitialTarget(); err != nil {
		return unsupportedPlatformLayout(root, err)
	}
	return nil
}

func mergeDirectRequest(request contracts.InstallRequest, direct directRequest) (contracts.InstallRequest, error) {
	normalized := direct
	if normalized.PlatformRoot == "" {
		normalized.PlatformRoot = request.PlatformRoot
	}
	// The wizard has already validated and canonicalized project selection.
	// Keep that result instead of re-normalizing a raw flag value here.
	normalized.ProjectRoot = request.ProjectRoot
	fields, err := normalized.normalize()
	if err != nil {
		return contracts.InstallRequest{}, err
	}
	if direct.PlatformRoot != "" {
		request.PlatformRoot = fields.PlatformRoot
	}
	if direct.PlatformVersion != "" || direct.PlatformChannel != "" {
		request.Platform = fields.Platform
	}
	if direct.SDKVersion != "" {
		request.SDK = fields.SDK
	}
	if direct.ServiceProfile != "" {
		request.ServiceProfile = fields.ServiceProfile
	}
	if direct.GatewayURL != "" {
		request.GatewayURL = fields.GatewayURL
	}
	if direct.Plugins != "" {
		request.Plugins = fields.Plugins
	}
	if direct.Adapters != "" {
		request.Adapters = fields.Adapters
	}
	if len(direct.Providers) > 0 {
		request.ProviderPreferences = fields.ProviderPreferences
	}
	if direct.Policy != "" {
		request.Policy = fields.Policy
	}
	if direct.Offline {
		request.Source = contracts.SourceOffline
	}
	return request.Normalize()
}

func installJSON(ctx context.Context, input installCommandInput, deps commandDependencies) (streamCompletion, *diag.Diag) {
	writer := stream.New(clikitcommand.StreamWriter(deps.Command))
	if input.Index == "" || (input.RequestFile == "" && input.Direct.PlatformRoot == "") {
		failure := contracts.NewDiag(contracts.CodeInputRequired, "install needs a release index and an install request", "Supply `--index` and either a readable request file with `--input` or a target directory with `--platform-root`, then rerun `kb-create install --json`.", nil)
		failure.ProcessExitCode = diag.ExitInput
		return finishInstallStreamFailure(writer, failure)
	}
	resolved, failure := resolvePlan(planInput{Operation: "install", Index: input.Index, RequestFile: input.RequestFile, Direct: input.Direct,
		ScenarioID: input.ScenarioID, Answers: input.Answers, Resume: input.Resume, Executable: input.Executable,
		KnownGateways: knownGatewayRoots(input.Executable)})
	if failure != nil {
		return finishInstallStreamFailure(writer, inputFailure(failure))
	}
	existingSelection, selectionFailure := existingProjectSelection(resolved.Plan.Request.PlatformRoot, resolved.Plan.Request.ProjectRoot)
	if selectionFailure != nil {
		selectionFailure.ProcessExitCode = diag.ExitInput
		return finishInstallStreamFailure(writer, selectionFailure)
	}
	deps.Runtime.Progress = streamProgress{writer: writer}
	result, failure := executeApplyPlanJSON(ctx, input, resolved, deps)
	if failure != nil {
		failure.ProcessExitCode = diag.ExitError
		if err := writer.Fail(failure); err != nil {
			return streamCompletion{}, diag.Wrap(err, "KB_RUNTIME_UNEXPECTED", "Install progress could not be written.")
		}
		if err := writer.Finish(failure.ProcessExitCode); err != nil {
			return streamCompletion{}, diag.Wrap(err, "KB_RUNTIME_UNEXPECTED", "Install progress could not be completed.")
		}
		return streamCompletion{ExitCode: writer.ExitCode()}, nil
	}
	request := result.Receipt.Plan.Request
	if projectRegistrationRequired(request.ProjectRoot, existingSelection) {
		registered, err := deps.ProjectRegistrar(request.PlatformRoot, request.ProjectRoot)
		if err != nil {
			failure := projectRegistrationFailure(request.PlatformRoot, request.ProjectRoot, err)
			failure.ProcessExitCode = diag.ExitError
			if writeErr := writer.Fail(failure); writeErr != nil {
				return streamCompletion{}, diag.Wrap(writeErr, "KB_RUNTIME_UNEXPECTED", "Install progress could not be written.")
			}
			if finishErr := writer.Finish(failure.ProcessExitCode); finishErr != nil {
				return streamCompletion{}, diag.Wrap(finishErr, "KB_RUNTIME_UNEXPECTED", "Install progress could not be completed.")
			}
			return streamCompletion{ExitCode: writer.ExitCode()}, nil
		}
		result.Receipt, err = recordProjectIdentity(request.PlatformRoot, request.ProjectRoot, registered)
		if err != nil {
			failure := contracts.NewDiag(contracts.CodeReceiptUnavailable, "The project is registered, but its identity could not be recorded in the active receipt.", "Run `"+contracts.ShellCommand("kb-create", "doctor", "--platform-root", request.PlatformRoot)+"` and inspect the installation before retrying.", err)
			failure.ProcessExitCode = diag.ExitError
			if writeErr := writer.Fail(failure); writeErr != nil {
				return streamCompletion{}, diag.Wrap(writeErr, "KB_RUNTIME_UNEXPECTED", "Install progress could not be written.")
			}
			if finishErr := writer.Finish(failure.ProcessExitCode); finishErr != nil {
				return streamCompletion{}, diag.Wrap(finishErr, "KB_RUNTIME_UNEXPECTED", "Install progress could not be completed.")
			}
			return streamCompletion{ExitCode: writer.ExitCode()}, nil
		}
	}
	if err := writer.Result(result); err != nil {
		return streamCompletion{}, diag.Wrap(err, "KB_RUNTIME_UNEXPECTED", "Install result could not be written.")
	}
	if err := writer.Finish(diag.ExitOK); err != nil {
		return streamCompletion{}, diag.Wrap(err, "KB_RUNTIME_UNEXPECTED", "Install progress could not be completed.")
	}
	return streamCompletion{ExitCode: writer.ExitCode()}, nil
}

func finishInstallStreamFailure(writer *stream.Writer, failure *diag.Diag) (streamCompletion, *diag.Diag) {
	if err := writer.Fail(failure); err != nil {
		return streamCompletion{}, diag.Wrap(err, "KB_RUNTIME_UNEXPECTED", "Install progress could not be written.")
	}
	if err := writer.Finish(diag.ExitCode(failure)); err != nil {
		return streamCompletion{}, diag.Wrap(err, "KB_RUNTIME_UNEXPECTED", "Install progress could not be completed.")
	}
	return streamCompletion{ExitCode: writer.ExitCode()}, nil
}

func executeApplyPlanJSON(ctx context.Context, input installCommandInput, resolved resolvedPlan, deps commandDependencies) (applyCommandResult, *diag.Diag) {
	return executeApply(ctx, applyCommandInput{Resolved: &resolved, Registry: input.Registry, SecretEnv: input.SecretEnv,
		Executable: input.Executable, TelemetryEndpoint: input.TelemetryEndpoint, TelemetryConsent: input.TelemetryConsent}, deps)
}

func projectRegistrationRequired(projectRoot string, existingSelection bool) bool {
	return projectRoot != "" && !existingSelection
}

func recordProjectIdentity(platformRoot, selectedPath string, registered projectRegistrationResult) (contracts.InstallReceipt, error) {
	receiptRoot := projectReceiptRoot(platformRoot)
	active, err := receipt.Read(receiptRoot)
	if err != nil {
		return contracts.InstallReceipt{}, err
	}
	active.ProjectSelectedPath = selectedPath
	active.ProjectID = registered.ID
	active.ProjectCanonicalPath = registered.CanonicalPath
	if err := receipt.Write(receiptRoot, active); err != nil {
		return contracts.InstallReceipt{}, err
	}
	return active, nil
}

func registerInstalledProject(platformRoot, projectRoot string, registrar func(string) (projectRegistrationResult, error)) (projectRegistrationResult, error) {
	if projectRoot == "" {
		return projectRegistrationResult{}, nil
	}
	abs, err := contracts.ValidateProjectSelection(projectRoot)
	if err != nil {
		var validation *contracts.RequestValidationError
		if errors.As(err, &validation) && errors.Is(err, os.ErrNotExist) {
			return projectRegistrationResult{}, diag.New("KB_PROJECT_PATH_NOT_FOUND", fmt.Sprintf("The project path %q does not exist.", projectRoot), diag.WithCauseAndReason(validation.Message))
		}
		return projectRegistrationResult{}, diag.Wrap(err, "KB_RUNTIME_INPUT_INVALID", "The selected project path could not be resolved.")
	}
	if registrar != nil {
		return registrar(abs)
	}
	return registerProject(platformRoot, abs)
}

func renderInstall(value any) string { return renderApply(value) }
