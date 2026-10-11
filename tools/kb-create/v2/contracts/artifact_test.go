package contracts

import "testing"

func TestArtifactIsBinaryOnlyForExecutableKind(t *testing.T) {
	for _, kind := range []string{"binary", "platform", "platform-member", "sdk", "plugin", "adapter", "package", "core"} {
		artifact := Artifact{Kind: kind}
		if got, want := artifact.IsBinary(), kind == "binary"; got != want {
			t.Errorf("Artifact{Kind:%q}.IsBinary() = %v, want %v", kind, got, want)
		}
	}
}

func TestValidateArtifactIdentitiesRejectsRepeatedPackageAcrossRoles(t *testing.T) {
	plan := ResolvedInstallPlan{Artifacts: []Artifact{
		{ID: "sqlite", Kind: "platform-member", Package: "@kb/adapters-sqlite"},
		{ID: "sqlite", Kind: "adapter", Package: "@kb/adapters-sqlite"},
	}}
	if err := ValidatePlanArtifactIdentities(plan); err == nil {
		t.Fatal("expected plan validation to reject repeated package identity across artifact roles")
	}
}
