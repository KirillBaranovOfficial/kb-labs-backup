package v2cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/kb-labs/clikit/diag"
	"github.com/kb-labs/create/v2/catalog"
	"github.com/kb-labs/create/v2/contracts"
	"github.com/kb-labs/create/v2/gateway"
	"github.com/kb-labs/create/v2/host"
	"github.com/kb-labs/create/v2/receipt"
	"github.com/kb-labs/create/v2/resolve"
	"github.com/kb-labs/create/v2/shim"
	"github.com/kb-labs/create/v2/telemetry"
	"github.com/kb-labs/create/v2/transport"
	"github.com/kb-labs/create/v2/versionroot"
)

type planInput struct {
	Operation     string
	Index         string
	RequestFile   string
	Request       *contracts.InstallRequest
	Direct        directRequest
	ScenarioID    string
	Answers       string
	Resume        bool
	Executable    string
	KnownGateways []gateway.Root
}

type resolvedPlan struct {
	Catalog catalog.Catalog
	Plan    contracts.ResolvedInstallPlan
	State   host.State
}

type planResult struct {
	Plan          contracts.ResolvedInstallPlan `json:"plan"`
	UpdateSummary *contracts.UpdateSummary      `json:"updateSummary,omitempty"`
}

type planCommandInput struct {
	Index, RequestFile, ScenarioID, Answers, Registry, Executable string
	Direct                                                        directRequest
	Resume                                                        bool
	KnownGateways                                                 []gateway.Root
	TelemetryEndpoint                                             string
	TelemetryConsent                                              bool
}

func executePlan(_ context.Context, request planCommandInput) (planResult, *diag.Diag) {
	started := time.Now()
	outcome, errorCode := "failure", "KB_INSTALL_OPERATION_FAILED"
	var plan contracts.ResolvedInstallPlan
	defer func() {
		telemetry.Send(request.TelemetryEndpoint, request.TelemetryConsent, telemetry.New("plan", outcome, errorCode,
			string(plan.Request.Platform.Channel), string(plan.Request.Source), len(plan.Artifacts), time.Since(started)))
	}()
	if request.Registry != "" {
		registry, err := url.Parse(request.Registry)
		if err != nil || registry.Host == "" || (registry.Scheme != "http" && registry.Scheme != "https") {
			cause := err
			if cause == nil {
				cause = fmt.Errorf("registry URL must use HTTP or HTTPS and include a host")
			}
			return planResult{}, inputFailure(failure(contracts.CodeInputRequired,
				"--registry must be an absolute HTTP or HTTPS URL",
				"pass a registry such as `https://registry.npmjs.org`, then rerun `kb-create plan`", cause))
		}
	}
	resolved, failure := resolvePlan(planInput{Index: request.Index, RequestFile: request.RequestFile, Direct: request.Direct,
		Operation: "plan", ScenarioID: request.ScenarioID, Answers: request.Answers, Resume: request.Resume,
		Executable: request.Executable, KnownGateways: request.KnownGateways})
	if failure != nil {
		return planResult{}, inputFailure(failure)
	}
	plan = resolved.Plan
	if plan.Host != nil {
		if err := host.CheckPorts(plan, "plan"); err != nil {
			return planResult{}, inputFailure(asDiag(err))
		}
	}
	var updateSummary *contracts.UpdateSummary
	activeRoot, layoutErr := activePlatformRoot(plan.Request.PlatformRoot)
	installed := layoutErr == nil
	if layoutErr != nil && !errors.Is(layoutErr, versionroot.ErrPlatformNotInstalled) {
		return planResult{}, inputFailure(unsupportedPlatformLayout(plan.Request.PlatformRoot, layoutErr))
	}
	if installed {
		receiptPath := receipt.Path(activeRoot)
		if _, err := os.Stat(receiptPath); err != nil {
			failure := contracts.NewDiag(contracts.CodeReceiptUnavailable, "the installed receipt could not be checked for update comparison", "check platform-root permissions, then rerun `kb-create plan`", err)
			return planResult{}, inputFailure(failure)
		}
		active, readErr := receipt.Read(activeRoot)
		if readErr != nil {
			if failure := receiptArtifactIdentityFailure(readErr, request.Index, plan.Request.PlatformRoot); failure != nil {
				return planResult{}, inputFailure(failure)
			}
			failure := contracts.NewDiag(contracts.CodeReceiptUnavailable, "the installed receipt could not be read for update comparison", "restore a named V2 snapshot, then rerun `kb-create plan`", readErr)
			return planResult{}, inputFailure(failure)
		}
		summary, summaryErr := resolve.SummarizeUpdate(active, plan, resolved.Catalog)
		if summaryErr != nil {
			failure := contracts.NewDiag(contracts.CodeOperationFailed, "the installed platform could not be compared with the resolved plan", "verify the active receipt and sealed release index, then rerun `kb-create plan`", summaryErr)
			return planResult{}, inputFailure(failure)
		}
		updateSummary = &summary
	}
	outcome, errorCode = "success", ""
	return planResult{Plan: plan, UpdateSummary: updateSummary}, nil
}

