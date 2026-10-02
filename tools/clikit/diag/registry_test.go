package diag

import "testing"

func TestNewDoesNotInventHintForUncataloguedCode(t *testing.T) {
	if got := New("KB_RUNTIME_UNREGISTERED", "failed").Hint; got != "" {
		t.Errorf("unknown code hint = %q, want no invented hint", got)
	}
}

func TestExitCode_NilIsOK(t *testing.T) {
	if got := ExitCode(nil); got != ExitOK {
		t.Errorf("ExitCode(nil) = %d, want %d", got, ExitOK)
	}
}
