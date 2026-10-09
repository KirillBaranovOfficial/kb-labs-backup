package v2cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/kb-labs/create/v2/contracts"
	"github.com/kb-labs/create/v2/secrets"
)

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
	var readiness struct {
		Bootstrap string `json:"bootstrap"`
	}
	if err := json.NewDecoder(response.Body).Decode(&readiness); err != nil {
		return fmt.Errorf("Gateway first-admin bootstrap status was invalid")
	}
	switch readiness.Bootstrap {
	case "provisioned", "exists-active", "exists-non-active":
		return store.Put(secrets.BootstrapCompleted, readiness.Bootstrap)
	default:
		return fmt.Errorf("Gateway first-admin bootstrap did not complete")
	}
}

func errorsJoinStop(cause error, stop func() error) error {
	if stop == nil {
		return cause
	}
	return fmt.Errorf("%w (host stop: %v)", cause, stop())
}
