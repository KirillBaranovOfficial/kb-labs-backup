// Code generated from services/gateway/contracts/src/auth-health-response.schema.json; DO NOT EDIT.

package v2cli

import (
	"encoding/json"
	"fmt"
)

type GatewayBootstrapStatus string

const (
	GatewayBootstrapStatusNotConfigured   GatewayBootstrapStatus = "not-configured"
	GatewayBootstrapStatusProvisioned     GatewayBootstrapStatus = "provisioned"
	GatewayBootstrapStatusExistsActive    GatewayBootstrapStatus = "exists-active"
	GatewayBootstrapStatusExistsNonActive GatewayBootstrapStatus = "exists-non-active"
	GatewayBootstrapStatusFailed          GatewayBootstrapStatus = "failed"
)

var gatewayBootstrapStatuses = [...]GatewayBootstrapStatus{
	GatewayBootstrapStatusNotConfigured,
	GatewayBootstrapStatusProvisioned,
	GatewayBootstrapStatusExistsActive,
	GatewayBootstrapStatusExistsNonActive,
	GatewayBootstrapStatusFailed,
}

func (status GatewayBootstrapStatus) Valid() bool {
	for _, known := range gatewayBootstrapStatuses {
		if status == known {
			return true
		}
	}
	return false
}

func (status *GatewayBootstrapStatus) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	parsed := GatewayBootstrapStatus(value)
	if !parsed.Valid() {
		return fmt.Errorf("invalid Gateway bootstrap status")
	}
	*status = parsed
	return nil
}
