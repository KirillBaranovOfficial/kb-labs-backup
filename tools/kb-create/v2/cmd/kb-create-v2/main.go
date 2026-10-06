// Package v2cli is the public V2 kb-create frontend. Both the released root
// binary and package-local tests enter through Execute, so there is exactly one
// launcher command surface and no legacy dispatcher to keep in sync.
package v2cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/kb-labs/clikit/diag"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	clikitcommand "github.com/kb-labs/clikit/command"
	"github.com/kb-labs/clikit/result"
	"github.com/kb-labs/create/v2/artifacts"
	"github.com/kb-labs/create/v2/catalog"
	"github.com/kb-labs/create/v2/contracts"
	"github.com/kb-labs/create/v2/host"
	"github.com/kb-labs/create/v2/logs"
	"github.com/kb-labs/create/v2/preflight"
	"github.com/kb-labs/create/v2/receipt"
	"github.com/kb-labs/create/v2/resolve"
	"github.com/kb-labs/create/v2/runtime"
	"github.com/kb-labs/create/v2/scenario"
	"github.com/kb-labs/create/v2/secrets"
	"github.com/kb-labs/create/v2/shim"
	"github.com/kb-labs/create/v2/telemetry"
	"github.com/kb-labs/create/v2/transport"
	"github.com/kb-labs/create/v2/wizard"
)

var telemetryEndpoint string
var telemetryConsent bool
var buildVersion = "dev"
var buildCommit = "none"

type repeatedValue []string

func (values *repeatedValue) String() string { return strings.Join(*values, ",") }
func (values *repeatedValue) Type() string   { return "string" }
func (values *repeatedValue) Set(value string) error {
	*values = append(*values, value)
	return nil
}

// SetVersionInfo exposes the build identity to diagnostics without changing
// the human --version output contract.
func SetVersionInfo(version, commit, _ string) {
	if version != "" {
		buildVersion = version
	}
	if commit != "" {
		buildCommit = commit
	}
}

// Execute runs the public kb-create Cobra tree through the shared clikit boundary.
func Execute() int {
	root := newRootCommand()
	root.Version = buildVersion
	root.SetVersionTemplate("kb-create {{.Version}}\n")
	return clikitcommand.Execute(root, os.Args[1:], os.Stdout, os.Stderr)
}

type directRequest struct {
	PlatformRoot, ProjectRoot, PlatformVersion, PlatformChannel, SDKVersion, ServiceProfile, Plugins, Adapters, Providers, Policy string
	Offline                                                                                                                       bool
}

// invocation is one parsed non-host command line. Every field is an input; the
// run method reads nothing from package state except the build version.
type invocation struct {
	Operation, Index, Input, DoctorInput, PlatformRoot, Snapshot, Registry, SecretEnv string
	DoctorFix                                                                         bool
	Recover                                                                           recoverRequest
	ScenarioID, ScenarioAnswers                                                       string
	ScenarioResume                                                                    bool
	Direct                                                                            directRequest
	// Executable is the launcher binary re-executed to supervise the host.
	Executable       string
	Foreground       bool
	JSON             bool
	Yes              bool
	InstallInput     io.Reader
	InstallOutput    io.Writer
	RuntimeDeps      *runtime.Dependencies
	NodePreflight    func() error
	ProjectRegistrar func(string) (string, error)
}

func (i invocation) hostCommand(output *os.File) hostCommand {
	return hostCommand{PlatformRoot: i.PlatformRoot, Foreground: i.Foreground, Executable: i.Executable, Version: buildVersion, JSON: i.JSON, Out: output, Err: os.Stderr}
}

func (i invocation) ensureNode() error {
	if i.NodePreflight != nil {
		return i.NodePreflight()
	}
	return preflight.EnsureNode(nil)
}

