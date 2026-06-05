package neo

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
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

// HTTPStatus returns the HTTP status the code maps to, defaulting to 500 for
// unknown or empty codes.
func (c ErrorCode) HTTPStatus() int {
	switch c {
	case CodeBadRequest:
		return http.StatusBadRequest
	case CodeUnauthorized:
		return http.StatusUnauthorized
	case CodeForbidden:
		return http.StatusForbidden
	case CodeNotFound:
		return http.StatusNotFound
	case CodeMethodNotAllowed:
		return http.StatusMethodNotAllowed
	case CodeConflict:
		return http.StatusConflict
	case CodeTooManyRequests:
		return http.StatusTooManyRequests
	case CodeInternal:
		return http.StatusInternalServerError
	case CodeNotImplemented:
		return http.StatusNotImplemented
	case CodeUnavailable:
		return http.StatusServiceUnavailable
	case CodeTimeout:
		return http.StatusGatewayTimeout
	default:
		return http.StatusInternalServerError
	}
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

// ErrorLogger receives full internal errors before a redacted response is sent.
// Replace it in tests or applications that already use structured logging.
var ErrorLogger interface{ Printf(string, ...any) } = log.New(os.Stderr, "neo: ", log.LstdFlags)

const internalErrorMessage = "internal server error"

type normalizedError struct {
	error    *Error
	explicit bool
}

// asError normalizes any error into an *Error. Plain Go errors are internal
// implementation failures and must not leak their messages to clients.
func asError(err error) normalizedError {
	var e *Error
	if errors.As(err, &e) {
		code := e.Code
		if code == "" {
			code = CodeInternal
		}
		message := e.Message
		if message == "" {
			message = code.defaultMessage()
		}
		return normalizedError{error: &Error{Code: code, Message: message, cause: e.cause}, explicit: true}
	}
	return normalizedError{error: &Error{Code: CodeInternal, Message: internalErrorMessage, cause: err}}
}

func (c ErrorCode) defaultMessage() string {
	switch c {
	case CodeBadRequest:
		return "bad request"
	case CodeUnauthorized:
		return "unauthorized"
	case CodeForbidden:
		return "forbidden"
	case CodeNotFound:
		return "not found"
	case CodeMethodNotAllowed:
		return "method not allowed"
	case CodeConflict:
		return "conflict"
	case CodeTooManyRequests:
		return "too many requests"
	case CodeNotImplemented:
		return "not implemented"
	case CodeUnavailable:
		return "unavailable"
	case CodeTimeout:
		return "timeout"
	default:
		return internalErrorMessage
	}
}

// writeProcedureError serializes an error to the wire with the right HTTP
// status and a machine-readable code. Internal failures are logged with their
// original detail but are redacted on the wire.
func writeProcedureError(w http.ResponseWriter, err error) {
	n := asError(err)
	e := n.error
	message := e.Message

	if e.Code == CodeInternal {
		if ErrorLogger != nil {
			ErrorLogger.Printf("internal procedure error: %v", err)
		}
		message = internalErrorMessage
	} else if !n.explicit {
		message = e.Code.defaultMessage()
	}

	writeJSON(w, e.Code.HTTPStatus(), Response{Code: string(e.Code), Error: message})
}

// responseError reconstructs a typed *Error from a decoded Response so clients
// can inspect the code via errors.As.
func responseError(res Response) error {
	if res.Error == "" && res.Code == "" {
		return &Error{Code: CodeInternal, Message: "unknown error"}
	}
	return &Error{Code: ErrorCode(res.Code), Message: res.Error}
}
