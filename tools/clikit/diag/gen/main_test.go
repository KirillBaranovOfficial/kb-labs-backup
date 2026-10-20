package main

import "testing"

func TestCatalogGeneratorRejectsMissingExitClass(t *testing.T) {
	err := validate([]entry{{Code: "KB_FIXTURE_MISSING_EXIT_CLASS"}})
	if err == nil {
		t.Fatal("catalog entry without processExitClass passed generation")
	}
}

func TestCatalogGeneratorAcceptsEveryDeclaredExitClass(t *testing.T) {
	for _, class := range []string{"input-refusal", "operation-failure"} {
		if err := validate([]entry{{Code: "KB_FIXTURE", ProcessExitClass: class}}); err != nil {
			t.Fatalf("class %q rejected: %v", class, err)
		}
	}
}
