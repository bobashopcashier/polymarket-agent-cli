package httpx

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// ErrorKind is a stable machine-readable classification for transport and
// upstream failures.
type ErrorKind string

const (
	ErrorInvalidRequest   ErrorKind = "invalid_request"
	ErrorCanceled         ErrorKind = "canceled"
	ErrorTimeout          ErrorKind = "timeout"
	ErrorTransport        ErrorKind = "transport"
	ErrorUnauthorized     ErrorKind = "unauthorized"
	ErrorNotFound         ErrorKind = "not_found"
	ErrorRateLimited      ErrorKind = "rate_limited"
	ErrorRejected         ErrorKind = "rejected"
	ErrorServer           ErrorKind = "server_error"
	ErrorResponseTooLarge ErrorKind = "response_too_large"
	ErrorInvalidResponse  ErrorKind = "invalid_response"
)

// Error describes a failure without retaining secrets or an unbounded
// upstream response body.
type Error struct {
	Kind       ErrorKind
	Service    string
	Method     string
	Path       string
	StatusCode int
	Code       string
	Message    string
	Retryable  bool
	RetryAfter time.Duration
	Cause      error
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	prefix := string(e.Kind)
	if e.Service != "" {
		prefix = e.Service + ": " + prefix
	}
	if e.StatusCode != 0 {
		prefix += fmt.Sprintf(" (HTTP %d)", e.StatusCode)
	}
	if e.Message == "" {
		return prefix
	}
	return prefix + ": " + e.Message
}

func (e *Error) Unwrap() error { return e.Cause }

type upstreamError struct {
	Error             string          `json:"error"`
	Code              string          `json:"code"`
	RetryAfterSeconds json.RawMessage `json:"retry_after_seconds"`
}

func readStatusError(service, method, path string, response *http.Response) *Error {
	body, _, readErr := readBounded(response.Body, maxErrorBodyBytes)
	kind, retryable := classifyStatus(response.StatusCode)
	result := &Error{
		Kind:       kind,
		Service:    service,
		Method:     method,
		Path:       path,
		StatusCode: response.StatusCode,
		Message:    http.StatusText(response.StatusCode),
		Retryable:  retryable,
		RetryAfter: parseRetryAfter(response.Header.Get("Retry-After"), time.Now()),
		Cause:      readErr,
	}

	var upstream upstreamError
	if len(body) != 0 && json.Unmarshal(body, &upstream) == nil {
		if upstream.Error != "" {
			result.Message = sanitizeMessage(upstream.Error)
		}
		result.Code = sanitizeCode(upstream.Code)
		if result.RetryAfter == 0 {
			result.RetryAfter = parseRetrySeconds(upstream.RetryAfterSeconds)
		}
	}
	if result.Message == "" {
		result.Message = "upstream rejected the request"
	}
	return result
}

func classifyStatus(status int) (ErrorKind, bool) {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return ErrorUnauthorized, false
	case http.StatusNotFound:
		return ErrorNotFound, false
	case http.StatusRequestTimeout:
		return ErrorTimeout, true
	case http.StatusTooEarly:
		return ErrorRejected, true
	case http.StatusTooManyRequests:
		return ErrorRateLimited, true
	default:
		if status >= 500 {
			return ErrorServer, true
		}
		return ErrorRejected, false
	}
}

func parseRetryAfter(value string, now time.Time) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second
	}
	if when, err := http.ParseTime(value); err == nil && when.After(now) {
		return when.Sub(now)
	}
	return 0
}

func parseRetrySeconds(raw json.RawMessage) time.Duration {
	if len(raw) == 0 {
		return 0
	}
	var seconds int64
	if json.Unmarshal(raw, &seconds) == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		if value, err := strconv.ParseInt(text, 10, 64); err == nil && value >= 0 {
			return time.Duration(value) * time.Second
		}
	}
	return 0
}

func sanitizeCode(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 128 {
		value = value[:128]
	}
	var result strings.Builder
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' || r == '.' {
			result.WriteRune(r)
		}
	}
	return result.String()
}

func sanitizeMessage(value string) string {
	value = strings.TrimSpace(value)
	var result strings.Builder
	for _, r := range value {
		if result.Len() >= 512 {
			break
		}
		if unicode.IsControl(r) || isBidiControl(r) {
			result.WriteRune(' ')
			continue
		}
		result.WriteRune(r)
	}
	return strings.Join(strings.Fields(result.String()), " ")
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
