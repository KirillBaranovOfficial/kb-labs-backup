package v2cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/kb-labs/clikit/diag"
	"github.com/kb-labs/create/v2/contracts"
	"github.com/kb-labs/create/v2/host"
	"github.com/kb-labs/create/v2/receipt"
	"github.com/kb-labs/create/v2/runtime"
	"github.com/kb-labs/create/v2/secrets"
	"github.com/spf13/cobra"
)

// case: S-100.1
func TestFirstAdminCredentialIsShownOnceAndNeverPersisted(t *testing.T) {
	const password = "generated-password-that-must-not-leak"
	credential := firstAdminCredential{Email: "admin@example.test", Password: password, ShownOnce: true}
	result := applyCommandResult{
		OK: true, Operation: "install", Receipt: contracts.InstallReceipt{
			Plan: contracts.ResolvedInstallPlan{Request: contracts.InstallRequest{Values: map[string]string{
				"gateway.access.mode": `"secured"`, "gateway.bootstrap.adminEmail": `"admin@example.test"`,
			}}},
		}, FirstAdmin: &credential,
	}
	jsonOutput, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(jsonOutput, &decoded); err != nil {
		t.Fatal(err)
	}
	admin, ok := decoded["firstAdmin"].(map[string]any)
	if !ok || admin["email"] != credential.Email || admin["password"] != password || admin["shownOnce"] != true {
		t.Fatalf("explicit one-time credential result = %#v", decoded["firstAdmin"])
	}

	human := renderApply(result)
	if strings.Count(human, password) != 1 || strings.Count(human, credential.Email) != 1 {
		t.Fatalf("human output must show the credential exactly once: %q", human)
	}
}

type bootstrapTestHost struct {
	address string
	stop    func() error
}

func (starter *bootstrapTestHost) Start(_ context.Context, spec contracts.HostSpec) (func() error, error) {
	listener, err := net.Listen("tcp", starter.address)
	if err != nil {
		return nil, err
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/health/auth" {
			_, _ = fmt.Fprint(w, `{"ok":true,"authEnabled":true,"tenantId":"default","activeAdmins":1,"bootstrap":"provisioned","issues":[]}`)
			return
		}
		_, _ = fmt.Fprint(w, `{"ok":true}`)
	}))
	server.Listener = listener
	server.Start()
	starter.stop = func() error { server.Close(); return nil }
	return starter.stop, nil
}

func TestGatewayBootstrapContractUsesSharedSchemaAndFixture(t *testing.T) {
	root := filepath.Join("..", "..", "..", "..", "..", "services", "gateway", "contracts", "src")
	schemaBytes, err := os.ReadFile(filepath.Join(root, "auth-health-response.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Properties struct {
			Bootstrap struct {
				Enum []string `json:"enum"`
			} `json:"bootstrap"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(schemaBytes, &schema); err != nil {
		t.Fatal(err)
	}
	want := make([]GatewayBootstrapStatus, 0, len(schema.Properties.Bootstrap.Enum))
	for _, status := range schema.Properties.Bootstrap.Enum {
		want = append(want, GatewayBootstrapStatus(status))
	}
	if !reflect.DeepEqual(gatewayBootstrapStatuses[:], want) {
		t.Fatalf("generated Gateway bootstrap values %v do not match owner schema %v", gatewayBootstrapStatuses, want)
	}
	fixture, err := os.ReadFile(filepath.Join(root, "__fixtures__", "auth-health-response.json"))
	if err != nil {
		t.Fatal(err)
	}
	var response gatewayAuthHealthResponse
	if err := json.Unmarshal(fixture, &response); err != nil {
		t.Fatalf("shared Gateway response fixture did not decode: %v", err)
	}
	if response.Bootstrap != GatewayBootstrapStatusProvisioned || response.OK == nil || response.AuthEnabled == nil || response.TenantID == nil || response.ActiveAdmins == nil || response.Issues == nil {
		t.Fatalf("shared Gateway response fixture was incomplete: %#v", response)
	}
	if err := json.Unmarshal(bytes.Replace(fixture, []byte("provisioned"), []byte("future-state"), 1), &response); err == nil {
		t.Fatal("unknown bootstrap enum was accepted")
	}
}

func TestUnknownGatewayBootstrapFailsWithCataloguedDiagnostic(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"ok":true,"authEnabled":true,"tenantId":"default","activeAdmins":0,"bootstrap":"future-state","issues":[]}`)
	}))
	defer server.Close()
	store := secrets.Store{PlatformRoot: t.TempDir()}
	err := readBootstrapOutcome(context.Background(), server.URL+"/health", store)
	var diagnostic *diag.Diag
	if !errors.As(err, &diagnostic) || diagnostic.Code != contracts.CodeApplyFailed || !strings.Contains(diagnostic.Hint, "rerun `kb-create install`") {
		t.Fatalf("unknown bootstrap status error = %#v, want catalogued apply diagnostic with next step", err)
	}
	if _, present, err := store.Get(secrets.BootstrapCompleted); err != nil || present {
		t.Fatalf("unknown bootstrap response persisted completion: present=%v error=%v", present, err)
	}
}

