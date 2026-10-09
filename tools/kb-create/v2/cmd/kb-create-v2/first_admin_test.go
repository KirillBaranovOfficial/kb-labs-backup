package v2cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kb-labs/create/v2/contracts"
	"github.com/kb-labs/create/v2/host"
	"github.com/kb-labs/create/v2/runtime"
	"github.com/kb-labs/create/v2/secrets"
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

	plan, err := json.Marshal(result.Receipt.Plan)
	if err != nil {
		t.Fatal(err)
	}
	receiptBytes, err := json.Marshal(result.Receipt)
	if err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(t.TempDir(), "kb-create.jsonl")
	if err := os.WriteFile(logPath, []byte("{\"phase\":\"verify\",\"state\":\"completed\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	logBytes, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	for label, output := range map[string][]byte{"plan": plan, "receipt": receiptBytes, "log": logBytes} {
		if strings.Contains(string(output), password) {
			t.Errorf("generated password leaked into %s: %s", label, output)
		}
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

type bootstrapTestProgress struct{ events []runtime.ProgressEvent }

func (progress *bootstrapTestProgress) Emit(event runtime.ProgressEvent) error {
	progress.events = append(progress.events, event)
	return nil
}

func (starter *bootstrapTestHost) Start(_ context.Context, spec contracts.HostSpec) (func() error, error) {
	listener, err := net.Listen("tcp", starter.address)
	if err != nil {
		return nil, err
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/health/auth" {
			_, _ = fmt.Fprint(w, `{"bootstrap":"provisioned"}`)
			return
		}
		_, _ = fmt.Fprint(w, `{"ok":true}`)
	}))
	server.Listener = listener
	server.Start()
	starter.stop = func() error { server.Close(); return nil }
	return starter.stop, nil
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
		Artifacts: []contracts.Artifact{{ID: "gateway", Config: []contracts.ConfigRequirement{
			{ID: "gateway.bootstrap.password", Secret: true, Generate: true}, {ID: "gateway.jwtSecret", Secret: true, Generate: true},
		}}}}
	resolved := resolvedPlan{Plan: plan, State: state}
	store := secrets.Store{PlatformRoot: root}
	starter := &bootstrapTestHost{address: address}
	progress := &bootstrapTestProgress{}
	deps := commandDependencies{NodeCheck: func() error { return nil }, Runtime: &runtime.Dependencies{
		Artifacts: installArtifacts{root: root}, Host: starter, Progress: progress,
	}}
	first, failure := executeApply(context.Background(), applyCommandInput{Resolved: &resolved}, deps)
	if failure != nil {
		t.Fatalf("first apply: %v", failure)
	}
	t.Cleanup(func() {
		if starter.stop != nil {
			_ = starter.stop()
		}
	})
	if first.FirstAdmin == nil || first.FirstAdmin.Email != "admin@example.test" || !first.FirstAdmin.ShownOnce {
		t.Fatalf("first apply did not disclose the new admin credential: %#v", first.FirstAdmin)
	}
	if rendered := renderApply(first); strings.Count(rendered, first.FirstAdmin.Password) != 1 {
		t.Fatalf("first apply must show the generated password once: %q", rendered)
	}
	password, present, err := store.Get("gateway.bootstrap.password")
	if err != nil || !present || password != first.FirstAdmin.Password {
		t.Fatalf("stored password = %q, present=%v, error=%v", password, present, err)
	}
	for label, output := range map[string][]byte{
		"plan":    mustJSON(t, first.Receipt.Plan),
		"receipt": mustJSON(t, first.Receipt),
	} {
		if strings.Contains(string(output), password) {
			t.Errorf("generated password leaked into %s", label)
		}
	}
	logBytes, err := os.ReadFile(first.LogPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(logBytes), password) {
		t.Fatal("generated password leaked into the operation log")
	}
	if strings.Contains(string(mustJSON(t, progress.events)), password) {
		t.Fatal("generated password leaked into progress events")
	}
	if err := starter.stop(); err != nil {
		t.Fatal(err)
	}
	second, failure := executeApply(context.Background(), applyCommandInput{Resolved: &resolved}, deps)
	if failure != nil {
		t.Fatalf("repeat apply: %v", failure)
	}
	unchanged, present, err := store.Get("gateway.bootstrap.password")
	if err != nil || !present || unchanged != password {
		t.Fatalf("repeat apply changed stored password: %q, present=%v, error=%v", unchanged, present, err)
	}
	if second.FirstAdmin != nil {
		t.Fatalf("repeat apply disclosed the credential again: %#v", second.FirstAdmin)
	}
	if rendered := renderApply(second); strings.Contains(rendered, password) {
		t.Fatalf("repeat apply human output disclosed password: %q", rendered)
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
