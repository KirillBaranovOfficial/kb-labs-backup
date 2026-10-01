// Package doctor compares installed manifest requirements with effective
// generated configuration. It reports a structured repair plan and never
// serializes secret values.
package doctor

import (
	"encoding/json"
	"sort"

	"github.com/kb-labs/create/v2/contracts"
)

const CodeAdapterCredentialsMissing = "KB_ADAPTER_CREDENTIALS_MISSING"

const (
	SeverityError   = "error"
	SeverityWarning = "warning"
)

type Manifest struct {
	ID           string        `json:"id"`
	Requirements []Requirement `json:"requirements"`
}
type Requirement struct {
	ID       string   `json:"id,omitempty"`
	Path     string   `json:"path"`
	Secret   bool     `json:"secret"`
	Generate bool     `json:"generate,omitempty"`
	Required bool     `json:"required"`
	Optional bool     `json:"optional,omitempty"`
	Hint     string   `json:"hint"`
	Action   string   `json:"action,omitempty"`
	Env      string   `json:"env,omitempty"`
	Services []string `json:"services,omitempty"`
	// Default is present only when the manifest explicitly declares a safe,
	// non-secret value. Its absence means doctor must request input instead of
	// inventing a configuration choice.
	Default json.RawMessage `json:"default,omitempty"`
}
type Finding struct {
	Code      string          `json:"code"`
	Severity  string          `json:"severity"`
	Component string          `json:"component"`
	Path      string          `json:"path"`
	Hint      string          `json:"hint"`
	Action    string          `json:"action,omitempty"`
	SafeFix   bool            `json:"safeFix"`
	Default   json.RawMessage `json:"default,omitempty"`
}

type RepairPlan struct {
	SafeDefaults  []Finding `json:"safeDefaults"`
	RequiredInput []Finding `json:"requiredInput"`
}

// Diagnose accepts values only as presence/validation state. Callers must not
// pass the actual secret material through this boundary.
func Diagnose(manifests []Manifest, configured map[string]bool) []Finding {
	findings := make([]Finding, 0)
	for _, manifest := range manifests {
		for _, requirement := range manifest.Requirements {
			key := requirement.Path
			if requirement.Secret {
				key = requirement.ID
			}
			if !requirement.Required || configured[key] {
				continue
			}
			code := contracts.CodeConfigRequired
			severity := SeverityError
			if requirement.Secret {
				code = CodeAdapterCredentialsMissing
			}
			if requirement.Secret && requirement.Optional {
				severity = SeverityWarning
			}
			safeFix := !requirement.Secret && len(requirement.Default) != 0
			findings = append(findings, Finding{Code: code, Severity: severity, Component: manifest.ID, Path: requirement.Path, Hint: requirement.Hint, Action: requirement.Action, SafeFix: safeFix, Default: append(json.RawMessage(nil), requirement.Default...)})
		}
	}
	sort.Slice(findings, func(i, j int) bool {
		if findings[i].Component == findings[j].Component {
			return findings[i].Path < findings[j].Path
		}
		return findings[i].Component < findings[j].Component
	})
	return findings
}

// PlanRepair turns findings into an explicit recovery contract. Safe defaults
// can be rendered automatically; error findings without a safe default remain
// required user/agent input and are never guessed by doctor --fix.
func PlanRepair(findings []Finding) RepairPlan {
	result := RepairPlan{}
	for _, finding := range findings {
		if finding.Severity == SeverityWarning {
			continue
		}
		if finding.SafeFix {
			result.SafeDefaults = append(result.SafeDefaults, finding)
		} else {
			result.RequiredInput = append(result.RequiredInput, finding)
		}
	}
	return result
}

func HasErrors(findings []Finding) bool {
	for _, finding := range findings {
		if finding.Severity != SeverityWarning {
			return true
		}
	}
	return false
}

// ApplySafe executes only the fixes explicitly marked safe. The callback
// receives a config path, never a secret value.
func ApplySafe(plan RepairPlan, apply func(Finding) error) error {
	for _, finding := range plan.SafeDefaults {
		if err := apply(finding); err != nil {
			return err
		}
	}
	return nil
}