func (i invocation) run(output *os.File) int {
	operation := i.Operation
	if i.PlatformRoot != "" {
		root, err := filepath.Abs(i.PlatformRoot)
		if err != nil {
			write(output, failure(contracts.CodeInputRequired, "platform root path is invalid", "pass a valid --platform-root path", err))
			return 2
		}
		i.PlatformRoot = filepath.Clean(root)
	}
	switch operation {
	case "install", "plan", "apply", "update", "uninstall", "rollback", "doctor", "status":
	default:
		write(output, failure("KB_INSTALL_OPERATION_INVALID", "operation is not supported", "use plan, apply, update, uninstall, rollback, doctor, status, start, stop or restart", nil))
		return 2
	}
	if operation == "doctor" {
		return i.runDoctor(output)
	}
	if operation == "status" {
		return i.runStatus(output)
	}
	if operation == "install" {
		return i.runInstall(output)
	}
	if operation == "apply" || operation == "update" {
		if err := i.ensureNode(); err != nil {
			write(output, failure(contracts.CodeNodeVersion, "Node.js 24.x is required before installation.", "Install Node.js 24, then run `kb-create install` (for nvm: `nvm install 24 && nvm use 24`).", err))
			return 2
		}
	}
	if operation == "uninstall" || operation == "rollback" {
		return i.runRecovery(output)
	}
	if i.Index == "" || (i.Input == "" && i.Direct.PlatformRoot == "") {
		write(output, failure("KB_INSTALL_INPUT_REQUIRED", "--index and either --input or --request-platform-root are required", "pass immutable release index plus V2 request JSON or direct CI flags", nil))
		return 2
	}
	source, err := catalog.LoadFile(i.Index)
	if err != nil {
		writeIndexFailure(output, err, "supply a valid immutable V2 release index")
		return 2
	}
	var response transport.PlanResponse
	if i.Input != "" {
		data, err := os.ReadFile(i.Input)
		if err != nil {
			write(output, failure("KB_INSTALL_INPUT_REQUIRED", "request could not be read", "supply a readable V2 request JSON file", err))
			return 2
		}
		response = transport.PlanWith(data, source, resolve.Options{LauncherVersion: buildVersion})
	} else {
		request, requestErr := i.Direct.normalize()
		if requestErr != nil {
			write(output, failure("KB_INSTALL_INPUT_REQUIRED", "direct request is invalid", "correct direct CI flags or provide --input", requestErr))
			return 2
		}
		if i.ScenarioID != "" {
			compiled, scenarioErr := compileScenario(i.ScenarioID, i.ScenarioAnswers, i.ScenarioResume, request)
			if scenarioErr != nil {
				write(output, failure("KB_INSTALL_SCENARIO_INVALID", "scenario could not be compiled", "inspect scenario answers and manifest requirements", scenarioErr))
				return 2
			}
			request = compiled
		}
		data, _ := json.Marshal(request)
		response = transport.PlanWith(data, source, resolve.Options{LauncherVersion: buildVersion})
	}
	if !response.OK {
		write(output, response)
		return 2
	}
	if operation == "update" {
		active, readErr := receipt.Read(response.Plan.Request.PlatformRoot)
		if readErr != nil {
			write(output, failure(contracts.CodeReceiptUnavailable, "active V2 receipt could not be read", "run apply first or restore a named V2 snapshot", readErr))
			return 2
		}
		if validationErr := resolve.ValidateUpdate(active, *response.Plan, source); validationErr != nil {
			write(output, map[string]any{"ok": false, "error": asDiag(validationErr)})
			return 2
		}
	}
	if operation == "apply" || operation == "update" {
		request, changed, requestErr := requestWithGeneratedSecrets(response.Plan.Request, response.Plan.Artifacts)
		if requestErr != nil {
			write(output, failure(contracts.CodeInputRequired, "the install request could not include generated secrets.", "rerun `kb-create install` with the selected platform manifest", requestErr))
			return 2
		}
		if changed {
			data, _ := json.Marshal(request)
			response = transport.PlanWith(data, source, resolve.Options{LauncherVersion: buildVersion})
			if !response.OK {
				write(output, response)
				return 2
			}
		}
	}
	root := response.Plan.Request.PlatformRoot
	state, stateErr := host.StateHome()
	if stateErr != nil {
		write(output, map[string]any{"ok": false, "error": asDiag(stateErr)})
		return 2
	}
	if operation == "apply" {
		if running := refuseRunningHost(state, root); running != nil {
			write(output, map[string]any{"ok": false, "error": running})
			return 2
		}
	} else if operation == "update" {
		if running := refuseForeignHost(state, root); running != nil {
			write(output, map[string]any{"ok": false, "error": running})
			return 2
		}
	}
	if operation == "apply" {
		root := response.Plan.Request.PlatformRoot
		if err := preflight.CheckInstallResources(root); err != nil {
			var typed *diag.Diag
			if errors.As(err, &typed) {
				write(output, map[string]any{"ok": false, "error": typed})
			} else {
				write(output, failure(contracts.CodeResourceCheckFailed, "The launcher could not check available memory and disk space.", "Check that the platform root is on a mounted writable filesystem, then run `kb-create install` again.", err))
			}
			return 2
		}
		if err := host.CheckPorts(*response.Plan, operation); err != nil {
			write(output, map[string]any{"ok": false, "error": asDiag(err)})
			return 2
		}
	}
	started := time.Now()
	outcome, errorCode := "failure", "KB_INSTALL_OPERATION_FAILED"
	defer func() {
		telemetry.Send(telemetryEndpoint, telemetryConsent, telemetry.New(operation, outcome, errorCode, string(response.Plan.Request.Platform.Channel), string(response.Plan.Request.Source), len(response.Plan.Artifacts), time.Since(started)))
	}()
	if operation == "plan" {
		write(output, response)
		outcome, errorCode = "success", ""
		return 0
	}
	correlationID := fmt.Sprintf("%s-%d", operation, time.Now().UTC().UnixNano())
	transcript, err := logs.New(root, correlationID, nil)
	if err != nil {
		write(output, failure("KB_INSTALL_LOG_UNAVAILABLE", "could not create local operation log", "check that the platform root is writable", err))
		return 2
	}
	defer transcript.Close()
	store := secrets.Store{PlatformRoot: root}
	if err := populateSecrets(store, i.SecretEnv); err != nil {
		write(output, failure("KB_INSTALL_SECRET_INPUT_INVALID", "secret input could not be stored", "use --secret-env requirement=ENV_VAR and set the environment variable", err))
		return 2
	}
	if err := secrets.EnsureGenerated(response.Plan, store); err != nil {
		write(output, failure(contracts.CodeApplyFailed, "the session signing secret could not be generated or stored.", "check that the platform root is writable, then rerun `kb-create install`", err))
		return 2
	}
	release, lockErr := state.AcquireOperation(operation)
	if lockErr != nil {
		write(output, map[string]any{"ok": false, "error": asDiag(lockErr)})
		return 2
	}
	defer release()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	offlineArtifacts := response.Plan.Request.Source == contracts.SourceOffline
	artifactExecutor := artifacts.Composite{Packages: artifacts.Pnpm{Root: root, Registry: i.Registry, Offline: offlineArtifacts, Log: transcript}, Binaries: artifacts.Binaries{Root: root, Offline: offlineArtifacts}}
	prober := host.Prober{PlatformRoot: root, State: state, Operation: operation}
	deps := runtime.Dependencies{Artifacts: artifactExecutor, CorrelationID: correlationID, Secrets: &store}
	if i.RuntimeDeps != nil {
		if i.RuntimeDeps.Artifacts != nil {
			deps.Artifacts = i.RuntimeDeps.Artifacts
		}
		if i.RuntimeDeps.Host != nil {
			deps.Host = i.RuntimeDeps.Host
		}
		if i.RuntimeDeps.Clock != nil {
			deps.Clock = i.RuntimeDeps.Clock
		}
		if i.RuntimeDeps.Secrets != nil {
			deps.Secrets = i.RuntimeDeps.Secrets
		}
		deps.Progress = i.RuntimeDeps.Progress
	}
	if operation == "apply" {
		if i.Operation == "apply" && i.RuntimeDeps != nil && deps.Progress != nil {
			// The shared apply engine owns artifact, render, host and verification events.
		}
		if deps.Host == nil {
			deps.Host = prober
		}
		receipt, applyErr := runtime.Apply(ctx, *response.Plan, deps)
		if applyErr == nil {
			var shimResult shim.EnsureResult
			if i.Executable != "" {
				if deps.Progress != nil {
					deps.Progress.Emit(runtime.ProgressEvent{Phase: "Create kb shim", Start: true})
				}
				var shimErr error
				shimResult, shimErr = shim.Ensure(i.Executable, root)
				if shimErr != nil {
					if deps.Progress != nil {
						deps.Progress.Emit(runtime.ProgressEvent{Phase: "Create kb shim", Start: false})
					}
					writeShimFailure(output, "KB_INSTALL_APPLY_FAILED", root, shimErr)
					return 1
				}
				if deps.Progress != nil {
					deps.Progress.Emit(runtime.ProgressEvent{Phase: "Create kb shim", Start: false})
				}
			}
			payload := map[string]any{"ok": true, "operation": operation, "receipt": receipt, "logPath": transcript.Path()}
			if i.Executable != "" {
				payload["shim"] = shimResult
			}
			write(output, payload)
			outcome, errorCode = "success", ""
			return 0
		}
		writeFailureDossier(output, *response.Plan, correlationID, transcript.Path(), applyErr)
		return 1
	}
	return i.runUpdate(ctx, output, *response.Plan, deps, prober, state, correlationID, transcript.Path(), &outcome, &errorCode)
}

