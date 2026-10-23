// Package identity owns the Marketplace package identity grammar.
package identity

import "fmt"

// Validate accepts canonical npm-style identities, including scoped names and
// the unscoped names used by Marketplace.
func Validate(value string) error {
	if value == "" {
		return fmt.Errorf("identity is empty")
	}
	parts := split(value)
	if len(parts) == 0 || len(parts) > 2 {
		return fmt.Errorf("identity must have one name or one scope and name")
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." || part[0] == '.' || part[0] == '_' {
			return fmt.Errorf("identity contains an invalid name segment")
		}
		for _, char := range part {
			if !((char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '-' || char == '_' || char == '.') {
				return fmt.Errorf("identity contains an invalid character")
			}
		}
	}
	return nil
}

func split(value string) []string {
	if value[0] == '@' {
		value = value[1:]
	}
	parts := make([]string, 0, 2)
	start := 0
	for index, char := range value {
		if char == '/' {
			parts = append(parts, value[start:index])
			start = index + 1
		}
	}
	return append(parts, value[start:])
}
