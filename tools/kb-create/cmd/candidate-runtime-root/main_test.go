package main

import (
	"bytes"
	"log"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRegistryReportsMetadataEncodingFailure(t *testing.T) {
	var output bytes.Buffer
	previousWriter := log.Writer()
	log.SetOutput(&output)
	t.Cleanup(func() { log.SetOutput(previousWriter) })

	registry := &packageRegistry{packages: map[string]map[string]candidatePackage{
		"@kb-labs/broken": {
			"1.0.0": {Manifest: map[string]any{"unsupported": make(chan int)}},
		},
	}}
	response := httptest.NewRecorder()
	request := httptest.NewRequest("GET", "/@kb-labs/broken", nil)
	registry.ServeHTTP(response, request)

	if response.Code != 500 {
		t.Fatalf("expected HTTP 500 for unencodable package metadata, got %d", response.Code)
	}
	if !strings.Contains(output.String(), "encode candidate package metadata") {
		t.Fatalf("expected encoding failure in server log, got %q", output.String())
	}
}
