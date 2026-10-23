package marketplace

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/kb-labs/create/v2/contracts"
)

const installEndpoint = "/api/v1/marketplace/packages/install"

type InstallClient interface {
	Install(ctx context.Context, gatewayOrigin string, identities []string) ([]contracts.MarketplaceInstallRecord, error)
}

type GatewayInstallClient struct {
	HTTP *http.Client
}

type InstallFailure struct {
	Identity string
	Source   string
	Cause    error
}

func (failure *InstallFailure) Error() string {
	return fmt.Sprintf("Marketplace install for %s failed through %s: %v", failure.Identity, failure.Source, failure.Cause)
}

func (failure *InstallFailure) Unwrap() error { return failure.Cause }

func (client GatewayInstallClient) Install(ctx context.Context, gatewayOrigin string, identities []string) ([]contracts.MarketplaceInstallRecord, error) {
	if gatewayOrigin == "" || len(identities) == 0 {
		return nil, fmt.Errorf("Gateway origin and Marketplace identities are required")
	}
	body, err := json.Marshal(struct {
		Specs []string `json:"specs"`
		Scope string   `json:"scope"`
	}{Specs: identities, Scope: "platform"})
	if err != nil {
		return nil, fmt.Errorf("encode Marketplace install request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(gatewayOrigin, "/")+installEndpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create Marketplace install request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	httpClient := client.HTTP
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	response, err := httpClient.Do(request)
	if err != nil {
		return nil, &InstallFailure{Identity: strings.Join(identities, ", "), Source: "configured Marketplace sources", Cause: err}
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, fmt.Errorf("read Marketplace install response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var failure struct {
			Error struct {
				Message string         `json:"message"`
				Cause   string         `json:"cause"`
				Details map[string]any `json:"details"`
				Meta    map[string]any `json:"meta"`
			} `json:"error"`
		}
		if err := json.Unmarshal(data, &failure); err != nil {
			return nil, fmt.Errorf("Marketplace install returned HTTP %d with an invalid error envelope", response.StatusCode)
		}
		identity := strings.Join(identities, ", ")
		if len(identities) == 1 {
			identity = identities[0]
		}
		source := ""
		for _, details := range []map[string]any{failure.Error.Details, failure.Error.Meta} {
			if value, ok := details["source"].(string); ok {
				source = value
				break
			}
		}
		if source == "" {
			source = "configured Marketplace sources"
		}
		message := failure.Error.Message
		if message == "" {
			message = fmt.Sprintf("Marketplace returned HTTP %d", response.StatusCode)
		}
		if failure.Error.Cause != "" {
			message += ": " + failure.Error.Cause
		}
		return nil, &InstallFailure{Identity: identity, Source: source, Cause: fmt.Errorf("%s", message)}
	}
	var result struct {
		Installed []contracts.MarketplaceInstallRecord `json:"installed"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("decode Marketplace install response: %w", err)
	}
	if len(result.Installed) != len(identities) {
		return nil, fmt.Errorf("Marketplace returned %d installed records for %d requested identities", len(result.Installed), len(identities))
	}
	return result.Installed, nil
}
