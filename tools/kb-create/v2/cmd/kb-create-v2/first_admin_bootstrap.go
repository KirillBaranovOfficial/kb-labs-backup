package v2cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/kb-labs/clikit/diag"
	"github.com/kb-labs/clikit/result"
	"github.com/kb-labs/create/v2/contracts"
	"github.com/kb-labs/create/v2/secrets"
)

//go:generate go run ../gen-gateway-bootstrap ../../../../../services/gateway/contracts/src/auth-health-response.schema.json gateway_bootstrap_gen.go

type gatewayAuthHealthIssue struct {
	Code     *string                         `json:"code"`
	Severity *GatewayAuthHealthIssueSeverity `json:"severity"`
	Message  *string                         `json:"message"`
	Hint     *string                         `json:"hint"`
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
	client := http.Client{Timeout: 3 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("Gateway did not report first-admin bootstrap status")
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return bootstrapContractFailure("Gateway returned an invalid first-administrator health response.", nil, store.PlatformRoot)
	}
	var payload json.RawMessage
	failure, decodeErr := result.DecodeResult(body, &payload)
	if failure != nil {
		return failure
	}
	if decodeErr != nil || response.StatusCode != http.StatusOK {
		return bootstrapContractFailure("Gateway returned an invalid first-administrator health response.", nil, store.PlatformRoot)
	}
	responseFields, err := validateGatewayRequiredFields(payload, gatewayAuthHealthResponseRequiredFields[:])
	if err != nil {
		return bootstrapContractFailure("Gateway returned an invalid first-administrator health response.", err, store.PlatformRoot)
	}
	var rawIssues []json.RawMessage
	if err := json.Unmarshal(responseFields["issues"], &rawIssues); err != nil {
		return bootstrapContractFailure("Gateway returned an invalid first-administrator health response.", err, store.PlatformRoot)
	}
	for _, rawIssue := range rawIssues {
		if _, err := validateGatewayRequiredFields(rawIssue, gatewayAuthHealthIssueRequiredFields[:]); err != nil {
			return bootstrapContractFailure("Gateway returned an invalid first-administrator health response.", err, store.PlatformRoot)
		}
	}
	var readiness gatewayAuthHealthResponse
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&readiness); err != nil {
		return bootstrapContractFailure("Gateway returned an invalid first-administrator health response.", err, store.PlatformRoot)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return bootstrapContractFailure("Gateway returned more than one first-administrator health response.", err, store.PlatformRoot)
	}
	if *readiness.ActiveAdmins < 0 {
		return bootstrapContractFailure("Gateway returned an invalid first-administrator health response.", nil, store.PlatformRoot)
	}
	switch *readiness.Bootstrap {
	case GatewayBootstrapStatusProvisioned, GatewayBootstrapStatusExistsActive, GatewayBootstrapStatusExistsNonActive:
		return store.Put(secrets.BootstrapCompleted, string(*readiness.Bootstrap))
	case GatewayBootstrapStatusNotConfigured, GatewayBootstrapStatusFailed:
		return bootstrapOutcomeFailure(store.PlatformRoot)
	default:
		return bootstrapContractFailure("Gateway returned an invalid first-administrator health response.", nil, store.PlatformRoot)
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

func bootstrapContractFailure(message string, cause error, platformRoot string) *diag.Diag {
	return contracts.NewDiag(contracts.CodeApplyFailed, message,
		"The installer and Gateway returned incompatible first-administrator health data. This is an internal installer/Gateway contract mismatch. Run `kb-create doctor --platform-root "+strconv.Quote(platformRoot)+"` to capture installation diagnostics, then provide the diagnostic file and output to KB Labs support.", cause)
}

func bootstrapOutcomeFailure(platformRoot string) *diag.Diag {
	return contracts.NewDiag(contracts.CodeApplyFailed, "Gateway reported that first-administrator bootstrap did not complete.",
		"Run `kb-create doctor --platform-root "+strconv.Quote(platformRoot)+"` to inspect Gateway authentication readiness and resolve its reported issue, then rerun `kb-create install`.", nil)
}

func errorsJoinStop(cause error, stop func() error) error {
	if stop == nil {
		return cause
	}
	return fmt.Errorf("%w (host stop: %v)", cause, stop())
}
