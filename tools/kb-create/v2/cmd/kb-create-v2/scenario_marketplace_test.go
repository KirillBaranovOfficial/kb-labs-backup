package v2cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/kb-labs/create/v2/contracts"
	"github.com/kb-labs/create/v2/marketplace"
	"github.com/kb-labs/create/v2/receipt"
)

type recordingMarketplaceInstaller struct{}

func (recordingMarketplaceInstaller) Install(_ context.Context, _ string, identities []string) ([]contracts.MarketplaceInstallRecord, error) {
	installs := make([]contracts.MarketplaceInstallRecord, 0, len(identities))
	for _, identity := range identities {
		installs = append(installs, contracts.MarketplaceInstallRecord{
			ID: identity, Version: "2.1.0", PrimaryKind: "plugin", Source: "registry", SignatureStatus: "verified",
		})
	}
	return installs, nil
}

func TestScenarioMarketplaceFailureKeepsVerifiedInstallReceiptAndHost(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/health" {
			writer.WriteHeader(http.StatusOK)
			return
		}
		if request.URL.Path != "/api/v1/marketplace/packages/install" || request.Method != http.MethodPost {
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
			t.Fatalf("Marketplace body = %#v", body)
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = writer.Write([]byte(`{"error":{"message":"Marketplace could not install @kb-labs/commit through registry.","details":{"source":"registry"}}}`))
	}))
	defer server.Close()

	root := t.TempDir()
	installed := contracts.InstallReceipt{
		Schema: contracts.ReceiptSchema, ID: "verified-receipt",
		Plan: contracts.ResolvedInstallPlan{Request: contracts.InstallRequest{
			PlatformRoot: root, ScenarioID: "commit", MarketplacePlugins: []string{"@kb-labs/commit"},
		}},
		Verification: contracts.Verification{HostHealthURL: server.URL + "/health"},
	}
	if err := receipt.Write(root, installed); err != nil {
		t.Fatal(err)
	}

	failure := installMarketplaceScenarioPlugins(context.Background(), root, &installed,
		marketplace.GatewayInstallClient{HTTP: server.Client()})
	if failure == nil || failure.Code != contracts.CodeMarketplaceInstallFailed || failure.Meta["source"] != "registry" {
		t.Fatalf("Marketplace failure = %#v", failure)
	}
	if !strings.Contains(failure.Hint, "kb marketplace install @kb-labs/commit --scope platform") {
		t.Fatalf("Marketplace retry hint = %q", failure.Hint)
	}
	readBack, err := receipt.Read(root)
	if err != nil || readBack.ID != installed.ID || readBack.Plan.Request.ScenarioID != "commit" {
		t.Fatalf("installed receipt after Marketplace failure = %#v, %v", readBack, err)
	}
	health, err := server.Client().Get(server.URL + "/health")
	if err != nil || health.StatusCode != http.StatusOK {
		t.Fatalf("host health after Marketplace failure = %v, %v", health, err)
	}
	_ = health.Body.Close()
	if _, err := os.Stat(root); err != nil {
		t.Fatalf("installed platform root disappeared: %v", err)
	}
}

func TestMarketplaceInstallFailureDiagnosticContainsCataloguedIdentitySourceAndRetry(t *testing.T) {
	diagnostic := contracts.MarketplaceInstallDiagnostic("@kb-labs/commit", "registry", fmt.Errorf("identity was not published"))
	if diagnostic.Code != contracts.CodeMarketplaceInstallFailed || diagnostic.Meta["source"] != "registry" ||
		!strings.Contains(diagnostic.Message, "@kb-labs/commit") || !strings.Contains(diagnostic.Message, "registry") ||
		!strings.Contains(diagnostic.Hint, "kb marketplace install @kb-labs/commit --scope platform") {
		t.Fatalf("Marketplace install diagnostic = %#v", diagnostic)
	}
}
