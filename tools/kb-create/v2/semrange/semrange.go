// Package semrange evaluates and validates the semantic-version range subset
// used by sealed KB release compatibility metadata.
package semrange

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var identifierPattern = regexp.MustCompile(`^[0-9A-Za-z-]+$`)

type version struct {
	major, minor, patch uint64
	pre                 []string
}

func parseVersion(value string) (version, error) {
	value = strings.TrimSpace(value)
	if value == "" || strings.TrimSpace(value) != value {
		return version{}, fmt.Errorf("version %q is not valid semantic version syntax", value)
	}
	if strings.Count(value, "+") > 1 {
		return version{}, fmt.Errorf("version %q has invalid build metadata", value)
	}
	if strings.Contains(value, "+") {
		build := strings.SplitN(value, "+", 2)[1]
		if build == "" || !validIdentifiers(build, false) {
			return version{}, fmt.Errorf("version %q has invalid build metadata", value)
		}
		value = strings.SplitN(value, "+", 2)[0]
	}
	base, preText, hasPre := strings.Cut(value, "-")
	parts := strings.Split(base, ".")
	if len(parts) != 3 {
		return version{}, fmt.Errorf("version %q must use major.minor.patch", value)
	}
	values := make([]uint64, 3)
	for index, part := range parts {
		if part == "" || (len(part) > 1 && part[0] == '0') {
			return version{}, fmt.Errorf("version %q has an invalid numeric component", value)
		}
		number, err := strconv.ParseUint(part, 10, 64)
		if err != nil {
			return version{}, fmt.Errorf("version %q has an invalid numeric component", value)
		}
		values[index] = number
	}
	result := version{major: values[0], minor: values[1], patch: values[2]}
	if hasPre {
		if preText == "" {
			return version{}, fmt.Errorf("version %q has an empty prerelease", value)
		}
		result.pre = strings.Split(preText, ".")
		if !validIdentifiers(preText, true) {
			return version{}, fmt.Errorf("version %q has an invalid prerelease identifier", value)
		}
		for _, identifier := range result.pre {
			numeric := true
			for _, char := range identifier {
				if char < '0' || char > '9' {
					numeric = false
					break
				}
			}
			if numeric {
				if _, err := strconv.ParseUint(identifier, 10, 64); err != nil {
					return version{}, fmt.Errorf("version %q has a prerelease number outside the supported 64-bit range", value)
				}
			}
		}
	}
	return result, nil
}

func validIdentifiers(value string, rejectNumericLeadingZero bool) bool {
	for _, identifier := range strings.Split(value, ".") {
		if !identifierPattern.MatchString(identifier) {
			return false
		}
		if rejectNumericLeadingZero && len(identifier) > 1 && identifier[0] == '0' {
			numeric := true
			for _, char := range identifier {
				if char < '0' || char > '9' {
					numeric = false
					break
				}
			}
			if numeric {
				return false
			}
		}
	}
	return true
}

func compare(left, right version) int {
	for _, pair := range [][2]uint64{{left.major, right.major}, {left.minor, right.minor}, {left.patch, right.patch}} {
		if pair[0] < pair[1] {
			return -1
		}
		if pair[0] > pair[1] {
			return 1
		}
	}
	if len(left.pre) == 0 && len(right.pre) > 0 {
		return 1
	}
	if len(left.pre) > 0 && len(right.pre) == 0 {
		return -1
	}
	for index := 0; index < len(left.pre) && index < len(right.pre); index++ {
		a, b := left.pre[index], right.pre[index]
		an, ae := strconv.ParseUint(a, 10, 64)
		bn, be := strconv.ParseUint(b, 10, 64)
		switch {
		case ae == nil && be == nil && an < bn:
			return -1
		case ae == nil && be == nil && an > bn:
			return 1
		case ae == nil && be != nil:
			return -1
		case ae != nil && be == nil:
			return 1
		case ae != nil && be != nil && a < b:
			return -1
		case ae != nil && be != nil && a > b:
			return 1
		}
	}
	if len(left.pre) < len(right.pre) {
		return -1
	}
	if len(left.pre) > len(right.pre) {
		return 1
	}
	return 0
}

// Validate accepts a deliberately bounded range grammar: exact versions,
// *, ^, ~, comparison operators, whitespace-conjoined comparators, and ||.
func Validate(value string) error {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	for _, alternative := range strings.Split(value, "||") {
		tokens := strings.Fields(alternative)
		if len(tokens) == 0 {
			return fmt.Errorf("range %q has an empty alternative", value)
		}
		for index := 0; index < len(tokens); index++ {
			token := tokens[index]
			operator := ""
			for _, prefix := range []string{">=", "<=", ">", "<", "=", "^", "~"} {
				if strings.HasPrefix(token, prefix) {
					operator, token = prefix, strings.TrimPrefix(token, prefix)
					break
				}
			}
			if token == "" && (operator == ">=" || operator == "<=" || operator == ">" || operator == "<" || operator == "=" || operator == "^" || operator == "~") {
				index++
				if index == len(tokens) {
					return fmt.Errorf("range %q has an operator without a version", value)
				}
				token = tokens[index]
			}
			if token == "*" {
				if operator != "" {
					return fmt.Errorf("range %q uses * with an operator", value)
				}
				continue
			}
			if _, err := parseVersion(token); err != nil {
				return fmt.Errorf("range %q: %w", value, err)
			}
		}
	}
	return nil
}

// Satisfies tests a semantic version against the supported range grammar.
func Satisfies(candidateValue, rangeValue string) (bool, error) {
	candidate, err := parseVersion(candidateValue)
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(rangeValue) == "" || strings.TrimSpace(rangeValue) == "*" {
		return true, nil
	}
	if err := Validate(rangeValue); err != nil {
		return false, err
	}
	for _, alternative := range strings.Split(rangeValue, "||") {
		tokens := strings.Fields(alternative)
		matches := true
		for index := 0; index < len(tokens); index++ {
			token := tokens[index]
			operator := "="
			for _, prefix := range []string{">=", "<=", ">", "<", "=", "^", "~"} {
				if strings.HasPrefix(token, prefix) {
					operator, token = prefix, strings.TrimPrefix(token, prefix)
					break
				}
			}
			if token == "" {
				index++
				token = tokens[index]
			}
			if token == "*" {
				continue
			}
			target, _ := parseVersion(token)
			comparison := compare(candidate, target)
			ok := false
			switch operator {
			case "=":
				ok = comparison == 0
			case ">":
				ok = comparison > 0
			case ">=":
				ok = comparison >= 0
			case "<":
				ok = comparison < 0
			case "<=":
				ok = comparison <= 0
			case "^":
				ok = comparison >= 0 && candidate.major == target.major
				if target.major == 0 {
					ok = comparison >= 0 && candidate.major == 0 && candidate.minor == target.minor
					if target.minor == 0 {
						ok = comparison >= 0 && candidate.major == 0 && candidate.minor == 0 && candidate.patch == target.patch
					}
				}
			case "~":
				ok = comparison >= 0 && candidate.major == target.major && candidate.minor == target.minor
			}
			if !ok {
				matches = false
				break
			}
		}
		if matches {
			return true, nil
		}
	}
	return false, nil
}

// Compare returns -1, 0, or 1 according to SemVer precedence.
func Compare(leftValue, rightValue string) (int, error) {
	left, err := parseVersion(leftValue)
	if err != nil {
		return 0, err
	}
	right, err := parseVersion(rightValue)
	if err != nil {
		return 0, err
	}
	return compare(left, right), nil
}
