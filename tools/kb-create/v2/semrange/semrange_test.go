package semrange

import "testing"

func TestParsedRangeEvaluatesEveryOperator(t *testing.T) {
	for _, test := range []struct {
		version, constraint string
		want                bool
	}{
		{"2.4.0", ">=2.0.0 <3.0.0 || ^4.0.0", true},
		{"4.1.0", ">=2.0.0 <3.0.0 || ^4.0.0", true},
		{"3.1.0", ">=2.0.0 <3.0.0 || ^4.0.0", false},
		{"0.2.8", "^0.2.0", true},
		{"0.3.0", "^0.2.0", false},
		{"1.4.9", "~1.4.0", true},
		{"1.5.0", "~1.4.0", false},
		{"2.0.0", "=2.0.0", true},
		{"2.0.1", "=2.0.0", false},
		{"2.0.1", ">2.0.0", true},
		{"2.0.0", ">2.0.0", false},
		{"2.0.0", ">=2.0.0", true},
		{"2.0.0", "<2.0.1", true},
		{"2.0.1", "<=2.0.1", true},
		{"3.3.0-rc.2", ">=3.0.0 <4.0.0", true},
		{"9.8.7", "*", true},
	} {
		candidate, err := ParseVersion(test.version)
		if err != nil {
			t.Fatalf("ParseVersion(%q): %v", test.version, err)
		}
		constraint, err := ParseRange(test.constraint)
		if err != nil {
			t.Fatalf("ParseRange(%q): %v", test.constraint, err)
		}
		if got := constraint.Satisfies(candidate); got != test.want {
			t.Errorf("ParseRange(%q).Satisfies(%q) = %v, want %v", test.constraint, test.version, got, test.want)
		}
	}
}

func TestParseRangeRejectsRangesOutsideSupportedGrammar(t *testing.T) {
	for _, invalid := range []string{"^3", ">=3.0.0 ||", "1.x", "~", ">=*"} {
		if _, err := ParseRange(invalid); err == nil {
			t.Errorf("ParseRange(%q) succeeded", invalid)
		}
	}
}

func TestParseVersionRejectsInvalidNumericFormsAndOverflow(t *testing.T) {
	for _, invalid := range []string{"1.2.3-01", "1.2.3-alpha..1", "1.2.3+bad+metadata", "01.2.3", "18446744073709551616.0.0", "1.2.3-18446744073709551616"} {
		if _, err := ParseVersion(invalid); err == nil {
			t.Errorf("ParseVersion(%q) succeeded", invalid)
		}
	}
}

func TestCompareUsesSemVerPrereleaseOrdering(t *testing.T) {
	for _, test := range []struct {
		left, right string
		want        int
	}{
		{"1.0.0-alpha", "1.0.0-alpha.1", -1},
		{"1.0.0-alpha.2", "1.0.0-alpha.10", -1},
		{"1.0.0-beta", "1.0.0-alpha", 1},
		{"1.0.0-rc.1", "1.0.0", -1},
	} {
		got, err := Compare(test.left, test.right)
		if err != nil || got != test.want {
			t.Errorf("Compare(%q, %q) = %d, %v; want %d", test.left, test.right, got, err, test.want)
		}
	}
}
