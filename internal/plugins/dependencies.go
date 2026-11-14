package plugins

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Dependency declares one required plugin and the semantic-version range that
// satisfies it. Dependencies are resolved from the installed registry; a
// package never supplies or auto-installs another package.
type Dependency struct {
	ID      ID     `yaml:"id" json:"id"`
	Version string `yaml:"version" json:"version"`
}

// dependencyError builds a typed, joined dependency error while preserving a
// human-readable explanation for the admin API.
func dependencyError(base error, format string, args ...any) error {
	return fmt.Errorf("%w: %s", base, fmt.Sprintf(format, args...))
}

// semVersion is a parsed semantic version. Constraint operands may be partial
// (1 or 1.2); resolved plugin versions are always complete.
type semVersion struct {
	major, minor, patch int
	pre                 []string
	hasMinor, hasPatch  bool
}

func parseSemVersion(raw string) (semVersion, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return semVersion{}, errors.New("version is empty")
	}
	core := value
	var pre []string
	if index := strings.IndexByte(core, '+'); index >= 0 {
		core = core[:index]
	}
	if index := strings.IndexByte(core, '-'); index >= 0 {
		preText := core[index+1:]
		core = core[:index]
		if preText == "" {
			return semVersion{}, fmt.Errorf("invalid prerelease in %q", raw)
		}
		pre = strings.Split(preText, ".")
		for _, identifier := range pre {
			if identifier == "" {
				return semVersion{}, fmt.Errorf("invalid prerelease in %q", raw)
			}
		}
	}
	parts := strings.Split(core, ".")
	if len(parts) == 0 || len(parts) > 3 {
		return semVersion{}, fmt.Errorf("invalid semantic version %q", raw)
	}
	numbers := make([]int, 3)
	for index, part := range parts {
		if part == "" || (len(part) > 1 && part[0] == '0') {
			return semVersion{}, fmt.Errorf("invalid semantic version %q", raw)
		}
		for _, character := range part {
			if character < '0' || character > '9' {
				return semVersion{}, fmt.Errorf("invalid semantic version %q", raw)
			}
		}
		parsed, err := strconv.Atoi(part)
		if err != nil || parsed < 0 {
			return semVersion{}, fmt.Errorf("invalid semantic version %q", raw)
		}
		numbers[index] = parsed
	}
	return semVersion{
		major: numbers[0], minor: numbers[1], patch: numbers[2],
		pre: pre, hasMinor: len(parts) >= 2, hasPatch: len(parts) >= 3,
	}, nil
}

func compareSemVersion(left, right semVersion) int {
	if left.major != right.major {
		return compareInt(left.major, right.major)
	}
	if left.minor != right.minor {
		return compareInt(left.minor, right.minor)
	}
	if left.patch != right.patch {
		return compareInt(left.patch, right.patch)
	}
	if len(left.pre) == 0 && len(right.pre) == 0 {
		return 0
	}
	if len(left.pre) == 0 {
		return 1
	}
	if len(right.pre) == 0 {
		return -1
	}
	for index := 0; index < len(left.pre) && index < len(right.pre); index++ {
		a, b := left.pre[index], right.pre[index]
		aNumeric, aErr := strconv.Atoi(a)
		bNumeric, bErr := strconv.Atoi(b)
		switch {
		case aErr == nil && bErr == nil:
			if aNumeric != bNumeric {
				return compareInt(aNumeric, bNumeric)
			}
		case aErr == nil:
			return -1
		case bErr == nil:
			return 1
		default:
			if a != b {
				if a < b {
					return -1
				}
				return 1
			}
		}
	}
	return compareInt(len(left.pre), len(right.pre))
}

func compareInt(left, right int) int {
	switch {
	case left < right:
		return -1
	case left > right:
		return 1
	default:
		return 0
	}
}

// versionSatisfies evaluates the documented range grammar. Supported forms:
// exact and partial versions, comparison operators, caret, tilde, whitespace or
// comma separated AND terms, and "||" OR alternatives. It fails closed on an
// unknown operator or malformed version.
func versionSatisfies(version, constraint string) (bool, error) {
	candidate, err := parseSemVersion(version)
	if err != nil || !candidate.hasMinor || !candidate.hasPatch {
		return false, fmt.Errorf("invalid plugin version %q", version)
	}
	constraint = strings.TrimSpace(constraint)
	if constraint == "" {
		return false, errors.New("dependency version range is required")
	}
	alternatives := strings.Split(constraint, "||")
	for _, alternative := range alternatives {
		if strings.TrimSpace(alternative) == "" {
			return false, errors.New("dependency version range has an empty alternative")
		}
	}
	for _, alternative := range alternatives {
		ok, termErr := alternativeSatisfies(candidate, alternative)
		if termErr != nil {
			return false, termErr
		}
		if ok {
			return true, nil
		}
	}
	return false, nil
}

