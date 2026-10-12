package command

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestExecuteResetsUnsetStringSliceToItsEmptyDefault(t *testing.T) {
	var values []string
	var observed [][]string
	root := &cobra.Command{Use: "tool"}
	run := &cobra.Command{
		Use: "run",
		RunE: func(_ *cobra.Command, _ []string) error {
			observed = append(observed, append([]string(nil), values...))
			return nil
		},
	}
	run.Flags().StringSliceVar(&values, "items", nil, "items")
	root.AddCommand(run)

	var stdout, stderr bytes.Buffer
	if code := Execute(root, []string{"run", "--items", "first"}, &stdout, &stderr); code != 0 {
		t.Fatalf("first Execute exit code = %d; stderr=%s", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := Execute(root, []string{"run"}, &stdout, &stderr); code != 0 {
		t.Fatalf("second Execute exit code = %d; stderr=%s", code, stderr.String())
	}
	if len(observed) != 2 || !reflect.DeepEqual(observed[0], []string{"first"}) ||
		len(observed[1]) != 0 || values == nil || len(values) != 0 {
		t.Fatalf("unset StringSlice after reset = %#v; observed=%#v, want a non-nil empty slice", values, observed)
	}
	if strings.Contains(stdout.String()+stderr.String(), "unexpected") {
		t.Fatalf("unexpected diagnostic after resetting flags: stdout=%s stderr=%s", stdout.String(), stderr.String())
	}
}
