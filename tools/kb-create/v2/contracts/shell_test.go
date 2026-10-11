package contracts

import (
	"reflect"
	"testing"
)

func TestShellQuoteAndParseShellCommandsRoundTripArgv(t *testing.T) {
	want := [][]string{{"kb-create", "stop", "--platform-root", "/tmp/Kb Labs/'prod'"},
		{"kb-create", "apply", "--secret-env", "gateway.token=VALUE"}}
	encoded := ""
	for commandIndex, command := range want {
		if commandIndex > 0 {
			encoded += " && "
		}
		for argIndex, argument := range command {
			if argIndex > 0 {
				encoded += " "
			}
			encoded += ShellQuote(argument)
		}
	}
	got, err := ParseShellCommands(encoded)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseShellCommands(%q) = %#v, %v; want %#v", encoded, got, err, want)
	}
}

func TestShellCommandRoundTripsEveryArgument(t *testing.T) {
	want := []string{"kb-create", "stop", "--platform-root", "/tmp/KB Labs/'prod'"}
	encoded := ShellCommand(want...)
	got, err := ParseShellCommands(encoded)
	if err != nil || len(got) != 1 || !reflect.DeepEqual(got[0], want) {
		t.Fatalf("ShellCommand(%q) = %#v, %v; want %#v", encoded, got, err, want)
	}
}

func TestParseShellCommandsRejectsUnsupportedOrIncompleteInput(t *testing.T) {
	for _, input := range []string{"", "cmd &&", "cmd & next", "cmd 'unterminated"} {
		if _, err := ParseShellCommands(input); err == nil {
			t.Errorf("ParseShellCommands(%q) succeeded, want an error", input)
		}
	}
}
