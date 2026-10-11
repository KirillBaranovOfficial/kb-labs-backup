package scenario

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kb-labs/create/v2/catalog"
	"github.com/kb-labs/create/v2/contracts"
	"github.com/kb-labs/create/v2/render"
	"github.com/kb-labs/create/v2/resolve"
)

func TestMigratedBuiltinsCompileToSharedV2Requests(t *testing.T) {
	ids, err := IDs()
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 5 {
		t.Fatalf("migrated scenario IDs = %#v", ids)
	}
	for _, id := range ids {
		t.Run(id, func(t *testing.T) {
			scenario, err := Load(id)
			if err != nil {
				t.Fatal(err)
			}
			state, err := New(scenario)
			if err != nil {
				t.Fatal(err)
			}
			base := contracts.InstallRequest{PlatformRoot: t.TempDir(), Platform: contracts.VersionSelector{Channel: contracts.ChannelStable}, ServiceProfile: "default", Source: contracts.SourceRegistry, Policy: contracts.PolicyCompatible}
			request, err := Compile(scenario, state, base)
			if err != nil {
				t.Fatal(err)
			}
			if request.ScenarioID != id {
				t.Fatalf("request = %#v", request)
			}
			if _, err := resolve.Plan(request, source()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestScenarioResumePersistsOnlyNonSecretAnswers(t *testing.T) {
	definition := Scenario{Schema: Schema, ID: "resume", Fields: []Field{{ID: "mode", Requirement: "mode", Type: "select", Default: []byte(`"local"`), Options: []Option{{Value: "local"}}}, {ID: "token", Requirement: "token", Type: "string", Secret: true, Required: true}}}
	state, err := New(definition)
	if err != nil {
		t.Fatal(err)
	}
	state, err = Answer(definition, state, "token", []byte(`"super-secret"`))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := SaveState(root, definition, state); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "state", "scenarios", "resume.json"))
	if err != nil || strings.Contains(string(data), "super-secret") {
		t.Fatalf("state/error = %s / %v", data, err)
	}
	loaded, err := LoadState(root, definition)
	if err != nil || string(loaded.Answers["mode"]) != `"local"` || loaded.Answers["token"] != nil {
		t.Fatalf("loaded/error = %#v / %v", loaded, err)
	}
}

func TestScenarioValueRendersIntoManifestOwnedConfig(t *testing.T) {
	definition, err := Load("custom")
	if err != nil {
		t.Fatal(err)
	}
	state, err := New(definition)
	if err != nil {
		t.Fatal(err)
	}
	state, err = Answer(definition, state, "access.mode", []byte(`"local"`))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	request, err := Compile(definition, state, contracts.InstallRequest{PlatformRoot: root, Platform: contracts.VersionSelector{Channel: contracts.ChannelStable}, ServiceProfile: "default", Source: contracts.SourceRegistry, Policy: contracts.PolicyCompatible})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := resolve.Plan(request, source())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := render.Write(plan); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, ".kb", "kb.config.jsonc"))
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	gateway := config["gateway"].(map[string]any)
	access := gateway["access"].(map[string]any)
	if access["mode"] != "local" {
		t.Fatalf("config = %#v", config)
	}
}

func TestSecuredCustomScenarioSelectsPersistentAuthStore(t *testing.T) {
	definition, err := Load("custom")
	if err != nil {
		t.Fatal(err)
	}
	state, err := New(definition)
	if err != nil {
		t.Fatal(err)
	}
	state, err = Answer(definition, state, "access.mode", []byte(`"secured"`))
	if err != nil {
		t.Fatal(err)
	}
	state, err = Answer(definition, state, "admin.email", []byte(`"admin@example.com"`))
	if err != nil {
		t.Fatal(err)
	}
	request, err := Compile(definition, state, contracts.InstallRequest{PlatformRoot: t.TempDir(), Platform: contracts.VersionSelector{Channel: contracts.ChannelStable}, ServiceProfile: "default", Source: contracts.SourceRegistry, Policy: contracts.PolicyCompatible})
	if err != nil {
		t.Fatal(err)
	}
	if got := request.ProviderPreferences["documentDatabase"]; got != "sqlite-stores" {
		t.Fatalf("secured auth store preference = %q, want sqlite-stores", got)
	}
	if len(request.Adapters) != 1 || request.Adapters[0].ID != "sqlite-stores" {
		t.Fatalf("secured auth store adapters = %#v, want sqlite-stores", request.Adapters)
	}
	plan, err := resolve.Plan(request, source())
	if err != nil {
		t.Fatal(err)
	}
	foundStore := false
	for _, artifact := range plan.Artifacts {
		if artifact.ID == "sqlite-stores" {
			foundStore = true
		}
	}
	if !foundStore {
		t.Fatalf("secured plan artifacts do not include sqlite store: %#v", plan.Artifacts)
	}
	foundFilename := false
	for _, patch := range plan.ConfigPatches {
		if patch.Path == "/platform/adapterOptions/documentDatabase/filename" && patch.JSON == `"database/kb.sqlite"` {
			foundFilename = true
		}
	}
	if !foundFilename {
		t.Fatalf("secured plan does not configure persistent auth database: %#v", plan.ConfigPatches)
	}
}

func TestScenarioRejectsUndeclaredConfigAndOption(t *testing.T) {
	scenario, err := Load("custom")
	if err != nil {
		t.Fatal(err)
	}
	state, err := New(scenario)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Answer(scenario, state, "access.mode", []byte(`"unsafe"`)); err == nil {
		t.Fatal("expected option validation")
	}
	state.Answers["unknown"] = []byte(`"value"`)
	request, err := Compile(scenario, state, contracts.InstallRequest{PlatformRoot: t.TempDir(), Platform: contracts.VersionSelector{Channel: contracts.ChannelStable}, ServiceProfile: "default", Source: contracts.SourceRegistry, Policy: contracts.PolicyCompatible})
	if err != nil {
		t.Fatal(err)
	}
	request.Values["not.manifest"] = `"nope"`
	if _, err := resolve.Plan(request, source()); err == nil {
		t.Fatal("expected manifest-bound config rejection")
	}
}

func TestPagedScenarioCompilesConditionalAndSecretFields(t *testing.T) {
	definition := Scenario{
		Schema: Schema,
		ID:     "paged",
		Pages: []Page{{ID: "access", Sections: []Section{{ID: "main", Fields: []Field{
			{ID: "mode", Requirement: "gateway.access.mode", Type: "select", Default: []byte(`"local"`), Options: []Option{{Value: "local"}, {Value: "secured"}}},
			{ID: "token", Requirement: "gateway.token", Type: "string", Secret: true, When: &Predicate{Path: "mode", Equals: "secured"}},
		}}}}},
	}
	state, err := New(definition)
	if err != nil {
		t.Fatal(err)
	}
	request, err := Compile(definition, state, contracts.InstallRequest{PlatformRoot: t.TempDir(), Platform: contracts.VersionSelector{Channel: contracts.ChannelStable}, ServiceProfile: "default", Source: contracts.SourceRegistry, Policy: contracts.PolicyCompatible})
	if err != nil {
		t.Fatal(err)
	}
	if request.Values["gateway.access.mode"] != `"local"` || len(request.SecretInputs) != 0 {
		t.Fatalf("request = %#v", request)
	}
	if len(VisiblePages(definition, state)) != 1 || len(VisibleFields(definition.Pages[0], state)) != 1 {
		t.Fatalf("visible pages/fields = %#v / %#v", VisiblePages(definition, state), VisibleFields(definition.Pages[0], state))
	}
}

func TestBuiltInSecuredScenarioGeneratesAnAdminPasswordWhenOmitted(t *testing.T) {
	definition, err := Load("custom")
	if err != nil {
		t.Fatal(err)
	}
	state, err := New(definition)
	if err != nil {
		t.Fatal(err)
	}
	state, err = Answer(definition, state, "access.mode", []byte(`"secured"`))
	if err != nil {
		t.Fatal(err)
	}
	state, err = Answer(definition, state, "admin.email", []byte(`"admin@example.test"`))
	if err != nil {
		t.Fatal(err)
	}
	request, err := Compile(definition, state, contracts.InstallRequest{PlatformRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	declared := false
	for _, input := range request.SecretInputs {
		declared = declared || input == "gateway.bootstrap.password"
	}
	if !declared {
		t.Fatalf("omitted generated password was not declared as a secret input: %#v", request.SecretInputs)
	}
	if request.Values["gateway.bootstrap.adminEmail"] != `"admin@example.test"` {
		t.Fatalf("admin email = %#v", request.Values)
	}
}

func source() catalog.Catalog {
	return catalog.Catalog{Schema: catalog.Schema, Channels: map[contracts.Channel]string{contracts.ChannelStable: "2.0.0"}, Platforms: []catalog.PlatformBundle{{ID: "platform", Version: "2.0.0", Package: "@kb/platform", SHA256: "platform", Host: &contracts.HostSpec{Command: "kb-host", HealthURL: "http://127.0.0.1:4000/health"}, Profiles: map[string]contracts.ServiceGraph{"default": {Services: []contracts.Service{{ID: "gateway", Command: "gateway", Required: true}}}}, Requires: []catalog.Requirement{{Capability: "serviceTransport", RequiredBy: "platform"}}, Config: []catalog.ConfigRequirement{{ID: "gateway.access.mode", Path: "/gateway/access/mode", Default: `"secured"`}, {ID: "gateway.bootstrap.adminEmail", Path: "/gateway/auth/bootstrap/adminEmail"}, {ID: "gateway.bootstrap.password", Secret: true, Generate: true, Env: "GATEWAY_BOOTSTRAP_ADMIN_PASSWORD", Services: []string{"gateway"}}, {ID: "gateway.jwtSecret", Secret: true, Generate: true, Env: "GATEWAY_JWT_SECRET", Services: []string{"gateway"}}, {ID: "gateway.authStorage.filename", Path: "/platform/adapterOptions/documentDatabase/filename", Default: `"database/kb.sqlite"`}}}}, SDKs: []catalog.Component{{ID: "sdk", Version: "2.0.0", Package: "@kb/sdk", SHA256: "sdk"}}, Plugins: []catalog.Component{{ID: "@kb-labs/commit", Version: "1", Package: "@kb/commit", SHA256: "commit"}, {ID: "@kb-labs/marketplace", Version: "1", Package: "@kb/marketplace", SHA256: "marketplace"}, {ID: "@kb-labs/review", Version: "1", Package: "@kb/review", SHA256: "review"}, {ID: "@kb-labs/scaffold", Version: "1", Package: "@kb/scaffold", SHA256: "scaffold"}, {ID: "@kb-labs/release", Version: "1", Package: "@kb/release", SHA256: "release"}}, Adapters: []catalog.Adapter{{Component: catalog.Component{ID: "state-broker", Version: "1", Package: "@kb/state", SHA256: "state"}, Provides: []string{"cache"}}, {Component: catalog.Component{ID: "sqlite-stores", Version: "1", Package: "@kb/sqlite", SHA256: "sqlite"}, Provides: []string{"documentDatabase"}}, {Component: catalog.Component{ID: "service-transport-http", Version: "1", Package: "@kb/transport", SHA256: "transport"}, Provides: []string{"serviceTransport"}}}}
}

// A field that is required only when it is visible (`when`) must not be
// demanded while it is hidden: choosing "local" access may not require the
// admin email that only "secured" access asks for.
func TestRequiredFieldIsOnlyRequiredWhileVisible(t *testing.T) {
	definition := Scenario{
		Schema: Schema,
		ID:     "conditional-required",
		Pages: []Page{{ID: "access", Sections: []Section{{ID: "main", Fields: []Field{
			{ID: "mode", Requirement: "gateway.access.mode", Type: "select", Default: []byte(`"local"`), Options: []Option{{Value: "local"}, {Value: "secured"}}},
			{ID: "email", Requirement: "gateway.bootstrap.adminEmail", Type: "string", Required: true, When: &Predicate{Path: "mode", Equals: "secured"}},
		}}}}},
	}
	base := contracts.InstallRequest{PlatformRoot: t.TempDir(), Platform: contracts.VersionSelector{Channel: contracts.ChannelStable}, ServiceProfile: "default", Source: contracts.SourceRegistry, Policy: contracts.PolicyCompatible}
	compile := func(answers map[string]string) (contracts.InstallRequest, error) {
		state, err := New(definition)
		if err != nil {
			t.Fatal(err)
		}
		for id, raw := range answers {
			if state, err = Answer(definition, state, id, []byte(raw)); err != nil {
				t.Fatal(err)
			}
		}
		return Compile(definition, state, base)
	}

	t.Run("hidden and unanswered: not required", func(t *testing.T) {
		request, err := compile(nil) // mode defaults to local
		if err != nil {
			t.Fatalf("a hidden required field must not fail the compile: %v", err)
		}
		if _, present := request.Values["gateway.bootstrap.adminEmail"]; present {
			t.Fatalf("hidden field leaked into values: %#v", request.Values)
		}
	})
	t.Run("hidden but answered earlier: not emitted", func(t *testing.T) {
		request, err := compile(map[string]string{"mode": `"local"`, "email": `"stale@example.com"`})
		if err != nil {
			t.Fatal(err)
		}
		if _, present := request.Values["gateway.bootstrap.adminEmail"]; present {
			t.Fatalf("an answer to a hidden field must not be emitted: %#v", request.Values)
		}
	})
	t.Run("visible and unanswered: required", func(t *testing.T) {
		if _, err := compile(map[string]string{"mode": `"secured"`}); err == nil {
			t.Fatal("a visible required field must still be required")
		}
	})
	t.Run("visible and answered: emitted", func(t *testing.T) {
		request, err := compile(map[string]string{"mode": `"secured"`, "email": `"admin@example.com"`})
		if err != nil {
			t.Fatal(err)
		}
		if request.Values["gateway.bootstrap.adminEmail"] != `"admin@example.com"` {
			t.Fatalf("values = %#v", request.Values)
		}
	})
}

func TestPatternValidator(t *testing.T) {
	definition := Scenario{Schema: Schema, ID: "pattern", Fields: []Field{{ID: "email", Requirement: "gateway.bootstrap.adminEmail", Type: "string", Validators: []Validator{{Kind: "pattern", Arg: `^[a-z]+@[a-z]+\.[a-z]{2,}$`}}}}}
	state, err := New(definition)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Answer(definition, state, "email", []byte(`"admin@example.com"`)); err != nil {
		t.Fatalf("matching value rejected: %v", err)
	}
	for _, bad := range []string{`"not-an-email"`, `"a@b"`, `123`} {
		if _, err := Answer(definition, state, "email", []byte(bad)); err == nil {
			t.Fatalf("%s must be rejected", bad)
		}
	}
}

func TestScenarioWithBadValidatorFailsWhenLoadedNotWhenAnswered(t *testing.T) {
	for name, validator := range map[string]Validator{
		"invalid regexp":    {Kind: "pattern", Arg: `(`},
		"unknown validator": {Kind: "shell", Arg: `rm -rf /`},
	} {
		definition := Scenario{Schema: Schema, ID: "bad", Fields: []Field{{ID: "f", Requirement: "r", Type: "string", Validators: []Validator{validator}}}}
		if err := Validate(definition); err == nil {
			t.Fatalf("%s must be rejected at load time", name)
		}
	}
}

func TestBlankOptionalStringIsUnsetNotAnEmptyValue(t *testing.T) {
	definition := Scenario{Schema: Schema, ID: "blank", Fields: []Field{
		{ID: "email", Requirement: "gateway.bootstrap.adminEmail", Type: "string", Validators: []Validator{{Kind: "pattern", Arg: `^[a-z]+@[a-z]+\.[a-z]{2,}$`}}},
		{ID: "name", Requirement: "some.name", Type: "string", Required: true},
	}}
	base := contracts.InstallRequest{PlatformRoot: t.TempDir(), Platform: contracts.VersionSelector{Channel: contracts.ChannelStable}, ServiceProfile: "default", Source: contracts.SourceRegistry, Policy: contracts.PolicyCompatible}
	state, err := New(definition)
	if err != nil {
		t.Fatal(err)
	}
	// Blank is accepted (not run through the pattern) ...
	if state, err = Answer(definition, state, "email", []byte(`""`)); err != nil {
		t.Fatalf("a blank optional field must be accepted: %v", err)
	}
	if state, err = Answer(definition, state, "name", []byte(`"x"`)); err != nil {
		t.Fatal(err)
	}
	request, err := Compile(definition, state, base)
	if err != nil {
		t.Fatal(err)
	}
	// ... and never emitted as an empty value.
	if _, present := request.Values["gateway.bootstrap.adminEmail"]; present {
		t.Fatalf("blank optional answer leaked into values: %#v", request.Values)
	}
	// A non-blank value is still validated, and a blank REQUIRED field is still rejected.
	if _, err := Answer(definition, state, "email", []byte(`"nope"`)); err == nil {
		t.Fatal("a non-blank value must still match the pattern")
	}
	if _, err := Answer(definition, state, "name", []byte(`""`)); err != nil {
		t.Fatalf("Answer only validates format: %v", err)
	}
	blankRequired, _ := Answer(definition, state, "name", []byte(`""`))
	blankRequired.Answers["name"] = []byte(``)
	if _, err := Compile(definition, blankRequired, base); err == nil {
		t.Fatal("a missing required field must still fail the compile")
	}
}

func TestGenerateIsOnlyValidOnSecretFields(t *testing.T) {
	ok := Scenario{Schema: Schema, ID: "gen", Fields: []Field{{ID: "k", Requirement: "r", Type: "string", Secret: true, Generate: true}}}
	if err := Validate(ok); err != nil {
		t.Fatalf("a generated secret must be valid: %v", err)
	}
	bad := Scenario{Schema: Schema, ID: "gen", Fields: []Field{{ID: "k", Requirement: "r", Type: "string", Generate: true}}}
	if err := Validate(bad); err == nil {
		t.Fatal("generating a non-secret value must be rejected")
	}
}

func TestValidatorMessageReplacesTheGenericFailureText(t *testing.T) {
	definition := Scenario{Schema: Schema, ID: "msg", Fields: []Field{{ID: "pw", Requirement: "r", Type: "string", Secret: true, Validators: []Validator{{Kind: "pattern", Arg: `^.{8,}$`, Message: "must be at least 8 characters"}}}}}
	state, _ := New(definition)
	if _, err := Answer(definition, state, "pw", []byte(`"short"`)); err == nil || !strings.Contains(err.Error(), "must be at least 8 characters") {
		t.Fatalf("error = %v", err)
	}
	if _, err := Answer(definition, state, "pw", []byte(`"long-enough"`)); err != nil {
		t.Fatalf("valid value rejected: %v", err)
	}
}

func TestValidateCatalogChecksTheCurrentBuiltinScenarioSet(t *testing.T) {
	source := source()
	if err := ValidateCatalog(source); err != nil {
		t.Fatalf("current built-in scenarios did not resolve: %v", err)
	}
	source.Plugins = source.Plugins[:len(source.Plugins)-1]
	if err := ValidateCatalog(source); err == nil {
		t.Fatal("a scenario referencing an absent plugin must fail validation")
	}
}

// case: S-054.15
func TestValidateCatalogAllowsMissingOptionalSecretRequirement(t *testing.T) {
	source := source()
	source.Platforms[0].Config = append(source.Platforms[0].Config, catalog.ConfigRequirement{
		ID: "openai.apiKey", Path: "/platform/adapterOptions/llm/apiKey", Secret: true, Required: true,
		Optional: true, Severity: "warning", Env: "OPENAI_API_KEY", Services: []string{"gateway"},
		Action: "set OPENAI_API_KEY to enable LLM features",
	})
	if err := ValidateCatalog(source); err != nil {
		t.Fatalf("built-in scenarios should resolve without optional OpenAI credentials: %v", err)
	}
	source.Platforms[0].Config[len(source.Platforms[0].Config)-1].Optional = false
	source.Platforms[0].Config[len(source.Platforms[0].Config)-1].Severity = "error"
	if err := ValidateCatalog(source); err == nil || !strings.Contains(err.Error(), "required secret input openai.apiKey is missing") {
		t.Fatalf("missing required secret should fail built-in scenario validation, got %v", err)
	}
}
