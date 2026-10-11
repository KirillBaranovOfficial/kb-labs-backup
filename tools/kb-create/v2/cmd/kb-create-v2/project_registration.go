package v2cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/kb-labs/clikit/diag"
	"github.com/kb-labs/clikit/result"
	"github.com/kb-labs/create/v2/contracts"
	"github.com/kb-labs/create/v2/gateway"
	"github.com/kb-labs/create/v2/receipt"
	"github.com/kb-labs/create/v2/secrets"
)

type loginTokens struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
	ExpiresIn    int    `json:"expiresIn"`
	TenantID     string `json:"tenantId"`
}

type projectRegistrationResponse struct {
	ID   string `json:"id"`
	Path string `json:"path"`
}

func registerProject(platformRoot, projectRoot string) (string, error) {
	active, err := receipt.Read(platformRoot)
	if err != nil {
		return "", registrationBoundaryFailure("KB_INSTALL_RECEIPT_UNAVAILABLE", err)
	}
	origin, err := gateway.OriginFromHealthURL(active.Verification.HostHealthURL)
	if err != nil {
		return "", registrationBoundaryFailure("KB_HOST_SPEC_INVALID", err)
	}
	accessToken := ""
	if adminEmail, secured := configuredFirstAdmin(active.Plan.Request); secured {
		password, present, readErr := (secrets.Store{PlatformRoot: platformRoot}).Get("gateway.bootstrap.password")
		if readErr != nil || !present {
			return "", registrationBoundaryFailure("KB_INSTALL_SECRET_INPUT_INVALID", fmt.Errorf("the first administrator password is unavailable"))
		}
		tenantID := unquoteRequestValue(active.Plan.Request.Values["gateway.bootstrap.tenantId"])
		tokens, loginErr := loginForProjectRegistration(origin, adminEmail, password, tenantID)
		if loginErr != nil {
			return "", loginErr
		}
		accessToken = tokens.AccessToken
	}
	return addProjectThroughGateway(origin, projectRoot, accessToken)
}

func existingProjectSelection(platformRoot, selected string) (bool, *diag.Diag) {
	if _, err := os.Stat(receipt.Path(platformRoot)); os.IsNotExist(err) {
		return false, nil
	} else if err != nil {
		return false, contracts.NewDiag(contracts.CodeReceiptUnavailable, "The active install receipt could not be checked.", "Check the platform root permissions, then run `kb-create doctor`.", err)
	}
	active, err := receipt.Read(platformRoot)
	if err != nil {
		return false, contracts.NewDiag(contracts.CodeReceiptUnavailable, "The active install receipt could not be read.", "Run `kb-create doctor --platform-root PATH` to inspect the installation.", err)
	}
	if selected == "" {
		return true, nil
	}
	selectedPath, err := filepath.Abs(selected)
	if err != nil {
		return true, contracts.NewDiag(contracts.CodeInputRequired, "The selected project path could not be resolved.", "Pass an existing project directory with `--project-root PATH`.", err)
	}
	activePath, err := filepath.Abs(active.Plan.Request.ProjectRoot)
	if err != nil || filepath.Clean(selectedPath) != filepath.Clean(activePath) {
		failure := contracts.NewDiag(contracts.CodeProjectSelectionChanged,
			"The selected project differs from the project recorded by this installation.",
			projectRegistrationHint(active, selected), nil)
		failure.ProcessExitCode = diag.ExitInput
		return true, failure
	}
	return true, nil
}

func projectRegistrationHint(active contracts.InstallReceipt, projectRoot string) string {
	if email, secured := configuredFirstAdmin(active.Plan.Request); secured {
		origin, _ := gateway.OriginFromHealthURL(active.Verification.HostHealthURL)
		return fmt.Sprintf("Run `kb auth login --gateway-url %q --email %q`, then `kb project add %q`, then `kb project show %q`.", origin, email, projectRoot, projectRoot)
	}
	return fmt.Sprintf("Run `kb project add %q`, then `kb project show %q`.", projectRoot, projectRoot)
}

func unquoteRequestValue(value string) string {
	var text string
	if json.Unmarshal([]byte(value), &text) == nil {
		return text
	}
	return ""
}

