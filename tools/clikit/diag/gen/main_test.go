package main

import "testing"

func TestCatalogGeneratorRejectsMissingExitClass(t *testing.T) {
	err := validate([]entry{{Code: "KB_FIXTURE_MISSING_EXIT_CLASS", HTTPStatus: 500}})
	if err == nil {
		t.Fatal("catalog entry without processExitClass passed generation")
	}
}

func TestCatalogGeneratorAcceptsEveryDeclaredExitClass(t *testing.T) {
	for _, class := range []string{"input-refusal", "operation-failure"} {
		if err := validate([]entry{{Code: "KB_FIXTURE", HTTPStatus: 500, ProcessExitClass: class}}); err != nil {
			t.Fatalf("class %q rejected: %v", class, err)
		}
	}
}

func TestCatalogGeneratorRejectsMissingHTTPStatus(t *testing.T) {
	if err := validate([]entry{{Code: "KB_FIXTURE", ProcessExitClass: "operation-failure"}}); err == nil {
		t.Fatal("catalog entry without httpStatus passed generation")
	}
	if err := validate([]entry{{Code: "KB_FIXTURE", HTTPStatus: 500, ProcessExitClass: "operation-failure"}}); err != nil {
		t.Fatalf("catalog entry with httpStatus failed generation: %v", err)
	}
}
