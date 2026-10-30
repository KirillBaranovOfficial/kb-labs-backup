package ui

import (
	_ "embed"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

//go:embed color-cases.json
var colorCasesJSON []byte

func TestResolveColorSupportSharedCases(t *testing.T) {
	var cases []struct {
		Name       string `json:"name"`
		TTY        bool   `json:"tty"`
		NoColor    string `json:"noColor"`
		ForceColor string `json:"forceColor"`
		Term       string `json:"term"`
		Want       bool   `json:"want"`
	}
	if err := json.Unmarshal(colorCasesJSON, &cases); err != nil {
		t.Fatal(err)
	}
	for _, testCase := range cases {
		t.Run(testCase.Name, func(t *testing.T) {
			if got := resolveColorSupport(testCase.TTY, testCase.NoColor, testCase.ForceColor, testCase.Term); got != testCase.Want {
				t.Fatalf("resolveColorSupport(%t, %q, %q, %q) = %t, want %t", testCase.TTY, testCase.NoColor, testCase.ForceColor, testCase.Term, got, testCase.Want)
			}
		})
	}
}

func TestColorEnabledTermDumb(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	t.Setenv("FORCE_COLOR", "")
	t.Setenv("TERM", "dumb")
	if ColorEnabled(os.Stdout) {
		t.Fatal("TERM=dumb must disable color")
	}
}

func TestColorEnabledEnvironmentPrecedence(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("FORCE_COLOR", "1")
	t.Setenv("NO_COLOR", "0")
	if ColorEnabled(os.Stdout) {
		t.Fatal("any non-empty NO_COLOR value must beat FORCE_COLOR")
	}
	t.Setenv("NO_COLOR", "")
	t.Setenv("FORCE_COLOR", "1")
	if !ColorEnabled(os.Stdout) {
		t.Fatal("FORCE_COLOR must enable color for a piped writer")
	}
	t.Setenv("FORCE_COLOR", "0")
	if ColorEnabled(os.Stdout) {
		t.Fatal("FORCE_COLOR=0 must disable color")
	}
}

func TestColorEnabledNonFileWriter(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	t.Setenv("FORCE_COLOR", "")
	t.Setenv("TERM", "xterm-256color")
	if ColorEnabled(&strings.Builder{}) {
		t.Fatal("a non-file writer must not enable color")
	}
}

func TestColorEnabledRegularFile(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	t.Setenv("FORCE_COLOR", "")
	t.Setenv("TERM", "xterm-256color")
	f, err := os.Create(filepath.Join(t.TempDir(), "out.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if ColorEnabled(f) {
		t.Fatal("a regular file is not a terminal")
	}
}

func TestColorDisabledUsesLipglossNoColor(t *testing.T) {
	if _, ok := color(false, "10").(lipgloss.NoColor); !ok {
		t.Fatalf("disabled color type = %T", color(false, "10"))
	}
}

func TestColorEnabledUsesLipglossColor(t *testing.T) {
	got, ok := color(true, "10").(lipgloss.Color)
	if !ok || string(got) != "10" {
		t.Fatalf("enabled color = %T(%v)", color(true, "10"), color(true, "10"))
	}
}
