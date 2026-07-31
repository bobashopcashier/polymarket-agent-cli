package provider

import (
	"fmt"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

var (
	slugPattern       = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,199}$`)
	orderFieldPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)
)

// ValidationError is returned before any network request is made.
type ValidationError struct {
	Field   string
	Problem string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("invalid %s: %s", e.Field, e.Problem)
}

func invalid(field, problem string) error {
	return &ValidationError{Field: field, Problem: problem}
}

func validatePositiveID(field, value string) error {
	if value == "" || len(value) > 20 {
		return invalid(field, "must be a positive decimal identifier")
	}
	id, err := strconv.ParseUint(value, 10, 64)
	if err != nil || id == 0 {
		return invalid(field, "must be a positive decimal identifier")
	}
	return nil
}

func validateSlug(field, value string) error {
	if !slugPattern.MatchString(value) {
		return invalid(field, "must be 1-200 letters, digits, underscores, or hyphens")
	}
	return nil
}

func validateTokenID(value string) error {
	if value == "" || len(value) > 78 {
		return invalid("token_id", "must be a positive uint256 decimal value")
	}
	parsed, ok := new(big.Int).SetString(value, 10)
	if !ok || parsed.Sign() <= 0 || parsed.BitLen() > 256 {
		return invalid("token_id", "must be a positive uint256 decimal value")
	}
	return nil
}

func validateLimit(field string, value, maximum int) error {
	if value < 0 || value > maximum {
		return invalid(field, fmt.Sprintf("must be between 1 and %d when provided", maximum))
	}
	return nil
}

func validateOffset(value int) error {
	if value < 0 || value > 100_000 {
		return invalid("offset", "must be between 0 and 100000")
	}
	return nil
}

func validateCursor(value string) error {
	if len(value) > 2048 || hasUnsafeText(value) {
		return invalid("after_cursor", "is too long or contains unsafe characters")
	}
	return nil
}

func validateOrder(value string) error {
	if value == "" {
		return nil
	}
	if len(value) > 256 {
		return invalid("order", "is too long")
	}
	for _, field := range strings.Split(value, ",") {
		if !orderFieldPattern.MatchString(field) {
			return invalid("order", "must be a comma-separated list of JSON field names")
		}
	}
	return nil
}

func validateSearchQuery(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 256 || hasUnsafeText(value) {
		return "", invalid("query", "must be 1-256 safe characters")
	}
	return value, nil
}

func hasUnsafeText(value string) bool {
	for _, r := range value {
		if unicode.IsControl(r) || isBidiControl(r) {
			return true
		}
	}
	return false
}

func isBidiControl(r rune) bool {
	switch r {
	case '\u061c', '\u200e', '\u200f', '\u202a', '\u202b', '\u202c', '\u202d', '\u202e', '\u2066', '\u2067', '\u2068', '\u2069':
		return true
	default:
		return false
	}
}
