package v2cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/kb-labs/clikit/diag"
	"github.com/kb-labs/clikit/result"
	"github.com/kb-labs/create/v2/contracts"
	"github.com/kb-labs/create/v2/secrets"
)

//go:generate go run ../gen-gateway-bootstrap ../../../../../services/gateway/contracts/src/auth-health-response.schema.json gateway_bootstrap_gen.go

type gatewayAuthHealthIssue struct {
	Code       *string                         `json:"code"`
	Severity   *GatewayAuthHealthIssueSeverity `json:"severity"`
	Message    *string                         `json:"message"`
	Hint       *string                         `json:"hint"`
	ProviderID *string                         `json:"providerId,omitempty"`
	Failure    *string                         `json:"failure,omitempty"`
}

type gatewayAuthHealthResponse struct {
	OK           *bool                     `json:"ok"`
	AuthEnabled  *bool                     `json:"authEnabled"`
	TenantID     *string                   `json:"tenantId"`
	ActiveAdmins *int                      `json:"activeAdmins"`
	Bootstrap    *GatewayBootstrapStatus   `json:"bootstrap"`
	Issues       *[]gatewayAuthHealthIssue `json:"issues"`
}

type bootstrapOutcomeHost struct {
	starter interface {
		Start(context.Context, contracts.HostSpec) (func() error, error)
	}
	store secrets.Store
}

func (host bootstrapOutcomeHost) Start(ctx context.Context, spec contracts.HostSpec) (func() error, error) {
	stop, err := host.starter.Start(ctx, spec)
	if err != nil {
		return nil, err
	}
	if err := readBootstrapOutcome(ctx, spec.HealthURL, host.store); err != nil {
		return nil, errorsJoinStop(err, stop)
	}
	return stop, nil
}

func readBootstrapOutcome(ctx context.Context, healthURL string, store secrets.Store) error {
	parsed, err := url.Parse(healthURL)
	if err != nil {
		return fmt.Errorf("Gateway auth readiness URL is invalid")
	}
	parsed.Path, parsed.RawPath, parsed.RawQuery, parsed.Fragment = "/health/auth", "", "", ""
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return fmt.Errorf("Gateway auth readiness request could not be created")
	}
	request.Header.Set("X-Request-Id", diag.NewCorrelationID())
	client := http.Client{Timeout: 3 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("Gateway did not report first-admin bootstrap status")
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return bootstrapContractFailure("Gateway returned an invalid first-administrator health response.", "Gateway auth-health response did not match the installer contract", store.PlatformRoot)
	}
	var payload json.RawMessage
	failure, decodeErr := result.DecodeResult(body, &payload)
	if failure != nil {
		return failure
	}
	if decodeErr != nil || response.StatusCode != http.StatusOK {
		return bootstrapContractFailure("Gateway returned an invalid first-administrator health response.", "Gateway auth-health request did not return a valid success result", store.PlatformRoot)
	}
	responseFields, err := validateGatewayRequiredFields(payload, gatewayAuthHealthResponseRequiredFields[:])
	if err != nil {
		return bootstrapContractFailure("Gateway returned an invalid first-administrator health response.", "Gateway auth-health response was missing required contract fields", store.PlatformRoot)
	}
	var rawIssues []json.RawMessage
	if err := json.Unmarshal(responseFields["issues"], &rawIssues); err != nil {
		return bootstrapContractFailure("Gateway returned an invalid first-administrator health response.", "Gateway auth-health issues did not match the installer contract", store.PlatformRoot)
	}
	for _, rawIssue := range rawIssues {
		if _, err := validateGatewayRequiredFields(rawIssue, gatewayAuthHealthIssueRequiredFields[:]); err != nil {
			return bootstrapContractFailure("Gateway returned an invalid first-administrator health response.", "Gateway auth-health issue did not match the installer contract", store.PlatformRoot)
		}
	}
	var readiness gatewayAuthHealthResponse
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&readiness); err != nil {
		return bootstrapContractFailure("Gateway returned an invalid first-administrator health response.", "Gateway auth-health response types did not match the installer contract", store.PlatformRoot)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return bootstrapContractFailure("Gateway returned more than one first-administrator health response.", "Gateway auth-health response contained more than one result", store.PlatformRoot)
	}
	if *readiness.ActiveAdmins < 0 {
		return bootstrapContractFailure("Gateway returned an invalid first-administrator health response.", "Gateway reported a negative active-administrator count", store.PlatformRoot)
	}
	if !*readiness.OK || *readiness.ActiveAdmins == 0 {
		cause := "Gateway auth readiness is not OK"
		if *readiness.ActiveAdmins == 0 {
			cause = "Gateway reports no usable active administrator"
		}
		return bootstrapOutcomeFailure(store.PlatformRoot, cause)
	}
	switch *readiness.Bootstrap {
	case GatewayBootstrapStatusProvisioned, GatewayBootstrapStatusExistsActive, GatewayBootstrapStatusExistsNonActive:
		return store.Put(secrets.BootstrapCompleted, string(*readiness.Bootstrap))
	case GatewayBootstrapStatusNotConfigured, GatewayBootstrapStatusFailed:
		return bootstrapOutcomeFailure(store.PlatformRoot, "Gateway reports that first-administrator bootstrap did not complete")
	default:
		return bootstrapContractFailure("Gateway returned an invalid first-administrator health response.", "Gateway returned an unsupported bootstrap status", store.PlatformRoot)
	}
}

