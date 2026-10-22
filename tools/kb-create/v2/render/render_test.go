package render

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kb-labs/create/v2/contracts"
)

func TestBuildKeepsSecretValuesOutOfConfigAndChecksTheirServices(t *testing.T) {
	plan := testPlan(t.TempDir())
	plan.ConfigPatches = []contracts.ConfigPatch{{Owner: "manifest:token", Environment: "TOKEN", Services: []string{"gateway"}}}
	output, err := Build(plan)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(output.Config), "TOKEN") {
		t.Fatalf("the config must not mention a secret variable: %s", output.Config)
	}
	plan.ConfigPatches[0].Services = []string{"missing"}
	if _, err := Build(plan); err == nil {
		t.Fatal("a secret bound to a service that is not in the plan must be rejected")
	}
}

func TestBuildOwnsLocalDeploymentTopology(t *testing.T) {
	plan := testPlan(t.TempDir())
	plan.ConfigPatches = []contracts.ConfigPatch{{
		Path:  "/platform/deployment/topology",
		JSON:  `"cloud"`,
		Owner: "manifest:platform.deployment.topology",
	}}
	output, err := Build(plan)
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Platform struct {
			Deployment struct {
				Topology string `json:"topology"`
			} `json:"deployment"`
		} `json:"platform"`
	}
	if err := json.Unmarshal(output.Config, &config); err != nil {
		t.Fatal(err)
	}
	if config.Platform.Deployment.Topology != "local" {
		t.Fatalf("topology = %q, want local; config: %s", config.Platform.Deployment.Topology, output.Config)
	}
}

func hostSection(t *testing.T, config []byte) map[string]any {
	t.Helper()
	var parsed struct {
		Host map[string]any `json:"host"`
	}
	if err := json.Unmarshal(config, &parsed); err != nil {
		t.Fatal(err)
	}
	return parsed.Host
}

// The host's policy overrides gateway access with host.auth, so the installer
// must write the value that matches the selected access mode: a "local" install
// is no-login (off); everything else, including no answer, is the gateway's
// secured default (on).
func TestBuildDerivesHostAuthFromTheAccessMode(t *testing.T) {
	cases := map[string]struct {
		patches []contracts.ConfigPatch
		want    string
	}{
		"local install has no login":       {[]contracts.ConfigPatch{{Path: "/gateway/access/mode", JSON: `"local"`, Owner: "manifest:gateway.access.mode"}}, "off"},
		"secured install requires login":   {[]contracts.ConfigPatch{{Path: "/gateway/access/mode", JSON: `"secured"`, Owner: "manifest:gateway.access.mode"}}, "on"},
		"no answer is the secured default": {nil, "on"},
		"an explicit host.auth is kept":    {[]contracts.ConfigPatch{{Path: "/gateway/access/mode", JSON: `"local"`, Owner: "manifest:a"}, {Path: "/host/auth", JSON: `"on"`, Owner: "manifest:b"}}, "on"},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			plan := testPlan(t.TempDir())
			plan.ConfigPatches = testCase.patches
			output, err := Build(plan)
			if err != nil {
				t.Fatal(err)
			}
			if got := hostSection(t, output.Config)["auth"]; got != testCase.want {
				t.Fatalf("host.auth = %v, want %s\n%s", got, testCase.want, output.Config)
			}
		})
	}
}

// Static gateway routes belong to the host, which creates routes only for its
// internal modules. REST and workflow are project-runtime services; the host
// starts them lazily behind project routing. Other legacy service packages
// are not started by the host. Package presence cannot create direct routes.
func TestBuildDoesNotRenderLegacyServiceGraphAsGatewayUpstreams(t *testing.T) {
	plan := testPlan(t.TempDir())
	plan.ServiceGraph.Services = append(plan.ServiceGraph.Services,
		contracts.Service{ID: "rest", Command: "serve", Port: 5050},
		contracts.Service{ID: "workflow", Command: "serve", Port: 7778},
	)
	output, err := Build(plan)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]json.RawMessage
	if err := json.Unmarshal(output.Config, &config); err != nil {
		t.Fatal(err)
	}
	if _, ok := config["gateway"]; ok {
		t.Fatalf("renderer emitted static gateway config for host-managed services: %s", output.Config)
	}
}

// Resolved bindings carry exact "pkg@version" specs (plan/receipt
// reproducibility) but the runtime adapter loader resolves the configured
// value as a module name from node_modules; a version suffix made every
// service fail at boot with "Failed to load adapter module pkg@version".
//
// Regression: the platform bundle ships a manifest default for
// /platform/adapters (a JSON patch, only serviceTransport), while the resolver
// separately binds capabilities that plugins require (cache, storage). The
// render used a typed map[string]string for the resolved side, so merging the
// map[string]any default degraded from a deep merge to a replacement and the
// resolved cache/storage bindings vanished from the rendered config.
func TestBuildKeepsResolvedAdapterBindingsAlongsideManifestDefault(t *testing.T) {
	plan := testPlan(t.TempDir())
	plan.ConfigPatches = []contracts.ConfigPatch{
		{Path: "/platform/adapters/cache", Value: "@kb-labs/adapters-state-broker@2.0.0", Owner: "adapter:state-broker"},
		{Path: "/platform/adapters/storage", Value: "@kb-labs/data-store@2.0.0", Owner: "adapter:disk-io-storage"},
		{Path: "/platform/adapters", JSON: `{"serviceTransport":"@kb-labs/adapters-service-transport-http","cache":"@kb-labs/adapters-redis"}`, Owner: "manifest:platform.adapters"},
	}
	output, err := Build(plan)
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Platform struct {
			Adapters map[string]string `json:"adapters"`
		} `json:"platform"`
	}
	if err := json.Unmarshal(output.Config, &config); err != nil {
		t.Fatal(err)
	}
	got := config.Platform.Adapters
	if got["serviceTransport"] != "@kb-labs/adapters-service-transport-http" {
		t.Fatalf("manifest default lost: %v", got)
	}
	if got["storage"] != "@kb-labs/data-store" {
		t.Fatalf("resolved storage binding lost or not a bare module name: %v", got)
	}
	if got["cache"] != "@kb-labs/adapters-state-broker" {
		t.Fatalf("resolved cache binding must win over a manifest default on the same key, got %q (all: %v)", got["cache"], got)
	}
}

