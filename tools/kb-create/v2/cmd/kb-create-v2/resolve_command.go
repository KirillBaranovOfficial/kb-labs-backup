package v2cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
)

type planInput struct {
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
	Plan contracts.ResolvedInstallPlan `json:"plan"`
}

type planCommandInput struct {
	Index, RequestFile, ScenarioID, Answers, Executable string
	Direct                                              directRequest
	Resume                                              bool
	KnownGateways                                       []gateway.Root
	TelemetryEndpoint                                   string
	TelemetryConsent                                    bool
}

func executePlan(_ context.Context, request planCommandInput) (planResult, *diag.Diag) {
	started := time.Now()
	outcome, errorCode := "failure", "KB_INSTALL_OPERATION_FAILED"
	var plan contracts.ResolvedInstallPlan
	defer func() {
		telemetry.Send(request.TelemetryEndpoint, request.TelemetryConsent, telemetry.New("plan", outcome, errorCode,
			string(plan.Request.Platform.Channel), string(plan.Request.Source), len(plan.Artifacts), time.Since(started)))
	}()
	resolved, failure := resolvePlan(planInput{Index: request.Index, RequestFile: request.RequestFile, Direct: request.Direct,
		ScenarioID: request.ScenarioID, Answers: request.Answers, Resume: request.Resume,
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
	outcome, errorCode = "success", ""
	return planResult{Plan: plan}, nil
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

// resolvePlan performs the shared, read-only request and release resolution
// used by plan, apply, update, and install. The caller owns operation-specific
// validation and mutation after resolution.
func resolvePlan(input planInput) (resolvedPlan, *diag.Diag) {
	if input.Index == "" || (input.RequestFile == "" && input.Request == nil && input.Direct.PlatformRoot == "") {
		return resolvedPlan{}, failure(contracts.CodeInputRequired, "--index and either --input or --request-platform-root are required", "pass immutable release index plus V2 request JSON or direct CI flags", nil)
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
		response = transport.PlanWith(data, source, resolve.Options{LauncherVersion: buildVersion})
	} else if input.RequestFile != "" {
		data, readErr := readRequest(input.RequestFile)
		if readErr != nil {
			return resolvedPlan{}, failure("KB_INSTALL_INPUT_REQUIRED", "request could not be read", "supply a readable V2 request JSON file", readErr)
		}
		response = transport.PlanWith(data, source, resolve.Options{LauncherVersion: buildVersion})
	} else {
		request, requestErr := input.Direct.normalize()
		if requestErr != nil {
			return resolvedPlan{}, failure("KB_INSTALL_INPUT_REQUIRED", "direct request is invalid", "correct direct CI flags or provide --input", requestErr)
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
		return resolvedPlan{}, response.Error
	}
	state, err := host.StateHome()
	if err != nil {
		return resolvedPlan{}, asDiag(err)
	}
	if response.Plan.Host != nil && planHasGateway(*response.Plan) {
		root := response.Plan.Request.PlatformRoot
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
		if active, activeErr := receipt.Read(root); requestedURL == "" && activeErr == nil {
			requestedURL = recordedGatewayURL(active)
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
				return resolvedPlan{}, response.Error
			}
		}
	}
	return resolvedPlan{Catalog: source, Plan: *response.Plan, State: state}, nil
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
