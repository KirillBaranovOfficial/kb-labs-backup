package v2cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/kb-labs/clikit/diag"
	"github.com/kb-labs/clikit/result"
	"github.com/kb-labs/create/v2/contracts"
	"github.com/kb-labs/create/v2/receipt"
	"github.com/kb-labs/create/v2/secrets"
	"github.com/kb-labs/create/v2/versionroot"
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
	layout, err := versionroot.New(root)
	if err != nil {
		t.Fatal(err)
	}
	secretStorePath, err := layout.SharedRuntimePath(".kb/v2/secrets.env")
	if err != nil {
		t.Fatal(err)
	}
	secretStoreBefore, err := os.ReadFile(secretStorePath)
	if err != nil {
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
	secretStoreAfter, err := os.ReadFile(secretStorePath)
	if err != nil || !bytes.Equal(secretStoreBefore, secretStoreAfter) {
		t.Fatalf("project registration changed the existing bootstrap secret store: %v", err)
	}
}

// case: S-224.1, S-224.2
func TestRegistrationFailuresNeverCopyRemoteTextIntoDiagnostics(t *testing.T) {
	const password = "x18-bootstrap-password-marker"
	const token = "x18-access-token-marker"
	const installerCorrelationID = "fedcba9876543210fedcba9876543210"
	tests := []struct {
		name      string
		causeCode string
		stage     string
		status    int
	}{
		{"login refused", "KB_AUTH_INVALID_CREDENTIALS", "login", http.StatusUnauthorized},
		{"login malformed", "KB_RUNTIME_GATEWAY_PROTOCOL_FAILURE", "login", http.StatusUnauthorized},
		{"register refused", "KB_AUTH_FORBIDDEN", "register", http.StatusForbidden},
		{"register malformed", "KB_RUNTIME_GATEWAY_PROTOCOL_FAILURE", "register", http.StatusBadGateway},
		{"register transport error", "KB_HOST_UNREACHABLE", "register", 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root, home, project := t.TempDir(), t.TempDir(), t.TempDir()
			t.Setenv("HOME", home)
			var requestIDMu sync.Mutex
			var lastRequestID string
			responseWithSecrets := func(w http.ResponseWriter, status int, code, requestID string) {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("X-Remote-Credential", password+" "+token)
				w.WriteHeader(status)
				if code == "" {
					_, _ = io.WriteString(w, `{"error":"`+password+" "+token+`"`)
					return
				}
				catalog, ok := diag.LookupCatalog(code)
				if !ok {
					t.Fatalf("test code %q is absent from the platform catalog", code)
				}
				body, err := json.Marshal(map[string]any{"ok": false, "error": map[string]any{
					"code": code, "area": catalog.Area, "stage": catalog.Stage, "severity": catalog.Severity, "retryable": catalog.Retryable,
					"message": password + " " + token, "hint": token + " " + password,
					"cause": password + " " + token, "correlationId": requestID,
					"details": map[string]string{"remote": password + " " + token},
				}})
				if err != nil {
					t.Fatal(err)
				}
				var payload struct{}
				decoded, decodeErr := result.DecodeResultWithContext(body, &payload, result.RemoteFailureContext{RequestStage: test.stage, HTTPStatus: status})
				if decodeErr != nil || decoded == nil || decoded.Code != code {
					t.Fatalf("shared decoder did not preserve catalogued code: code=%q failure=%#v err=%v", code, decoded, decodeErr)
				}
				_, _ = w.Write(body)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requestID := r.Header.Get("X-Request-Id")
				if !diag.IsValidCorrelationID(requestID) {
					t.Errorf("request correlation ID = %q, want valid Gateway correlation ID", requestID)
				}
				requestIDMu.Lock()
				lastRequestID = requestID
				requestIDMu.Unlock()
				if r.URL.Path == "/auth/login/cli" {
					if test.stage == "login" {
						if test.name == "login malformed" {
							responseWithSecrets(w, test.status, "", requestID)
						} else {
							responseWithSecrets(w, test.status, "KB_AUTH_INVALID_CREDENTIALS", requestID)
						}
						return
					}
					w.Header().Set("Content-Type", "application/json")
					_, _ = fmt.Fprintf(w, `{"accessToken":%q,"refreshToken":%q,"expiresIn":3600,"tenantId":"workspace"}`, token, token)
					return
				}
				if test.name == "register transport error" {
					connection, _, err := w.(http.Hijacker).Hijack()
					if err != nil {
						t.Error(err)
						return
					}
					_, _ = fmt.Fprintf(connection, "invalid-response %s %s\r\n\r\n", password, token)
					_ = connection.Close()
					return
				}
				if test.name == "register malformed" {
					responseWithSecrets(w, test.status, "", requestID)
					return
				}
				responseWithSecrets(w, test.status, "KB_AUTH_FORBIDDEN", requestID)
			}))
			defer server.Close()

			active := contracts.InstallReceipt{ID: "failed-receipt", Plan: contracts.ResolvedInstallPlan{Request: contracts.InstallRequest{Values: map[string]string{
				"gateway.access.mode":          `"secured"`,
				"gateway.bootstrap.adminEmail": `"admin@example.test"`,
				"gateway.bootstrap.tenantId":   `"workspace"`,
			}}}, Verification: contracts.Verification{HostHealthURL: server.URL + "/health"}}
			if err := receipt.Write(root, active); err != nil {
				t.Fatal(err)
			}
			if err := (secrets.Store{PlatformRoot: root}).Put("gateway.bootstrap.password", password); err != nil {
				t.Fatal(err)
			}
			layout, err := versionroot.New(root)
			if err != nil {
				t.Fatal(err)
			}
			secretStorePath, err := layout.SharedRuntimePath(".kb/v2/secrets.env")
			if err != nil {
				t.Fatal(err)
			}
			secretStoreBefore, err := os.ReadFile(secretStorePath)
			if err != nil {
				t.Fatal(err)
			}
			canonicalSecretStorePath, err := filepath.EvalSymlinks(secretStorePath)
			if err != nil {
				t.Fatal(err)
			}
			_, cause := registerProject(root, project)
			if cause == nil {
				t.Fatal("registration failure unexpectedly succeeded")
			}
			failure := projectRegistrationFailure(root, project, cause)
			statusMatches := test.status == 0 && failure.Meta["httpStatus"] == nil ||
				test.status != 0 && failure.Meta["httpStatus"] == test.status
			if failure.Code != "KB_INSTALL_PROJECT_REGISTRATION_FAILED" || failure.Meta["causeCode"] != test.causeCode ||
				failure.Meta["requestStage"] != test.stage || !statusMatches {
				t.Fatalf("registration diagnostic metadata = %#v", failure.Meta)
			}
			failure.CorrelationID = installerCorrelationID
			remoteCorrelationID, hasRemoteCorrelationID := failure.Meta["remoteCorrelationId"]
			wantRemoteCorrelationID := test.name == "login refused" || test.name == "register refused"
			requestIDMu.Lock()
			gatewayCorrelationID := lastRequestID
			requestIDMu.Unlock()
			if hasRemoteCorrelationID != wantRemoteCorrelationID || (wantRemoteCorrelationID && remoteCorrelationID != gatewayCorrelationID) {
				t.Fatalf("remote correlation metadata = %#v, want present=%v value=%q", remoteCorrelationID, wantRemoteCorrelationID, gatewayCorrelationID)
			}
			var jsonOutput, humanOutput bytes.Buffer
			result.RenderDiag(&jsonOutput, &humanOutput, failure, result.ModeJSON)
			result.RenderDiag(&bytes.Buffer{}, &humanOutput, failure, result.ModeHuman)
			if wantRemoteCorrelationID {
				var envelope struct {
					Error struct {
						CorrelationID string            `json:"correlationId"`
						Details       map[string]string `json:"details"`
					} `json:"error"`
				}
				if err := json.Unmarshal(jsonOutput.Bytes(), &envelope); err != nil {
					t.Fatal(err)
				}
				if envelope.Error.CorrelationID != installerCorrelationID ||
					envelope.Error.Details["remoteCorrelationId"] != gatewayCorrelationID ||
					!strings.Contains(humanOutput.String(), "correlationId: "+installerCorrelationID) ||
					!strings.Contains(humanOutput.String(), "remote correlationId: "+gatewayCorrelationID) {
					t.Fatalf("diagnostic outputs did not retain distinct installer and Gateway IDs: json=%s human=%s", jsonOutput.String(), humanOutput.String())
				}
			}
			for _, secret := range []string{password, token} {
				if strings.Contains(failure.Message+failure.Hint+failure.Reason+failure.Cause, secret) ||
					strings.Contains(jsonOutput.String(), secret) || strings.Contains(humanOutput.String(), secret) {
					t.Fatalf("remote secret %q reached a diagnostic or renderer", secret)
				}
			}
			for _, path := range []string{root, home} {
				err := filepath.WalkDir(path, func(file string, entry os.DirEntry, walkErr error) error {
					if walkErr != nil || entry.IsDir() {
						return walkErr
					}
					data, err := os.ReadFile(file)
					if err != nil {
						return err
					}
					if bytes.Contains(data, []byte(token)) {
						return fmt.Errorf("file %s contains a remote token", file)
					}
					if bytes.Contains(data, []byte(password)) {
						canonicalFile, err := filepath.EvalSymlinks(file)
						if err != nil || filepath.Clean(canonicalFile) != filepath.Clean(canonicalSecretStorePath) {
							return fmt.Errorf("file %s contains the bootstrap password outside its unchanged secret store", file)
						}
					}
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			secretStoreAfter, err := os.ReadFile(secretStorePath)
			if err != nil || !bytes.Equal(secretStoreBefore, secretStoreAfter) {
				t.Fatalf("registration failure changed the existing bootstrap secret store: %v", err)
			}
		})
	}
}

// case: S-224.1
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
	writeVersionedReceipt(t, root, active)
	existing, failure := existingProjectSelection(platformRootForTest(t, root), selectedProject)
	if !existing || failure == nil || failure.Code != "KB_INSTALL_PROJECT_SELECTION_CHANGED" || !strings.Contains(failure.Hint, "kb project add") || !strings.Contains(failure.Hint, "kb project show") {
		t.Fatalf("existing/failure = %v / %#v", existing, failure)
	}
	if _, failure := existingProjectSelection(platformRootForTest(t, root), ""); failure != nil {
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
	writeVersionedReceipt(t, root, active)
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
