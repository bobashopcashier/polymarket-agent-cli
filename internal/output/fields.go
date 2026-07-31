package output

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/bobashopcashier/polymarket-agent-cli/internal/contract"
)

const (
	MaximumFieldMaskBytes = 1024
	MaximumFieldPaths     = 64
)

var fieldSegmentPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

type fieldMaskNode map[string]fieldMaskNode

func ParseFieldMask(raw string) ([][]string, error) {
	if len(raw) == 0 || len(raw) > MaximumFieldMaskBytes {
		return nil, contract.Invalid("invalid_fields", fmt.Sprintf("--fields must be between 1 and %d bytes", MaximumFieldMaskBytes))
	}
	parts := strings.Split(raw, ",")
	if len(parts) > MaximumFieldPaths {
		return nil, contract.Invalid("invalid_fields", fmt.Sprintf("--fields accepts at most %d paths", MaximumFieldPaths))
	}
	paths := make([][]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, rawPath := range parts {
		rawPath = strings.TrimSpace(rawPath)
		if rawPath == "" {
			return nil, contract.Invalid("invalid_fields", "--fields contains an empty path")
		}
		segments := strings.Split(rawPath, ".")
		for _, segment := range segments {
			if !fieldSegmentPattern.MatchString(segment) {
				return nil, contract.Invalid("invalid_fields", "--fields paths may contain only letters, numbers, underscores, hyphens, and dots")
			}
		}
		if _, duplicate := seen[rawPath]; duplicate {
			return nil, contract.Invalid("invalid_fields", fmt.Sprintf("--fields contains duplicate path %q", rawPath))
		}
		seen[rawPath] = struct{}{}
		paths = append(paths, segments)
	}
	return paths, nil
}

// ValidateFieldMask verifies a projection entirely from the declared response
// schema. Call it before provider execution so hallucinated fields cannot cause
// a network request.
func ValidateFieldMask(raw string, allowedPaths []string) error {
	paths, err := ParseFieldMask(raw)
	if err != nil {
		return err
	}
	if len(allowedPaths) == 0 {
		return contract.Invalid("invalid_fields", "this command does not expose projectable response fields")
	}
	for _, path := range paths {
		joined := strings.Join(path, ".")
		if !allowedField(joined, allowedPaths) {
			return contract.Invalid("unknown_field", fmt.Sprintf("JSON field path not found in response schema: %s", joined))
		}
	}
	return nil
}

// ProjectData projects only the success envelope's data member. Envelope
// metadata, effects, pagination, and truncation therefore cannot be hidden by
// a field mask. allowedPaths should come from the response schema. If it is
// empty, field existence is checked against the concrete response.
func ProjectData(value any, rawMask string, allowedPaths []string) (any, error) {
	paths, err := ParseFieldMask(rawMask)
	if err != nil {
		return nil, err
	}
	document, err := toDocument(value)
	if err != nil {
		return nil, err
	}
	mask := fieldMaskNode{}
	for _, path := range paths {
		joined := strings.Join(path, ".")
		if len(allowedPaths) > 0 {
			if !allowedField(joined, allowedPaths) {
				return nil, contract.Invalid("unknown_field", fmt.Sprintf("JSON field path not found in response schema: %s", joined))
			}
		} else if !fieldPathExists(document, path) {
			return nil, contract.Invalid("unknown_field", fmt.Sprintf("JSON field path not found: %s", joined))
		}
		current := mask
		for _, segment := range path {
			if current[segment] == nil {
				current[segment] = fieldMaskNode{}
			}
			current = current[segment]
		}
	}
	projected, ok := projectNode(document, mask)
	if !ok {
		return nil, contract.Invalid("invalid_fields", "--fields requires an object or array-of-objects response")
	}
	return projected, nil
}

func ProjectEnvelope(envelope contract.SuccessEnvelope, rawMask string, allowedPaths []string) (contract.SuccessEnvelope, error) {
	if strings.TrimSpace(rawMask) == "" {
		return envelope, nil
	}
	projected, err := ProjectData(envelope.Data, rawMask, allowedPaths)
	if err != nil {
		return contract.SuccessEnvelope{}, err
	}
	envelope.Data = projected
	return envelope, nil
}

func toDocument(value any) (any, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, contract.Internal("could not prepare JSON field projection", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	var document any
	if err := decoder.Decode(&document); err != nil {
		return nil, contract.Internal("could not decode JSON field projection", err)
	}
	return document, nil
}

func allowedField(path string, allowed []string) bool {
	for _, candidate := range allowed {
		if path == candidate || strings.HasPrefix(candidate, path+".") {
			return true
		}
	}
	return false
}

func fieldPathExists(value any, path []string) bool {
	if len(path) == 0 {
		return true
	}
	switch typed := value.(type) {
	case map[string]any:
		next, ok := typed[path[0]]
		return ok && fieldPathExists(next, path[1:])
	case []any:
		for _, item := range typed {
			if fieldPathExists(item, path) {
				return true
			}
		}
	}
	return false
}

func projectNode(value any, mask fieldMaskNode) (any, bool) {
	if len(mask) == 0 {
		return value, true
	}
	switch typed := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(mask))
		for key, childMask := range mask {
			child, exists := typed[key]
			if !exists {
				continue
			}
			if projected, ok := projectNode(child, childMask); ok {
				result[key] = projected
			}
		}
		return result, true
	case []any:
		result := make([]any, 0, len(typed))
		for _, item := range typed {
			if projected, ok := projectNode(item, mask); ok {
				result = append(result, projected)
			}
		}
		return result, true
	default:
		return nil, false
	}
}
