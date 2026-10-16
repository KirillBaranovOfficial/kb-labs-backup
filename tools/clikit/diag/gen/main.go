// Command gen generates the shared Go error catalog from the platform source.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"runtime"
	"sort"

	"github.com/kb-labs/clikit/command"
	"github.com/kb-labs/clikit/diag"
)

type entry struct {
	Code                     string `json:"code"`
	HTTPStatus               int    `json:"httpStatus"`
	Area                     string `json:"area"`
	Stage                    string `json:"stage"`
	Severity                 string `json:"severity"`
	Retryable                bool   `json:"retryable"`
	ProcessExitClass         string `json:"processExitClass"`
	Message                  string `json:"message"`
	Hint                     string `json:"hint"`
	HintWithoutCorrelationID string `json:"hintWithoutCorrelationId"`
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
		if item.HTTPStatus == 0 {
			_, _ = fmt.Fprintf(&source, "\t%q: {Code: %q, Area: %q, Stage: %q, Severity: %q, Retryable: %t, ProcessExitClass: ProcessExitClass(%q), Message: %q, Hint: %q%s},\n", item.Code, item.Code, item.Area, item.Stage, item.Severity, item.Retryable, item.ProcessExitClass, item.Message, item.Hint, hintWithoutCorrelationID)
			continue
		}
		_, _ = fmt.Fprintf(&source, "\t%q: {Code: %q, HTTPStatus: %d, Area: %q, Stage: %q, Severity: %q, Retryable: %t, ProcessExitClass: ProcessExitClass(%q), Message: %q, Hint: %q%s},\n", item.Code, item.Code, item.HTTPStatus, item.Area, item.Stage, item.Severity, item.Retryable, item.ProcessExitClass, item.Message, item.Hint, hintWithoutCorrelationID)
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

func validate(entries []entry) error {
	for _, item := range entries {
		if item.ProcessExitClass != "input-refusal" && item.ProcessExitClass != "operation-failure" {
			return fmt.Errorf("catalog entry %s has missing or invalid processExitClass %q", item.Code, item.ProcessExitClass)
		}
	}
	return nil
}

func fail(err error) diag.ExitStatus {
	fmt.Fprintln(os.Stderr, err)
	return diag.ExitCode(diag.New("KB_RUNTIME_UNEXPECTED", "The Go diagnostic catalog could not be generated.", diag.WithCauseAndReason(err.Error()), diag.WithHint("Fix the catalog generator input, then rerun `go generate ./diag`.")))
}