// runInstall is the human frontend. It gathers answers, prints the shared
// resolver's plan, then invokes the same apply engine used by JSON callers.
func (i invocation) runInstall(output *os.File) int {
	if i.JSON {
		return i.runJSONInstall(output)
	}
	var humanOut io.Writer = output
	if i.InstallOutput != nil {
		humanOut = i.InstallOutput
	}
	if err := i.ensureNode(); err != nil {
		writeHumanFailure(humanOut, contracts.NewDiag(contracts.CodeNodeVersion, "Node.js 24.x is required before installation.", "Install Node.js 24, then run `kb-create install` (for nvm: `nvm install 24 && nvm use 24`).", err))
		return 2
	}
	home, err := os.UserHomeDir()
	if err != nil {
		writeHumanFailure(humanOut, contracts.NewDiag(contracts.CodeInputRequired, "The home directory could not be determined.", "Set HOME to a writable directory, then rerun `kb-create install`.", err))
		return 2
	}
	index := i.Index
	if index == "" {
		index = filepath.Join(home, ".local", "share", "kb-create", "release-index.json")
	}
	root := i.Direct.PlatformRoot
	if root == "" {
		root = i.PlatformRoot
	}
	if root == "" {
		root = filepath.Join(home, "kb-platform")
	}
	root, err = filepath.Abs(root)
	if err != nil {
		writeHumanFailure(humanOut, contracts.NewDiag(contracts.CodeInputRequired, "The platform path is invalid.", "Pass a valid path with `kb-create install --platform-root PATH`.", err))
		return 2
	}
	if err := preflight.CheckInstallResources(root); err != nil {
		var typed *diag.Diag
		if errors.As(err, &typed) {
			writeHumanFailure(humanOut, typed)
		} else {
			writeHumanFailure(humanOut, contracts.NewDiag(contracts.CodeResourceCheckFailed, "The launcher could not check available memory and disk space.", "Check that the platform root is on a mounted writable filesystem, then run `kb-create install` again.", err))
		}
		return 2
	}
	phaseStart := time.Now()
	source, err := catalog.LoadFile(index)
	if err != nil {
		writeHumanFailure(humanOut, asDiag(err))
		return 2
	}
	fmt.Fprintln(humanOut, "Set up KB Labs. Local access is enabled by default; it only accepts connections from this computer.")
	input := i.InstallInput
	if input == nil {
		input = os.Stdin
	}
	request, err := wizard.RequestScenario(source, root, "explore", i.Direct.ProjectRoot, wizard.IO{In: input, Out: humanOut})
	if err != nil {
		writeHumanFailure(humanOut, contracts.NewDiag("KB_INSTALL_SCENARIO_INVALID", "The install answers could not be compiled.", "Correct the answers and rerun `kb-create install`.", err))
		return 2
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		writeHumanFailure(humanOut, contracts.NewDiag(contracts.CodeInputRequired, "The install request could not be encoded.", "Rerun `kb-create install`; if this repeats, report the launcher version.", err))
		return 2
	}
	planned := transport.PlanWith(encoded, source, resolve.Options{LauncherVersion: buildVersion})
	if !planned.OK {
		writeHumanFailure(humanOut, planned.Error)
		return 2
	}
	fmt.Fprintf(humanOut, "  ✓ Resolve (%s)\n", time.Since(phaseStart).Round(time.Millisecond))
	fmt.Fprintf(humanOut, "\nPlan: platform %s at %s, %d artifacts", planned.Plan.ServiceGraph.PlatformVersion, root, len(planned.Plan.Artifacts))
	if planned.Plan.Request.ProjectRoot != "" {
		fmt.Fprintf(humanOut, "; project %s", planned.Plan.Request.ProjectRoot)
	}
	fmt.Fprintln(humanOut)
	for _, artifact := range planned.Plan.Artifacts {
		fmt.Fprintf(humanOut, "  - %s %s (%s)\n", artifact.ID, artifact.Version, artifact.Kind)
	}
	fmt.Fprintln(humanOut, "\nApplying installation…")
	requestFile, err := os.CreateTemp("", "kb-create-request-*.json")
	if err != nil {
		writeHumanFailure(humanOut, temporaryFileError(err))
		return 2
	}
	defer os.Remove(requestFile.Name())
	if _, err := requestFile.Write(encoded); err != nil {
		requestFile.Close()
		writeHumanFailure(humanOut, temporaryFileError(err))
		return 2
	}
	if err := requestFile.Close(); err != nil {
		writeHumanFailure(humanOut, temporaryFileError(err))
		return 2
	}
	result, err := os.CreateTemp("", "kb-create-result-*.json")
	if err != nil {
		writeHumanFailure(humanOut, temporaryFileError(err))
		return 2
	}
	defer os.Remove(result.Name())
	deps := i.RuntimeDeps
	if deps == nil {
		deps = &runtime.Dependencies{}
	}
	if !i.JSON {
		deps.Progress = &humanProgress{out: humanOut, started: map[string]time.Time{}}
	}
	code := (invocation{Operation: "apply", Index: index, Input: requestFile.Name(), Registry: i.Registry, SecretEnv: i.SecretEnv, Executable: i.Executable, RuntimeDeps: deps, NodePreflight: func() error { return nil }}).run(result)
	if err := result.Close(); err != nil {
		writeHumanFailure(humanOut, temporaryFileError(err))
		return 2
	}
	data, err := os.ReadFile(result.Name())
	if err != nil {
		writeHumanFailure(humanOut, temporaryFileError(err))
		return 2
	}
	var envelope struct {
		OK    bool       `json:"ok"`
		Error *diag.Diag `json:"error"`
	}
	if json.Unmarshal(data, &envelope) != nil || code != 0 || !envelope.OK {
		if envelope.Error != nil {
			writeHumanFailure(humanOut, envelope.Error)
		} else {
			writeHumanFailure(humanOut, contracts.NewDiag("KB_INSTALL_APPLY_FAILED", "Installation failed before a structured result was available.", "Run `kb-create doctor`, then rerun `kb-create install`.", nil))
		}
		return code
	}
	var applied map[string]any
	if json.Unmarshal(data, &applied) == nil {
		if status, ok := applied["shim"].(map[string]any); ok && status["linked"] == false {
			owner, _ := status["owner"].(string)
			fmt.Fprintf(humanOut, "The kb command is owned by %s; run %s to use this platform.\n", owner, shim.Target(root))
		}
	}
	if planned.Plan.Request.ProjectRoot == "" {
		fmt.Fprintf(humanOut, "\nKB Labs is installed at %s.\nNext: register a project with `kb project add <path>`.\n", root)
		return 0
	}
	projectID, registerErr := i.registerProject(planned.Plan.Request.ProjectRoot)
	if registerErr != nil {
		fmt.Fprintf(humanOut, "\nKB Labs is installed at %s, but the selected project could not be registered: %s.\nNext: run `kb project add %q`.\n", root, registerErr, planned.Plan.Request.ProjectRoot)
		return 1
	}
	fmt.Fprintf(humanOut, "\nVerification: platform installed at %s; project %s registered at %s.\nNext: run `kb-create status --platform-root %q` and `kb project show %s`.\n", root, projectID, planned.Plan.Request.ProjectRoot, root, projectID)
	return 0
}

