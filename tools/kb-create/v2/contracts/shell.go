package contracts

import (
	"fmt"
	"strings"
)

// ParseShellCommands parses the POSIX words emitted by ShellQuote and the
// command separator used in actionable diagnostic hints. It does not evaluate
// shell expansions, substitutions, redirections, or other shell syntax.
func ParseShellCommands(input string) ([][]string, error) {
	var commands [][]string
	var argv []string
	var word strings.Builder
	started, escaped := false, false
	quote := rune(0)
	flushWord := func() {
		if started {
			argv = append(argv, word.String())
			word.Reset()
			started = false
		}
	}
	flushCommand := func() error {
		flushWord()
		if len(argv) == 0 {
			return fmt.Errorf("shell command sequence contains an empty command")
		}
		commands = append(commands, argv)
		argv = nil
		return nil
	}

	characters := []rune(input)
	for index := 0; index < len(characters); index++ {
		char := characters[index]
		if escaped {
			word.WriteRune(char)
			started, escaped = true, false
			continue
		}
		if quote == '\'' {
			if char == '\'' {
				quote = 0
			} else {
				word.WriteRune(char)
			}
			continue
		}
		if quote == '"' {
			switch char {
			case '"':
				quote = 0
			case '\\':
				escaped = true
			default:
				word.WriteRune(char)
			}
			continue
		}
		switch char {
		case '\'', '"':
			quote, started = char, true
		case '\\':
			escaped, started = true, true
		case ' ', '\t', '\r', '\n':
			flushWord()
		case '&':
			if index+1 >= len(characters) || characters[index+1] != '&' {
				return nil, fmt.Errorf("unsupported shell operator '&'")
			}
			index++
			if err := flushCommand(); err != nil {
				return nil, err
			}
		default:
			word.WriteRune(char)
			started = true
		}
	}
	if escaped || quote != 0 {
		return nil, fmt.Errorf("shell command sequence contains an unterminated quote or escape")
	}
	if len(commands) > 0 && !started && len(argv) == 0 {
		return nil, fmt.Errorf("shell command sequence contains an empty command")
	}
	if started || len(argv) > 0 {
		if err := flushCommand(); err != nil {
			return nil, err
		}
	}
	if len(commands) == 0 {
		return nil, fmt.Errorf("shell command sequence is empty")
	}
	return commands, nil
}
