package doctor

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kb-labs/clikit/result"
	"github.com/kb-labs/create/v2/contracts"
	"github.com/kb-labs/create/v2/host"
	"github.com/kb-labs/create/v2/receipt"
)

// Doctor answers two different questions and keeps them apart, because they
// have different owners and different fixes:
//
//   - Installation: is what the launcher put on disk complete and runnable
//     (receipt, generated config, host binary, launcher state)? The launcher
//     owns it; "kb-create doctor --fix" and "kb-create apply" repair it.
//   - Platform: is the running host and what it serves healthy (process,
//     upstreams, adapters, sign-in)? The platform owns it; the fix is a
//     restart, a config value or "kb-create doctor --recover".
//
// An installation problem makes the platform section meaningless, so it is
// still reported, but from what can be observed.

// Check is one named observation.
type Check struct {
	ID             string `json:"id"`
	Code           string `json:"code,omitempty"`
	OK             bool   `json:"ok"`
	Detail         string `json:"detail,omitempty"`
	Hint           string `json:"hint,omitempty"`
	Slot           string `json:"slot,omitempty"`
	Mode           string `json:"mode,omitempty"`
	Implementation string `json:"implementation,omitempty"`
}

// Section groups the checks of one question.
type Section struct {
	OK     bool    `json:"ok"`
	Checks []Check `json:"checks"`
}

// NewSection is healthy exactly when every check is.
func NewSection(checks []Check) Section {
	section := Section{OK: true, Checks: checks}
	for _, check := range checks {
		if !check.OK {
			section.OK = false
		}
	}
	return section
}

// InspectInstallation checks the launcher-owned side. A missing or unreadable
// receipt is a failed check, not an error: it is precisely what doctor reports.
func InspectInstallation(platformRoot string, state host.State) Section {
	var checks []Check
	active, err := receipt.Read(platformRoot)
	if err != nil {
		checks = append(checks, Check{ID: "receipt", Detail: "The active installation receipt cannot be read: " + err.Error(), Hint: "Run \"kb-create apply\" to install, or restore a snapshot with \"kb-create rollback\"."})
		return NewSection(append(checks, stateCheck(state)))
	}
	checks = append(checks, Check{ID: "receipt", OK: true, Detail: "platform " + active.Plan.ServiceGraph.PlatformVersion + ", receipt " + active.ID})
	if _, err := os.Stat(filepath.Join(platformRoot, ".kb", "kb.config.jsonc")); err != nil {
		checks = append(checks, Check{ID: "config", Detail: "The generated runtime config is missing.", Hint: "Run \"kb-create apply\" again to regenerate it."})
	} else {
		checks = append(checks, Check{ID: "config", OK: true})
	}
	checks = append(checks, hostSpecCheck(platformRoot, active))
	checks = append(checks, stateCheck(state))
	return NewSection(checks)
}

func hostSpecCheck(platformRoot string, active contracts.InstallReceipt) Check {
	if active.Plan.Host == nil {
		return Check{ID: "host-spec", Detail: "The receipt declares no host; it predates the host-only launcher.", Hint: "Reinstall the platform with \"kb-create apply\"."}
	}
	spec, err := host.NewSpec(platformRoot, *active.Plan.Host)
	if err != nil {
		return Check{ID: "host-spec", Code: contracts.CodeHostSpecInvalid, Detail: "The host specification is invalid: " + err.Error() + ".", Hint: fmt.Sprintf("Run `kb-create apply --platform-root %q` to regenerate the installed host specification.", platformRoot)}
	}
	if _, err := exec.LookPath(spec.Command); err != nil {
		return Check{ID: "host-spec", Code: contracts.CodeHostSpecInvalid, Detail: "The host command " + spec.Command + " is not installed.", Hint: fmt.Sprintf("Run `kb-create apply --platform-root %q` or `kb-create rollback --platform-root %q` to restore the platform files.", platformRoot, platformRoot)}
	}
	return Check{ID: "host-spec", OK: true, Detail: spec.Command}
}

func stateCheck(state host.State) Check {
	if err := state.Ensure(); err != nil {
		return Check{ID: "launcher-state", Detail: "Cannot write to " + state.Dir + ": " + err.Error(), Hint: "Fix the directory permissions or set KB_HOME to a writable directory."}
	}
	return Check{ID: "launcher-state", OK: true, Detail: state.Dir}
}

