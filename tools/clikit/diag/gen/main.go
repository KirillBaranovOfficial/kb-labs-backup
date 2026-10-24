// Command gen generates the shared Go error catalog from the platform source.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"

	"github.com/kb-labs/clikit/command"
	"github.com/kb-labs/clikit/diag"
)

type entry struct {
	Code                      string            `json:"code"`
	HTTPStatus                int               `json:"httpStatus"`
	Area                      string            `json:"area"`
	Stage                     string            `json:"stage"`
	Severity                  string            `json:"severity"`
	Retryable                 bool              `json:"retryable"`
	ProcessExitClass          string            `json:"processExitClass"`
	Message                   string            `json:"message"`
	Hint                      string            `json:"hint"`
	HintWithoutCorrelationID  string            `json:"hintWithoutCorrelationId"`
	RuntimeTemplateParameters map[string]string `json:"runtimeTemplateParameters"`
	StructuredDetailFields    []string          `json:"structuredDetailFields"`
	EnvironmentVariables      map[string]string `json:"environmentVariables"`
}

func main() {
	command.Exit(run())
}

func run() diag.ExitStatus {
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		return fail(fmt.Errorf("locate diagnostic generator source"))
	}
	root, err := filepath.Abs(filepath.Join(filepath.Dir(sourceFile), "../../../.."))
	if err != nil {
		return fail(err)
	}
	catalogPath := filepath.Join(root, "core", "platform", "src", "error-envelope", "errors.catalog.json")
	content, err := os.ReadFile(catalogPath)
	if err != nil {
		return fail(err)
	}
	var catalog struct {
		Codes []entry `json:"codes"`
	}
	if err := json.Unmarshal(content, &catalog); err != nil {
		return fail(err)
	}
	if err := validate(catalog.Codes); err != nil {
		return fail(err)
	}
	sort.Slice(catalog.Codes, func(i, j int) bool { return catalog.Codes[i].Code < catalog.Codes[j].Code })
	var source bytes.Buffer
	_, _ = source.WriteString("// Code generated from core/platform/src/error-envelope/errors.catalog.json; DO NOT EDIT.\n\npackage diag\n\nvar catalogEntries = map[string]CatalogEntry{\n")
	for _, item := range catalog.Codes {
		hintWithoutCorrelationID := ""
		if item.HintWithoutCorrelationID != "" {
			hintWithoutCorrelationID = fmt.Sprintf(", HintWithoutCorrelationID: %q", item.HintWithoutCorrelationID)
		}
		environmentVariables := ""
		if len(item.EnvironmentVariables) > 0 {
			environmentVariables = ", EnvironmentVariables: " + stringMapLiteral(item.EnvironmentVariables)
		}
		_, _ = fmt.Fprintf(&source, "\t%q: {Code: %q, HTTPStatus: %d, Area: %q, Stage: %q, Severity: %q, Retryable: %t, ProcessExitClass: ProcessExitClass(%q), Message: %q, Hint: %q%s, TemplateParameters: %s, RequiredTemplateParameters: %s, StructuredDetailFields: %s, RuntimeTemplateParameters: %s%s},\n", item.Code, item.Code, item.HTTPStatus, item.Area, item.Stage, item.Severity, item.Retryable, item.ProcessExitClass, item.Message, item.Hint, hintWithoutCorrelationID, stringSliceLiteral(templateParameters(item)), stringSliceLiteral(requiredTemplateParameters(item)), stringSliceLiteral(item.StructuredDetailFields), stringMapLiteral(item.RuntimeTemplateParameters), environmentVariables)
	}
	_, _ = source.WriteString("}\n")
	formatted, err := format.Source(source.Bytes())
	if err != nil {
		return fail(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(sourceFile), "..", "catalog_gen.go"), formatted, 0o644); err != nil {
		return fail(err)
	}
	return diag.SuccessStatus()
}

var templateParameterPattern = regexp.MustCompile(`\{([A-Za-z0-9_]+)\}`)

func templateParameters(item entry) []string {
	seen := make(map[string]struct{})
	var names []string
	for _, template := range []string{item.Message, item.Hint, item.HintWithoutCorrelationID} {
		for _, match := range templateParameterPattern.FindAllStringSubmatch(template, -1) {
			if _, exists := seen[match[1]]; exists {
				continue
			}
			seen[match[1]] = struct{}{}
			names = append(names, match[1])
		}
	}
	sort.Strings(names)
	return names
}

func requiredTemplateParameters(item entry) []string {
	var names []string
	for _, name := range templateParameters(item) {
		if item.RuntimeTemplateParameters[name] != "optional" {
			names = append(names, name)
		}
	}
	return names
}

func stringSliceLiteral(values []string) string {
	if len(values) == 0 {
		return "nil"
	}
	parts := make([]string, len(values))
	for index, value := range values {
		parts[index] = fmt.Sprintf("%q", value)
	}
	return "[]string{" + strings.Join(parts, ", ") + "}"
}

func stringMapLiteral(values map[string]string) string {
	if len(values) == 0 {
		return "nil"
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for index, key := range keys {
		parts[index] = fmt.Sprintf("%q: %q", key, values[key])
	}
	return "map[string]string{" + strings.Join(parts, ", ") + "}"
}

func validate(entries []entry) error {

	for _, item := range entries {
		if item.HTTPStatus < 100 || item.HTTPStatus > 599 {
			return fmt.Errorf("catalog entry %s has missing or invalid httpStatus %d", item.Code, item.HTTPStatus)
		}
		if item.ProcessExitClass != "input-refusal" && item.ProcessExitClass != "operation-failure" {
			return fmt.Errorf("catalog entry %s has missing or invalid processExitClass %q", item.Code, item.ProcessExitClass)
		}
		names := templateParameters(item)
		used := make(map[string]struct{}, len(names))
		for _, name := range names {
			used[name] = struct{}{}
		}
		for name, requirement := range item.RuntimeTemplateParameters {
			if _, exists := used[name]; !exists {
				return fmt.Errorf("catalog entry %s declares unused runtime template parameter %s", item.Code, name)
			}
			if requirement != "required" && requirement != "optional" {
				return fmt.Errorf("catalog entry %s has invalid runtime template parameter requirement %q", item.Code, requirement)
			}
		}
		structured := make(map[string]struct{}, len(item.StructuredDetailFields))
		for _, name := range item.StructuredDetailFields {
			if strings.TrimSpace(name) == "" {
				return fmt.Errorf("catalog entry %s declares an empty structured detail field", item.Code)
			}
			if _, exists := structured[name]; exists {
				return fmt.Errorf("catalog entry %s declares duplicate structured detail field %s", item.Code, name)
			}
			structured[name] = struct{}{}
		}
	}
	return nil
}

func fail(err error) diag.ExitStatus {
	fmt.Fprintln(os.Stderr, err)
	return diag.ExitCode(diag.New("KB_RUNTIME_UNEXPECTED", "The Go diagnostic catalog could not be generated.", diag.WithCauseAndReason(err.Error()), diag.WithHint("Fix the catalog generator input, then rerun `go generate ./diag`.")))
}