func (i invocation) runJSONInstall(output *os.File) int {
	if i.Index == "" || (i.Input == "" && i.Direct.PlatformRoot == "") {
		write(output, failure(contracts.CodeInputRequired, "install needs a release index and an install request", "pass --index and either --input REQUEST.json or --request-platform-root PATH, then rerun `kb-create install --json`", nil))
		return 2
	}
	if i.Input != "" {
		i.Operation = "apply"
		return i.run(output)
	}
	_, err := catalog.LoadFile(i.Index)
	if err != nil {
		writeIndexFailure(output, err, "supply a valid immutable V2 release index")
		return 2
	}
	request, err := i.Direct.normalize()
	if err != nil {
		write(output, failure(contracts.CodeInputRequired, "install request is invalid", "correct the install flags or supply --input REQUEST.json", err))
		return 2
	}
	if i.ScenarioID != "" {
		request, err = compileScenario(i.ScenarioID, i.ScenarioAnswers, i.ScenarioResume, request)
		if err != nil {
			write(output, failure(contracts.CodeScenarioInvalid, "install scenario answers are invalid", "correct --scenario-answers and rerun `kb-create install --json`", err))
			return 2
		}
	}
	data, err := json.Marshal(request)
	if err != nil {
		write(output, failure(contracts.CodeInputRequired, "install request could not be encoded", "supply a valid install request and rerun `kb-create install --json`", err))
		return 2
	}
	requestFile, err := os.CreateTemp("", "kb-create-install-request-*.json")
	if err != nil {
		write(output, failure("KB_RUNTIME_UNEXPECTED", "install request could not be staged", "check temporary-directory permissions and rerun `kb-create install --json`", err))
		return 1
	}
	defer os.Remove(requestFile.Name())
	if _, err := requestFile.Write(data); err != nil {
		_ = requestFile.Close()
		write(output, failure("KB_RUNTIME_UNEXPECTED", "install request could not be staged", "check temporary-directory permissions and rerun `kb-create install --json`", err))
		return 1
	}
	if err := requestFile.Close(); err != nil {
		write(output, failure("KB_RUNTIME_UNEXPECTED", "install request could not be staged", "check temporary-directory permissions and rerun `kb-create install --json`", err))
		return 1
	}
	i.Operation, i.Input = "apply", requestFile.Name()
	i.Direct = directRequest{}
	return i.run(output)
}

