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
