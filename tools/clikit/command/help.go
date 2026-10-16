package command

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/kb-labs/clikit/diag"
	"github.com/kb-labs/clikit/result"
	"github.com/spf13/cobra"
)

// Root is the shared identity contract for Cobra and non-Cobra dispatchers.
type Root interface {
	Name() string
}

type invocationRoot struct {
	name string
}

func (r invocationRoot) Name() string { return r.name }

// HandleRootHelp prints a tool's shared root usage for root help spellings.
// Cobra-backed tools use the same spellings through Execute's Cobra boundary.
// InvocationRoot creates the common root metadata for a CLI whose dispatcher
// is not Cobra-backed. Its identity is derived from the invoked executable.
func InvocationRoot(invocation string) Root {
	name := filepath.Base(invocation)
	name = strings.TrimSuffix(name, filepath.Ext(name))
	return invocationRoot{name: name}
}

func HandleRootHelp(args []string, root Root, usage string, output io.Writer) bool {
	if len(args) == 0 {
		return false
	}
	if args[0] != "--help" && args[0] != "-h" && args[0] != "help" {
		return false
	}
	_, _ = fmt.Fprintf(output, "Usage: %s %s\n", root.Name(), usage)
	return true
}

// HandleUnknownCommand renders one actionable input diagnostic when a
// dispatcher cannot resolve an invocation against its command set.
func HandleUnknownCommand(root Root, args []string, stdout, stderr io.Writer, mode result.Mode, resolved bool) (diag.ExitStatus, bool) {
	if resolved {
		return diag.ExitStatus{}, false
	}
	failure := diag.New("KB_RUNTIME_UNKNOWN_COMMAND", "The requested command is not available.",
		diag.WithBinary(root.Name()))
	return result.RenderDiag(stdout, stderr, failure, mode), true
}

// HandleCobraHelp handles Cobra help requests at the shared tool boundary,
// without entering a command's execution path.
func HandleCobraHelp(root *cobra.Command, args []string, output io.Writer) bool {
	requested := false
	path := make([]string, 0, len(args))
	for _, arg := range args {
		switch arg {
		case "--help", "-h":
			requested = true
		case "help":
			requested = true
		default:
			if strings.HasPrefix(arg, "-") {
				continue
			}
			path = append(path, arg)
		}
	}
	if !requested {
		return false
	}
	if len(path) > 0 && path[0] == "help" {
		path = path[1:]
	}
	selected, _, err := root.Find(path)
	if err != nil || selected == nil {
		selected = root
	}
	selected.SetOut(output)
	selected.SetErr(output)
	_ = selected.Help()
	return true
}
