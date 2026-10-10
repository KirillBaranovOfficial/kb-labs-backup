// Command gen-gateway-bootstrap generates Go auth health values and required
// field lists from the response schema owned by @kb-labs/gateway-contracts.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/format"
	"os"
	"strings"
)

type schema struct {
	Required   []string `json:"required"`
	Properties struct {
		Bootstrap struct {
			Enum []string `json:"enum"`
		} `json:"bootstrap"`
		Issues struct {
			Items struct {
				Required   []string `json:"required"`
				Properties struct {
					Severity struct {
						Enum []string `json:"enum"`
					} `json:"severity"`
				} `json:"properties"`
			} `json:"items"`
		} `json:"issues"`
	} `json:"properties"`
}

func main() {
	if len(os.Args) != 3 {
		fail("usage: gen-gateway-bootstrap <gateway-schema.json> <output.go>")
	}
	data, err := os.ReadFile(os.Args[1])
	if err != nil {
		fail(err.Error())
	}
	var contract schema
	if err := json.Unmarshal(data, &contract); err != nil {
		fail(err.Error())
	}
	if len(contract.Properties.Bootstrap.Enum) == 0 {
		fail("Gateway bootstrap enum is empty")
	}
	if len(contract.Properties.Issues.Items.Properties.Severity.Enum) == 0 {
		fail("Gateway auth health issue severity enum is empty")
	}
	if len(contract.Required) == 0 {
		fail("Gateway auth health required field list is empty")
	}
	if len(contract.Properties.Issues.Items.Required) == 0 {
		fail("Gateway auth health issue required field list is empty")
	}

	var source bytes.Buffer
	_, _ = source.WriteString("// Code generated from services/gateway/contracts/src/auth-health-response.schema.json; DO NOT EDIT.\n\npackage v2cli\n\nimport (\n\t\"encoding/json\"\n\t\"fmt\"\n)\n\n")
	writeGoStringArray(&source, "gatewayAuthHealthResponseRequiredFields", contract.Required)
	writeGoStringArray(&source, "gatewayAuthHealthIssueRequiredFields", contract.Properties.Issues.Items.Required)
	_, _ = source.WriteString("type GatewayBootstrapStatus string\n\nconst (\n")
	for _, status := range contract.Properties.Bootstrap.Enum {
		_, _ = fmt.Fprintf(&source, "\tGatewayBootstrapStatus%s GatewayBootstrapStatus = %q\n", identifier(status), status)
	}
	_, _ = source.WriteString(")\n\nvar gatewayBootstrapStatuses = [...]GatewayBootstrapStatus{\n")
	for _, status := range contract.Properties.Bootstrap.Enum {
		_, _ = fmt.Fprintf(&source, "\tGatewayBootstrapStatus%s,\n", identifier(status))
	}
	_, _ = source.WriteString(`}

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
`)
	_, _ = source.WriteString("\ntype GatewayAuthHealthIssueSeverity string\n\nconst (\n")
	for _, severity := range contract.Properties.Issues.Items.Properties.Severity.Enum {
		_, _ = fmt.Fprintf(&source, "\tGatewayAuthHealthIssueSeverity%s GatewayAuthHealthIssueSeverity = %q\n", identifier(severity), severity)
	}
	_, _ = source.WriteString(")\n\nvar gatewayAuthHealthIssueSeverities = [...]GatewayAuthHealthIssueSeverity{\n")
	for _, severity := range contract.Properties.Issues.Items.Properties.Severity.Enum {
		_, _ = fmt.Fprintf(&source, "\tGatewayAuthHealthIssueSeverity%s,\n", identifier(severity))
	}
	_, _ = source.WriteString(`}

func (severity GatewayAuthHealthIssueSeverity) Valid() bool {
	for _, known := range gatewayAuthHealthIssueSeverities {
		if severity == known {
			return true
		}
	}
	return false
}

func (severity *GatewayAuthHealthIssueSeverity) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	parsed := GatewayAuthHealthIssueSeverity(value)
	if !parsed.Valid() {
		return fmt.Errorf("invalid Gateway auth health issue severity")
	}
	*severity = parsed
	return nil
}
`)
	formatted, err := format.Source(source.Bytes())
	if err != nil {
		fail(err.Error())
	}
	if err := os.WriteFile(os.Args[2], formatted, 0o644); err != nil {
		fail(err.Error())
	}
}

func writeGoStringArray(source *bytes.Buffer, name string, values []string) {
	_, _ = fmt.Fprintf(source, "var %s = [...]string{\n", name)
	for _, value := range values {
		_, _ = fmt.Fprintf(source, "\t%q,\n", value)
	}
	_, _ = source.WriteString("}\n\n")
}

func identifier(value string) string {
	parts := strings.FieldsFunc(value, func(r rune) bool { return r == '-' })
	var out strings.Builder
	for _, part := range parts {
		if part == "" {
			continue
		}
		out.WriteString(strings.ToUpper(part[:1]))
		out.WriteString(part[1:])
	}
	return out.String()
}

func fail(message string) {
	fmt.Fprintln(os.Stderr, message)
	os.Exit(1)
}