func (i invocation) registerProject(projectRoot string) (string, error) {
	if i.ProjectRegistrar != nil {
		return i.ProjectRegistrar(projectRoot)
	}
	kbPath := "kb"
	if i.Executable != "" {
		kbPath = filepath.Join(filepath.Dir(i.Executable), "kb")
	}
	command := exec.Command(kbPath, "project", "add", projectRoot, "--json")
	output, err := command.Output()
	if err != nil {
		var failure struct {
			Error *diag.Diag `json:"error"`
		}
		if json.Unmarshal(output, &failure) == nil && failure.Error != nil {
			return "", fmt.Errorf("%s [%s]. %s", failure.Error.Message, failure.Error.Code, failure.Error.Hint)
		}
		return "", fmt.Errorf("the project registry command failed (%s)", err)
	}
	var result struct {
		OK      bool `json:"ok"`
		Project struct {
			ID   string `json:"id"`
			Path string `json:"path"`
		} `json:"project"`
	}
	if err := json.Unmarshal(output, &result); err != nil {
		return "", fmt.Errorf("decode project registration result: %w", err)
	}
	if !result.OK || result.Project.ID == "" || result.Project.Path != projectRoot {
		return "", fmt.Errorf("project registration returned an unexpected result")
	}
	return result.Project.ID, nil
}