func receiptArtifactIdentityFailure(err error, index, platformRoot string) *diag.Diag {
	var failure *diag.Diag
	if !errors.As(err, &failure) || failure.Code != contracts.CodeArtifactIdentityInvalid {
		return nil
	}
	failure.Hint = "Reinstall this platform with `" + contracts.ShellCommand("kb-create", "apply", "--index", index, "--platform-root", platformRoot) + "`."
	return failure
}

func inputFailure(failure *diag.Diag) *diag.Diag {
	if failure != nil && failure.ProcessExitCode == diag.ExitOK {
		failure.ProcessExitCode = diag.ExitInput
	}
	return failure
}

func knownGatewayRoots(executable string) []gateway.Root {
	state, err := host.StateHome()
	if err != nil {
		return nil
	}
	known := map[string]gateway.Root{}
	remember := func(candidate string) {
		if candidate == "" {
			return
		}
		if active, readErr := receipt.Read(candidate); readErr == nil {
			if url := recordedGatewayURL(active); url != "" {
				root := filepath.Clean(candidate)
				known[root] = gateway.Root{PlatformRoot: root, URL: url}
			}
		}
	}
	if owner, ok := shim.Owner(executable); ok {
		remember(owner)
	}
	if active, ok := host.ActiveHost(state); ok {
		remember(active.PlatformRoot)
	}
	result := make([]gateway.Root, 0, len(known))
	for _, root := range known {
		result = append(result, root)
	}
	return result
}

func activePlatformRoot(installRoot string) (string, error) {
	layout, err := versionroot.New(installRoot)
	if err != nil {
		return "", err
	}
	return layout.ExistingPlatformRoot()
}

