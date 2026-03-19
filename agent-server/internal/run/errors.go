package run

import (
	"errors"
	"fmt"
)

type ErrorKind string

const (
	ErrorKindInvalid     ErrorKind = "invalid"
	ErrorKindConflict    ErrorKind = "conflict"
	ErrorKindUnavailable ErrorKind = "unavailable"
	ErrorKindInternal    ErrorKind = "internal"
)

type Error struct {
	Kind    ErrorKind
	Code    string
	Message string
	Details map[string]any
	Err     error
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.Code == "" {
		return e.Message
	}
	if e.Message == "" {
		return e.Code
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func newError(kind ErrorKind, code, message string, details map[string]any, err error) *Error {
	return &Error{
		Kind:    kind,
		Code:    code,
		Message: message,
		Details: details,
		Err:     err,
	}
}

func invalidError(code, message string, details map[string]any, err error) *Error {
	return newError(ErrorKindInvalid, code, message, details, err)
}

func conflictError(code, message string, details map[string]any, err error) *Error {
	return newError(ErrorKindConflict, code, message, details, err)
}

func unavailableError(code, message string, details map[string]any, err error) *Error {
	return newError(ErrorKindUnavailable, code, message, details, err)
}

func internalError(code, message string, details map[string]any, err error) *Error {
	return newError(ErrorKindInternal, code, message, details, err)
}

func asError(err error) (*Error, bool) {
	var target *Error
	if errors.As(err, &target) {
		return target, true
	}
	return nil, false
}