// InspectPlatform checks the running host: its process, the gateway health
// document (upstreams and adapters) and sign-in readiness. A stopped host is a
// finding with its own fix, not a crash of the check.
func InspectPlatform(ctx context.Context, status host.Status, client *http.Client, bindings []contracts.ProviderBinding) Section {
	if client == nil {
		client = &http.Client{Timeout: 3 * time.Second}
	}
	switch status.State {
	case host.StateStopped:
		return NewSection([]Check{
			{ID: "host-process", Detail: "The host is not running.", Hint: "Run \"kb-create start\"."},
			{ID: "adapters", Detail: "Adapter status cannot be checked while the host is stopped.", Hint: "Run \"kb-create start\"."},
		})
	case host.StateUnhealthy:
		return NewSection([]Check{{ID: "host-process", Detail: fmt.Sprintf("The host process %d is running but does not answer its health check.", status.PID), Hint: "Run \"kb-create restart\"; read the host log if it persists."}})
	}
	checks := []Check{{ID: "host-process", OK: true, Detail: fmt.Sprintf("pid %d", status.PID)}}
	checks = append(checks, healthChecks(ctx, client, status.HealthURL)...)
	checks = append(checks, adapterChecks(ctx, client, status.HealthURL, bindings)...)
	checks = append(checks, authChecks(ctx, client, status.HealthURL)...)
	return NewSection(checks)
}

// adapterChecks reads the gateway's runtime registry rather than inferring
// providers from the install manifest. This reports what the host loaded.
func adapterChecks(ctx context.Context, client *http.Client, healthURL string, bindings []contracts.ProviderBinding) []Check {
	parsed, err := url.Parse(healthURL)
	if err != nil {
		return []Check{{ID: "adapters", Detail: "Adapter status cannot be checked because the host health URL is invalid."}}
	}
	parsed.Path = "/health/adapters"
	var statuses []struct {
		Slot           string `json:"slot"`
		Mode           string `json:"mode"`
		Implementation string `json:"implementation"`
	}
	status, err := getJSON(ctx, client, parsed.String(), &statuses)
	if err != nil || status != http.StatusOK {
		return []Check{{ID: "adapters", Detail: "Adapter status cannot be read from the running host.", Hint: "Run \"kb-create restart\"; read the host log if it persists."}}
	}
	if len(statuses) == 0 {
		return []Check{{ID: "adapters", Detail: "The running host has not reported any adapter slots."}}
	}
	expected := make(map[string]string, len(bindings))
	for _, binding := range bindings {
		if healthReportedSlot(binding.Capability) {
			expected[binding.Capability] = binding.Package
		}
	}
	checks := make([]Check, 0, len(statuses)+len(bindings))
	seen := make(map[string]bool, len(statuses))
	for _, adapter := range statuses {
		seen[adapter.Slot] = true
		provider, hasPersistentProvider := expected[adapter.Slot]
		fallback := hasPersistentProvider && (adapter.Mode == "inmemory" || adapter.Mode == "noop")
		detail := fmt.Sprintf("%s is loaded in %s mode using %s.", adapter.Slot, adapter.Mode, adapter.Implementation)
		hint := ""
		if fallback {
			detail = fmt.Sprintf("%s has a configured persistent provider (%s) but loaded the %s fallback.", adapter.Slot, provider, adapter.Mode)
			hint = fmt.Sprintf("From a project using this platform, run `kb adapter bind %s %s`.", adapter.Slot, provider)
		}
		checks = append(checks, Check{
			ID: "adapter:" + adapter.Slot, OK: !fallback,
			Slot: adapter.Slot, Mode: adapter.Mode, Implementation: adapter.Implementation,
			Detail: detail, Hint: hint,
		})
	}
	for slot, provider := range expected {
		if !seen[slot] {
			checks = append(checks, Check{ID: "adapter:" + slot, Slot: slot,
				Detail: fmt.Sprintf("%s has a configured persistent provider (%s) but was not reported by the host.", slot, provider),
				Hint:   fmt.Sprintf("From a project using this platform, run `kb adapter bind %s %s`.", slot, provider)})
		}
	}
	return checks
}

