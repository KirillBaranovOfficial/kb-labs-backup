package v2cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/kb-labs/clikit/diag"
	"github.com/kb-labs/create/v2/contracts"
	"github.com/kb-labs/create/v2/secrets"
)

//go:generate go run ../gen-gateway-bootstrap ../../../../../services/gateway/contracts/src/auth-health-response.schema.json gateway_bootstrap_gen.go

type gatewayAuthHealthIssue struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
	Hint     string `json:"hint"`
}

type gatewayAuthHealthResponse struct {
	OK           *bool                     `json:"ok"`
	AuthEnabled  *bool                     `json:"authEnabled"`
	TenantID     *string                   `json:"tenantId"`
	ActiveAdmins *int                      `json:"activeAdmins"`
	Bootstrap    GatewayBootstrapStatus    `json:"bootstrap"`
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
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("Gateway did not report first-admin bootstrap status")
	}
	var readiness gatewayAuthHealthResponse
	decoder := json.NewDecoder(response.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&readiness); err != nil {
		return bootstrapContractFailure("Gateway returned an invalid first-administrator health response.", err)
	}
	if readiness.OK == nil || readiness.AuthEnabled == nil || readiness.TenantID == nil || readiness.ActiveAdmins == nil || readiness.Issues == nil {
		return bootstrapContractFailure("Gateway returned an incomplete first-administrator health response.", nil)
	}
	if *readiness.ActiveAdmins < 0 {
		return bootstrapContractFailure("Gateway returned an invalid first-administrator health response.", nil)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return bootstrapContractFailure("Gateway returned more than one first-administrator health response.", err)
	}
	switch readiness.Bootstrap {
	case GatewayBootstrapStatusProvisioned, GatewayBootstrapStatusExistsActive, GatewayBootstrapStatusExistsNonActive:
		return store.Put(secrets.BootstrapCompleted, string(readiness.Bootstrap))
	default:
		return bootstrapContractFailure("Gateway reported that first-administrator bootstrap did not complete.", nil)
	}
}

func bootstrapContractFailure(message string, cause error) *diag.Diag {
	return contracts.NewDiag(contracts.CodeApplyFailed, message,
		"Update the Gateway to a version with the supported auth health contract, then rerun `kb-create install`.", cause)
}

func errorsJoinStop(cause error, stop func() error) error {
	if stop == nil {
		return cause
	}
	return fmt.Errorf("%w (host stop: %v)", cause, stop())
}