type humanProgress struct {
	out     io.Writer
	started map[string]time.Time
}

func temporaryFileError(err error) *diag.Diag {
	if errors.Is(err, syscall.ENOSPC) {
		return contracts.NewDiag("KB_INSTALL_NO_DISK_SPACE", "The launcher ran out of disk space while staging the install.", "Free disk space and rerun `kb-create install`.", err)
	}
	return contracts.NewDiag("KB_INSTALL_NO_WRITE_ACCESS", "The launcher could not create or write its temporary install files.", "Check temporary-directory permissions, then rerun `kb-create install`.", err)
}

func (p *humanProgress) Emit(event runtime.ProgressEvent) {
	if event.Start {
		p.started[event.Phase] = time.Now()
		fmt.Fprintf(p.out, "  … %s\n", event.Phase)
		return
	}
	started, ok := p.started[event.Phase]
	if !ok {
		return
	}
	fmt.Fprintf(p.out, "  ✓ %s (%s)\n", event.Phase, time.Since(started).Round(time.Millisecond))
	delete(p.started, event.Phase)
}

func writeHumanFailure(output io.Writer, failure *diag.Diag) {
	if failure == nil {
		failure = contracts.NewDiag(contracts.CodeApplyFailed, "Installation failed unexpectedly.", "Run `kb-create doctor`, then rerun `kb-create install`.", nil)
	}
	fmt.Fprintf(output, "Install failed [%s]: %s\n", failure.Code, failure.Message)
	if failure.Reason != "" {
		fmt.Fprintf(output, "Why: %s\n", failure.Reason)
	}
	fmt.Fprintf(output, "Next: %s\n", failure.Hint)
}