// resolvePlan performs the shared, read-only request and release resolution
// used by plan, apply, update, and install. The caller owns operation-specific
// validation and mutation after resolution.
func resolvePlan(input planInput) (resolvedPlan, *diag.Diag) {
	if input.Index == "" || (input.RequestFile == "" && input.Request == nil && input.Direct.PlatformRoot == "") {
		return resolvedPlan{}, failure(contracts.CodeInputRequired, "--index and either --input or --platform-root are required", "pass immutable release index plus V2 request JSON or direct CI flags", nil)
	}
	source, err := catalog.LoadFile(input.Index)
	if err != nil {
		return resolvedPlan{}, indexFailure(err, "supply a valid immutable V2 release index")
	}
	var response transport.PlanResponse
	if input.Request != nil {
		data, marshalErr := json.Marshal(input.Request)
		if marshalErr != nil {
			return resolvedPlan{}, failure(contracts.CodeInputRequired, "install request could not be encoded", "correct direct CI flags or provide --input", marshalErr)
		}
		response = transport.PlanWith(data, source, resolve.Options{LauncherVersion: buildVersion, ProjectRootSelection: input.Operation != "update"})
	} else if input.RequestFile != "" {
		data, readErr := readRequest(input.RequestFile)
		if readErr != nil {
			return resolvedPlan{}, failure("KB_INSTALL_INPUT_REQUIRED", "request could not be read", "supply a readable V2 request JSON file", readErr)
		}
		response = transport.PlanWith(data, source, resolve.Options{LauncherVersion: buildVersion, ProjectRootSelection: input.Operation != "update"})
	} else {
		request, requestErr := input.Direct.normalize()
		if requestErr != nil {
			var validation *contracts.RequestValidationError
			if errors.As(requestErr, &validation) {
				diagnostic := validation.Diagnostic()
				setRequestRetryHint(diagnostic, input, validation.Field)
				return resolvedPlan{}, diagnostic
			}
			return resolvedPlan{}, failure("KB_INSTALL_INPUT_REQUIRED", "direct request is invalid", "correct direct CI flags or provide --input", requestErr)
		}
		if input.Operation != "update" && request.ProjectRoot != "" {
			request.ProjectRoot, requestErr = contracts.ValidateProjectSelection(request.ProjectRoot)
			if requestErr != nil {
				var validation *contracts.RequestValidationError
				if errors.As(requestErr, &validation) {
					diagnostic := validation.Diagnostic()
					setRequestRetryHint(diagnostic, input, validation.Field)
					return resolvedPlan{}, diagnostic
				}
				return resolvedPlan{}, failure(contracts.CodeInputRequired, "project selection is invalid", "pass an existing project directory, then retry", requestErr)
			}
		}
		if input.ScenarioID != "" {
			if failure := validateInstallTarget(request.PlatformRoot); failure != nil {
				return resolvedPlan{}, failure
			}
		}
		if input.ScenarioID != "" {
			request, requestErr = compileScenario(input.ScenarioID, input.Answers, input.Resume, request)
			if requestErr != nil {
				return resolvedPlan{}, failure("KB_INSTALL_SCENARIO_INVALID", "scenario could not be compiled", "inspect scenario answers and manifest requirements", requestErr)
			}
		}
		data, marshalErr := json.Marshal(request)
		if marshalErr != nil {
			return resolvedPlan{}, failure(contracts.CodeInputRequired, "direct request is invalid", "correct direct CI flags or provide --input", marshalErr)
		}
		response = transport.PlanWith(data, source, resolve.Options{LauncherVersion: buildVersion})
	}
	if !response.OK {
		if response.Error != nil {
			if field, ok := response.Error.Meta["field"].(string); ok {
				setRequestRetryHint(response.Error, input, field)
			}
		}
		return resolvedPlan{}, response.Error
	}
	state, err := host.StateHome()
	if err != nil {
		return resolvedPlan{}, asDiag(err)
	}
	if response.Plan.Host != nil && planHasGateway(*response.Plan) {
		root := response.Plan.Request.PlatformRoot
		activeRoot, layoutErr := activePlatformRoot(root)
		installed := layoutErr == nil
		if layoutErr != nil && !errors.Is(layoutErr, versionroot.ErrPlatformNotInstalled) {
			return resolvedPlan{}, unsupportedPlatformLayout(root, layoutErr)
		}
		registry := gateway.Registry{Path: filepath.Join(state.Dir, "gateway-roots.json")}
		knownByRoot := make(map[string]gateway.Root, len(input.KnownGateways))
		for _, candidate := range input.KnownGateways {
			if candidate.PlatformRoot == "" || filepath.Clean(candidate.PlatformRoot) == filepath.Clean(root) {
				continue
			}
			if activeOwner, ownerErr := receipt.Read(candidate.PlatformRoot); ownerErr == nil {
				if ownerURL := recordedGatewayURL(activeOwner); ownerURL != "" {
					knownByRoot[filepath.Clean(candidate.PlatformRoot)] = gateway.Root{PlatformRoot: filepath.Clean(candidate.PlatformRoot), URL: ownerURL}
				}
			}
		}
		known := make([]gateway.Root, 0, len(knownByRoot))
		for _, item := range knownByRoot {
			known = append(known, item)
		}
		requestedURL := response.Plan.Request.GatewayURL
		if installed && requestedURL == "" {
			if active, activeErr := receipt.Read(activeRoot); activeErr == nil {
				requestedURL = recordedGatewayURL(active)
			}
		}
		defaultURL, originErr := gateway.OriginFromHealthURL(response.Plan.Host.HealthURL)
		if originErr != nil {
			return resolvedPlan{}, failure(contracts.CodeHostSpecInvalid, "the release Gateway address is invalid", "select a release with a valid loopback Gateway health URL", originErr)
		}
		selectedURL, allocErr := registry.AllocateWithKnown(root, defaultURL, requestedURL, known)
		if allocErr != nil {
			return resolvedPlan{}, failure(contracts.CodeApplyFailed, "installed Gateway addresses could not be read", "check the launcher state directory and rerun `kb-create apply`", allocErr)
		}
		claimant, claimed, claimErr := registry.ClaimantAmong(root, selectedURL, known)
		if claimErr != nil {
			return resolvedPlan{}, failure(contracts.CodeApplyFailed, "installed Gateway addresses could not be read", "check the launcher state directory and rerun `kb-create apply`", claimErr)
		}
		if claimed {
			diagnostic := contracts.NewDiag(contracts.CodePortInUse,
				"Gateway address "+selectedURL+" is already assigned to installed root "+claimant.PlatformRoot+".",
				"Choose another `--gateway-url`, then rerun `kb-create apply`.", nil)
			diagnostic.Meta = map[string]any{"address": selectedURL, "platformRoot": claimant.PlatformRoot}
			return resolvedPlan{}, diagnostic
		}
		if selectedURL != response.Plan.Request.GatewayURL {
			request := response.Plan.Request
			request.GatewayURL = selectedURL
			data, marshalErr := json.Marshal(request)
			if marshalErr != nil {
				return resolvedPlan{}, failure(contracts.CodeInputRequired, "the install request could not be encoded", "rerun `kb-create plan` with the same request", marshalErr)
			}
			response = transport.PlanWith(data, source, resolve.Options{LauncherVersion: buildVersion})
			if !response.OK {
				if response.Error != nil {
					if field, ok := response.Error.Meta["field"].(string); ok {
						setRequestRetryHint(response.Error, input, field)
					}
				}
				return resolvedPlan{}, response.Error
			}
		}
	}
	return resolvedPlan{Catalog: source, Plan: *response.Plan, State: state}, nil
}