func TestApplyDisclosesGeneratedAdminOnceAcrossRepeatedInstall(t *testing.T) {
	root := t.TempDir()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	state := host.State{Dir: t.TempDir()}
	request := contracts.InstallRequest{Schema: contracts.RequestSchema, PlatformRoot: root,
		Values:       map[string]string{"gateway.access.mode": `"secured"`, "gateway.bootstrap.adminEmail": `"admin@example.test"`},
		SecretInputs: []string{"gateway.bootstrap.password", "gateway.jwtSecret"}}
	plan := contracts.ResolvedInstallPlan{Schema: contracts.ResolvedPlanSchema, PlanHash: "admin-bootstrap-repeat",
		Request: request, Host: &contracts.HostSpec{Command: "kb-host", HealthURL: fmt.Sprintf("http://127.0.0.1:%d/health", port), ReadyTimeoutSeconds: 5},
		ServiceGraph: contracts.ServiceGraph{PlatformVersion: "2.0.0", Profile: "default"},
		Artifacts: []contracts.Artifact{{ID: "gateway", Package: "@kb-labs/gateway", Version: "2.0.0", Config: []contracts.ConfigRequirement{
			{ID: "gateway.bootstrap.password", Secret: true, Generate: true}, {ID: "gateway.jwtSecret", Secret: true, Generate: true},
		}}}}
	resolved := resolvedPlan{Plan: plan, State: state}
	store := secrets.Store{PlatformRoot: root}
	starter := &bootstrapTestHost{address: address}
	deps := commandDependencies{NodeCheck: func() error { return nil }, ArtifactRunner: operationLogTestRunner{}, Runtime: &runtime.Dependencies{Host: starter}}
	firstOutput := runApplyStreamForTest(t, resolved, deps)
	t.Cleanup(func() {
		if starter.stop != nil {
			_ = starter.stop()
		}
	})
	password, present, err := store.Get("gateway.bootstrap.password")
	if err != nil || !present || strings.Count(firstOutput, password) != 1 {
		t.Fatalf("stored password = %q, present=%v, error=%v", password, present, err)
	}
	assertSingleAdminResultRecord(t, firstOutput, "admin@example.test", password)
	if strings.Count(firstOutput, password) != 1 {
		t.Errorf("credential must appear only in its result record")
	}
	firstResult := applyResultFromStream(t, firstOutput)
	logBytes, err := os.ReadFile(firstResult.LogPath)
	if err != nil {
		t.Fatalf("read operation log %q from apply result: %v", firstResult.LogPath, err)
	}
	if len(logBytes) == 0 {
		t.Fatalf("operation log %q is empty", firstResult.LogPath)
	}
	assertSecretAbsent(t, "operation log", logBytes, password)
	receiptBytes, err := os.ReadFile(receipt.Path(root))
	if err != nil {
		t.Fatalf("read apply receipt: %v", err)
	}
	if len(receiptBytes) == 0 {
		t.Fatal("apply receipt is empty")
	}
	assertSecretAbsent(t, "receipt file", receiptBytes, password)
	planBytes := mustJSON(t, firstResult.Receipt.Plan)
	assertSecretAbsent(t, "plan output", planBytes, password)
	if err := starter.stop(); err != nil {
		t.Fatal(err)
	}
	secondOutput := runApplyStreamForTest(t, resolved, deps)
	unchanged, present, err := store.Get("gateway.bootstrap.password")
	if err != nil || !present || unchanged != password {
		t.Fatalf("repeat apply changed stored password: %q, present=%v, error=%v", unchanged, present, err)
	}
	assertSingleAdminResultRecord(t, secondOutput, "", "")
	if strings.Contains(secondOutput, password) {
		t.Fatalf("repeat apply disclosed password in JSONL: %s", secondOutput)
	}
}

