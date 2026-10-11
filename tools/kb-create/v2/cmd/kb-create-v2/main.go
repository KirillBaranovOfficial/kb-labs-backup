// Package v2cli is the public V2 kb-create frontend. Both the released root
// binary and package-local tests enter through Execute, so there is exactly one
// launcher command surface and no legacy dispatcher to keep in sync.
package v2cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/kb-labs/clikit/diag"

	clikitcommand "github.com/kb-labs/clikit/command"
	"github.com/kb-labs/create/v2/artifacts"
	"github.com/kb-labs/create/v2/contracts"
	"github.com/kb-labs/create/v2/gateway"
	"github.com/kb-labs/create/v2/preflight"
	"github.com/kb-labs/create/v2/runtime"
	"github.com/kb-labs/create/v2/scenario"
	"github.com/kb-labs/create/v2/secrets"
	"github.com/kb-labs/create/v2/shim"
	"github.com/spf13/cobra"
)

var buildVersion = "dev"
var buildCommit = "none"

type repeatedValue []string

type applyCommandResult struct {
	OK         bool                     `json:"ok"`
	Operation  string                   `json:"operation"`
	Receipt    contracts.InstallReceipt `json:"receipt"`
	LogPath    string                   `json:"logPath"`
	Profile    string                   `json:"profile"`
	Services   []contracts.Service      `json:"services"`
	Shim       *shim.EnsureResult       `json:"shim,omitempty"`
	FirstAdmin *firstAdminCredential    `json:"firstAdmin,omitempty"`
}

type firstAdminCredential struct {
	Email     string `json:"email"`
	Password  string `json:"password"`
	ShownOnce bool   `json:"shownOnce"`
}

func (values *repeatedValue) String() string { return strings.Join(*values, ",") }
func (values *repeatedValue) Type() string   { return "string" }
func (values *repeatedValue) Set(value string) error {
	*values = append(*values, value)
	return nil
}

// SetVersionInfo exposes the build identity to diagnostics without changing
// the human --version output contract.
func SetVersionInfo(version, commit, _ string) {
	if version != "" {
		buildVersion = version
	}
	if commit != "" {
		buildCommit = commit
	}
}

// Execute runs the public kb-create Cobra tree through the shared clikit boundary.
func Execute() int {
	root := newRootCommand()
	root.Version = buildVersion
	root.SetVersionTemplate("kb-create {{.Version}}\n")
	return clikitcommand.Execute(root, os.Args[1:], os.Stdout, os.Stderr)
}

type directRequest struct {
	PlatformRoot, ProjectRoot, PlatformVersion, PlatformChannel, SDKVersion, ServiceProfile, GatewayURL, Plugins, Adapters, Providers, Policy string
	Offline                                                                                                                                   bool
}

type commandDependencies struct {
	Runtime          *runtime.Dependencies
	ArtifactRunner   artifacts.Runner
	NodeCheck        func() error
	InstallInput     io.Reader
	InstallOutput    io.Writer
	ProjectRegistrar func(string, string) (string, error)
	Command          *cobra.Command
	Executable       string
}

func productionCommandDependencies(command *cobra.Command) commandDependencies {
	return commandDependencies{
		Runtime:       &runtime.Dependencies{},
		NodeCheck:     func() error { return preflight.EnsureNode(nil) },
		InstallInput:  clikitcommand.FromCommand(command).Input(),
		InstallOutput: clikitcommand.FromCommand(command).ProgressWriter(),
		Command:       command,
		Executable:    executablePath(),
		ProjectRegistrar: func(platformRoot, projectRoot string) (string, error) {
			return registerInstalledProject(platformRoot, projectRoot, nil)
		},
	}
}

func updateCompatibilityDiag(failure *diag.Diag) *diag.Diag {
	if failure.Code == contracts.CodeIncompatibleComponents {
		failure.Code = contracts.CodeUpdateIncompatible
	}
	return failure
}

func recordedGatewayURL(active contracts.InstallReceipt) string {
	if active.Plan.Request.GatewayURL != "" {
		return active.Plan.Request.GatewayURL
	}
	origin, _ := gateway.OriginFromHealthURL(active.Verification.HostHealthURL)
	return origin
}

func planHasGateway(plan contracts.ResolvedInstallPlan) bool {
	for _, service := range plan.ServiceGraph.Services {
		if service.ID == "gateway" {
			return true
		}
	}
	return false
}

