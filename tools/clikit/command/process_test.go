package command

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/kb-labs/clikit/diag"
)

type statusHolder struct {
	status diag.ExitStatus
}

func unassignedNamedStatus() (status diag.ExitStatus) { return }

func TestExitRejectsUnselectedStatusSubprocess(t *testing.T) {
	if mode := os.Getenv("KB_EXIT_STATUS_MUTATION"); mode != "" {
		var status diag.ExitStatus
		switch mode {
		case "composite":
			status = diag.ExitStatus{}
		case "variable":
			status = varStatus()
		case "pointer":
			status = *new(diag.ExitStatus)
		case "field":
			status = statusHolder{}.status
		case "named-return":
			status = unassignedNamedStatus()
		default:
			t.Fatalf("unknown mutation mode %q", mode)
		}
		Exit(status)
		t.Fatal("Exit returned")
	}

	for _, mode := range []string{"composite", "variable", "pointer", "field", "named-return"} {
		t.Run(mode, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=^TestExitRejectsUnselectedStatusSubprocess$")
			cmd.Env = append(os.Environ(), "KB_EXIT_STATUS_MUTATION="+mode)
			output, err := cmd.CombinedOutput()
			var exitErr *exec.ExitError
			if !strings.Contains(string(output), "KB_RUNTIME_UNEXPECTED") ||
				!strings.Contains(string(output), "returned without selecting an exit status") {
				t.Fatalf("unselected status diagnosis missing from output: %s", output)
			}
			if !strings.Contains(string(output), "Hint:") || !strings.Contains(string(output), "--debug") {
				t.Fatalf("unselected status recovery hint missing from output: %s", output)
			}
			if err == nil || !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
				t.Fatalf("unselected status exited with %v; output: %s", err, output)
			}
		})
	}
}

func TestUnselectedStatusCodeIsOperationFailure(t *testing.T) {
	if got := (diag.ExitStatus{}).Code(); got != 1 {
		t.Fatalf("unselected ExitStatus.Code() = %d, want operation-failure status 1", got)
	}
}

func varStatus() diag.ExitStatus {
	var status diag.ExitStatus
	return status
}
