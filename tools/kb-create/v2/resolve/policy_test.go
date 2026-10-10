package resolve

import (
	"encoding/json"
	"github.com/kb-labs/clikit/diag"
	"path/filepath"
	stdruntime "runtime"
	"testing"

	"github.com/kb-labs/create/v2/catalog"
	"github.com/kb-labs/create/v2/contracts"
	"github.com/kb-labs/create/v2/internal/testfixtures"
)

func policyCatalog() catalog.Catalog {
	var source catalog.Catalog
	if err := json.Unmarshal(testfixtures.ReleaseIndexJSON("3.2.0", "3.0.0", stdruntime.GOOS, stdruntime.GOARCH), &source); err != nil {
		panic(err)
	}
	source.Platforms[0].SDKRange = ">=3.0.0 <4.0.0"
	source.Compatibility.Labels[0].Requires[0].Constraint = ">=3.0.0 <4.0.0"
	source.SDKs = append(source.SDKs, catalog.Component{ID: "sdk", Version: "3.2.0", Package: "@kb/sdk", Tarball: "https://example.test/sdk-3.2.0.tgz", SHA256: "sdk-3.2.0"})
	source.Compatibility.Labels = append(source.Compatibility.Labels, catalog.CompatibilityLabel{
		ID: "sdk@3.2.0", Kind: "sdk", ArtifactID: "sdk", Version: "3.2.0", Status: "prepared", ValidatedBy: []string{"test"},
	})
	source.Compatibility.Labels[2].Requires = append(source.Compatibility.Labels[2].Requires, catalog.CompatibilityRelation{Label: "sdk@3.2.0"})
	return source
}

// case: S-053.2
func TestValidJourneyReleaseIndexResolvesPolicyIncompatibility(t *testing.T) {
	index, err := catalog.LoadFile(filepath.Join("../../../../e2e/platform/fixtures/installer-cases",
		"compatibility-matrix-linux-arm64.json"))
	if err != nil {
		t.Fatalf("valid compatibility fixture did not load: %v", err)
	}
	request := contracts.InstallRequest{PlatformRoot: t.TempDir(),
		Platform: contracts.VersionSelector{Version: "3.2.0"},
		SDK:      contracts.VersionSelector{Version: "3.2.0"},
		Policy:   contracts.PolicyStrict}
	_, err = PlanWith(request, index, Options{OS: "linux", Arch: "arm64"})
	launcherErr, ok := err.(*diag.Diag)
	if !ok || launcherErr.Code != contracts.CodeIncompatibleComponents ||
		launcherErr.Meta["kind"] != "sdk" || launcherErr.Meta["id"] != "sdk" || launcherErr.Meta["selectedVersion"] != "3.2.0" ||
		launcherErr.Meta["requiredRange"] != ">=3.0.0 <4.0.0" ||
		launcherErr.Hint != "Use an exact platform/SDK pair from the release matrix or choose --policy compatible." {
		t.Fatalf("strict policy did not produce the indexed SDK incompatibility: %#v", err)
	}
	request.Policy = contracts.PolicyCompatible
	if _, err := PlanWith(request, index, Options{OS: "linux", Arch: "arm64"}); err != nil {
		t.Fatalf("compatible policy rejected the SDK admitted by the valid fixture: %v", err)
	}
}

func TestStrictRequiresExactMatrixRelationAndCompatibleUsesMatrixRange(t *testing.T) {
	request := contracts.InstallRequest{PlatformRoot: "/tmp/kb-policy", Platform: contracts.VersionSelector{Version: "3.2.0"}, SDK: contracts.VersionSelector{Version: "3.2.0"}, Policy: contracts.PolicyStrict}
	if _, err := Plan(request, policyCatalog()); err == nil {
		t.Fatal("strict policy accepted an SDK without an exact matrix relation")
	} else if launcherErr, ok := err.(*diag.Diag); !ok || launcherErr.Code != contracts.CodeIncompatibleComponents || launcherErr.Meta["kind"] != "sdk" || launcherErr.Meta["id"] != "sdk" || launcherErr.Meta["selectedVersion"] != "3.2.0" || launcherErr.Meta["requiredRange"] != ">=3.0.0 <4.0.0" || launcherErr.Hint != "Use an exact platform/SDK pair from the release matrix or choose --policy compatible." {
		t.Fatalf("strict matrix error = %#v", err)
	}
	request.Policy = contracts.PolicyCompatible
	plan, err := Plan(request, policyCatalog())
	if err != nil {
		t.Fatalf("compatible policy rejected SDK admitted by the matrix range: %v", err)
	}
	if plan.PlatformChannel != "stable" {
		t.Fatalf("platform channel = %q", plan.PlatformChannel)
	}
}