func setRequestRetryHint(diagnostic *diag.Diag, input planInput, field string) {
	operation := input.Operation
	args := []string{"kb-create", operation}
	appendFlag := func(name, value string) {
		if value != "" {
			args = append(args, "--"+name, value)
		}
	}
	appendFlag("index", input.Index)
	if input.RequestFile != "" {
		appendFlag("input", input.RequestFile)
	} else {
		appendFlag("platform-root", input.Direct.PlatformRoot)
		appendFlag("project-root", input.Direct.ProjectRoot)
		appendFlag("platform-version", input.Direct.PlatformVersion)
		appendFlag("platform-channel", input.Direct.PlatformChannel)
		// Gateway URLs can contain credentials; scenario answer JSON can contain secrets.
		appendFlag("sdk-version", input.Direct.SDKVersion)
		appendFlag("service-profile", input.Direct.ServiceProfile)
		appendFlag("plugins", input.Direct.Plugins)
		appendFlag("adapters", input.Direct.Adapters)
		appendFlag("provider", input.Direct.Providers)
		appendFlag("policy", input.Direct.Policy)
		if input.Direct.Offline {
			args = append(args, "--offline")
		}
		appendFlag("scenario", input.ScenarioID)
	}
	args = append(args, "--json")
	command := contracts.ShellCommand(args...)
	var retry string
	if input.RequestFile != "" {
		retry = fmt.Sprintf("Correct %s in request file %s, then retry: %s", field, contracts.ShellQuote(input.RequestFile), command)
	} else {
		retry = fmt.Sprintf("Correct %s using the corresponding request flag, then retry: %s", field, command)
		if input.ScenarioID != "" {
			// Scenario answers can contain secret values, so keep them out of diagnostics.
			retry += " Reuse the scenario answers from your original command if needed."
		}
	}
	catalogEntry, catalogued := diag.LookupCatalog(diagnostic.Code)
	if diagnostic.Hint == "" || (catalogued && diagnostic.Hint == catalogEntry.Hint) {
		diagnostic.Hint = retry
	} else {
		diagnostic.Hint += " " + retry
	}
	if diagnostic.Meta == nil {
		diagnostic.Meta = map[string]any{}
	}
	diagnostic.Meta["field"] = field
}

func readRequest(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read request %q: %w", path, err)
	}
	return data, nil
}

func indexFailure(err error, hint string) *diag.Diag {
	var typed *diag.Diag
	if errors.As(err, &typed) {
		return inputFailure(typed)
	}
	return inputFailure(contracts.NewDiag(contracts.CodeReleaseIndexInvalid, "The release index could not be loaded.", hint, err))
}
