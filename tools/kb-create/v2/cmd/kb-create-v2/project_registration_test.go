package v2cli

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kb-labs/clikit/diag"
	"github.com/kb-labs/create/v2/contracts"
	"github.com/kb-labs/create/v2/receipt"
	"github.com/kb-labs/create/v2/secrets"
)

// case: S-187.10
func TestSecuredProjectRegistrationUsesTransientGatewayLogin(t *testing.T) {
	project := t.TempDir()
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	password := "bootstrap-password-for-test"
	token := "access-token-for-test"
	loginCalls, projectCalls := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/auth/login/cli":
			loginCalls++
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["password"] != password || body["email"] != "admin@example.test" || body["tenantId"] != "workspace" {
				t.Errorf("login body did not contain the bootstrap login fields")
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"accessToken":"` + token + `","refreshToken":"refresh-token-for-test","expiresIn":3600,"tenantId":"workspace"}`))
		case "/api/v1/projects":
			projectCalls++
			if r.Header.Get("Authorization") != "Bearer "+token {
				t.Errorf("project request lacked its transient bearer token")
			}
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["path"] != project {
				t.Errorf("project request path = %q", body["path"])
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"ok":true,"data":{"id":"prj_test","path":"` + project + `"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	installed := contracts.InstallReceipt{
		ID: "receipt-test",
		Plan: contracts.ResolvedInstallPlan{Request: contracts.InstallRequest{Values: map[string]string{
			"gateway.access.mode":          `"secured"`,
			"gateway.bootstrap.adminEmail": `"admin@example.test"`,
			"gateway.bootstrap.tenantId":   `"workspace"`,
		}}},
		Verification: contracts.Verification{HostHealthURL: server.URL + "/health"},
	}
	if err := receipt.Write(root, installed); err != nil {
		t.Fatal(err)
	}
	if err := (secrets.Store{PlatformRoot: root}).Put("gateway.bootstrap.password", password); err != nil {
		t.Fatal(err)
	}
	id, err := registerProject(root, project)
	if err != nil || id != "prj_test" || loginCalls != 1 || projectCalls != 1 {
		t.Fatalf("registration id/error/calls = %q / %v / %d / %d", id, err, loginCalls, projectCalls)
	}
	for _, path := range []string{filepath.Join(home, ".kb/session.json"), filepath.Join(home, ".kb/credentials.json")} {
		if _, err := os.Stat(path); err == nil {
			t.Fatalf("registration persisted a CLI session at %s", path)
		}
	}
	if data, err := os.ReadFile(receipt.Path(root)); err != nil || strings.Contains(string(data), token) {
		t.Fatalf("receipt read/error or token persisted: %v", err)
	}
	if stored, present, err := (secrets.Store{PlatformRoot: root}).Get("gateway.bootstrap.password"); err != nil || !present || stored != password {
		t.Fatalf("bootstrap password state changed: present=%v err=%v", present, err)
	}
}

// case: S-223.1
func TestRegistrationFailureKeepsCauseAndActionableSecuredSequence(t *testing.T) {
	root := t.TempDir()
	project := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		envelope, err := diag.ToEnvelope(diag.New("KB_AUTH_INVALID_CREDENTIALS", "The credentials are invalid."))
		if err != nil {
			t.Error(err)
			return
		}
		envelope.CorrelationID = "registration-test"
		_, _ = w.Write(append(append([]byte(`{"ok":false,"error":`), mustJSON(t, envelope)...), '}'))
	}))
	defer server.Close()
	active := contracts.InstallReceipt{ID: "failed-receipt", Plan: contracts.ResolvedInstallPlan{Request: contracts.InstallRequest{Values: map[string]string{
		"gateway.access.mode":          `"secured"`,
		"gateway.bootstrap.adminEmail": `"admin@example.test"`,
	}}}, Verification: contracts.Verification{HostHealthURL: server.URL + "/health"}}
	if err := receipt.Write(root, active); err != nil {
		t.Fatal(err)
	}
	if err := (secrets.Store{PlatformRoot: root}).Put("gateway.bootstrap.password", "bootstrap-password-for-test"); err != nil {
		t.Fatal(err)
	}
	_, cause := registerProject(root, project)
	if cause == nil {
		t.Fatal("login refusal unexpectedly registered project")
	}
	failure := projectRegistrationFailure(root, project, cause)
	if failure.Code != "KB_INSTALL_PROJECT_REGISTRATION_FAILED" || failure.Meta["causeCode"] != "KB_AUTH_INVALID_CREDENTIALS" ||
		!strings.Contains(failure.Hint, "kb auth login --gateway-url") || !strings.Contains(failure.Hint, "kb project add") ||
		!strings.Contains(failure.Hint, "kb project show") || failure.Cause == "bootstrap-password-for-test" {
		t.Fatalf("registration diagnostic is incomplete or exposes a secret: %#v", failure)
	}
}

// case: S-223.1
func TestProjectPathRemovedAfterApplyRetainsCataloguedCause(t *testing.T) {
	root := t.TempDir()
	missing := filepath.Join(t.TempDir(), "removed-after-apply")
	_, cause := registerInstalledProject(root, missing, nil)
	var typed *diag.Diag
	if !errors.As(cause, &typed) || typed.Code != "KB_PROJECT_PATH_NOT_FOUND" {
		t.Fatalf("missing path cause = %#v", cause)
	}
	failure := projectRegistrationFailure(root, missing, cause)
	if failure.Meta["causeCode"] != "KB_PROJECT_PATH_NOT_FOUND" {
		t.Fatalf("registration failure did not preserve the path cause: %#v", failure.Meta)
	}
}

// case: S-187.11
func TestExistingInstallationRejectsDifferentProjectBeforeApply(t *testing.T) {
	root := t.TempDir()
	activeProject := t.TempDir()
	selectedProject := t.TempDir()
	active := contracts.InstallReceipt{ID: "receipt-existing", Plan: contracts.ResolvedInstallPlan{Request: contracts.InstallRequest{
		PlatformRoot: root, ProjectRoot: activeProject,
	}}}
	if err := receipt.Write(root, active); err != nil {
		t.Fatal(err)
	}
	existing, failure := existingProjectSelection(root, selectedProject)
	if !existing || failure == nil || failure.Code != "KB_INSTALL_PROJECT_SELECTION_CHANGED" || !strings.Contains(failure.Hint, "kb project add") || !strings.Contains(failure.Hint, "kb project show") {
		t.Fatalf("existing/failure = %v / %#v", existing, failure)
	}
	if _, failure := existingProjectSelection(root, ""); failure != nil {
		t.Fatalf("empty project selection refused: %v", failure)
	}
}

// case: S-187.11
func TestExistingApplyRejectsDifferentProjectBeforeOperation(t *testing.T) {
	root := t.TempDir()
	activeProject := t.TempDir()
	selectedProject := t.TempDir()
	active := contracts.InstallReceipt{ID: "receipt-existing", Plan: contracts.ResolvedInstallPlan{Request: contracts.InstallRequest{
		PlatformRoot: root, ProjectRoot: activeProject,
	}}}
	if err := receipt.Write(root, active); err != nil {
		t.Fatal(err)
	}
	resolved := resolvedPlan{Plan: contracts.ResolvedInstallPlan{Request: contracts.InstallRequest{
		PlatformRoot: root, ProjectRoot: selectedProject,
	}}}
	_, failure := applyPlan(context.Background(), applyCommandInput{Resolved: &resolved}, commandDependencies{
		NodeCheck: func() error { return nil },
	})
	if failure == nil || failure.Code != "KB_INSTALL_PROJECT_SELECTION_CHANGED" ||
		failure.ProcessExitCode != diag.ExitInput || !strings.Contains(failure.Hint, "kb project add") ||
		!strings.Contains(failure.Hint, "kb project show") {
		t.Fatalf("apply selection failure = %#v", failure)
	}
}
