package upstream

import (
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

const redacted = "[REDACTED]"

var (
	credentialAssignmentPattern = regexp.MustCompile(`(?i)(private[ _-]?key|api[ _-]?key|secret|passphrase|password|authorization|bearer)([[:space:]]*[:=][[:space:]]*["']?)([^[:space:]"',}]+)`)
	authorizationPattern        = regexp.MustCompile(`(?i)(authorization[[:space:]]*[:=][[:space:]]*)([^\r\n]+)`)
	privateKeyPattern           = regexp.MustCompile(`(?i)0x[0-9a-f]{64}`)
)

func sanitizeJSON(raw []byte, exactSecret string) (json.RawMessage, error) {
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err == nil {
		return nil, fmt.Errorf("multiple JSON values")
	} else if !errorsIsEOF(err) {
		return nil, fmt.Errorf("trailing non-JSON data")
	}
	value, err := sanitizeValue(value, exactSecret, "$")
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(encoded), nil
}

func sanitizeValue(value any, exactSecret, path string) (any, error) {
	switch typed := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(typed))
		originalKeys := make(map[string]string, len(typed))
		for key, child := range typed {
			safeKey := sanitizeText(key)
			if original, exists := originalKeys[safeKey]; exists && original != key {
				return nil, fmt.Errorf("object keys collide after sanitization at %s", path)
			}
			originalKeys[safeKey] = key
			if credentialKey(key) {
				result[safeKey] = redacted
				continue
			}
			safeChild, err := sanitizeValue(child, exactSecret, path+"."+safeKey)
			if err != nil {
				return nil, err
			}
			result[safeKey] = safeChild
		}
		return result, nil
	case []any:
		result := make([]any, len(typed))
		for index, child := range typed {
			safeChild, err := sanitizeValue(child, exactSecret, fmt.Sprintf("%s[%d]", path, index))
			if err != nil {
				return nil, err
			}
			result[index] = safeChild
		}
		return result, nil
	case string:
		return sanitizeDataText(typed, exactSecret), nil
	default:
		return value, nil
	}
}

func credentialKey(key string) bool {
	var normalized strings.Builder
	for _, char := range strings.ToLower(key) {
		if unicode.IsLetter(char) || unicode.IsDigit(char) {
			normalized.WriteRune(char)
		}
	}
	switch normalized.String() {
	case "privatekey", "secret", "clobsecret", "passphrase", "password",
		"apikey", "apikeys", "authorization", "credential", "credentials",
		"mnemonic", "seed", "seedphrase", "signature", "signedtransaction",
		"rawtransaction", "hmac":
		return true
	default:
		return false
	}
}

func sanitizeDataText(value, exactSecret string) string {
	if exactSecret != "" {
		matcher := regexp.MustCompile(`(?i)` + regexp.QuoteMeta(exactSecret))
		value = matcher.ReplaceAllString(value, redacted)
	}
	value = authorizationPattern.ReplaceAllString(value, `${1}`+redacted)
	value = credentialAssignmentPattern.ReplaceAllString(value, `${1}${2}`+redacted)
	return sanitizeText(value)
}

func errorsIsEOF(err error) bool { return err == io.EOF }

func sanitizeErrorText(value, exactSecret string) string {
	value = sanitizeDataText(value, exactSecret)
	value = privateKeyPattern.ReplaceAllString(value, redacted)
	value = strings.Join(strings.Fields(value), " ")
	if len(value) > 2048 {
		value = value[:2048] + "..."
	}
	return value
}

func sanitizeText(value string) string {
	var output strings.Builder
	for len(value) > 0 {
		current, size := utf8.DecodeRuneInString(value)
		if current == utf8.RuneError && size == 1 {
			output.WriteString(`\uFFFD`)
			value = value[1:]
			continue
		}
		value = value[size:]
		if unsafeRune(current) {
			if current <= 0xffff {
				fmt.Fprintf(&output, `\u%04X`, current)
			} else {
				fmt.Fprintf(&output, `\U%08X`, current)
			}
			continue
		}
		output.WriteRune(current)
	}
	return output.String()
}

func unsafeRune(current rune) bool {
	return current < 0x20 || current == 0x7f || current >= 0x80 && current <= 0x9f ||
		unicode.Is(unicode.Cf, current) || current == 0x2028 || current == 0x2029
}
