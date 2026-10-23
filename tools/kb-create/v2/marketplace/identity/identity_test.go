package identity

import "testing"

func TestValidateMarketplaceIdentity(t *testing.T) {
	for _, value := range []string{"@kb-labs/commit", "journey-npm-only-fixture", "author/plugin"} {
		if err := Validate(value); err != nil {
			t.Errorf("Validate(%q) = %v", value, err)
		}
	}
	for _, value := range []string{"", "@kb-labs/commit/entry", "@/commit", "@kb-labs/", "../commit", "@KB-Labs/commit", "@kb labs/commit"} {
		if err := Validate(value); err == nil {
			t.Errorf("Validate(%q) accepted malformed Marketplace identity", value)
		}
	}
}
