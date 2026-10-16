package contracts

import (
	"errors"
	"fmt"
	"strings"

	"github.com/kb-labs/clikit/diag"
)

type ProvenanceField string

// ObservedProvenanceValue carries presence separately from its bytes. An
// empty present value is therefore distinct from a field that was not found.
type ObservedProvenanceValue struct {
	value    string
	location string
	present  bool
}

func PresentProvenanceValue(value string) ObservedProvenanceValue {
	return ObservedProvenanceValue{value: value, present: true}
}

func MissingProvenanceValue(location string) ObservedProvenanceValue {
	return ObservedProvenanceValue{location: location}
}

func (value ObservedProvenanceValue) Present() bool { return value.present }

func (value ObservedProvenanceValue) Value() string { return value.value }

func (value ObservedProvenanceValue) Display() string {
	if !value.present {
		return "<missing in " + value.location + ">"
	}
	if value.value == "" {
		return "<empty>"
	}
	return value.value
}

const (
	ProvenanceIdentityField  ProvenanceField = "identity"
	ProvenanceIntegrityField ProvenanceField = "integrity"
	ProvenanceDigestField    ProvenanceField = "digest"
)

func ProvenanceMismatch(packageName string, field ProvenanceField, expected string, actual ObservedProvenanceValue, packagePath, digestVersion string) error {
	if packageName == "" {
		packageName = "<unknown package>"
	}
	entry, _ := diag.LookupCatalog(CodeProvenanceMismatch)
	actualValue := actual.Display()
	replacements := strings.NewReplacer(
		"{package}", packageName,
		"{field}", string(field),
		"{expected}", expected,
		"{actual}", actualValue,
		"{packagePath}", packagePath,
		"{digestVersion}", digestVersion,
	)
	message := replacements.Replace(entry.Message)
	hint := replacements.Replace(entry.Hint)
	reason := fmt.Sprintf("package %s differs at %s: expected %s; actual %s; path %s; digest version %s",
		packageName, field, expected, actualValue, packagePath, digestVersion)
	meta := map[string]any{
		"package": packageName, "field": field, "expected": expected,
		"actual": actualValue, "actualPresent": actual.Present(), "packagePath": packagePath,
		"digestVersion": digestVersion,
	}
	return diag.Wrap(errors.New(reason), entry.Code, message,
		diag.WithCauseAndReason(reason), diag.WithHint(hint), diag.WithMeta(meta))
}
