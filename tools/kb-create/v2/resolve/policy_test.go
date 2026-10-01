package resolve

import (
	"testing"

	"github.com/kb-labs/create/v2/catalog"
	"github.com/kb-labs/create/v2/contracts"
)

func policyCatalog() catalog.Catalog {
	return catalog.Catalog{
		Channels: map[contracts.Channel]string{contracts.ChannelStable: "3.2.0"},
		Compatibility: &catalog.CompatibilityMatrix{Schema: catalog.CompatibilitySchema, Labels: []catalog.CompatibilityLabel{
			{ID: "platform@3.2.0", Kind: "platform", ArtifactID: "platform", Version: "3.2.0", Requires: []catalog.CompatibilityRelation{{Label: "sdk@3.0.0", Constraint: ">=3.0.0 <4.0.0"}}, Status: "prepared", ValidatedBy: []string{"test"}},
			{ID: "sdk@3.0.0", Kind: "sdk", ArtifactID: "sdk", Version: "3.0.0", Status: "prepared", ValidatedBy: []string{"test"}},
			{ID: "sdk@3.2.0", Kind: "sdk", ArtifactID: "sdk", Version: "3.2.0", Status: "prepared", ValidatedBy: []string{"test"}},
		}},
		Platforms: []catalog.PlatformBundle{{ID: "platform", Version: "3.2.0", Package: "@kb/platform", SHA256: "platform", SDKRange: ">=3.0.0 <4.0.0", Host: &contracts.HostSpec{Command: "kb-host", HealthURL: "http://127.0.0.1:4000/health"}, Profiles: map[string]contracts.ServiceGraph{"default": {}}}},
		SDKs: []catalog.Component{
			{ID: "sdk", Version: "3.0.0", Package: "@kb/sdk", SHA256: "sdk-3.0.0"},
			{ID: "sdk", Version: "3.2.0", Package: "@kb/sdk", SHA256: "sdk-3.2.0"},
		},
	}
}

func TestStrictRequiresExactMatrixRelationAndCompatibleUsesMatrixRange(t *testing.T) {
	request := contracts.InstallRequest{PlatformRoot: "/tmp/kb-policy", Platform: contracts.VersionSelector{Version: "3.2.0"}, SDK: contracts.VersionSelector{Version: "3.2.0"}, Policy: contracts.PolicyStrict}
	if _, err := Plan(request, policyCatalog()); err == nil {
		t.Fatal("strict policy accepted an SDK without an exact matrix relation")
	} else if launcherErr, ok := err.(*contracts.LauncherError); !ok || launcherErr.Code != contracts.CodeIncompatibleComponents || launcherErr.Details["component"] != "SDK" || launcherErr.Details["version"] != "3.2.0" || launcherErr.Details["range"] != ">=3.0.0 <4.0.0" || launcherErr.Hint != "Use an exact platform/SDK pair from the release matrix or choose --policy compatible." {
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
	request := contracts.InstallRequest{PlatformRoot: "/tmp/kb-policy", Platform: contracts.VersionSelector{Version: "3.2.0"}, SDK: contracts.VersionSelector{Version: "3.3.0-rc.2"}, Policy: contracts.PolicyCompatible}
	if _, err := Plan(request, source); err != nil {
		t.Fatalf("compatible policy rejected a prerelease admitted by the range: %v", err)
	}
}

func TestIncompatibleComponentErrorExposesRangeAndRemediationHint(t *testing.T) {
	err := compatible("=3.2.0", "3.3.0", "adapter fixture-adapter platform", contracts.PolicyCompatible)
	launcherErr, ok := err.(*contracts.LauncherError)
	if !ok || launcherErr.Code != contracts.CodeIncompatibleComponents {
		t.Fatalf("component error = %#v", err)
	}
	if launcherErr.Details["component"] != "adapter fixture-adapter platform" || launcherErr.Details["range"] != "=3.2.0" || launcherErr.Details["version"] != "3.3.0" {
		t.Fatalf("component error details = %#v", launcherErr.Details)
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
	err := ValidateUpdate(active, target, policyCatalog())
	if launcherErr, ok := err.(*contracts.LauncherError); !ok || launcherErr.Code != "KB_UPDATE_DOWNGRADE_REFUSED" || launcherErr.Hint != "use `kb-create rollback`" {
		t.Fatalf("downgrade error = %#v", err)
	}
	target.ServiceGraph.PlatformVersion = "3.3.0"
	err = ValidateUpdate(active, target, policyCatalog())
	launcherErr, ok := err.(*contracts.LauncherError)
	if !ok || launcherErr.Code != "KB_UPDATE_INCOMPATIBLE" || launcherErr.Details["component"] != "fixture-compat" || launcherErr.Details["range"] != "=3.2.0" {
		t.Fatalf("component compatibility error = %#v", err)
	}
	if launcherErr.Hint != "Update or remove plugin fixture-compat first, then rerun `kb-create update`." {
		t.Fatalf("component compatibility hint = %q", launcherErr.Hint)
	}
}
