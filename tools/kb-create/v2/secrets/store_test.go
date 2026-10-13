package secrets

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/kb-labs/create/v2/contracts"
	"github.com/kb-labs/create/v2/versionroot"
)

func TestStoreIsPrivateAndSupportsExistenceOnly(t *testing.T) {
	root := t.TempDir()
	store := Store{PlatformRoot: root}
	if err := store.Put("OPENAI_API_KEY", "super-secret"); err != nil {
		t.Fatal(err)
	}
	exists, err := store.Exists("OPENAI_API_KEY")
	if err != nil || !exists {
		t.Fatalf("exists/error = %v / %v", exists, err)
	}
	path := secretsPath(t, root)
	data, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(data), "super-secret") {
		t.Fatalf("secret store write failed: err=%v", err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("mode/error = %v / %v", info.Mode(), err)
	}
}

func TestReadOnlyStoreRejectsStagedWrites(t *testing.T) {
	root := t.TempDir()
	store := Store{PlatformRoot: root}
	if err := store.Put("stable", "before"); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(secretsPath(t, root))
	if err != nil {
		t.Fatal(err)
	}
	store.ReadOnly = true
	if err := store.Put("stable", "after"); err == nil {
		t.Fatal("read-only staged secret write unexpectedly succeeded")
	}
	after, err := os.ReadFile(secretsPath(t, root))
	if err != nil || string(after) != string(before) {
		t.Fatalf("read-only secret write changed shared state: err=%v", err)
	}
}

func TestEnsureGeneratedCreatesPrivateRandomSecretAndPreservesSuppliedValue(t *testing.T) {
	root := t.TempDir()
	store := Store{PlatformRoot: root}
	plan := &contracts.ResolvedInstallPlan{
		Artifacts: []contracts.Artifact{{Config: []contracts.ConfigRequirement{{ID: "gateway.jwtSecret", Secret: true, Generate: true}}}},
	}
	if err := EnsureGenerated(plan, store); err != nil {
		t.Fatal(err)
	}
	first, present, err := store.Get("gateway.jwtSecret")
	if err != nil || !present || !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(first) {
		t.Fatalf("generated secret availability mismatch: present=%v, err=%v", present, err)
	}
	if err := EnsureGenerated(plan, store); err != nil {
		t.Fatal(err)
	}
	second, _, _ := store.Get("gateway.jwtSecret")
	if second != first {
		t.Fatal("existing operator or generated secret was replaced")
	}
	if err := store.Put("gateway.jwtSecret", "operator-selected-secret"); err != nil {
		t.Fatal(err)
	}
	if err := EnsureGenerated(plan, store); err != nil {
		t.Fatal(err)
	}
	got, _, _ := store.Get("gateway.jwtSecret")
	if got != "operator-selected-secret" {
		t.Fatal("operator secret was not preserved")
	}
}

func TestBootstrapPasswordOriginIsAtomicWithThePassword(t *testing.T) {
	store := Store{PlatformRoot: t.TempDir()}
	if err := store.PutBootstrapPassword("generated-test-secret", BootstrapOriginGenerated); err != nil {
		t.Fatal(err)
	}
	password, present, err := store.Get(BootstrapPassword)
	if err != nil || !present || password != "generated-test-secret" {
		t.Fatal("generated bootstrap password was not stored")
	}
	origin, present, err := store.BootstrapOrigin()
	if err != nil || !present || origin != BootstrapOriginGenerated {
		t.Fatal("generated origin was not committed with the bootstrap password")
	}
	if err := store.Put(BootstrapPassword, "supplied-test-secret"); err != nil {
		t.Fatal(err)
	}
	origin, present, err = store.BootstrapOrigin()
	if err != nil || !present || origin != BootstrapOriginSupplied {
		t.Fatal("supplied origin was not committed with the bootstrap password")
	}
}

// case: S-038.4
func TestEnsureGeneratedInternalSecretBindsEveryProtectedService(t *testing.T) {
	root := t.TempDir()
	store := Store{PlatformRoot: root}
	plan := &contracts.ResolvedInstallPlan{
		Artifacts: []contracts.Artifact{{Config: []contracts.ConfigRequirement{{
			ID: "gateway.internalSecret", Secret: true, Generate: true, Env: "GATEWAY_INTERNAL_SECRET",
			Services: []string{"gateway", "rest", "marketplace-registry"},
		}}}},
	}
	if err := EnsureGenerated(plan, store); err != nil {
		t.Fatal(err)
	}
	value, present, err := store.Get("gateway.internalSecret")
	if err != nil || !present || !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(value) {
		t.Fatalf("generated internal secret availability mismatch: present=%v, err=%v", present, err)
	}
	if err := store.BindEnvironments([]contracts.ConfigPatch{{
		Owner: "manifest:gateway.internalSecret", Environment: "GATEWAY_INTERNAL_SECRET",
		Services: []string{"gateway", "rest", "marketplace-registry"},
	}}); err != nil {
		t.Fatal(err)
	}
	environment, err := store.Environment()
	if err != nil || environment.Values["GATEWAY_INTERNAL_SECRET"] != value {
		t.Fatalf("service environment = %#v, err=%v", environment, err)
	}
}

