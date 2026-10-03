package doctor

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kb-labs/create/v2/contracts"
	"github.com/kb-labs/create/v2/host"
	"github.com/kb-labs/create/v2/receipt"
)

func check(t *testing.T, section Section, id string) Check {
	t.Helper()
	for _, item := range section.Checks {
		if item.ID == id {
			return item
		}
	}
	t.Fatalf("no check %q in %+v", id, section.Checks)
	return Check{}
}

func installation(t *testing.T, hostCommand string) (root string, state host.State) {
	t.Helper()
	root = t.TempDir()
	state = host.State{Dir: filepath.Join(t.TempDir(), "launcher")}
	plan := contracts.ResolvedInstallPlan{Schema: contracts.ResolvedPlanSchema, ServiceGraph: contracts.ServiceGraph{PlatformVersion: "2.5.0"}, Host: &contracts.HostSpec{Command: hostCommand, HealthURL: "http://127.0.0.1:4000/health"}}
	if err := receipt.Write(root, contracts.InstallReceipt{ID: "r1", Plan: plan}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".kb"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".kb", "kb.config.jsonc"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return root, state
}

func TestInstallationSectionReportsACompleteInstallationAsHealthy(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	root, state := installation(t, executable)
	section := InspectInstallation(root, state)
	if !section.OK {
		t.Fatalf("section = %+v", section)
	}
	if detail := check(t, section, "receipt").Detail; !strings.Contains(detail, "2.5.0") {
		t.Fatalf("receipt detail = %q", detail)
	}
}

func TestInstallationSectionNamesWhatIsMissing(t *testing.T) {
	root, state := installation(t, filepath.Join(t.TempDir(), "no-such-host"))
	if err := os.Remove(filepath.Join(root, ".kb", "kb.config.jsonc")); err != nil {
		t.Fatal(err)
	}
	section := InspectInstallation(root, state)
	if section.OK || check(t, section, "config").OK || check(t, section, "host-spec").OK || !check(t, section, "receipt").OK {
		t.Fatalf("section = %+v", section)
	}
	if !strings.Contains(check(t, section, "host-spec").Hint, "kb-create") {
		t.Fatalf("a failed check must say how to fix it: %+v", check(t, section, "host-spec"))
	}
}

func TestInstallationSectionWithoutAReceiptIsAFindingNotAnError(t *testing.T) {
	section := InspectInstallation(t.TempDir(), host.State{Dir: t.TempDir()})
	if section.OK || check(t, section, "receipt").OK || !check(t, section, "launcher-state").OK {
		t.Fatalf("section = %+v", section)
	}
}

func TestInstallationSectionRejectsAReceiptWithoutAHost(t *testing.T) {
	root := t.TempDir()
	if err := receipt.Write(root, contracts.InstallReceipt{ID: "old", Plan: contracts.ResolvedInstallPlan{Schema: contracts.ResolvedPlanSchema}}); err != nil {
		t.Fatal(err)
	}
	section := InspectInstallation(root, host.State{Dir: t.TempDir()})
	if got := check(t, section, "host-spec"); got.OK || !strings.Contains(got.Detail, "no host") {
		t.Fatalf("host-spec = %+v", got)
	}
}

func TestPlatformSectionReportsAStoppedOrUnhealthyHost(t *testing.T) {
	stopped := InspectPlatform(context.Background(), host.Status{State: host.StateStopped}, nil, nil)
	if stopped.OK || !strings.Contains(check(t, stopped, "host-process").Hint, "kb-create start") {
		t.Fatalf("stopped = %+v", stopped)
	}
	adapterCheck := check(t, stopped, "adapters")
	if adapterCheck.OK || !strings.Contains(adapterCheck.Detail, "cannot be checked") || !strings.Contains(adapterCheck.Hint, "kb-create start") {
		t.Fatalf("stopped adapter status = %+v", adapterCheck)
	}
	unhealthy := InspectPlatform(context.Background(), host.Status{State: host.StateUnhealthy, PID: 7}, nil, nil)
	if unhealthy.OK || !strings.Contains(check(t, unhealthy, "host-process").Hint, "kb-create restart") {
		t.Fatalf("unhealthy = %+v", unhealthy)
	}
}

func gatewayDouble(t *testing.T, health, auth string) host.Status {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			_, _ = w.Write([]byte(health))
		case "/health/adapters":
			_, _ = w.Write([]byte(`{"ok":true,"data":[{"slot":"storage","mode":"real","implementation":"@kb-labs/adapters-fs"},{"slot":"cache","mode":"inmemory","implementation":"StateBroker"}]}`))
		case "/health/auth":
			if auth == "" {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write([]byte(auth))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return host.Status{State: host.StateRunning, PID: 9, Healthy: true, HealthURL: server.URL + "/health"}
}

func TestPlatformSectionReportsPerSlotAdapterModeAndImplementation(t *testing.T) {
	status := gatewayDouble(t, `{"ok":true,"data":{"upstreams":{}}}`, "")
	section := InspectPlatform(context.Background(), status, nil, nil)
	encoded, err := json.Marshal(section)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Checks []map[string]any `json:"checks"`
	}
	if err := json.Unmarshal(encoded, &result); err != nil {
		t.Fatal(err)
	}
	for _, want := range []map[string]string{
		{"slot": "storage", "mode": "real", "implementation": "@kb-labs/adapters-fs"},
		{"slot": "cache", "mode": "inmemory", "implementation": "StateBroker"},
	} {
		found := false
		for _, got := range result.Checks {
			if got["slot"] == want["slot"] && got["mode"] == want["mode"] && got["implementation"] == want["implementation"] {
				found = true
			}
		}
		if !found {
			t.Fatalf("checks do not report adapter status %+v: %s", want, encoded)
		}
	}
}

// case: S-054.10
func TestPlatformSectionRejectsInMemoryFallbackWhenPersistentProviderIsBound(t *testing.T) {
	status := gatewayDouble(t, `{"ok":true,"data":{"upstreams":{}}}`, "")
	section := InspectPlatform(context.Background(), status, nil, []contracts.ProviderBinding{{
		Capability: "cache", Package: "@kb-labs/adapters-state-broker",
	}})
	got := check(t, section, "adapter:cache")
	if got.OK || got.Mode != "inmemory" || !strings.Contains(got.Hint, "kb adapter bind cache @kb-labs/adapters-state-broker") {
		t.Fatalf("persistent cache fallback finding = %+v", got)
	}
}

func TestPlatformSectionReadsUpstreamsAdaptersAndSignInReadiness(t *testing.T) {
	status := gatewayDouble(t,
		`{"ok":true,"data":{"upstreams":{"marketplace":{"status":"up"},"state":{"status":"down"}},"adapters":{"cache":{"available":true},"llm":{"available":false}}}}`,
		`{"ok":true,"data":{"ok":false,"issues":[{"code":"no_active_admin","severity":"error","message":"nobody can log in","hint":"kb-create doctor --recover"}]}}`)
	section := InspectPlatform(context.Background(), status, nil, nil)
	if section.OK {
		t.Fatalf("section = %+v", section)
	}
	for id, wantOK := range map[string]bool{"host-process": true, "upstream:marketplace": true, "upstream:state": false, "adapter:cache": true, "adapter:llm": false, "auth:no_active_admin": false} {
		if got := check(t, section, id); got.OK != wantOK {
			t.Fatalf("%s = %+v, want ok=%v", id, got, wantOK)
		}
	}
	if hint := check(t, section, "auth:no_active_admin").Hint; hint != "kb-create doctor --recover" {
		t.Fatalf("the sign-in hint must reach the operator unchanged: %q", hint)
	}
}

func TestPlatformSectionIsHealthyWhenEverythingReportsUp(t *testing.T) {
	status := gatewayDouble(t, `{"ok":true,"data":{"upstreams":{"state":{"status":"up"}},"projectRuntimes":{"active":0,"limit":4}}}`, `{"ok":true,"data":{"ok":true,"issues":[]}}`)
	if section := InspectPlatform(context.Background(), status, nil, nil); !section.OK {
		t.Fatalf("section = %+v", section)
	} else if check(t, section, "project-runtimes").Detail != "Project runtimes start on demand; 0 of 4 active." {
		t.Fatalf("project runtime status = %+v", check(t, section, "project-runtimes"))
	}
}

func TestPlatformSectionDoesNotRequireAdminWhenAuthIsDisabled(t *testing.T) {
	status := gatewayDouble(t, `{"ok":true,"data":{"upstreams":{"marketplace":{"status":"up"}}}}`,
		`{"ok":true,"data":{"ok":true,"authEnabled":false,"issues":[{"code":"no_active_admin","severity":"error","message":"nobody can log in"},{"code":"jwt_secret_default","severity":"error","message":"shared secret"}]}}`)
	section := InspectPlatform(context.Background(), status, nil, nil)
	for _, item := range section.Checks {
		if item.ID == "auth:no_active_admin" {
			t.Fatalf("local mode must not report missing admin: %+v", item)
		}
	}
	if got := check(t, section, "auth:jwt_secret_default"); got.OK {
		t.Fatalf("local mode must still report the shared default secret: %+v", got)
	}
}

func TestPlatformSectionRejectsAHealthBodyWithoutTheUxEnvelope(t *testing.T) {
	status := gatewayDouble(t, `ok`, "")
	section := InspectPlatform(context.Background(), status, nil, nil)
	if section.OK || check(t, section, "health").OK {
		t.Fatalf("section = %+v", section)
	}
}

// case: S-203.1
func TestPlatformSectionRejectsRawHealthJSON(t *testing.T) {
	status := gatewayDouble(t, `{"upstreams":{"state":{"status":"up"}}}`, "")
	section := InspectPlatform(context.Background(), status, nil, nil)
	if section.OK || check(t, section, "health").OK {
		t.Fatalf("raw health JSON must not be treated as a successful response: %+v", section)
	}
}

// case: S-203.1
func TestPlatformSectionRejectsRawAuthReadinessJSON(t *testing.T) {
	status := gatewayDouble(t, `{"ok":true,"data":{"upstreams":{}}}`, `{"ok":true,"authEnabled":true,"issues":[]}`)
	section := InspectPlatform(context.Background(), status, nil, nil)
	if section.OK || check(t, section, "auth").OK {
		t.Fatalf("raw auth readiness JSON must fail its check: %+v", section)
	}
}

func TestSectionIsHealthyOnlyWhenEveryCheckIs(t *testing.T) {
	if !NewSection(nil).OK || !NewSection([]Check{{OK: true}}).OK {
		t.Fatal("empty and passing sections are healthy")
	}
	if NewSection([]Check{{OK: true}, {OK: false}}).OK {
		t.Fatal("one failing check fails the section")
	}
}