func healthReportedSlot(slot string) bool {
	switch slot {
	case "logger", "analytics", "vectorStore", "llm", "embeddings", "cache", "config", "storage",
		"eventBus", "invoke", "documentDatabase", "kvStore", "logs", "artifacts", "snapshotManager", "notifier":
		return true
	default:
		return false
	}
}

func getJSON(ctx context.Context, client *http.Client, target string, out any) (int, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return 0, err
	}
	response, err := client.Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return response.StatusCode, err
	}
	if response.StatusCode != http.StatusOK {
		return response.StatusCode, nil
	}
	if err := result.DecodeSuccessResult(data, out); err != nil {
		return response.StatusCode, err
	}
	return response.StatusCode, nil
}

// healthChecks reads the gateway health document the host serves: every
// upstream and adapter it reports becomes one check.
func healthChecks(ctx context.Context, client *http.Client, healthURL string) []Check {
	var document struct {
		Upstreams map[string]struct {
			Status string `json:"status"`
		} `json:"upstreams"`
		Adapters map[string]struct {
			Available *bool `json:"available"`
		} `json:"adapters"`
		ProjectRuntimes *struct {
			Active int `json:"active"`
			Limit  int `json:"limit"`
		} `json:"projectRuntimes"`
	}
	status, err := getJSON(ctx, client, healthURL, &document)
	if err != nil || status != http.StatusOK {
		return []Check{{ID: "health", Detail: "The health document cannot be read.", Hint: "Run \"kb-create restart\"; read the host log if it persists."}}
	}
	var checks []Check
	for _, name := range sortedKeys(document.Upstreams) {
		up := document.Upstreams[name].Status == "up"
		check := Check{ID: "upstream:" + name, OK: up}
		if !up {
			check.Detail = "The upstream " + name + " reports " + document.Upstreams[name].Status + "."
			check.Hint = "Run \"kb-create restart\"; read the host log if it persists."
		}
		checks = append(checks, check)
	}
	for _, name := range sortedKeys(document.Adapters) {
		available := document.Adapters[name].Available == nil || *document.Adapters[name].Available
		check := Check{ID: "adapter:" + name, OK: available}
		if !available {
			check.Detail = "The adapter " + name + " is not available."
			check.Hint = "Check the adapter configuration in the platform config."
		}
		checks = append(checks, check)
	}
	if document.ProjectRuntimes != nil {
		checks = append(checks, Check{
			ID: "project-runtimes", OK: true,
			Detail: fmt.Sprintf("Project runtimes start on demand; %d of %d active.", document.ProjectRuntimes.Active, document.ProjectRuntimes.Limit),
		})
	}
	if len(checks) == 0 {
		checks = append(checks, Check{ID: "health", OK: true})
	}
	return checks
}

// authChecks reads the gateway's sign-in readiness (served to local operators
// only). No route or a non-200 answer means there is nothing to report.
func authChecks(ctx context.Context, client *http.Client, healthURL string) []Check {
	parsed, err := url.Parse(healthURL)
	if err != nil {
		return nil
	}
	parsed.Path = "/health/auth"
	var readiness struct {
		AuthEnabled *bool `json:"authEnabled"`
		Issues      []struct {
			Code     string `json:"code"`
			Severity string `json:"severity"`
			Message  string `json:"message"`
			Hint     string `json:"hint"`
		} `json:"issues"`
	}
	status, err := getJSON(ctx, client, parsed.String(), &readiness)
	if status != http.StatusOK {
		return nil
	}
	if err != nil {
		return []Check{{ID: "auth", Detail: "Auth readiness returned an invalid result envelope.", Hint: "Run \"kb-create restart\"; read the host log if it persists."}}
	}
	checks := make([]Check, 0, len(readiness.Issues)+1)
	for _, issue := range readiness.Issues {
		if issue.Code == "no_active_admin" && readiness.AuthEnabled != nil && !*readiness.AuthEnabled {
			continue
		}
		checks = append(checks, Check{ID: "auth:" + issue.Code, OK: issue.Severity != "error", Detail: issue.Message, Hint: strings.TrimSpace(issue.Hint)})
	}
	if len(checks) == 0 {
		checks = append(checks, Check{ID: "auth", OK: true})
	}
	return checks
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