func progressWriteFailure(err error, next string) *diag.Diag {
	command := "kb-create " + next
	return contracts.NewDiag("KB_RUNTIME_UNEXPECTED", "Command progress could not be written.",
		fmt.Sprintf("Check that command output is writable, then run `%s` to verify the installation.", command), err)
}

// runInstall is the human frontend. It gathers answers, prints the shared
// resolver's plan, then invokes the same apply engine used by JSON callers.
func projectRegistrationFailure(platformRoot, projectRoot string, cause error) *diag.Diag {
	failure := contracts.NewDiag(contracts.CodeApplyFailed,
		"The platform is installed, but the selected project could not be registered.",
		fmt.Sprintf("Run `kb project add %q`, then verify with `kb project show %q`.", projectRoot, projectRoot), cause)
	failure.Meta = map[string]any{"platformRoot": platformRoot, "projectRoot": projectRoot}
	return failure
}

func projectCLIPath(platformRoot string) string {
	return filepath.Join(platformRoot, ".kb", "v2", "bin", "kb")
}

func registerProject(platformRoot, projectRoot string, registrar func(string) (string, error)) (string, error) {
	if registrar != nil {
		return registrar(projectRoot)
	}
	command := exec.Command(projectCLIPath(platformRoot), "project", "add", projectRoot, "--json")
	output, err := command.Output()
	if err != nil {
		var failure struct {
			Error *diag.Diag `json:"error"`
		}
		if json.Unmarshal(output, &failure) == nil && failure.Error != nil {
			return "", fmt.Errorf("%s [%s]. %s", failure.Error.Message, failure.Error.Code, failure.Error.Hint)
		}
		return "", fmt.Errorf("the project registry command failed (%s)", err)
	}
	var result struct {
		OK   bool `json:"ok"`
		Data struct {
			Project struct {
				ID   string `json:"id"`
				Path string `json:"path"`
			} `json:"project"`
		} `json:"data"`
	}
	if err := json.Unmarshal(output, &result); err != nil {
		return "", fmt.Errorf("decode project registration result: %w", err)
	}
	if !result.OK || result.Data.Project.ID == "" || result.Data.Project.Path != projectRoot {
		return "", fmt.Errorf("project registration returned an unexpected result")
	}
	return result.Data.Project.ID, nil
}

type humanProgress struct {
	out     io.Writer
	started map[string]time.Time
}

func (p *humanProgress) Emit(event runtime.ProgressEvent) error {
	if event.Start {
		p.started[event.Phase] = time.Now()
		if _, err := fmt.Fprintf(p.out, "  … %s\n", event.Phase); err != nil {
			delete(p.started, event.Phase)
			return progressWriteFailure(err, "apply")
		}
		return nil
	}
	started, ok := p.started[event.Phase]
	if !ok {
		return nil
	}
	if _, err := fmt.Fprintf(p.out, "  ✓ %s (%s)\n", event.Phase, time.Since(started).Round(time.Millisecond)); err != nil {
		return progressWriteFailure(err, "apply")
	}
	delete(p.started, event.Phase)
	return nil
}

// runStatus reports the host of the receipt-owned installation without
// resolving a new release, so it is safe for stable, canary and exact-version
// installations: it describes the immutable decision that was actually applied.
func requestWithGeneratedSecrets(request contracts.InstallRequest, artifacts []contracts.Artifact) (contracts.InstallRequest, bool, error) {
	known := make(map[string]bool, len(request.SecretInputs))
	for _, id := range request.SecretInputs {
		known[id] = true
	}
	changed := false
	for _, artifact := range artifacts {
		for _, requirement := range artifact.Config {
			if requirement.Secret && requirement.Generate && !known[requirement.ID] {
				request.SecretInputs = append(request.SecretInputs, requirement.ID)
				known[requirement.ID] = true
				changed = true
			}
		}
	}
	if changed {
		var err error
		request, err = request.Normalize()
		if err != nil {
			return contracts.InstallRequest{}, false, err
		}
	}
	return request, changed, nil
}

func defaultPlatformRoot() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, "kb-platform")
}

func populateSecrets(store secrets.Store, mappings string) error {
	if strings.TrimSpace(mappings) == "" {
		return nil
	}
	for _, pair := range strings.Split(mappings, ",") {
		name, environment, ok := strings.Cut(strings.TrimSpace(pair), "=")
		if !ok || strings.TrimSpace(name) == "" || strings.TrimSpace(environment) == "" {
			return fmt.Errorf("secret mapping %q must be requirement=ENV_VAR", pair)
		}
		value, exists := os.LookupEnv(environment)
		if !exists || value == "" {
			return fmt.Errorf("environment variable %q is not set", environment)
		}
		if err := store.Put(name, value); err != nil {
			return err
		}
	}
	return nil
}