func TestCompatiblePolicyUsesPrereleaseAwareComparatorRanges(t *testing.T) {
	source := policyCatalog()
	source.Platforms[0].SDKRange = ">=3.0.0 <4.0.0 || ^5.1.0"
	source.Compatibility.Labels[0].Requires[0].Constraint = ">=3.0.0 <4.0.0 || ^5.1.0"
	source.SDKs = append(source.SDKs, catalog.Component{ID: "sdk", Version: "3.3.0-rc.2", Package: "@kb/sdk", SHA256: "sdk-rc"})
	source.Compatibility.Labels = append(source.Compatibility.Labels, catalog.CompatibilityLabel{ID: "sdk@3.3.0-rc.2", Kind: "sdk", ArtifactID: "sdk", Version: "3.3.0-rc.2", Status: "prepared", ValidatedBy: []string{"test"}})
	source.Compatibility.Labels[2].Requires = append(source.Compatibility.Labels[2].Requires, catalog.CompatibilityRelation{Label: "sdk@3.3.0-rc.2"})
	request := contracts.InstallRequest{PlatformRoot: "/tmp/kb-policy", Platform: contracts.VersionSelector{Version: "3.2.0"}, SDK: contracts.VersionSelector{Version: "3.3.0-rc.2"}, Policy: contracts.PolicyCompatible}
	if _, err := Plan(request, source); err != nil {
		t.Fatalf("compatible policy rejected a prerelease admitted by the range: %v", err)
	}
}

func TestIncompatibleComponentErrorExposesRangeAndRemediationHint(t *testing.T) {
	err := compatible("=3.2.0", "3.3.0", "adapter fixture-adapter platform")
	launcherErr, ok := err.(*diag.Diag)
	if !ok || launcherErr.Code != contracts.CodeIncompatibleComponents {
		t.Fatalf("component error = %#v", err)
	}
	if launcherErr.Meta["kind"] != "adapter" || launcherErr.Meta["id"] != "fixture-adapter" || launcherErr.Meta["requiredRange"] != "=3.2.0" || launcherErr.Meta["selectedVersion"] != "3.3.0" {
		t.Fatalf("component error details = %#v", launcherErr.Meta)
	}
	if len(launcherErr.Meta) != 4 {
		t.Fatalf("component error has non-canonical detail aliases: %#v", launcherErr.Meta)
	}
	if launcherErr.Hint != "Choose compatible versions or pass an explicit supported provider." {
		t.Fatalf("component error hint = %q", launcherErr.Hint)
	}
}

func TestUpdateValidationRefusesDowngradeAndNamesInstalledPluginRange(t *testing.T) {
	active := contracts.InstallReceipt{Plan: contracts.ResolvedInstallPlan{
		ServiceGraph: contracts.ServiceGraph{PlatformVersion: "3.2.0"},
		Artifacts:    []contracts.Artifact{{ID: "fixture-compat", Kind: "plugin", Version: "1.0.0", PlatformRange: "=3.2.0"}},
	}}
	target := contracts.ResolvedInstallPlan{ServiceGraph: contracts.ServiceGraph{PlatformVersion: "3.1.0"}}
	_, err := ValidateUpdate(active, target, policyCatalog())
	if launcherErr, ok := err.(*diag.Diag); !ok || launcherErr.Code != "KB_UPDATE_DOWNGRADE_REFUSED" || launcherErr.Hint != "use `kb-create rollback`" {
		t.Fatalf("downgrade error = %#v", err)
	}
	target.ServiceGraph.PlatformVersion = "3.3.0"
	_, err = ValidateUpdate(active, target, policyCatalog())
	launcherErr, ok := err.(*diag.Diag)
	if !ok || launcherErr.Code != contracts.CodeIncompatibleComponents || launcherErr.Meta["kind"] != "plugin" || launcherErr.Meta["id"] != "fixture-compat" || launcherErr.Meta["requiredRange"] != "=3.2.0" || launcherErr.Meta["selectedVersion"] != "3.3.0" {
		t.Fatalf("component compatibility error = %#v", err)
	}
	if launcherErr.Hint != "Update or remove plugin fixture-compat first, then rerun `kb-create update`." {
		t.Fatalf("component compatibility hint = %q", launcherErr.Hint)
	}
}

// case: S-053.1
func TestStrictPolicyStillAcceptsDeclaredSemverRanges(t *testing.T) {
	if err := compatible("^2.0.0", "2.0.0", "plugin commit platform"); err != nil {
		t.Fatalf("strict selection rejected a version its declared range admits: %v", err)
	}
}
