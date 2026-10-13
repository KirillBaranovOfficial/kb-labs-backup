package command

import (
	"bytes"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/kb-labs/clikit/diag"
	"github.com/kb-labs/clikit/result"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type rejectingResultWriter struct{ err error }

func (writer rejectingResultWriter) Write([]byte) (int, error) { return 0, writer.err }

func TestReturnedEmitterRecordsOnlyAfterSuccessfulResultWrite(t *testing.T) {
	for _, mode := range []struct {
		name string
		args []string
	}{
		{name: "human"},
		{name: "json", args: []string{"--json"}},
	} {
		t.Run(mode.name, func(t *testing.T) {
			var emitted int
			root := &cobra.Command{Use: "tool"}
			root.Flags().Bool("json", false, "emit JSON")
			root.RunE = func(*cobra.Command, []string) error {
				return result.ReturnWithEmitter(map[string]string{"credential": "sentinel"}, func(write func() error) *diag.Diag {
					if err := write(); err != nil {
						return diag.New("KB_RUNTIME_UNEXPECTED", "The result write failed.", diag.WithCauseAndReason("writer rejected the result"))
					}
					emitted++
					return nil
				})
			}
			var stderr bytes.Buffer
			if code := Execute(root, mode.args, rejectingResultWriter{err: fmt.Errorf("closed output")}, &stderr); code == 0 {
				t.Fatal("failed result write returned success")
			}
			if emitted != 0 {
				t.Fatalf("post-write callback ran %d times after failed output", emitted)
			}
			if !strings.Contains(stderr.String(), "The result write failed.") {
				t.Fatalf("output failure was not rendered as a typed diagnostic: %s", stderr.String())
			}

			var stdout, successStderr bytes.Buffer
			root = &cobra.Command{Use: "tool"}
			root.Flags().Bool("json", false, "emit JSON")
			root.RunE = func(*cobra.Command, []string) error {
				return result.ReturnWithEmitter(map[string]string{"credential": "sentinel"}, func(write func() error) *diag.Diag {
					if err := write(); err != nil {
						return diag.New("KB_RUNTIME_UNEXPECTED", "The result write failed.", diag.WithCauseAndReason("writer rejected the result"))
					}
					emitted++
					return nil
				})
			}
			if code := Execute(root, mode.args, &stdout, &successStderr); code != 0 {
				t.Fatalf("successful result write exit code = %d, stderr=%s", code, successStderr.String())
			}
			if emitted != 1 {
				t.Fatalf("post-write callback count = %d, want 1", emitted)
			}
			if !strings.Contains(stdout.String(), "sentinel") {
				t.Fatalf("successful result was not written before callback: %s", stdout.String())
			}
		})
	}
}

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

func TestExecuteKeepsSliceFlagValuesStableAcrossRepeatedInvocations(t *testing.T) {
	var localItems, inheritedItems []string
	var observed [][]string
	var inheritedExecutionValues []pflag.Value
	root := &cobra.Command{Use: "tool"}
	root.PersistentFlags().StringSliceVar(&inheritedItems, "inherited-items", []string{"parent-default"}, "inherited items")
	run := &cobra.Command{
		Use: "run",
		RunE: func(command *cobra.Command, _ []string) error {
			inheritedExecutionValues = append(inheritedExecutionValues, command.Flags().Lookup("inherited-items").Value)
			inherited, err := command.Flags().GetStringSlice("inherited-items")
			if err != nil {
				return err
			}
			observed = append(observed, []string{
				strings.Join(localItems, ","),
				strings.Join(inherited, ","),
				fmt.Sprintf("local changed=%t inherited changed=%t", command.Flags().Lookup("items").Changed, command.Flags().Lookup("inherited-items").Changed),
			})
			return nil
		},
	}
	run.Flags().StringSliceVar(&localItems, "items", []string{"local-default"}, "local items")
	root.AddCommand(run)

	var stdout, stderr bytes.Buffer
	var localValue, inheritedValue any
	for call := 0; call < 300; call++ {
		args := []string{"run"}
		if call%2 == 0 {
			args = append(args, "--items", fmt.Sprintf("local-%d", call), "--inherited-items", fmt.Sprintf("parent-%d", call))
		}
		if code := Execute(root, args, &stdout, &stderr); code != 0 {
			t.Fatalf("Execute call %d exit code = %d; stdout=%s stderr=%s", call, code, stdout.String(), stderr.String())
		}
		localFlag := run.Flags().Lookup("items")
		inheritedFlag := root.PersistentFlags().Lookup("inherited-items")
		if call == 0 {
			localValue, inheritedValue = localFlag.Value, inheritedFlag.Value
		} else if localFlag.Value != localValue || inheritedFlag.Value != inheritedValue {
			t.Fatalf("flag value identity changed at call %d: local %T -> %T; inherited %T -> %T", call,
				localValue, localFlag.Value, inheritedValue, inheritedFlag.Value)
		}
		if depth := sliceResetWrapperDepth(localFlag.Value); depth > 1 {
			t.Fatalf("local slice reset wrapper depth at call %d = %d, want at most one", call, depth)
		}
		if depth := sliceResetWrapperDepth(inheritedFlag.Value); depth > 1 {
			t.Fatalf("inherited slice reset wrapper depth at call %d = %d, want at most one", call, depth)
		}
		stdout.Reset()
		stderr.Reset()
	}
	if len(observed) != 300 {
		t.Fatalf("observed invocations = %d, want 300", len(observed))
	}
	for call, value := range inheritedExecutionValues {
		if call > 0 && value != inheritedExecutionValues[0] {
			t.Fatalf("inherited effective flag value identity changed at call %d: %T -> %T", call, inheritedExecutionValues[0], value)
		}
	}
	for call, values := range observed {
		wantLocal, wantInherited := "local-default", "parent-default"
		changed := "local changed=false inherited changed=false"
		if call%2 == 0 {
			wantLocal, wantInherited = fmt.Sprintf("local-%d", call), fmt.Sprintf("parent-%d", call)
			changed = "local changed=true inherited changed=true"
		}
		want := []string{wantLocal, wantInherited, changed}
		if !reflect.DeepEqual(values, want) {
			t.Fatalf("invocation %d values = %#v, want %#v", call, values, want)
		}
	}
}

func sliceResetWrapperDepth(value pflag.Value) int {
	wrapped, ok := value.(*resettableSliceValue)
	if !ok {
		return 0
	}
	return 1 + sliceResetWrapperDepth(wrapped.value.(pflag.Value))
}