func loginForProjectRegistration(origin, email, password, tenantID string) (loginTokens, error) {
	credentials := map[string]string{"email": email, "password": password}
	if tenantID != "" {
		credentials["tenantId"] = tenantID
	}
	body, err := json.Marshal(credentials)
	if err != nil {
		return loginTokens{}, registrationBoundaryFailure("KB_RUNTIME_UNEXPECTED", err)
	}
	responseBody, status, err := postRegistrationRequest(origin+"/auth/login/cli", body, "")
	if err != nil {
		return loginTokens{}, registrationBoundaryFailure("KB_AUTH_GATEWAY_UNREACHABLE", err)
	}
	if status < 200 || status >= 300 {
		var raw json.RawMessage
		if failure, decodeErr := result.DecodeResult(responseBody, &raw); failure != nil {
			if _, catalogued := diag.LookupCatalog(failure.Code); !catalogued {
				return loginTokens{}, registrationBoundaryFailure("KB_RUNTIME_GATEWAY_PROTOCOL_FAILURE", fmt.Errorf("Gateway returned an unknown login diagnostic"))
			}
			return loginTokens{}, failure
		} else if decodeErr != nil {
			return loginTokens{}, registrationBoundaryFailure("KB_RUNTIME_GATEWAY_PROTOCOL_FAILURE", decodeErr)
		}
		return loginTokens{}, registrationBoundaryFailure("KB_RUNTIME_GATEWAY_PROTOCOL_FAILURE", fmt.Errorf("Gateway returned HTTP %d without a diagnostic", status))
	}
	var tokens loginTokens
	if err := json.Unmarshal(responseBody, &tokens); err != nil || tokens.AccessToken == "" || tokens.RefreshToken == "" || tokens.ExpiresIn <= 0 {
		return loginTokens{}, registrationBoundaryFailure("KB_RUNTIME_GATEWAY_PROTOCOL_FAILURE", fmt.Errorf("Gateway returned an invalid CLI login response"))
	}
	return tokens, nil
}

func addProjectThroughGateway(origin, projectRoot, accessToken string) (string, error) {
	body, err := json.Marshal(map[string]string{"path": projectRoot})
	if err != nil {
		return "", registrationBoundaryFailure("KB_RUNTIME_UNEXPECTED", err)
	}
	responseBody, status, err := postRegistrationRequest(origin+"/api/v1/projects", body, accessToken)
	if err != nil {
		return "", registrationBoundaryFailure("KB_HOST_UNREACHABLE", err)
	}
	var payload projectRegistrationResponse
	failure, decodeErr := result.DecodeResult(responseBody, &payload)
	if failure != nil {
		if _, catalogued := diag.LookupCatalog(failure.Code); !catalogued {
			return "", registrationBoundaryFailure("KB_RUNTIME_GATEWAY_PROTOCOL_FAILURE", fmt.Errorf("Gateway returned an unknown project diagnostic"))
		}
		return "", failure
	}
	if decodeErr != nil || status < 200 || status >= 300 || payload.ID == "" || filepath.Clean(payload.Path) != filepath.Clean(projectRoot) {
		if decodeErr == nil {
			decodeErr = fmt.Errorf("Gateway returned an invalid project registration response")
		}
		return "", registrationBoundaryFailure("KB_RUNTIME_GATEWAY_PROTOCOL_FAILURE", decodeErr)
	}
	return payload.ID, nil
}

func postRegistrationRequest(target string, body []byte, accessToken string) ([]byte, int, error) {
	parsed, err := url.Parse(target)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, 0, fmt.Errorf("the applied Gateway URL is invalid")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return nil, 0, fmt.Errorf("the Gateway request could not be created")
	}
	request.Header.Set("Content-Type", "application/json")
	if accessToken != "" {
		request.Header.Set("Authorization", "Bearer "+accessToken)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, 0, err
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return nil, response.StatusCode, fmt.Errorf("the Gateway response could not be read")
	}
	return responseBody, response.StatusCode, nil
}

func registrationBoundaryFailure(code string, cause error) *diag.Diag {
	return diag.New(code, cause.Error())
}

func projectRegistrationFailure(platformRoot, projectRoot string, cause error) *diag.Diag {
	active, _ := receipt.Read(platformRoot)
	hint := projectRegistrationHint(active, projectRoot)
	causeCode := contracts.CodeProjectRegistrationFailed
	var typed *diag.Diag
	if errors.As(cause, &typed) {
		causeCode = typed.Code
	}
	failure := contracts.NewDiag(contracts.CodeProjectRegistrationFailed,
		"The platform is installed, but the selected project is not registered.", hint, cause)
	failure.Meta = map[string]any{"platformRoot": platformRoot, "projectRoot": projectRoot, "causeCode": causeCode}
	return failure
}
