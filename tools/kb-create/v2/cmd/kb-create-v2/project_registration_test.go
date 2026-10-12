package v2cli

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

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
	projectAlias := filepath.Join(t.TempDir(), "selected-project")
	if err := os.Symlink(project, projectAlias); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("KB_HOME", filepath.Join(home, ".kb"))
	password := "bootstrap-password-for-test"
	origin := startSecuredProjectRegistrationGateway(t)
	canonicalProject, err := filepath.EvalSymlinks(project)
	if err != nil {
		t.Fatal(err)
	}

	installed := contracts.InstallReceipt{
		ID: "receipt-test",
		Plan: contracts.ResolvedInstallPlan{Request: contracts.InstallRequest{Values: map[string]string{
			"gateway.access.mode":          `"secured"`,
			"gateway.bootstrap.adminEmail": `"admin@example.test"`,
			"gateway.bootstrap.tenantId":   `"workspace"`,
		}}},
		Verification: contracts.Verification{HostHealthURL: origin + "/health"},
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
	homeBefore := snapshotFilesystem(t, home)
	rootBefore := snapshotFilesystem(t, root)
	registered, err := registerProject(root, projectAlias)
	if err != nil || registered.ID == "" || registered.CanonicalPath != canonicalProject || registered.AlreadyExisted {
		var failure *diag.Diag
		_ = errors.As(err, &failure)
		t.Fatalf("registration identity/error = %#v / %v (diagnostic=%#v)", registered, err, failure)
	}
	activeReceipt, err := recordProjectIdentity(root, projectAlias, registered)
	if err != nil || activeReceipt.ProjectSelectedPath != projectAlias || activeReceipt.ProjectID != registered.ID || activeReceipt.ProjectCanonicalPath != canonicalProject {
		t.Fatalf("receipt project identity = %#v, err=%v", activeReceipt, err)
	}
	for _, path := range []string{filepath.Join(home, ".kb/session.json"), filepath.Join(home, ".kb/credentials.json")} {
		if _, err := os.Stat(path); err == nil {
			t.Fatalf("registration persisted a CLI session at %s", path)
		}
	}
	if data, err := os.ReadFile(receipt.Path(root)); err != nil || strings.Contains(string(data), password) ||
		strings.Contains(string(data), "accessToken") || strings.Contains(string(data), "refreshToken") {
		t.Fatalf("receipt read/error or authentication material persisted: %v", err)
	}
	if stored, present, err := (secrets.Store{PlatformRoot: root}).Get("gateway.bootstrap.password"); err != nil || !present || stored != password {
		t.Fatalf("bootstrap password state changed: present=%v err=%v", present, err)
	}
	secretStoreAfter, err := os.ReadFile(secretStorePath)
	if err != nil || !bytes.Equal(secretStoreBefore, secretStoreAfter) {
		t.Fatalf("project registration changed the existing bootstrap secret store: %v", err)
	}
	homeChanges := changedFilesystemPaths(homeBefore, snapshotFilesystem(t, home))
	rootChanges := changedFilesystemPaths(rootBefore, snapshotFilesystem(t, root))
	kbHome := filepath.Join(home, ".kb")
	for _, path := range homeChanges {
		if !allowedProjectRegistrationHomePath(path, registered.ID) {
			t.Errorf("project registration changed unapproved HOME path %q", path)
		}
	}
	relativeReceipt, err := filepath.Rel(root, receipt.Path(root))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range rootChanges {
		if path != relativeReceipt {
			t.Errorf("project registration changed unapproved install-root path %q", path)
		}
	}
	if !containsPath(homeChanges, filepath.Join(".kb", "projects.json")) {
		t.Fatalf("project registry file was not recorded as the sole registration write: HOME=%v", homeChanges)
	}
	registryData, err := os.ReadFile(filepath.Join(kbHome, "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	var registry struct {
		Projects []struct {
			ID   string `json:"id"`
			Path string `json:"path"`
		} `json:"projects"`
	}
	if err := json.Unmarshal(registryData, &registry); err != nil || len(registry.Projects) != 1 ||
		registry.Projects[0].ID != registered.ID || registry.Projects[0].Path != canonicalProject {
		t.Fatalf("approved project registry write = %#v, err=%v", registry, err)
	}
}

func allowedProjectRegistrationHomePath(path, projectID string) bool {
	allowed := map[string]bool{
		filepath.Join(".kb"):                     true,
		filepath.Join(".kb", "projects.json"):    true,
		filepath.Join(".kb", "state"):            true,
		filepath.Join(".kb", "state", projectID): true,
	}
	stateRoot := filepath.Join(".kb", "state", projectID) + string(filepath.Separator)
	return allowed[path] || strings.HasPrefix(path, stateRoot)
}

func containsPath(paths []string, expected string) bool {
	for _, path := range paths {
		if path == expected {
			return true
		}
	}
	return false
}

func snapshotFilesystem(t *testing.T, root string) map[string]string {
	t.Helper()
	snapshot := make(map[string]string)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if relative == "." {
			return nil
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		value := info.Mode().String()
		if info.Mode()&os.ModeSymlink != 0 {
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			value += ":" + link
		} else if info.Mode().IsRegular() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			value += fmt.Sprintf(":%x:%d", sha256.Sum256(data), info.ModTime().UnixNano())
		}
		snapshot[relative] = value
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func changedFilesystemPaths(before, after map[string]string) []string {
	changed := make([]string, 0)
	for path, prior := range before {
		if current, exists := after[path]; !exists || current != prior {
			changed = append(changed, path)
		}
	}
	for path := range after {
		if _, existed := before[path]; !existed {
			changed = append(changed, path)
		}
	}
	sort.Strings(changed)
	return changed
}

func TestSecuredGatewayFixtureReportsMissingBuildArtifact(t *testing.T) {
	missingRoot := t.TempDir()
	err := requireSecuredGatewayFixtureBuildArtifacts(missingRoot)
	if err == nil || !strings.Contains(err.Error(), filepath.Join(missingRoot, "core", "project-registry", "dist", "index.js")) ||
		!strings.Contains(err.Error(), "corepack pnpm -r --filter '@kb-labs/gateway-app...'") {
		t.Fatalf("missing artifact diagnostic = %v", err)
	}
	moduleFailure := "Error [ERR_MODULE_NOT_FOUND]: Cannot find package '@kb-labs/host-app' imported from fixture.ts"
	if got := missingSecuredGatewayFixtureModule(moduleFailure + "\n    at file:///fixture.ts"); got != moduleFailure {
		t.Fatalf("missing module classifier = %q, want %q", got, moduleFailure)
	}
}

const securedGatewayFixtureBuildCommand = "corepack pnpm -r --filter '@kb-labs/gateway-app...' --filter '@kb-labs/host-app...' --filter '@kb-labs/core-project-registry...' --workspace-concurrency=2 build"

func requireSecuredGatewayFixtureBuildArtifacts(workspaceRoot string) error {
	registryEntry := filepath.Join(workspaceRoot, "core", "project-registry", "dist", "index.js")
	if _, err := os.Stat(registryEntry); err != nil {
		return fmt.Errorf("secured Gateway fixture is missing built artifact %s; run `%s`", registryEntry, securedGatewayFixtureBuildCommand)
	}
	return nil
}

func startSecuredProjectRegistrationGateway(t *testing.T) string {
	t.Helper()
	workspaceRoot, err := filepath.Abs(filepath.Join("..", "..", "..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if err := requireSecuredGatewayFixtureBuildArtifacts(workspaceRoot); err != nil {
		t.Fatal(err)
	}
	fixture := filepath.Join(workspaceRoot, "services", "gateway", "app", "test-fixtures", "secured-project-registration-gateway.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	tsRunner := filepath.Join(workspaceRoot, "services", "gateway", "app", "node_modules", ".bin", "tsx")
	command := exec.CommandContext(ctx, tsRunner, fixture)
	command.Dir = workspaceRoot
	command.Env = os.Environ()
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdout, err := command.StdoutPipe()
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		cancel()
		t.Fatalf("start secured Gateway fixture: %v", err)
	}
	t.Cleanup(func() {
		cancel()
		if command.ProcessState == nil {
			_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		}
		_ = command.Wait()
		if stderr.Len() > 0 {
			t.Logf("secured Gateway fixture stderr: %s", stderr.String())
		}
	})
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		_ = command.Wait()
		if missing := missingSecuredGatewayFixtureModule(stderr.String()); missing != "" {
			t.Fatalf("secured Gateway fixture could not load built input %s; run `%s`", missing, securedGatewayFixtureBuildCommand)
		}
		t.Fatalf("secured Gateway fixture did not become ready: %v", err)
	}
	fields := strings.Fields(line)
	if len(fields) != 2 || fields[0] != "READY" {
		t.Fatalf("secured Gateway fixture readiness = %q; stderr: %s", line, stderr.String())
	}
	return fields[1]
}

func missingSecuredGatewayFixtureModule(stderr string) string {
	for _, line := range strings.Split(stderr, "\n") {
		line = strings.TrimSpace(line)
		if strings.Contains(line, "Cannot find module") || strings.Contains(line, "Cannot find package") || strings.Contains(line, "ERR_MODULE_NOT_FOUND") {
			return line
		}
	}
	return ""
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
		{"login transport error", "KB_AUTH_GATEWAY_UNREACHABLE", "login", 0},
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
					if test.name == "login transport error" {
						connection, _, err := w.(http.Hijacker).Hijack()
						if err != nil {
							t.Error(err)
							return
						}
						_, _ = fmt.Fprintf(connection, "invalid-response %s %s\r\n\r\n", password, token)
						_ = connection.Close()
						return
					}
					if test.stage == "login" {
						if test.name == "login malformed" {
							responseWithSecrets(w, test.status, "", requestID)
						} else {
							responseWithSecrets(w, test.status, "KB_AUTH_INVALID_CREDENTIALS", requestID)
						}
						return
					}
					w.Header().Set("Content-Type", "application/json")
					_, _ = fmt.Fprintf(w, `{"ok":true,"data":{"accessToken":%q,"refreshToken":%q,"expiresIn":3600,"tenantId":"workspace"}}`, token, token)
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
			if failure.Cause == "" || !strings.Contains(failure.Cause, "failed during "+test.stage) || !strings.Contains(failure.Cause, test.causeCode) {
				t.Fatalf("registration diagnostic cause = %q", failure.Cause)
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
						Cause         string            `json:"cause"`
						Details       map[string]string `json:"details"`
					} `json:"error"`
				}
				if err := json.Unmarshal(jsonOutput.Bytes(), &envelope); err != nil {
					t.Fatal(err)
				}
				if envelope.Error.CorrelationID != installerCorrelationID ||
					envelope.Error.Details["remoteCorrelationId"] != gatewayCorrelationID ||
					!strings.Contains(envelope.Error.Cause, "Gateway correlation ID "+gatewayCorrelationID) ||
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
	active := contracts.InstallReceipt{ID: "receipt-existing", ProjectSelectedPath: activeProject, ProjectID: "prj_0123456789abcdef", ProjectCanonicalPath: activeProject, Plan: contracts.ResolvedInstallPlan{Request: contracts.InstallRequest{
		PlatformRoot: root, ProjectRoot: activeProject,
	}}}
	writeVersionedReceipt(t, root, active)
	existing, failure := existingProjectSelection(platformRootForTest(t, root), selectedProject)
	if !existing || failure == nil || failure.Code != "KB_INSTALL_PROJECT_SELECTION_CHANGED" ||
		!strings.Contains(failure.Hint, contracts.ShellCommand("kb", "project", "add", selectedProject)) ||
		!strings.Contains(failure.Hint, contracts.ShellCommand("kb", "project", "show", selectedProject)) {
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
	active := contracts.InstallReceipt{ID: "receipt-existing", ProjectSelectedPath: activeProject, ProjectID: "prj_0123456789abcdef", ProjectCanonicalPath: activeProject, Plan: contracts.ResolvedInstallPlan{Request: contracts.InstallRequest{
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
		failure.ProcessExitCode != diag.ExitInput ||
		!strings.Contains(failure.Hint, contracts.ShellCommand("kb", "project", "add", selectedProject)) ||
		!strings.Contains(failure.Hint, contracts.ShellCommand("kb", "project", "show", selectedProject)) {
		t.Fatalf("apply selection failure = %#v", failure)
	}
}

// case: S-187.11
func TestExistingProjectSelectionRejectsAliasEvenWhenItTargetsRecordedProject(t *testing.T) {
	root := t.TempDir()
	project := t.TempDir()
	alias := filepath.Join(t.TempDir(), "same-project-alias")
	if err := os.Symlink(project, alias); err != nil {
		t.Fatal(err)
	}
	active := contracts.InstallReceipt{ID: "receipt-existing", ProjectSelectedPath: project,
		ProjectID: "prj_0123456789abcdef", ProjectCanonicalPath: project,
		Plan: contracts.ResolvedInstallPlan{Request: contracts.InstallRequest{PlatformRoot: root, ProjectRoot: project}}}
	writeVersionedReceipt(t, root, active)
	beforeRoot := snapshotFilesystem(t, root)
	existing, failure := existingProjectSelection(platformRootForTest(t, root), alias)
	if !existing || failure == nil || failure.Code != contracts.CodeProjectSelectionChanged {
		t.Fatalf("existing/failure = %v / %#v, want changed selection", existing, failure)
	}
	if strings.Contains(strings.ToLower(failure.Message), "different project") || !strings.Contains(failure.Message, "path spelling differs") {
		t.Fatalf("selection refusal overstates project identity: %q", failure.Message)
	}
	if !strings.Contains(failure.Hint, contracts.ShellCommand("kb", "project", "add", alias)) ||
		!strings.Contains(failure.Hint, contracts.ShellCommand("kb", "project", "show", alias)) {
		t.Fatalf("alias settling hint = %q", failure.Hint)
	}
	if changes := changedFilesystemPaths(beforeRoot, snapshotFilesystem(t, root)); len(changes) != 0 {
		t.Fatalf("changed selection mutated the install root: %v", changes)
	}
}

// case: S-187.12
func TestExistingProjectSelectionAcceptsEitherRecordedSpelling(t *testing.T) {
	root := t.TempDir()
	target := t.TempDir()
	alias := filepath.Join(t.TempDir(), "project-alias")
	if err := os.Symlink(target, alias); err != nil {
		t.Fatal(err)
	}
	active := contracts.InstallReceipt{ID: "receipt-existing", ProjectSelectedPath: alias,
		ProjectID: "prj_0123456789abcdef", ProjectCanonicalPath: target,
		Plan: contracts.ResolvedInstallPlan{Request: contracts.InstallRequest{PlatformRoot: root, ProjectRoot: alias}}}
	writeVersionedReceipt(t, root, active)
	for _, selected := range []string{alias, target} {
		existing, failure := existingProjectSelection(platformRootForTest(t, root), selected)
		if !existing || failure != nil {
			t.Fatalf("selection %q existing/failure = %v / %#v", selected, existing, failure)
		}
	}
}

// case: S-224.3
func TestExistingProjectSelectionDiagnosesReceiptWithoutIdentityFields(t *testing.T) {
	root := t.TempDir()
	project := t.TempDir()
	legacyReceipt := contracts.InstallReceipt{Schema: "kb.create.receipt/v2", ID: "receipt-legacy",
		Plan: contracts.ResolvedInstallPlan{Request: contracts.InstallRequest{PlatformRoot: root, ProjectRoot: project}}}
	data, err := json.Marshal(legacyReceipt)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(receipt.Path(root)), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(receipt.Path(root), data, 0o600); err != nil {
		t.Fatal(err)
	}
	existing, failure := existingProjectSelection(root, project)
	if existing || failure == nil || failure.Code != contracts.CodeReceiptUnavailable ||
		!strings.Contains(failure.Message, "receipt schema") ||
		!strings.Contains(failure.Hint, contracts.ShellCommand("kb-create", "doctor", "--platform-root", root)) ||
		!strings.Contains(failure.Hint, "reinstall") {
		t.Fatalf("legacy receipt diagnosis = existing %v, failure %#v", existing, failure)
	}
}