// case: S-038.5
func TestEnsureGeneratedPreservesInternalSecretDuringUpdate(t *testing.T) {
	store := Store{PlatformRoot: t.TempDir()}
	plan := &contracts.ResolvedInstallPlan{
		Artifacts: []contracts.Artifact{{Config: []contracts.ConfigRequirement{{ID: "gateway.internalSecret", Secret: true, Generate: true}}}},
	}
	if err := EnsureGenerated(plan, store); err != nil {
		t.Fatal(err)
	}
	before, _, _ := store.Get("gateway.internalSecret")
	if err := EnsureGenerated(plan, store); err != nil { // update resolves the same generated requirement
		t.Fatal(err)
	}
	after, _, _ := store.Get("gateway.internalSecret")
	if after != before {
		t.Fatal("update replaced generated secret")
	}
}
func TestStoreRejectsInjectionName(t *testing.T) {
	if err := (Store{PlatformRoot: t.TempDir()}).Put("TOKEN\nEVIL", "x"); err == nil {
		t.Fatal("accepted injection name")
	}
}

func TestGetReturnsStoredValueAndReportsAbsence(t *testing.T) {
	store := Store{PlatformRoot: t.TempDir()}
	if _, present, err := store.Get("missing"); err != nil || present {
		t.Fatalf("absent secret: present=%v err=%v", present, err)
	}
	if err := store.Put("gateway.jwtSecret", "value=with=equals"); err != nil {
		t.Fatal(err)
	}
	value, present, err := store.Get("gateway.jwtSecret")
	if err != nil || !present || value != "value=with=equals" {
		t.Fatalf("secret availability mismatch: present=%v, err=%v", present, err)
	}
	if _, _, err := store.Get("bad=name"); err == nil {
		t.Fatal("an invalid name must be rejected")
	}
}

func secretsFile(t *testing.T, root string) map[string]string {
	t.Helper()
	data, err := os.ReadFile(secretsPath(t, root))
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if key, value, ok := strings.Cut(line, "="); ok {
			values[key] = value
		}
	}
	return values
}