// runStatus reports the host of the receipt-owned installation without
// resolving a new release, so it is safe for stable, canary and exact-version
// installations: it describes the immutable decision that was actually applied.
func (i invocation) runStatus(output *os.File) int {
	root := i.PlatformRoot
	if root == "" {
		root = defaultPlatformRoot()
	}
	if _, err := receipt.Read(root); err != nil {
		if i.JSON {
			write(output, map[string]any{"ok": true, "operation": "status", "installed": false, "running": false, "reachable": false, "next": "kb-create install"})
		} else {
			fmt.Fprintln(output, "Installed: no\nRunning: no\nReachable: no\nNext: kb-create install")
		}
		return 0
	}
	command := i.hostCommand(output)
	command.PlatformRoot = root
	runner, state, setupErr := command.setup()
	if setupErr != nil {
		return command.fail(2, setupErr)
	}
	return command.hostStatus(context.Background(), runner, state)
}

func requestWithGeneratedSecrets(request contracts.InstallRequest, artifacts []contracts.Artifact) (contracts.InstallRequest, bool, error) {
	known := make(map[string]bool, len(request.SecretInputs))
	for _, id := range request.SecretInputs {
		known[id] = true
	}
	changed := false
	for _, artifact := range artifacts {
		for _, requirement := range artifact.Config {
			if requirement.Secret && requirement.Generate && !known[requirement.ID] {
				request.SecretInputs = append(request.SecretInputs, requirement.ID)
				known[requirement.ID] = true
				changed = true
			}
		}
	}
	if changed {
		var err error
		request, err = request.Normalize()
		if err != nil {
			return contracts.InstallRequest{}, false, err
		}
	}
	return request, changed, nil
}

func defaultPlatformRoot() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, "kb-platform")
}

func populateSecrets(store secrets.Store, mappings string) error {
	if strings.TrimSpace(mappings) == "" {
		return nil
	}
	for _, pair := range strings.Split(mappings, ",") {
		name, environment, ok := strings.Cut(strings.TrimSpace(pair), "=")
		if !ok || strings.TrimSpace(name) == "" || strings.TrimSpace(environment) == "" {
			return fmt.Errorf("secret mapping %q must be requirement=ENV_VAR", pair)
		}
		value, exists := os.LookupEnv(environment)
		if !exists || value == "" {
			return fmt.Errorf("environment variable %q is not set", environment)
		}
		if err := store.Put(name, value); err != nil {
			return err
		}
	}
	return nil
}

func compileScenario(id, answers string, resume bool, base contracts.InstallRequest) (contracts.InstallRequest, error) {
	definition, err := scenario.Load(id)
	if err != nil {
		return contracts.InstallRequest{}, err
	}
	state, err := scenario.New(definition)
	if resume {
		state, err = scenario.LoadState(base.PlatformRoot, definition)
	}
	if err != nil {
		return contracts.InstallRequest{}, err
	}
	if answers != "" {
		var values map[string]json.RawMessage
		if err := json.Unmarshal([]byte(answers), &values); err != nil {
			return contracts.InstallRequest{}, fmt.Errorf("decode scenario answers: %w", err)
		}
		for field, value := range values {
			state, err = scenario.Answer(definition, state, field, value)
			if err != nil {
				return contracts.InstallRequest{}, err
			}
		}
	}
	if err := scenario.SaveState(base.PlatformRoot, definition, state); err != nil {
		return contracts.InstallRequest{}, err
	}
	request, err := scenario.Compile(definition, state, base)
	if err != nil {
		return contracts.InstallRequest{}, err
	}
	request.ScenarioStateDigest, err = scenario.StateDigest(definition, state)
	return request, err
}

func (value directRequest) normalize() (contracts.InstallRequest, error) {
	request := contracts.InstallRequest{Schema: contracts.RequestSchema, PlatformRoot: value.PlatformRoot, ProjectRoot: value.ProjectRoot, Platform: contracts.VersionSelector{Version: value.PlatformVersion, Channel: contracts.Channel(value.PlatformChannel)}, SDK: contracts.VersionSelector{Version: value.SDKVersion}, ServiceProfile: value.ServiceProfile, Policy: contracts.CompatibilityPolicy(value.Policy), Source: contracts.SourceRegistry}
	if value.Offline {
		request.Source = contracts.SourceOffline
	}
	plugins, err := parseComponents(value.Plugins)
	if err != nil {
		return contracts.InstallRequest{}, err
	}
	adapters, err := parseComponents(value.Adapters)
	if err != nil {
		return contracts.InstallRequest{}, err
	}
	request.Plugins, request.Adapters = plugins, adapters
	request.ProviderPreferences, err = parseProviders(value.Providers)
	if err != nil {
		return contracts.InstallRequest{}, err
	}
	return request.Normalize()
}

