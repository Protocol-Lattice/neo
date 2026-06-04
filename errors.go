package neo

import (
	"errors"
	"fmt"
	"net/http"
)

// ErrorCode is a transport-agnostic, machine-readable error classifier.
// Each code maps to exactly one HTTP status via ErrorCode.HTTPStatus.
type ErrorCode string

const (
	CodeBadRequest       ErrorCode = "BAD_REQUEST"
	CodeUnauthorized     ErrorCode = "UNAUTHORIZED"
	CodeForbidden        ErrorCode = "FORBIDDEN"
	CodeNotFound         ErrorCode = "NOT_FOUND"
	CodeMethodNotAllowed ErrorCode = "METHOD_NOT_ALLOWED"
	CodeConflict         ErrorCode = "CONFLICT"
	CodeTooManyRequests  ErrorCode = "TOO_MANY_REQUESTS"
	CodeInternal         ErrorCode = "INTERNAL"
	CodeNotImplemented   ErrorCode = "NOT_IMPLEMENTED"
	CodeUnavailable      ErrorCode = "UNAVAILABLE"
	CodeTimeout          ErrorCode = "TIMEOUT"
)

var codeToStatus = map[ErrorCode]int{
	CodeBadRequest:       http.StatusBadRequest,
	CodeUnauthorized:     http.StatusUnauthorized,
	CodeForbidden:        http.StatusForbidden,
	CodeNotFound:         http.StatusNotFound,
	CodeMethodNotAllowed: http.StatusMethodNotAllowed,
	CodeConflict:         http.StatusConflict,
	CodeTooManyRequests:  http.StatusTooManyRequests,
	CodeInternal:         http.StatusInternalServerError,
	CodeNotImplemented:   http.StatusNotImplemented,
	CodeUnavailable:      http.StatusServiceUnavailable,
	CodeTimeout:          http.StatusGatewayTimeout,
}

// HTTPStatus returns the HTTP status the code maps to, defaulting to 500 for
// unknown or empty codes.
func (c ErrorCode) HTTPStatus() int {
	if status, ok := codeToStatus[c]; ok {
		return status
	}
	return http.StatusInternalServerError
}

// Error is the structured error procedures return to control the response
// status and to hand the client a machine-readable code. Any non-*Error
// returned by a handler is treated as CodeInternal (HTTP 500), so existing
// code that returns plain errors keeps working unchanged.
type Error struct {
	Code    ErrorCode
	Message string
	cause   error
}

// NewError builds an *Error with the given code and message.
func NewError(code ErrorCode, message string) *Error {
	return &Error{Code: code, Message: message}
}

// Errorf builds an *Error with a formatted message.
func Errorf(code ErrorCode, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// WrapError attaches a code and message to an existing error, preserving it for
// errors.Is/errors.As via Unwrap.
func WrapError(code ErrorCode, message string, cause error) *Error {
	return &Error{Code: code, Message: message, cause: cause}
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	switch {
	case e.Code == "":
		return e.Message
	case e.Message == "":
		return string(e.Code)
	default:
		return string(e.Code) + ": " + e.Message
	}
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

// asError normalizes any error into an *Error, defaulting unknown errors to
// CodeInternal with their original message.
func asError(err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		code := e.Code
		if code == "" {
			code = CodeInternal
		}
		message := e.Message
		if message == "" {
			message = err.Error()
		}
		return &Error{Code: code, Message: message, cause: e.cause}
	}
	return &Error{Code: CodeInternal, Message: err.Error()}
}

// writeProcedureError serializes an error to the wire with the right HTTP
// status and a machine-readable code.
func writeProcedureError(w http.ResponseWriter, err error) {
	e := asError(err)
	writeJSON(w, e.Code.HTTPStatus(), Response{Code: string(e.Code), Error: e.Message})
}

// responseError reconstructs a typed *Error from a decoded Response so clients
// can inspect the code via errors.As.
func responseError(res Response) error {
	if res.Error == "" && res.Code == "" {
		return &Error{Code: CodeInternal, Message: "unknown error"}
	}
	return &Error{Code: ErrorCode(res.Code), Message: res.Error}
}
