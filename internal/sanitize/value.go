package sanitize

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/bobashopcashier/polymarket-agent-cli/internal/contract"
)

// Value converts a JSON-serializable value into inert JSON data. Every string
// value and object key is sanitized. If two distinct keys become identical
// after sanitization, Value rejects the document instead of overwriting data.
// Numbers are retained as json.Number so large IDs and exact decimals do not
// pass through float64.
func Value(value any) (any, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, contract.Internal("could not encode value for sanitization", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	var document any
	if err := decoder.Decode(&document); err != nil {
		return nil, contract.Internal("could not decode value for sanitization", err)
	}
	return sanitizeValue(document, "$")
}

func sanitizeValue(value any, path string) (any, error) {
	switch typed := value.(type) {
	case string:
		return Text(typed), nil
	case map[string]any:
		result := make(map[string]any, len(typed))
		sources := make(map[string]string, len(typed))
		for key, child := range typed {
			safeKey := Text(key)
			if previous, exists := sources[safeKey]; exists && previous != key {
				return nil, contract.Invalid(
					"sanitized_key_collision",
					fmt.Sprintf("object keys collide after sanitization at %s", path),
				).WithDetails(map[string]any{"path": path, "firstKey": Text(previous), "secondKey": Text(key)})
			}
			safeChild, err := sanitizeValue(child, path+"."+safeKey)
			if err != nil {
				return nil, err
			}
			sources[safeKey] = key
			result[safeKey] = safeChild
		}
		return result, nil
	case []any:
		result := make([]any, len(typed))
		for index, child := range typed {
			safeChild, err := sanitizeValue(child, fmt.Sprintf("%s[%d]", path, index))
			if err != nil {
				return nil, err
			}
			result[index] = safeChild
		}
		return result, nil
	case nil, bool, json.Number:
		return value, nil
	default:
		return nil, contract.Internal("sanitizer encountered a non-JSON value", nil)
	}
}
