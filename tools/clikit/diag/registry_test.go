package diag

import "testing"

func TestHintFor_UnknownCodeIsEmpty(t *testing.T) {
	if got := HintFor("KB_RUNTIME_UNREGISTERED"); got != "" {
		t.Errorf("HintFor(unknown) = %q, want empty", got)
	}
}

func TestRegisterHints_LastWriteWins(t *testing.T) {
	RegisterHints(map[string]string{"KB_TEST_HINT": "first"})
	RegisterHints(map[string]string{"KB_TEST_HINT": "second"})
	if got := HintFor("KB_TEST_HINT"); got != "second" {
		t.Errorf("HintFor after re-register = %q, want second", got)
	}
}

func TestExitCode_NilIsOK(t *testing.T) {
	if got := ExitCode(nil); got != ExitOK {
		t.Errorf("ExitCode(nil) = %d, want %d", got, ExitOK)
	}
}
