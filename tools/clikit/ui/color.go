package ui

import (
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// ColorEnabled reports whether ANSI color should be used when writing to w.
// NO_COLOR follows https://no-color.org/ and wins when non-empty. FORCE_COLOR
// follows https://github.com/chalk/supports-color: 0 disables; other non-empty
// values enable. With neither override, TERM=dumb disables color and color
// requires a character device.
func ColorEnabled(w io.Writer) bool {
	return resolveColorSupport(isTerminalWriter(w), os.Getenv("NO_COLOR"), os.Getenv("FORCE_COLOR"), os.Getenv("TERM"))
}

// StdoutColorEnabled reports the color policy for the CLI's standard output.
func StdoutColorEnabled() bool {
	return ColorEnabled(os.Stdout)
}

func isTerminalWriter(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

func resolveColorSupport(isTTY bool, noColor, forceColor, term string) bool {
	if noColor != "" {
		return false
	}
	if forceColor == "0" {
		return false
	}
	if forceColor != "" {
		return true
	}
	if strings.EqualFold(term, "dumb") {
		return false
	}
	return isTTY
}

func color(enabled bool, ansi string) lipgloss.TerminalColor {
	if !enabled {
		return lipgloss.NoColor{}
	}
	return lipgloss.Color(ansi)
}