type operationLogTestRunner struct{}

func (operationLogTestRunner) Run(_ context.Context, output io.Writer, _ string, _ ...string) error {
	_, err := io.WriteString(output, "fake package manager operation completed\n")
	return err
}

func applyResultFromStream(t *testing.T, output string) applyCommandResult {
	t.Helper()
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		var record struct {
			Meta struct {
				Stream struct {
					Result bool `json:"result"`
				} `json:"stream"`
			} `json:"meta"`
			Data applyCommandResult `json:"data"`
		}
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("decode apply stream record: %v", err)
		}
		if record.Meta.Stream.Result {
			return record.Data
		}
	}
	t.Fatal("apply stream has no result record")
	return applyCommandResult{}
}

func assertSecretAbsent(t *testing.T, label string, output []byte, password string) {
	t.Helper()
	if bytes.Contains(output, []byte(password)) {
		t.Errorf("generated password leaked into %s", label)
	}
	encoded, err := json.Marshal(password)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(output, encoded) {
		t.Errorf("JSON-escaped generated password leaked into %s", label)
	}
}

func runApplyStreamForTest(t *testing.T, resolved resolvedPlan, deps commandDependencies) string {
	t.Helper()
	var stdout, stderr bytes.Buffer
	command := &cobra.Command{Use: "kb-create apply"}
	command.SetOut(&stdout)
	command.SetErr(&stderr)
	deps.Command = command
	completion, failure := executeApplyStream(context.Background(), applyCommandInput{Resolved: &resolved}, deps)
	if failure != nil || completion.ExitCode != 0 {
		t.Fatalf("apply stream failed: completion=%#v failure=%v stdout=%s stderr=%s", completion, failure, stdout.String(), stderr.String())
	}
	return stdout.String()
}

func assertSingleAdminResultRecord(t *testing.T, output, email, password string) {
	t.Helper()
	resultCount := 0
	credentialCount := 0
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("invalid JSONL record %q: %v", line, err)
		}
		meta, _ := record["meta"].(map[string]any)
		streamMeta, _ := meta["stream"].(map[string]any)
		if streamMeta["result"] != true {
			if password != "" && strings.Contains(line, password) {
				t.Errorf("password leaked in a non-result event: %s", line)
			}
			continue
		}
		resultCount++
		data, _ := record["data"].(map[string]any)
		credential, exists := data["firstAdmin"].(map[string]any)
		if exists {
			credentialCount++
			if credential["email"] != email || credential["password"] != password || credential["shownOnce"] != true {
				t.Errorf("firstAdmin result = %#v", credential)
			}
		}
	}
	if resultCount != 1 {
		t.Errorf("stream result record count = %d, want 1", resultCount)
	}
	wantCredentials := 0
	if password != "" {
		wantCredentials = 1
	}
	if credentialCount != wantCredentials {
		t.Errorf("firstAdmin result count = %d, want %d", credentialCount, wantCredentials)
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}