func secretsPath(t *testing.T, root string) string {
	t.Helper()
	layout, err := versionroot.New(root)
	if err != nil {
		t.Fatal(err)
	}
	path, err := layout.SharedRuntimePath(".kb/v2/secrets.env")
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func gatewaySecretPatches() []contracts.ConfigPatch {
	return []contracts.ConfigPatch{
		{Path: "/gateway/access/mode", JSON: `"secured"`, Owner: "manifest:gateway.access.mode"},
		{Owner: "manifest:gateway.jwtSecret", Environment: "GATEWAY_JWT_SECRET", Services: []string{"gateway"}},
		{Owner: "manifest:gateway.bootstrap.password", Environment: "GATEWAY_BOOTSTRAP_ADMIN_PASSWORD", Services: []string{"gateway"}},
	}
}

// A secret must be reachable under the environment variable name the generated
// service env references (kb-dev resolves ${GATEWAY_JWT_SECRET} by that name),
// not only under its requirement ID — whatever put it in the store.
func TestBindEnvironmentsAddsTheBoundEnvironmentKey(t *testing.T) {
	root := t.TempDir()
	store := Store{PlatformRoot: root}
	// As the wizard (or --secret-env) leaves them: keyed by requirement ID only.
	if err := store.Put("gateway.jwtSecret", "jwt-value"); err != nil {
		t.Fatal(err)
	}
	if err := store.Put("gateway.bootstrap.password", "admin-value"); err != nil {
		t.Fatal(err)
	}
	if err := store.BindEnvironments(gatewaySecretPatches()); err != nil {
		t.Fatal(err)
	}
	got := secretsFile(t, root)
	want := map[string]string{
		"gateway.jwtSecret":                "jwt-value",
		"GATEWAY_JWT_SECRET":               "jwt-value",
		"gateway.bootstrap.password":       "admin-value",
		"GATEWAY_BOOTSTRAP_ADMIN_PASSWORD": "admin-value",
	}
	if len(got) != len(want) {
		t.Fatalf("stored secret key set mismatch: got=%v want=%v", got, want)
	}
	for key, value := range want {
		if got[key] != value {
			t.Fatalf("stored value mismatch for secret key %s", key)
		}
	}
	if origin, present, err := store.BootstrapOrigin(); err != nil || !present || origin != BootstrapOriginSupplied {
		t.Fatal("supplied bootstrap origin was not stored in the internal lifecycle record")
	}
	if exists, err := store.Exists("gateway.jwtSecret"); err != nil || !exists {
		t.Fatalf("requirement ID must stay verifiable: %v / %v", exists, err)
	}
}

func TestBindEnvironmentsIsIdempotentAndSkipsWhatIsNotThere(t *testing.T) {
	root := t.TempDir()
	store := Store{PlatformRoot: root}
	if err := store.Put("gateway.jwtSecret", "jwt-value"); err != nil {
		t.Fatal(err)
	}
	patches := append(gatewaySecretPatches(),
		// bound to an env equal to its own ID: nothing to add
		contracts.ConfigPatch{Owner: "manifest:OPENAI_API_KEY", Environment: "OPENAI_API_KEY", Services: []string{"gateway"}},
		// a non-secret patch that happens to carry no environment
		contracts.ConfigPatch{Path: "/x", JSON: `1`, Owner: "platform"},
	)
	for i := 0; i < 2; i++ {
		if err := store.BindEnvironments(patches); err != nil {
			t.Fatal(err)
		}
	}
	got := secretsFile(t, root)
	// gateway.bootstrap.password was never provided: no alias is invented for it.
	if len(got) != 2 || got["gateway.jwtSecret"] != "jwt-value" || got["GATEWAY_JWT_SECRET"] != "jwt-value" {
		t.Fatalf("stored keys = %v", got)
	}
}

// case: S-222.11
func TestEnvironmentOmitsBootstrapPasswordAfterCompletion(t *testing.T) {
	store := Store{PlatformRoot: t.TempDir()}
	if err := store.Put("gateway.bootstrap.password", "one-time-bootstrap-password"); err != nil {
		t.Fatal(err)
	}
	if err := store.Put("GATEWAY_BOOTSTRAP_ADMIN_PASSWORD", "one-time-bootstrap-password"); err != nil {
		t.Fatal(err)
	}
	if err := store.Put("gateway.jwtSecret", strings.Repeat("j", 64)); err != nil {
		t.Fatal(err)
	}
	if err := store.Put("GATEWAY_JWT_SECRET", strings.Repeat("j", 64)); err != nil {
		t.Fatal(err)
	}
	before, err := store.Environment()
	if err != nil || before.Values["GATEWAY_BOOTSTRAP_ADMIN_PASSWORD"] != "one-time-bootstrap-password" {
		t.Fatalf("pre-completion environment binding mismatch: err=%v", err)
	}
	if err := store.MarkBootstrapCompleted("provisioned"); err != nil {
		t.Fatal(err)
	}
	after, err := store.Environment()
	if err != nil {
		t.Fatal(err)
	}
	if _, present := after.Values["GATEWAY_BOOTSTRAP_ADMIN_PASSWORD"]; present {
		t.Fatal("completed host environment still contains bootstrap password")
	}
	if after.Values["GATEWAY_JWT_SECRET"] != strings.Repeat("j", 64) {
		t.Fatal("completion removed an unrelated Gateway secret")
	}
	if password, present, err := store.Get("gateway.bootstrap.password"); err != nil || !present || password != "one-time-bootstrap-password" {
		t.Fatalf("completion deleted stored bootstrap secret: present=%v err=%v", present, err)
	}
}

// case: S-222.11
func TestBootstrapDisclosureAtomicallyErasesPasswordAndRejectsReinsertion(t *testing.T) {
	root := t.TempDir()
	store := Store{PlatformRoot: root}
	const password = "bootstrap-disclosure-test-sentinel"
	for name, value := range map[string]string{
		"gateway.bootstrap.password": password,
		BootstrapPasswordEnvironment: password,
		"gateway.jwtSecret":          "unrelated-secret",
	} {
		if err := store.Put(name, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.MarkBootstrapDisclosed(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(secretsPath(t, root))
	if err != nil {
		t.Fatal(err)
	}
	disclosed, err := store.BootstrapDisclosed()
	if err != nil || !disclosed || strings.Contains(string(data), password) || !strings.Contains(string(data), "unrelated-secret") {
		t.Fatal("disclosure update did not atomically preserve unrelated values, record the marker, and erase the password")
	}
	for _, name := range []string{"gateway.bootstrap.password", BootstrapPasswordEnvironment} {
		if present, err := store.Exists(name); err != nil || present {
			t.Fatalf("disclosed password key %q present=%v, err=%v", name, present, err)
		}
	}
	for _, name := range []string{BootstrapPassword, BootstrapPasswordEnvironment} {
		if err := store.Put(name, password); err != ErrBootstrapPasswordDisclosed {
			t.Fatalf("password reinsertion error for %q = %v; want ErrBootstrapPasswordDisclosed", name, err)
		}
	}
	if err := store.Put("gateway.bootstrap.disclosed", "false"); err != nil {
		t.Fatal("generic values remain separate from the bootstrap lifecycle record")
	}
	if disclosed, err := store.BootstrapDisclosed(); err != nil || !disclosed {
		t.Fatal("generic storage changed the typed disclosure fact")
	}
}

func TestOpeningStoreConvergesInterruptedBootstrapDisclosure(t *testing.T) {
	root := t.TempDir()
	store := Store{PlatformRoot: root}
	const password = "interrupted-disclosure-test-sentinel"
	if err := store.MarkBootstrapDisclosed(); err != nil {
		t.Fatal(err)
	}
	if err := store.write(map[string]string{BootstrapPassword: password, BootstrapPasswordEnvironment: password}); err != nil {
		t.Fatal(err)
	}
	if disclosed, err := store.BootstrapDisclosed(); err != nil || !disclosed {
		t.Fatal("opening the lifecycle record did not observe disclosure")
	}
	stored, err := os.ReadFile(secretsPath(t, root))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(stored), password) {
		t.Fatal("opening the secret store did not converge disclosure marker and password erasure")
	}
}

// case: S-222.13
func TestGenericRequirementNamesCannotWriteBootstrapLifecycleFacts(t *testing.T) {
	store := Store{PlatformRoot: t.TempDir()}
	for _, id := range []string{"gateway.bootstrap.generated", "gateway.bootstrap.disclosed", "gateway.bootstrap.completed"} {
		if err := store.Put(id, "operator-value"); err != nil {
			t.Fatal(err)
		}
	}
	origin, present, err := store.BootstrapOrigin()
	if err != nil || present || origin != "" {
		t.Fatal("generic requirement write changed bootstrap origin")
	}
	disclosed, err := store.BootstrapDisclosed()
	if err != nil || disclosed {
		t.Fatal("generic requirement write changed bootstrap disclosure state")
	}
	completed, err := store.BootstrapCompleted()
	if err != nil || completed {
		t.Fatal("generic requirement write changed bootstrap completion state")
	}
}

// case: S-222.13
func TestLegacyFlatBootstrapMarkersReturnUnsupportedFormatWithoutReset(t *testing.T) {
	root := t.TempDir()
	store := Store{PlatformRoot: root}
	data := []byte("gateway.bootstrap.generated=true\n" + BootstrapPassword + "=sentinel\n")
	if err := os.MkdirAll(filepath.Dir(secretsPath(t, root)), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secretsPath(t, root), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Get(BootstrapPassword); !errors.Is(err, ErrUnsupportedSecretStoreFormat) {
		t.Fatal("legacy flat store was not rejected with the unsupported-format diagnosis")
	}
	after, err := os.ReadFile(secretsPath(t, root))
	if err != nil || !bytes.Equal(after, data) {
		t.Fatal("unsupported legacy store was silently reset or changed")
	}
}

func TestBindEnvironmentsRejectsConflictingValuesForOneVariable(t *testing.T) {
	store := Store{PlatformRoot: t.TempDir()}
	_ = store.Put("a.secret", "one")
	_ = store.Put("b.secret", "two")
	patches := []contracts.ConfigPatch{
		{Owner: "manifest:a.secret", Environment: "SHARED_ENV", Services: []string{"gateway"}},
		{Owner: "manifest:b.secret", Environment: "SHARED_ENV", Services: []string{"gateway"}},
	}
	if err := store.BindEnvironments(patches); err == nil {
		t.Fatal("two different values for one environment variable must be rejected, not silently last-write-wins")
	}
	_ = store.Put("b.secret", "one")
	if err := store.BindEnvironments(patches); err != nil {
		t.Fatalf("identical values are not a conflict: %v", err)
	}
}
