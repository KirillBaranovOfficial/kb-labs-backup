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
)

type entry struct {
	Code            string `json:"code"`
	HTTPStatus      int    `json:"httpStatus"`
	Area            string `json:"area"`
	Stage           string `json:"stage"`
	Severity        string `json:"severity"`
	Retryable       bool   `json:"retryable"`
	ProcessExitCode int    `json:"processExitCode"`
	Message         string `json:"message"`
	Hint            string `json:"hint"`
}

func main() {
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		fail(fmt.Errorf("locate diagnostic generator source"))
	}
	root, err := filepath.Abs(filepath.Join(filepath.Dir(sourceFile), "../../../.."))
	if err != nil {
		fail(err)
	}
	catalogPath := filepath.Join(root, "core", "platform", "src", "error-envelope", "errors.catalog.json")
	content, err := os.ReadFile(catalogPath)
	if err != nil {
		fail(err)
	}
	var catalog struct {
		Codes []entry `json:"codes"`
	}
	if err := json.Unmarshal(content, &catalog); err != nil {
		fail(err)
	}
	sort.Slice(catalog.Codes, func(i, j int) bool { return catalog.Codes[i].Code < catalog.Codes[j].Code })
	var source bytes.Buffer
	_, _ = source.WriteString("// Code generated from core/platform/src/error-envelope/errors.catalog.json; DO NOT EDIT.\n\npackage diag\n\nvar catalogEntries = map[string]CatalogEntry{\n")
	for _, item := range catalog.Codes {
		processExitCode := ""
		if item.ProcessExitCode != 0 {
			processExitCode = fmt.Sprintf("ProcessExitCode: %d, ", item.ProcessExitCode)
		}
		if item.HTTPStatus == 0 {
			_, _ = fmt.Fprintf(&source, "\t%q: {Code: %q, Area: %q, Stage: %q, Severity: %q, Retryable: %t, %sMessage: %q, Hint: %q},\n", item.Code, item.Code, item.Area, item.Stage, item.Severity, item.Retryable, processExitCode, item.Message, item.Hint)
			continue
		}
		_, _ = fmt.Fprintf(&source, "\t%q: {Code: %q, HTTPStatus: %d, Area: %q, Stage: %q, Severity: %q, Retryable: %t, %sMessage: %q, Hint: %q},\n", item.Code, item.Code, item.HTTPStatus, item.Area, item.Stage, item.Severity, item.Retryable, processExitCode, item.Message, item.Hint)
	}
	_, _ = source.WriteString("}\n")
	formatted, err := format.Source(source.Bytes())
	if err != nil {
		fail(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(sourceFile), "..", "catalog_gen.go"), formatted, 0o644); err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
