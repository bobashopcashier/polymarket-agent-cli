package params

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"regexp"
	"strings"

	"github.com/bobashopcashier/polymarket-agent-cli/internal/contract"
	"github.com/bobashopcashier/polymarket-agent-cli/internal/registry"
)

const (
	MaximumBytes = 64 << 10
	maximumDepth = 64
)

// ReadSource accepts inline JSON or exactly "-" for bounded stdin. File
// shorthand is intentionally unsupported so an agent cannot cause an implicit
// filesystem read by prefixing a value with '@'.
func ReadSource(raw string, stdin io.Reader, maximum int) ([]byte, error) {
	maximum = boundedMaximum(maximum)
	if raw != "-" {
		if len(raw) == 0 || len(raw) > maximum {
			return nil, contract.Invalid("invalid_params", fmt.Sprintf("--params must contain a JSON object no larger than %d bytes", maximum))
		}
		return []byte(raw), nil
	}
	if stdin == nil {
		return nil, contract.Invalid("invalid_params", "--params - requires stdin")
	}
	data, err := io.ReadAll(io.LimitReader(stdin, int64(maximum)+1))
	if err != nil {
		return nil, contract.Invalid("invalid_params", "could not read --params from stdin").WithCause(err)
	}
	if len(data) == 0 || len(data) > maximum {
		return nil, contract.Invalid("invalid_params", fmt.Sprintf("--params stdin must contain a JSON object no larger than %d bytes", maximum))
	}
	return data, nil
}