func validateGatewayRequiredFields(payload []byte, required []string) (map[string]json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil {
		return nil, err
	}
	if fields == nil {
		return nil, fmt.Errorf("Gateway health response object is required")
	}
	for _, field := range required {
		value, exists := fields[field]
		if !exists || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return nil, fmt.Errorf("Gateway health response is missing required field %q", field)
		}
	}
	return fields, nil
}

func bootstrapContractFailure(message, cause, platformRoot string) *diag.Diag {
	return contracts.NewDiag(contracts.CodeApplyFailed, message,
		"The installer and Gateway returned incompatible first-administrator health data. This is an internal installer/Gateway contract mismatch. Run `"+contracts.ShellCommand("kb-create", "doctor", "--platform-root", platformRoot)+"` to capture installation diagnostics, then provide the diagnostic file and output to KB Labs support.", fmt.Errorf("%s", cause))
}

func bootstrapOutcomeFailure(platformRoot, cause string) *diag.Diag {
	return contracts.NewDiag(contracts.CodeApplyFailed, "Gateway did not prove first-administrator bootstrap and secured authentication are ready with a usable active administrator.",
		"Run `"+contracts.ShellCommand("kb-create", "doctor", "--platform-root", platformRoot)+"` to inspect Gateway authentication readiness and resolve the issue, then rerun `kb-create install`.", fmt.Errorf("%s", cause))
}

func firstAdminDisclosure(store secrets.Store, adminEmail string, completed bool, platformRoot string) (*firstAdminCredential, *diag.Diag) {
	bootstrapState, present, readErr := store.Get(secrets.BootstrapCompleted)
	generated, generatedErr := store.Exists(secrets.BootstrapGenerated)
	disclosed, disclosedErr := store.Exists(secrets.BootstrapDisclosed)
	if readErr != nil || generatedErr != nil || disclosedErr != nil {
		return nil, contracts.NewDiag(contracts.CodeApplyFailed, "the first administrator bootstrap state could not be read", "check that the platform root is writable, then rerun `kb-create install`", errors.Join(readErr, generatedErr, disclosedErr))
	}
	if !present || bootstrapState != "provisioned" || !generated || disclosed {
		return nil, nil
	}
	if completed {
		return nil, contracts.NewDiag(contracts.CodeApplyFailed,
			"the first administrator credential was not disclosed during the original install and cannot be disclosed after bootstrap completion",
			"Sign in with the administrator's current password. If no administrator can sign in, run `"+contracts.ShellCommand("kb-create", "doctor", "--platform-root", platformRoot, "--recover", "--email", adminEmail, "--generate", "--yes")+"` to recover access.",
			fmt.Errorf("bootstrap completion predates the undisclosed credential state"))
	}
	password, passwordPresent, passwordErr := store.Get("gateway.bootstrap.password")
	if passwordErr != nil || !passwordPresent {
		return nil, contracts.NewDiag(contracts.CodeApplyFailed, "the generated first administrator password could not be read", "check that the platform root is writable, then rerun `kb-create install`", passwordErr)
	}
	if err := store.Put(secrets.BootstrapDisclosed, "true"); err != nil {
		return nil, contracts.NewDiag(contracts.CodeApplyFailed, "the first administrator credential disclosure could not be recorded", "check that the platform root is writable, then rerun `kb-create install`", err)
	}
	return &firstAdminCredential{Email: adminEmail, Password: password, ShownOnce: true}, nil
}

func errorsJoinStop(cause error, stop func() error) error {
	if stop == nil {
		return cause
	}
	return fmt.Errorf("%w (host stop: %v)", cause, stop())
}
