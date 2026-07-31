package contract

import (
	"errors"
	"fmt"
)

// ErrorCategory groups stable error codes into retry and operator-action classes.
type ErrorCategory string

const (
	CategoryInput         ErrorCategory = "input"
	CategoryAuth          ErrorCategory = "auth"
	CategoryPolicy        ErrorCategory = "policy"
	CategoryNotFound      ErrorCategory = "not_found"
	CategoryProvider      ErrorCategory = "provider"
	CategoryTransient     ErrorCategory = "transient"
	CategoryPartial       ErrorCategory = "partial"
	CategoryIndeterminate ErrorCategory = "indeterminate"
	CategoryInternal      ErrorCategory = "internal"
)

// Error is safe for machine output. Details must never contain credentials,
// signatures, request headers, or an unbounded upstream response body.
type Error struct {
	Code      string         `json:"code"`
	Category  ErrorCategory  `json:"category"`
	Message   string         `json:"message"`
	Retryable bool           `json:"retryable"`
	Hint      string         `json:"hint,omitempty"`
	Details   map[string]any `json:"details,omitempty"`
	ExitCode  ExitCode       `json:"exitCode"`
	Cause     error          `json:"-"`
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func NewError(code string, category ErrorCategory, message string, exit ExitCode) *Error {
	return &Error{Code: code, Category: category, Message: message, ExitCode: exit}
}

func Invalid(code, message string) *Error {
	return NewError(code, CategoryInput, message, ExitInvalidInput)
}

func PolicyDenied(code, message string) *Error {
	return NewError(code, CategoryPolicy, message, ExitPolicy)
}

func Internal(message string, cause error) *Error {
	return &Error{
		Code: "internal_error", Category: CategoryInternal, Message: message,
		ExitCode: ExitInternal, Cause: cause,
	}
}

// AsError converts an arbitrary error to the stable contract without exposing
// its implementation text as a machine-facing error code.
func AsError(err error) *Error {
	if err == nil {
		return nil
	}
	var appErr *Error
	if errors.As(err, &appErr) {
		if appErr.ExitCode == ExitOK {
			copy := *appErr
			copy.ExitCode = ExitInternal
			return &copy
		}
		return appErr
	}
	return Internal("an internal error occurred", err)
}

func (e *Error) WithHint(hint string) *Error {
	if e == nil {
		return nil
	}
	e.Hint = hint
	return e
}

func (e *Error) WithDetails(details map[string]any) *Error {
	if e == nil {
		return nil
	}
	e.Details = details
	return e
}

func (e *Error) WithCause(cause error) *Error {
	if e == nil {
		return nil
	}
	e.Cause = cause
	return e
}

func (e *Error) String() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}
