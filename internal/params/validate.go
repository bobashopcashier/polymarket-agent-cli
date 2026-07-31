package params

import (
	"fmt"
	"math/big"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/bobashopcashier/polymarket-agent-cli/internal/contract"
	"github.com/bobashopcashier/polymarket-agent-cli/internal/sanitize"
)

var (
	decimalPattern   = regexp.MustCompile(`^-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?$`)
	numericIDPattern = regexp.MustCompile(`^[0-9]{1,128}$`)
	hexIDPattern     = regexp.MustCompile(`^0x[0-9a-fA-F]{64}$`)
)

func RejectControls(label, value string) error {
	if !utf8.ValidString(value) {
		return contract.Invalid("unsafe_input", fmt.Sprintf("%s contains invalid UTF-8", label))
	}
	for _, current := range value {
		if IsUnsafeRune(current) {
			return contract.Invalid("unsafe_input", fmt.Sprintf("%s contains a control or directionality character", label))
		}
	}
	return nil
}

func RejectArgumentControls(arguments []string) error {
	for index, argument := range arguments {
		if err := RejectControls(fmt.Sprintf("argument %d", index+1), argument); err != nil {
			return err
		}
	}
	return nil
}

func IsUnsafeRune(current rune) bool {
	return sanitize.IsUnsafeRune(current)
}

func ValidateResourceID(label, value string, maximum int) error {
	if value == "" {
		return contract.Invalid("invalid_resource_id", fmt.Sprintf("%s is required", label))
	}
	if len(value) > maximum {
		return contract.Invalid("invalid_resource_id", fmt.Sprintf("%s exceeds %d bytes", label, maximum))
	}
	if err := RejectControls(label, value); err != nil {
		return err
	}
	if strings.ContainsAny(value, "?#%/\\") || value == "." || value == ".." || strings.Contains(value, "..") {
		return contract.Invalid("invalid_resource_id", fmt.Sprintf("%s contains reserved URL or traversal characters", label))
	}
	return nil
}

func ValidateNumericID(label, value string) error {
	if !numericIDPattern.MatchString(value) {
		return contract.Invalid("invalid_resource_id", fmt.Sprintf("%s must be a numeric identifier", label))
	}
	return nil
}

func ValidateHex32(label, value string) error {
	if !hexIDPattern.MatchString(value) {
		return contract.Invalid("invalid_resource_id", fmt.Sprintf("%s must be a 0x-prefixed 32-byte hexadecimal identifier", label))
	}
	return nil
}

// ValidateDecimal checks a non-exponent decimal string exactly, without any
// float64 conversion. Empty minimum or maximum values disable that bound.
func ValidateDecimal(label, value, minimum, maximum string) error {
	if len(value) == 0 || len(value) > 128 || !decimalPattern.MatchString(value) {
		return contract.Invalid("invalid_decimal", fmt.Sprintf("%s must be a plain decimal string", label))
	}
	number, ok := new(big.Rat).SetString(value)
	if !ok {
		return contract.Invalid("invalid_decimal", fmt.Sprintf("%s must be a valid decimal string", label))
	}
	if minimum != "" {
		bound, valid := new(big.Rat).SetString(minimum)
		if !valid {
			return contract.Internal("invalid minimum decimal invariant", nil)
		}
		if number.Cmp(bound) < 0 {
			return contract.Invalid("invalid_decimal", fmt.Sprintf("%s must be at least %s", label, minimum))
		}
	}
	if maximum != "" {
		bound, valid := new(big.Rat).SetString(maximum)
		if !valid {
			return contract.Internal("invalid maximum decimal invariant", nil)
		}
		if number.Cmp(bound) > 0 {
			return contract.Invalid("invalid_decimal", fmt.Sprintf("%s must be at most %s", label, maximum))
		}
	}
	return nil
}

// EnsureParamsExclusive rejects convenience request inputs when raw params are
// active. Callers should pass only request-shaping tokens, not output controls.
func EnsureParamsExclusive(hasParams bool, convenienceInputs []string) error {
	if !hasParams || len(convenienceInputs) == 0 {
		return nil
	}
	return contract.Invalid("conflicting_inputs", "--params cannot be combined with positional arguments or convenience request flags")
}