func alternativeSatisfies(candidate semVersion, alternative string) (bool, error) {
	fields := strings.Fields(strings.ReplaceAll(alternative, ",", " "))
	terms := make([]string, 0, len(fields))
	for index := 0; index < len(fields); index++ {
		field := fields[index]
		if isOperatorOnly(field) {
			if index+1 >= len(fields) {
				return false, fmt.Errorf("operator %q has no version operand", field)
			}
			terms = append(terms, field+fields[index+1])
			index++
			continue
		}
		terms = append(terms, field)
	}
	if len(terms) == 0 {
		return false, errors.New("dependency version range is empty")
	}
	for _, term := range terms {
		ok, err := termSatisfies(candidate, term)
		if err != nil {
			return false, err
		}
		if !ok {
			return false, nil
		}
	}
	return true, nil
}

func isOperatorOnly(value string) bool {
	switch value {
	case ">", ">=", "<", "<=", "=", "^", "~":
		return true
	default:
		return false
	}
}

func termSatisfies(candidate semVersion, term string) (bool, error) {
	if term == "*" || term == "x" || term == "X" {
		return true, nil
	}
	operator := ""
	for _, prefix := range []string{">=", "<=", ">", "<", "=", "^", "~"} {
		if strings.HasPrefix(term, prefix) {
			operator = prefix
			term = strings.TrimSpace(term[len(prefix):])
			break
		}
	}
	if term == "" {
		return false, errors.New("dependency version range is empty")
	}
	operand, err := parseSemVersion(term)
	if err != nil {
		return false, err
	}
	switch operator {
	case "", "=":
		lower := operand
		upper := nextPartial(operand)
		return compareSemVersion(candidate, lower) >= 0 && compareSemVersion(candidate, upper) < 0, nil
	case ">":
		return compareSemVersion(candidate, operand) > 0, nil
	case ">=":
		return compareSemVersion(candidate, operand) >= 0, nil
	case "<":
		return compareSemVersion(candidate, operand) < 0, nil
	case "<=":
		return compareSemVersion(candidate, operand) <= 0, nil
	case "^":
		return compareSemVersion(candidate, operand) >= 0 && compareSemVersion(candidate, caretUpper(operand)) < 0, nil
	case "~":
		return compareSemVersion(candidate, operand) >= 0 && compareSemVersion(candidate, tildeUpper(operand)) < 0, nil
	default:
		return false, fmt.Errorf("unsupported dependency operator %q", operator)
	}
}

// nextPartial returns the exclusive upper bound for an exact/partial operand:
// 1.2.3 -> 1.2.4, 1.2 -> 1.3.0, 1 -> 2.0.0.
func nextPartial(operand semVersion) semVersion {
	switch {
	case operand.hasPatch:
		return semVersion{major: operand.major, minor: operand.minor, patch: operand.patch + 1, hasMinor: true, hasPatch: true}
	case operand.hasMinor:
		return semVersion{major: operand.major, minor: operand.minor + 1, hasMinor: true, hasPatch: true}
	default:
		return semVersion{major: operand.major + 1, hasMinor: true, hasPatch: true}
	}
}

func caretUpper(operand semVersion) semVersion {
	switch {
	case operand.major > 0:
		return semVersion{major: operand.major + 1, hasMinor: true, hasPatch: true}
	case operand.hasMinor && operand.minor > 0:
		return semVersion{minor: operand.minor + 1, hasMinor: true, hasPatch: true}
	case operand.hasMinor:
		return semVersion{patch: operand.patch + 1, hasMinor: true, hasPatch: true}
	default:
		return semVersion{major: 1, hasMinor: true, hasPatch: true}
	}
}

func tildeUpper(operand semVersion) semVersion {
	if operand.hasMinor {
		return semVersion{major: operand.major, minor: operand.minor + 1, hasMinor: true, hasPatch: true}
	}
	return semVersion{major: operand.major + 1, hasMinor: true, hasPatch: true}
}
