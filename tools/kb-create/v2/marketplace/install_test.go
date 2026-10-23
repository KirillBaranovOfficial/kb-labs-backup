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
		if body.Scope != "platform" || len(body.Specs) != 1 || body.Specs[0] != "@kb-labs/commit" {
			t.Fatalf("Marketplace request body = %#v", body)
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusCreated)
		_, _ = writer.Write([]byte(`{"installed":[{"id":"@kb-labs/commit","version":"2.1.0","primaryKind":"plugin","source":"registry","signatureStatus":"verified"}],"scope":"platform"}`))
	}))
	defer server.Close()

	installed, err := (GatewayInstallClient{HTTP: server.Client()}).Install(context.Background(), server.URL, []string{"@kb-labs/commit"})
	if err != nil {
		t.Fatal(err)
	}
	if len(installed) != 1 || installed[0].ID != "@kb-labs/commit" || installed[0].Source != "registry" || installed[0].Version != "2.1.0" {
		t.Fatalf("Marketplace result = %#v", installed)
	}
}

func TestGatewayInstallClientPreservesAttemptedMarketplaceSource(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = writer.Write([]byte(`{"error":{"message":"Marketplace could not install @kb-labs/commit through registry.","details":{"source":"registry"}}}`))
	}))
	defer server.Close()

	_, err := (GatewayInstallClient{HTTP: server.Client()}).Install(context.Background(), server.URL, []string{"@kb-labs/commit"})
	failure, ok := err.(*InstallFailure)
	if !ok || failure.Identity != "@kb-labs/commit" || failure.Source != "registry" {
		t.Fatalf("Marketplace failure = %#v", err)
	}
}
