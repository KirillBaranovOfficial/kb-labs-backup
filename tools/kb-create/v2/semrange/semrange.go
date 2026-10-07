// Package semrange parses and evaluates the semantic-version subset used by
// sealed KB release compatibility metadata.
package semrange

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var identifierPattern = regexp.MustCompile(`^[0-9A-Za-z-]+$`)

// Version is a parsed semantic version. Its fields are private so callers can
// only obtain values that passed the shared parser.
type Version struct {
	major, minor, patch uint64
	pre                 []identifier
}

type identifier struct {
	text    string
	numeric bool
	number  uint64
}

// Range is a parsed disjunction of comparator conjunctions.
type Range struct {
	alternatives [][]comparator
}

type comparator struct {
	operator string
	version  Version
}

// ParseVersion parses a strict major.minor.patch SemVer value.
func ParseVersion(value string) (Version, error) {
	if value == "" || strings.TrimSpace(value) != value {
		return Version{}, fmt.Errorf("version %q is not valid semantic version syntax", value)
	}
	if strings.Count(value, "+") > 1 {
		return Version{}, fmt.Errorf("version %q has invalid build metadata", value)
	}
	if strings.Contains(value, "+") {
		build := strings.SplitN(value, "+", 2)[1]
		if build == "" || !validIdentifiers(build, false) {
			return Version{}, fmt.Errorf("version %q has invalid build metadata", value)
		}
		value = strings.SplitN(value, "+", 2)[0]
	}
	base, preText, hasPre := strings.Cut(value, "-")
	parts := strings.Split(base, ".")
	if len(parts) != 3 {
		return Version{}, fmt.Errorf("version %q must use major.minor.patch", value)
	}
	values := make([]uint64, 3)
	for index, part := range parts {
		if part == "" || (len(part) > 1 && part[0] == '0') {
			return Version{}, fmt.Errorf("version %q has an invalid numeric component", value)
		}
		number, err := strconv.ParseUint(part, 10, 64)
		if err != nil {
			return Version{}, fmt.Errorf("version %q has an invalid numeric component", value)
		}
		values[index] = number
	}
	result := Version{major: values[0], minor: values[1], patch: values[2]}
	if hasPre {
		if preText == "" || !validIdentifiers(preText, true) {
			return Version{}, fmt.Errorf("version %q has an invalid prerelease identifier", value)
		}
		for _, part := range strings.Split(preText, ".") {
			id := identifier{text: part}
			if numericIdentifier(part) {
				number, err := strconv.ParseUint(part, 10, 64)
				if err != nil {
					return Version{}, fmt.Errorf("version %q has a prerelease number outside the supported 64-bit range", value)
				}
				id.numeric, id.number = true, number
			}
			result.pre = append(result.pre, id)
		}
	}
	return result, nil
}

func numericIdentifier(value string) bool {
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return value != ""
}

func validIdentifiers(value string, rejectNumericLeadingZero bool) bool {
	for _, part := range strings.Split(value, ".") {
		if !identifierPattern.MatchString(part) {
			return false
		}
		if rejectNumericLeadingZero && numericIdentifier(part) && len(part) > 1 && part[0] == '0' {
			return false
		}
	}
	return true
}

// ParseRange parses exact versions, *, ^, ~, comparison operators,
// whitespace-conjoined comparators, and || alternatives. An empty range is
// the unconstrained range.
func ParseRange(value string) (Range, error) {
	if strings.TrimSpace(value) == "" || strings.TrimSpace(value) == "*" {
		return Range{}, nil
	}
	result := Range{}
	for _, alternative := range strings.Split(value, "||") {
		tokens := strings.Fields(alternative)
		if len(tokens) == 0 {
			return Range{}, fmt.Errorf("range %q has an empty alternative", value)
		}
		group := make([]comparator, 0, len(tokens))
		for index := 0; index < len(tokens); index++ {
			token := tokens[index]
			operator := "="
			explicitOperator := false
			for _, prefix := range []string{">=", "<=", ">", "<", "=", "^", "~"} {
				if strings.HasPrefix(token, prefix) {
					operator, token = prefix, strings.TrimPrefix(token, prefix)
					explicitOperator = true
					break
				}
			}
			if token == "" && explicitOperator {
				index++
				if index == len(tokens) {
					return Range{}, fmt.Errorf("range %q has an operator without a version", value)
				}
				token = tokens[index]
			}
			if token == "*" {
				if operator != "=" {
					return Range{}, fmt.Errorf("range %q uses * with an operator", value)
				}
				continue
			}
			version, err := ParseVersion(token)
			if err != nil {
				return Range{}, fmt.Errorf("range %q: %w", value, err)
			}
			group = append(group, comparator{operator: operator, version: version})
		}
		result.alternatives = append(result.alternatives, group)
	}
	return result, nil
}

// AtLeast constructs a lower-bound range from a parsed version.
func AtLeast(version Version) Range {
	return Range{alternatives: [][]comparator{{{operator: ">=", version: version}}}}
}

// Satisfies evaluates an already parsed version against an already parsed range.
func (r Range) Satisfies(candidate Version) bool {
	if len(r.alternatives) == 0 {
		return true
	}
	for _, alternative := range r.alternatives {
		matches := true
		for _, condition := range alternative {
			comparison := compare(candidate, condition.version)
			ok := false
			switch condition.operator {
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
				ok = comparison >= 0 && candidate.major == condition.version.major
				if condition.version.major == 0 {
					ok = comparison >= 0 && candidate.major == 0 && candidate.minor == condition.version.minor
					if condition.version.minor == 0 {
						ok = comparison >= 0 && candidate.major == 0 && candidate.minor == 0 && candidate.patch == condition.version.patch
					}
				}
			case "~":
				ok = comparison >= 0 && candidate.major == condition.version.major && candidate.minor == condition.version.minor
			}
			if !ok {
				matches = false
				break
			}
		}
		if matches {
			return true
		}
	}
	return false
}

// Compare returns -1, 0, or 1 according to SemVer precedence.
func Compare(leftValue, rightValue string) (int, error) {
	left, err := ParseVersion(leftValue)
	if err != nil {
		return 0, err
	}
	right, err := ParseVersion(rightValue)
	if err != nil {
		return 0, err
	}
	return compare(left, right), nil
}

func compare(left, right Version) int {
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
		switch {
		case a.numeric && b.numeric && a.number < b.number:
			return -1
		case a.numeric && b.numeric && a.number > b.number:
			return 1
		case a.numeric && !b.numeric:
			return -1
		case !a.numeric && b.numeric:
			return 1
		case !a.numeric && !b.numeric && a.text < b.text:
			return -1
		case !a.numeric && !b.numeric && a.text > b.text:
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
