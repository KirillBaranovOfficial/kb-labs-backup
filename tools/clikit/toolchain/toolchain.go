// Package toolchain defines the shared runtime contract for KB Labs launchers.
// It is intentionally independent of a particular CLI so human, agent and CI
// frontends report the same fail-fast diagnostics.
package toolchain

import (
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/kb-labs/clikit/diag"
)

//go:generate node generate-node-policy.mjs

const (
	SupportedPnpmMajor = 11
	SupportedPnpm      = "11.4.0"
)

type Status struct{ NodeVersion, PnpmVersion string }

type ValidationError struct {
	Tool, Detected, Requirement, Remediation string
}

func (e *ValidationError) Error() string {
	if e.Detected == "" {
		return fmt.Sprintf("%s was not found; KB Labs requires %s. %s", e.Tool, e.Requirement, e.Remediation)
	}
	return fmt.Sprintf("%s %s is unsupported; KB Labs requires %s. %s", e.Tool, e.Detected, e.Requirement, e.Remediation)
}

func NodeRemediation() string {
	entry, ok := diag.LookupCatalog("KB_NODE_VERSION_UNSUPPORTED")
	if !ok {
		panic("Node.js unsupported diagnostic is missing from the platform catalog")
	}
	return diag.RenderTemplate(entry.Hint, map[string]string{"requiredMajor": strconv.Itoa(SupportedNodeMajor)})
}
func PnpmRemediation() string { return fmt.Sprintf("Install pnpm@%s and retry", SupportedPnpm) }

// NodeMissingDiagnostic reports that Node.js could not be executed at all.
func NodeMissingDiagnostic() *diag.Diag {
	entry, ok := diag.LookupCatalog("KB_INSTALL_NODE_MISSING")
	if !ok {
		panic("Node.js missing diagnostic is missing from the platform catalog")
	}
	values := map[string]string{"requirement": "Node.js"}
	return diag.New(entry.Code, diag.RenderTemplate(entry.Message, values),
		diag.WithHint(diag.RenderTemplate(entry.Hint, values)), diag.WithMeta(map[string]any{"requirement": values["requirement"]}))
}

// NodeCheckDiagnostic preserves a structured preflight failure or reports that
// no Node.js executable could be run.
func NodeCheckDiagnostic(err error) *diag.Diag {
	var failure *diag.Diag
	if errors.As(err, &failure) {
		return failure
	}
	return NodeMissingDiagnostic()
}

func Version(binary string) (string, error) {
	if strings.TrimSpace(binary) == "" {
		return "", nil
	}
	out, err := exec.Command(binary, "--version").Output() // #nosec G204 -- binary is selected by the launcher
	if err != nil {
		return "", fmt.Errorf("read version from %s: %w", binary, err)
	}
	return strings.TrimSpace(string(out)), nil
}

func ValidateNode(version string) error {
	major, err := major(version)
	if err != nil || major != SupportedNodeMajor {
		entry, ok := diag.LookupCatalog("KB_NODE_VERSION_UNSUPPORTED")
		if !ok {
			panic("Node.js unsupported diagnostic is missing from the platform catalog")
		}
		actualVersion := strings.TrimPrefix(strings.TrimSpace(version), "v")
		values := map[string]string{
			"requiredMajor": strconv.Itoa(SupportedNodeMajor),
			"actualVersion": actualVersion,
		}
		return diag.New(entry.Code, diag.RenderTemplate(entry.Message, values),
			diag.WithHint(diag.RenderTemplate(entry.Hint, values)), diag.WithMeta(map[string]any{
				"requiredMajor": values["requiredMajor"], "actualVersion": values["actualVersion"],
			}))
	}
	return nil
}

func ValidatePnpm(version string) error {
	major, err := major(version)
	if err != nil || major != SupportedPnpmMajor {
		return &ValidationError{Tool: "pnpm", Detected: version, Requirement: fmt.Sprintf("pnpm %d.x", SupportedPnpmMajor), Remediation: PnpmRemediation()}
	}
	return nil
}

func Validate(status Status, requirePnpm bool) error {
	if err := ValidateNode(status.NodeVersion); err != nil {
		return err
	}
	if requirePnpm {
		return ValidatePnpm(status.PnpmVersion)
	}
	return nil
}

func major(version string) (int, error) {
	part := strings.TrimPrefix(strings.TrimSpace(version), "v")
	if i := strings.IndexByte(part, '.'); i >= 0 {
		part = part[:i]
	}
	value, err := strconv.Atoi(part)
	if err != nil {
		return 0, fmt.Errorf("invalid semantic version")
	}
	return value, nil
}