func parseProviders(value string) (map[string]string, error) {
	preferences := map[string]string{}
	for _, item := range strings.Split(value, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		capability, adapter, ok := strings.Cut(item, "=")
		capability, adapter = strings.TrimSpace(capability), strings.TrimSpace(adapter)
		if !ok || strings.TrimSpace(capability) == "" || strings.TrimSpace(adapter) == "" {
			return nil, fmt.Errorf("provider %q must be capability=adapter", item)
		}
		if _, exists := preferences[capability]; exists {
			return nil, fmt.Errorf("provider for capability %q was specified more than once", capability)
		}
		preferences[capability] = adapter
	}
	return preferences, nil
}

func parseComponents(value string) ([]contracts.ComponentRequest, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	items := strings.Split(value, ",")
	result := make([]contracts.ComponentRequest, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			return nil, fmt.Errorf("component list contains empty item")
		}
		// Scoped npm-style component IDs contain an @ themselves. Only the last
		// separator denotes an optional immutable version pin.
		id, version, pinned := item, "", false
		if at := strings.LastIndex(item, "@"); at > 0 {
			id, version, pinned = item[:at], item[at+1:], true
		}
		if strings.TrimSpace(id) == "" {
			return nil, fmt.Errorf("component ID is required")
		}
		component := contracts.ComponentRequest{ID: id}
		if pinned {
			if version == "" {
				return nil, fmt.Errorf("component %q has empty version", id)
			}
			component.Version.Version = version
		}
		result = append(result, component)
	}
	return result, nil
}

func write(output *os.File, value any) { _ = json.NewEncoder(output).Encode(value) }

// writeIndexFailure emits the shared diagnostic from the index gate as is
// (schema unsupported, corrupt digest); anything else is a generic load failure.
func writeIndexFailure(output *os.File, err error, hint string) {
	var typed *diag.Diag
	if errors.As(err, &typed) {
		write(output, result.FailureEnvelope(typed))
		return
	}
	write(output, result.FailureEnvelope(contracts.NewDiag(
		contracts.CodeReleaseIndexInvalid, "The release index could not be loaded.", hint, err)))
}

func failure(code, message, hint string, cause error) map[string]any {
	return map[string]any{"ok": false, "error": contracts.NewDiag(code, message, hint, cause)}
}

func writeShimFailure(output *os.File, code, platformRoot string, cause error) {
	var details map[string]string
	var message string
	var failureCause *shim.Failure
	if errors.As(cause, &failureCause) {
		reason := failureCause.Reason
		if failureCause.Cause != nil {
			reason = failureCause.Cause.Error()
		}
		details = map[string]string{"path": failureCause.Path, "reason": reason}
		message = fmt.Sprintf("Platform files and receipt remain applied at %s, but the kb command shim could not be created.", platformRoot)
		diagnosticReason := fmt.Sprintf("The filesystem refused shim path %s: %s", failureCause.Path, reason)
		diagnostic := contracts.NewDiag(code, message,
			fmt.Sprintf("Fix the filesystem reason for the shim path, then run `kb-create doctor --fix --platform-root %q`.", platformRoot), cause)
		diagnostic.Reason = diagnosticReason
		diagnostic.Meta = map[string]any{"path": details["path"], "reason": details["reason"]}
		write(output, map[string]any{"ok": false, "error": diagnostic})
		return
	} else {
		message = fmt.Sprintf("Platform files and receipt remain applied at %s, but the kb command shim could not be created: %s", platformRoot, cause)
	}
	diagnostic := contracts.NewDiag(code, message,
		fmt.Sprintf("Fix the filesystem reason for the shim path, then run `kb-create doctor --fix --platform-root %q`.", platformRoot), cause)
	write(output, map[string]any{"ok": false, "error": diagnostic})
}

func writeFailureDossier(output *os.File, plan contracts.ResolvedInstallPlan, correlationID, logPath string, cause error) {
	var resolutionDiag *diag.Diag
	if !errors.As(cause, &resolutionDiag) {
		resolutionDiag = contracts.NewDiag(contracts.CodeApplyFailed, "V2 operation did not reach a verified installation", "Inspect the local log and diagnostic dossier, then fix the reported prerequisite or run doctor --fix.", cause)
	}
	writeDiagnosticFailure(output, plan.Request.PlatformRoot, correlationID, logPath, resolutionDiag, &plan)
}
