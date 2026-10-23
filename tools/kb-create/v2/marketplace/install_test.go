package marketplace

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGatewayInstallClientUsesPlatformMarketplaceOperation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != installEndpoint {
			t.Fatalf("Marketplace request = %s %s", request.Method, request.URL.Path)
		}
		var body struct {
			Specs []string `json:"specs"`
			Scope string   `json:"scope"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Scope != "platform" || len(body.Specs) != 1 || body.Specs[0] != "@kb-labs/commit-entry" {
			t.Fatalf("Marketplace request body = %#v", body)
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusCreated)
		_, _ = writer.Write([]byte(`{"installed":[{"spec":"@kb-labs/commit-entry","id":"@kb-labs/commit","version":"2.1.0","primaryKind":"plugin","source":"registry","signatureStatus":"verified"}],"scope":"platform"}`))
	}))
	defer server.Close()

	installed, err := (GatewayInstallClient{HTTP: server.Client()}).Install(context.Background(), server.URL, []string{"@kb-labs/commit-entry"})
	if err != nil {
		t.Fatal(err)
	}
	if len(installed) != 1 || installed[0].Spec != "@kb-labs/commit-entry" || installed[0].ID != "@kb-labs/commit" || installed[0].Source != "registry" || installed[0].Version != "2.1.0" {
		t.Fatalf("Marketplace result = %#v", installed)
	}
}

func TestGatewayInstallClientPreservesAttemptedMarketplaceSource(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = writer.Write([]byte(`{"error":{"code":"KB_PLUGIN_NOT_FOUND","message":"Requested package spec was not published by the configured sources.","cause":"registry: not-found-unowned; npm: not-found-unowned","details":{"source":"configured Marketplace sources"}}}`))
	}))
	defer server.Close()

	_, err := (GatewayInstallClient{HTTP: server.Client()}).Install(context.Background(), server.URL, []string{"@kb-labs/commit-entry"})
	failure, ok := err.(*InstallFailure)
	if !ok || failure.Spec != "@kb-labs/commit-entry" || failure.Code != "KB_PLUGIN_NOT_FOUND" ||
		failure.Source != "configured Marketplace sources" || failure.Message != "Requested package spec was not published by the configured sources." ||
		failure.Cause == nil || failure.Cause.Error() != "registry: not-found-unowned; npm: not-found-unowned" {
		t.Fatalf("Marketplace failure = %#v", err)
	}
}

func TestGatewayInstallClientSelectsTheFailedSpecFromMarketplaceDetails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusNotFound)
		_, _ = writer.Write([]byte(`{"error":{"code":"KB_PLUGIN_NOT_FOUND","message":"Requested package spec was not published.","cause":"registry: not-found-unowned; npm: not-found-unowned","details":{"plugin":"@kb-labs/review-entry"}}}`))
	}))
	defer server.Close()

	_, err := (GatewayInstallClient{HTTP: server.Client()}).Install(context.Background(), server.URL,
		[]string{"@kb-labs/commit-entry", "@kb-labs/review-entry"})
	failure, ok := err.(*InstallFailure)
	if !ok || failure.Spec != "@kb-labs/review-entry" {
		t.Fatalf("Marketplace failure spec = %#v, want the one identified by error details", err)
	}
}

func TestGatewayInstallClientRejectsResultThatDoesNotEchoRequestedSpec(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"installed":[{"id":"@kb-labs/commit","version":"2.1.0","primaryKind":"plugin","source":"npm","signatureStatus":"unsigned"}],"scope":"platform"}`))
	}))
	defer server.Close()

	_, err := (GatewayInstallClient{HTTP: server.Client()}).Install(context.Background(), server.URL, []string{"@kb-labs/commit-entry"})
	if err == nil || err.Error() != `Marketplace returned install spec "" at position 0, want "@kb-labs/commit-entry"` {
		t.Fatalf("Marketplace missing-spec response error = %v", err)
	}
}