// case: S-054.1
func TestBuildPreservesResolvedAdapterExportSubpath(t *testing.T) {
	plan := testPlan(t.TempDir())
	plan.ConfigPatches = []contracts.ConfigPatch{
		{Path: "/platform/adapters/kvStore", Value: "@kb-labs/adapters-sqlite@2.0.0", Owner: "adapter:sqlite-stores"},
		{Path: "/platform/adapters", JSON: `{"kvStore":"@kb-labs/adapters-sqlite/kv"}`, Owner: "manifest:platform.adapters"},
	}
	output, err := Build(plan)
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Platform struct {
			Adapters map[string]string `json:"adapters"`
		} `json:"platform"`
	}
	if err := json.Unmarshal(output.Config, &config); err != nil {
		t.Fatal(err)
	}
	if got := config.Platform.Adapters["kvStore"]; got != "@kb-labs/adapters-sqlite/kv" {
		t.Fatalf("rendered kvStore module specifier = %q, want package export subpath", got)
	}
}

// case: S-179.10
func TestBuildNeverRendersTheWorkspaceDevelopmentModeMarker(t *testing.T) {
	plan := testPlan(t.TempDir())
	plan.ConfigPatches = []contracts.ConfigPatch{{Path: "/platform/mode", JSON: `"development"`, Owner: "manifest:platform.mode"}}
	output, err := Build(plan)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal(output.Config, &config); err != nil {
		t.Fatal(err)
	}
	platformConfig, _ := config["platform"].(map[string]any)
	if mode, ok := platformConfig["mode"]; ok {
		t.Fatalf("installed platform config must not contain workspace development mode, got %v", mode)
	}
}

func TestWriteProducesTheRuntimeConfigAndNoServiceManagerFile(t *testing.T) {
	root := t.TempDir()
	if _, err := Write(testPlan(root)); err != nil {
		t.Fatal(err)
	}
	relative := ".kb/kb.config.jsonc"
	data, err := os.ReadFile(filepath.Join(root, relative))
	if err != nil || len(data) == 0 {
		t.Fatalf("%s = %q, %v", relative, data, err)
	}
	if _, err := os.Stat(filepath.Join(root, relative+".tmp")); !os.IsNotExist(err) {
		t.Fatalf("temporary projection remains for %s", relative)
	}
	if _, err := os.Stat(filepath.Join(root, ".kb", "devservices.yaml")); !os.IsNotExist(err) {
		t.Fatalf("the user path must not render devservices.yaml: %v", err)
	}
}

func TestWritePreservesSharedConfigLinkAndUpdatesItsInstallRootTarget(t *testing.T) {
	installRoot := t.TempDir()
	versionRoot := filepath.Join(installRoot, "versions", "v1")
	if err := os.MkdirAll(filepath.Join(versionRoot, ".kb"), 0o700); err != nil {
		t.Fatal(err)
	}
	stateRoot := filepath.Join(installRoot, "state")
	if err := os.MkdirAll(stateRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	configLink := filepath.Join(versionRoot, ".kb", ConfigFilename)
	configTarget := filepath.Join(stateRoot, ConfigFilename)
	relativeTarget, err := filepath.Rel(filepath.Dir(configLink), configTarget)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(relativeTarget, configLink); err != nil {
		t.Fatal(err)
	}
	plan := contracts.ResolvedInstallPlan{Schema: contracts.ResolvedPlanSchema,
		Request:      contracts.InstallRequest{PlatformRoot: versionRoot},
		ServiceGraph: contracts.ServiceGraph{PlatformVersion: "1.0.0"},
		Host:         &contracts.HostSpec{Command: "host", HealthURL: "http://127.0.0.1:4000/health"}}
	if _, err := Write(plan); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(configLink)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("shared config link was replaced: info=%v err=%v", info, err)
	}
	if _, err := os.Stat(configTarget); err != nil {
		t.Fatalf("shared install-root config was not created: %v", err)
	}
}

func TestWriteNeverCreatesFilesInSelectedProject(t *testing.T) {
	platformRoot, projectRoot := t.TempDir(), t.TempDir()
	plan := testPlan(platformRoot)
	plan.Request.ProjectRoot = projectRoot
	if _, err := Write(plan); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(projectRoot, ".kb")); !os.IsNotExist(err) {
		t.Fatalf("installer created project files: %v", err)
	}
}

func testPlan(root string) contracts.ResolvedInstallPlan {
	return contracts.ResolvedInstallPlan{
		Schema:       contracts.ResolvedPlanSchema,
		Request:      contracts.InstallRequest{PlatformRoot: root},
		ServiceGraph: contracts.ServiceGraph{PlatformVersion: "2.0.0", Services: []contracts.Service{{ID: "gateway", Command: "serve", Port: 4000}}},
	}
}