func compileScenario(id, answers string, resume bool, base contracts.InstallRequest) (contracts.InstallRequest, error) {
	definition, err := scenario.Load(id)
	if err != nil {
		return contracts.InstallRequest{}, err
	}
	state, err := scenario.New(definition)
	if resume {
		state, err = scenario.LoadState(base.PlatformRoot, definition)
	}
	if err != nil {
		return contracts.InstallRequest{}, err
	}
	if answers != "" {
		var values map[string]json.RawMessage
		if err := json.Unmarshal([]byte(answers), &values); err != nil {
			return contracts.InstallRequest{}, fmt.Errorf("decode scenario answers: %w", err)
		}
		for field, value := range values {
			state, err = scenario.Answer(definition, state, field, value)
			if err != nil {
				return contracts.InstallRequest{}, err
			}
		}
	}
	if err := scenario.SaveState(base.PlatformRoot, definition, state); err != nil {
		return contracts.InstallRequest{}, err
	}
	request, err := scenario.Compile(definition, state, base)
	if err != nil {
		return contracts.InstallRequest{}, err
	}
	request.ScenarioStateDigest, err = scenario.StateDigest(definition, state)
	return request, err
}

func (value directRequest) normalize() (contracts.InstallRequest, error) {
	request := contracts.InstallRequest{Schema: contracts.RequestSchema, PlatformRoot: value.PlatformRoot, ProjectRoot: value.ProjectRoot, Platform: contracts.VersionSelector{Version: value.PlatformVersion, Channel: contracts.Channel(value.PlatformChannel)}, SDK: contracts.VersionSelector{Version: value.SDKVersion}, ServiceProfile: value.ServiceProfile, GatewayURL: value.GatewayURL, Policy: contracts.CompatibilityPolicy(value.Policy), Source: contracts.SourceRegistry}
	if value.Offline {
		request.Source = contracts.SourceOffline
	}
	plugins, err := parseComponents(value.Plugins)
	if err != nil {
		return contracts.InstallRequest{}, err
	}
	adapters, err := parseComponents(value.Adapters)
	if err != nil {
		return contracts.InstallRequest{}, err
	}
	request.Plugins, request.Adapters = plugins, adapters
	request.ProviderPreferences, err = parseProviders(value.Providers)
	if err != nil {
		return contracts.InstallRequest{}, err
	}
	return request.Normalize()
}

func configuredFirstAdmin(request contracts.InstallRequest) (string, bool) {
	if request.Values["gateway.access.mode"] != `"secured"` {
		return "", false
	}
	var email string
	if err := json.Unmarshal([]byte(request.Values["gateway.bootstrap.adminEmail"]), &email); err != nil || email == "" {
		return "", false
	}
	return email, true
}

func parseProviders(value string) (map[string]string, error) {
	preferences := map[string]string{}
	for _, item := range strings.Split(value, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		capability, adapter, ok := strings.Cut(item, "=")
		capability, adapter = strings.TrimSpace(capability), strings.TrimSpace(adapter)
		if !ok || strings.TrimSpace(capability) == "" || strings.TrimSpace(adapter) == "" {
			return nil, fmt.Errorf("provider %q must be capability=adapter", item)
		}
		if _, exists := preferences[capability]; exists {
			return nil, fmt.Errorf("provider for capability %q was specified more than once", capability)
		}
		preferences[capability] = adapter
	}
	return preferences, nil
}

func parseComponents(value string) ([]contracts.ComponentRequest, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	items := strings.Split(value, ",")
	result := make([]contracts.ComponentRequest, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			return nil, fmt.Errorf("component list contains empty item")
		}
		// Scoped npm-style component IDs contain an @ themselves. Only the last
		// separator denotes an optional immutable version pin.
		id, version, pinned := item, "", false
		if at := strings.LastIndex(item, "@"); at > 0 {
			id, version, pinned = item[:at], item[at+1:], true
		}
		if strings.TrimSpace(id) == "" {
			return nil, fmt.Errorf("component ID is required")
		}
		component := contracts.ComponentRequest{ID: id}
		if pinned {
			if version == "" {
				return nil, fmt.Errorf("component %q has empty version", id)
			}
			component.Version.Version = version
		}
		result = append(result, component)
	}
	return result, nil
}

func failure(code, message, hint string, cause error) *diag.Diag {
	return contracts.NewDiag(code, message, hint, cause)
}
