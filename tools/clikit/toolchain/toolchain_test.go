package toolchain

import "testing"

func TestValidateNodeAcceptsOnlySupportedMajor(t *testing.T) {
	if err := ValidateNode("v24.18.0"); err != nil {
		t.Fatal(err)
	}
	for _, version := range []string{"v20.18.0", "v22.13.0", "v26.0.0"} {
		if err := ValidateNode(version); err == nil {
			t.Fatalf("%s must be rejected", version)
		}
	}
}