// DecodeObject parses exactly one JSON object and rejects duplicate keys at any
// nesting level. Numbers remain json.Number values.
func DecodeObject(data []byte, maximum int) (map[string]any, error) {
	maximum = boundedMaximum(maximum)
	if len(data) == 0 || len(data) > maximum {
		return nil, contract.Invalid("invalid_params", fmt.Sprintf("--params must contain a JSON object no larger than %d bytes", maximum))
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	value, err := decodeValue(decoder, 0)
	if err != nil {
		return nil, err
	}
	if token, err := decoder.Token(); err != io.EOF || token != nil {
		return nil, contract.Invalid("invalid_params", "--params must contain exactly one JSON object")
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, contract.Invalid("invalid_params", "--params must contain one JSON object")
	}
	return object, nil
}

// Decode validates a raw object against the authoritative command schema and
// returns a normalized map suitable for typed decoding.
func Decode(data []byte, schema registry.ObjectSpec) (map[string]any, error) {
	object, err := DecodeObject(data, schema.MaximumBytes)
	if err != nil {
		return nil, err
	}
	if err := rejectCredentialKeys(object, ""); err != nil {
		return nil, err
	}
	return validateObject(object, schema.Fields, schema.AdditionalProperties, "")
}

// DecodeInto performs strict schema validation before decoding into dst. dst
// must be a non-nil pointer. Unknown JSON fields are rejected a second time by
// encoding/json to catch registry/type drift.
func DecodeInto(data []byte, schema registry.ObjectSpec, dst any) error {
	object, err := Decode(data, schema)
	if err != nil {
		return err
	}
	normalized, err := json.Marshal(object)
	if err != nil {
		return contract.Internal("could not encode validated parameters", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(normalized))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return contract.Invalid("invalid_params", "validated parameters do not match the command request type").WithCause(err)
	}
	return nil
}

func decodeValue(decoder *json.Decoder, depth int) (any, error) {
	if depth > maximumDepth {
		return nil, contract.Invalid("invalid_params", "--params exceeds the maximum nesting depth")
	}
	token, err := decoder.Token()
	if err != nil {
		return nil, contract.Invalid("invalid_params", "--params contains invalid JSON").WithCause(err)
	}
	switch typed := token.(type) {
	case json.Delim:
		switch typed {
		case '{':
			object := map[string]any{}
			for decoder.More() {
				keyToken, keyErr := decoder.Token()
				if keyErr != nil {
					return nil, contract.Invalid("invalid_params", "--params contains invalid JSON").WithCause(keyErr)
				}
				key, ok := keyToken.(string)
				if !ok {
					return nil, contract.Invalid("invalid_params", "--params contains a non-string object key")
				}
				if _, exists := object[key]; exists {
					return nil, contract.Invalid("duplicate_parameter", fmt.Sprintf("--params contains duplicate field %q", key))
				}
				value, valueErr := decodeValue(decoder, depth+1)
				if valueErr != nil {
					return nil, valueErr
				}
				object[key] = value
			}
			if _, closeErr := decoder.Token(); closeErr != nil {
				return nil, contract.Invalid("invalid_params", "--params contains an unterminated object").WithCause(closeErr)
			}
			return object, nil
		case '[':
			array := []any{}
			for decoder.More() {
				value, valueErr := decodeValue(decoder, depth+1)
				if valueErr != nil {
					return nil, valueErr
				}
				array = append(array, value)
			}
			if _, closeErr := decoder.Token(); closeErr != nil {
				return nil, contract.Invalid("invalid_params", "--params contains an unterminated array").WithCause(closeErr)
			}
			return array, nil
		default:
			return nil, contract.Invalid("invalid_params", "--params contains an unexpected delimiter")
		}
	default:
		return token, nil
	}
}

func validateObject(object map[string]any, fields []registry.FieldSpec, additional bool, prefix string) (map[string]any, error) {
	definitions := make(map[string]registry.FieldSpec, len(fields))
	for _, field := range fields {
		definitions[field.Name] = field
	}
	for name := range object {
		if _, exists := definitions[name]; !exists && !additional {
			return nil, contract.Invalid("unknown_parameter", fmt.Sprintf("unknown parameter %q", joinPath(prefix, name)))
		}
	}
	result := make(map[string]any, len(object)+len(fields))
	for name, value := range object {
		definition, exists := definitions[name]
		if !exists {
			result[name] = value
			continue
		}
		validated, err := validateField(value, definition, joinPath(prefix, name))
		if err != nil {
			return nil, err
		}
		result[name] = validated
	}
	for _, field := range fields {
		if _, exists := object[field.Name]; exists {
			continue
		}
		if field.Required {
			return nil, contract.Invalid("missing_parameter", fmt.Sprintf("missing required parameter %q", joinPath(prefix, field.Name)))
		}
		if field.Default != nil {
			result[field.Name] = field.Default
		}
	}
	return result, nil
}

func validateField(value any, field registry.FieldSpec, path string) (any, error) {
	if value == nil {
		if field.Nullable {
			return nil, nil
		}
		return nil, contract.Invalid("invalid_parameter_type", fmt.Sprintf("parameter %q cannot be null", path))
	}
	if !matchesKind(value, field.Kind) {
		return nil, contract.Invalid("invalid_parameter_type", fmt.Sprintf("parameter %q must be %s", path, field.Kind))
	}
	switch typed := value.(type) {
	case string:
		if field.MaxBytes > 0 && len(typed) > field.MaxBytes {
			return nil, contract.Invalid("parameter_too_large", fmt.Sprintf("parameter %q exceeds %d bytes", path, field.MaxBytes))
		}
		if err := RejectControls(path, typed); err != nil {
			return nil, err
		}
		switch field.Normalize {
		case registry.NormalizeTrim:
			typed = strings.TrimSpace(typed)
		case registry.NormalizeLowercase:
			typed = strings.ToLower(strings.TrimSpace(typed))
		case registry.NormalizeUppercase:
			typed = strings.ToUpper(strings.TrimSpace(typed))
		}
		if len(field.Enum) > 0 && !contains(field.Enum, typed) {
			return nil, contract.Invalid("invalid_parameter_value", fmt.Sprintf("parameter %q must be one of: %s", path, strings.Join(field.Enum, ", ")))
		}
		if field.Pattern != "" {
			pattern, err := regexp.Compile(field.Pattern)
			if err != nil {
				return nil, contract.Internal("command registry contains an invalid parameter pattern", err)
			}
			if !pattern.MatchString(typed) {
				return nil, contract.Invalid("invalid_parameter_value", fmt.Sprintf("parameter %q has an invalid format", path))
			}
		}
		return typed, nil
	case json.Number:
		if field.Kind == registry.KindInteger && !isInteger(typed.String()) {
			return nil, contract.Invalid("invalid_parameter_value", fmt.Sprintf("parameter %q must be a whole number", path))
		}
		if field.Minimum != nil || field.Maximum != nil {
			if err := withinBounds(path, typed.String(), field.Minimum, field.Maximum); err != nil {
				return nil, err
			}
		}
		return typed, nil
	case map[string]any:
		return validateObject(typed, field.Properties, field.AdditionalProperties, path)
	case []any:
		if field.MaxItems > 0 && len(typed) > field.MaxItems {
			return nil, contract.Invalid("parameter_too_large", fmt.Sprintf("parameter %q accepts at most %d items", path, field.MaxItems))
		}
		if field.Items == nil {
			return typed, nil
		}
		result := make([]any, len(typed))
		for index, item := range typed {
			validated, err := validateValueSpec(item, *field.Items, fmt.Sprintf("%s[%d]", path, index))
			if err != nil {
				return nil, err
			}
			result[index] = validated
		}
		return result, nil
	default:
		return value, nil
	}
}

func validateValueSpec(value any, spec registry.ValueSpec, path string) (any, error) {
	field := registry.FieldSpec{
		Name: path, Kind: spec.Kind, Nullable: spec.Nullable, Enum: spec.Enum,
		Pattern: spec.Pattern, Format: spec.Format, MaxBytes: spec.MaxBytes,
		MaxItems: spec.MaxItems, Items: spec.Items, Properties: spec.Properties,
		AdditionalProperties: spec.AdditionalProperties,
	}
	return validateField(value, field, path)
}

func matchesKind(value any, kind registry.FieldKind) bool {
	switch kind {
	case registry.KindString:
		_, ok := value.(string)
		return ok
	case registry.KindBoolean:
		_, ok := value.(bool)
		return ok
	case registry.KindInteger:
		number, ok := value.(json.Number)
		return ok && isInteger(number.String())
	case registry.KindNumber:
		_, ok := value.(json.Number)
		return ok
	case registry.KindObject:
		_, ok := value.(map[string]any)
		return ok
	case registry.KindArray:
		_, ok := value.([]any)
		return ok
	default:
		return false
	}
}

func isInteger(value string) bool {
	if value == "" {
		return false
	}
	if value[0] == '-' {
		value = value[1:]
	}
	if value == "" {
		return false
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

func withinBounds(path, value string, minimum, maximum *string) error {
	number, ok := new(big.Rat).SetString(value)
	if !ok {
		return contract.Invalid("invalid_parameter_value", fmt.Sprintf("parameter %q must be a decimal number", path))
	}
	if minimum != nil {
		bound, valid := new(big.Rat).SetString(*minimum)
		if !valid {
			return contract.Internal("command registry contains an invalid minimum", nil)
		}
		if number.Cmp(bound) < 0 {
			return contract.Invalid("invalid_parameter_value", fmt.Sprintf("parameter %q must be at least %s", path, *minimum))
		}
	}
	if maximum != nil {
		bound, valid := new(big.Rat).SetString(*maximum)
		if !valid {
			return contract.Internal("command registry contains an invalid maximum", nil)
		}
		if number.Cmp(bound) > 0 {
			return contract.Invalid("invalid_parameter_value", fmt.Sprintf("parameter %q must be at most %s", path, *maximum))
		}
	}
	return nil
}

func boundedMaximum(maximum int) int {
	if maximum <= 0 || maximum > MaximumBytes {
		return MaximumBytes
	}
	return maximum
}

func joinPath(prefix, name string) string {
	if prefix == "" {
		return name
	}
	return prefix + "." + name
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func rejectCredentialKeys(value any, prefix string) error {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			path := joinPath(prefix, key)
			canonical := strings.ToLower(strings.NewReplacer("-", "", "_", "").Replace(key))
			switch canonical {
			case "privatekey", "apikey", "secret", "apisecret", "password", "credential", "credentials", "mnemonic", "seedphrase":
				return contract.Invalid("credential_parameter", fmt.Sprintf("credential field %q is forbidden in --params", path))
			}
			if err := rejectCredentialKeys(child, path); err != nil {
				return err
			}
		}
	case []any:
		for index, child := range typed {
			if err := rejectCredentialKeys(child, fmt.Sprintf("%s[%d]", prefix, index)); err != nil {
				return err
			}
		}
	}
	return nil
}
