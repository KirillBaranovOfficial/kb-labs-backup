package semrange

import "testing"

func TestSatisfiesComparatorConjunctionsAndAlternatives(t *testing.T) {
	for _, test := range []struct {
		version, constraint string
		want                bool
	}{
		{"2.4.0", ">=2.0.0 <3.0.0 || ^4.0.0", true},
		{"4.1.0", ">=2.0.0 <3.0.0 || ^4.0.0", true},
		{"3.1.0", ">=2.0.0 <3.0.0 || ^4.0.0", false},
		{"0.2.8", "^0.2.0", true},
		{"0.3.0", "^0.2.0", false},
		{"3.3.0-rc.2", ">=3.0.0 <4.0.0", true},
	} {
		got, err := Satisfies(test.version, test.constraint)
		if err != nil || got != test.want {
			t.Fatalf("Satisfies(%q, %q) = %v, %v; want %v", test.version, test.constraint, got, err, test.want)
		}
	}
}

func TestValidateRejectsRangesOutsideTheSupportedGrammar(t *testing.T) {
	for _, invalid := range []string{"^3", ">=3.0.0 ||", "1.x", "~", ">=*"} {
		if err := Validate(invalid); err == nil {
			t.Errorf("Validate(%q) succeeded", invalid)
		}
	}
}

func TestValidateRejectsInvalidSemVerIdentifiers(t *testing.T) {
	for _, invalid := range []string{"1.2.3-01", "1.2.3-alpha..1", "1.2.3+bad+metadata", "01.2.3", "1.2.3-18446744073709551616"} {
		if err := Validate(invalid); err == nil {
			t.Errorf("Validate(%q) succeeded", invalid)
		}
	}
}

func TestCompareOrdersPrereleasesBeforeStable(t *testing.T) {
	comparison, err := Compare("3.0.1-canary.2", "3.0.1")
	if err != nil || comparison >= 0 {
		t.Fatalf("Compare() = %d, %v; prerelease should precede stable", comparison, err)
	}
}
