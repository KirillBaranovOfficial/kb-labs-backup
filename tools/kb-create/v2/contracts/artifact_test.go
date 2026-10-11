package contracts

import (
	"testing"

	"github.com/kb-labs/clikit/diag"
)

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

func TestValidateArtifactIdentitiesRejectsMissingIdentity(t *testing.T) {
	for _, tc := range []struct {
		name     string
		artifact Artifact
	}{
		{"missing id", Artifact{Kind: "plugin"}},
		{"package without plugin id", Artifact{Kind: "plugin", Package: "@kb/fixture-plugin"}},
		{"package without adapter id", Artifact{Kind: "adapter", Package: "@kb/fixture-adapter"}},
		{"registered package without id", Artifact{Kind: "platform-member", Registry: "plugin", Package: "@kb/fixture-plugin"}},
		{"empty", Artifact{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan := ResolvedInstallPlan{Artifacts: []Artifact{tc.artifact}}
			err := ValidatePlanArtifactIdentities(plan)
			if err == nil {
				t.Fatalf("expected validation to reject artifact without identity: %#v", tc.artifact)
			}
			failure, ok := err.(*diag.Diag)
			if !ok || failure.Code != CodeArtifactIdentityInvalid {
				t.Fatalf("expected CodeArtifactIdentityInvalid, got %#v", err)
			}
		})
	}
}
