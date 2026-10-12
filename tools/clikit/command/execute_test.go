package command

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
	"time"

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

type appendValue []string

func (v *appendValue) String() string { return strings.Join(*v, ",") }
func (v *appendValue) Type() string   { return "string" }
func (v *appendValue) Set(value string) error {
	*v = append(*v, value)
	return nil
}
func (v *appendValue) ResetFlagValue(defaultValue string) error {
	*v = nil
	if defaultValue == "" {
		return nil
	}
	return v.Set(defaultValue)
}

func TestExecuteResetsEveryRegisteredFlagKindBetweenInvocations(t *testing.T) {
	var name string
	var enabled bool
	var count int
	var duration time.Duration
	var items []string
	var defaultItems []string
	var appended appendValue
	var observed []map[string]any

	root := &cobra.Command{Use: "tool"}
	run := &cobra.Command{
		Use: "run",
		RunE: func(command *cobra.Command, _ []string) error {
			observed = append(observed, map[string]any{
				"name": name, "enabled": enabled, "count": count, "duration": duration,
				"items": append([]string(nil), items...), "defaultItems": append([]string(nil), defaultItems...),
				"appended": append([]string(nil), appended...),
				"changed": map[string]bool{
					"name":          command.Flags().Lookup("name").Changed,
					"enabled":       command.Flags().Lookup("enabled").Changed,
					"count":         command.Flags().Lookup("count").Changed,
					"duration":      command.Flags().Lookup("duration").Changed,
					"items":         command.Flags().Lookup("items").Changed,
					"default-items": command.Flags().Lookup("default-items").Changed,
					"append":        command.Flags().Lookup("append").Changed,
				},
			})
			return nil
		},
	}
	run.Flags().StringVar(&name, "name", "default", "name")
	run.Flags().BoolVar(&enabled, "enabled", true, "enabled")
	run.Flags().IntVar(&count, "count", 2, "count")
	run.Flags().DurationVar(&duration, "duration", 2*time.Second, "duration")
	run.Flags().StringSliceVar(&items, "items", []string{"default"}, "items")
	run.Flags().StringSliceVar(&defaultItems, "default-items", []string{"baseline"}, "default items")
	run.Flags().Var(&appended, "append", "append values")
	root.AddCommand(run)

	var stdout, stderr bytes.Buffer
	if code := Execute(root, []string{"run", "--name", "first", "--enabled=false", "--count", "4", "--duration", "1s", "--items", "one,two", "--default-items", "prior", "--append", "old"}, &stdout, &stderr); code != 0 {
		t.Fatalf("first Execute exit code = %d; stderr=%s", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := Execute(root, []string{"run", "--items", "three", "--append", "new"}, &stdout, &stderr); code != 0 {
		t.Fatalf("second Execute exit code = %d; stderr=%s", code, stderr.String())
	}
	want := []map[string]any{
		{
			"name": "first", "enabled": false, "count": 4, "duration": time.Second,
			"items": []string{"one", "two"}, "defaultItems": []string{"prior"}, "appended": []string{"old"},
			"changed": map[string]bool{"name": true, "enabled": true, "count": true, "duration": true, "items": true, "default-items": true, "append": true},
		},
		{
			"name": "default", "enabled": true, "count": 2, "duration": 2 * time.Second,
			"items": []string{"three"}, "defaultItems": []string{"baseline"}, "appended": []string{"new"},
			"changed": map[string]bool{"name": false, "enabled": false, "count": false, "duration": false, "items": true, "default-items": false, "append": true},
		},
	}
	if !reflect.DeepEqual(observed, want) {
		t.Fatalf("invocations observed = %#v, want %#v", observed, want)
	}
	if strings.Contains(stdout.String()+stderr.String(), "unexpected") {
		t.Fatalf("unexpected diagnostic after resetting flags: stdout=%s stderr=%s", stdout.String(), stderr.String())
	}
}

type unsupportedValue string

func (v *unsupportedValue) String() string { return string(*v) }
func (v *unsupportedValue) Type() string   { return "string" }
func (v *unsupportedValue) Set(value string) error {
	*v = unsupportedValue(value)
	return nil
}

func TestExecuteRejectsCustomFlagsWithoutResetContract(t *testing.T) {
	var value unsupportedValue
	var executed bool
	root := &cobra.Command{Use: "tool"}
	run := &cobra.Command{
		Use: "run",
		Run: func(*cobra.Command, []string) { executed = true },
	}
	run.Flags().Var(&value, "custom", "custom value")
	root.AddCommand(run)
	var stdout, stderr bytes.Buffer
	code := Execute(root, []string{"run", "--custom", "value", "--json"}, &stdout, &stderr)
	if code == 0 || executed || !strings.Contains(stdout.String(), "KB_RUNTIME_INPUT_INVALID") ||
		!strings.Contains(stdout.String(), "Flag --custom uses unsupported value type") ||
		!strings.Contains(stdout.String(), "FlagValueResetter") {
		t.Fatalf("unsupported custom flag outcome: exit=%d executed=%t stdout=%s stderr=%s", code, executed, stdout.String(), stderr.String())
	}
}
