package apperr

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

// APIError is a structured error for HTTP responses.
type APIError struct {
	Status  int
	Code    string
	Message string
	Details map[string]any
}

func (e *APIError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func New(status int, code, message string, details map[string]any) *APIError {
	return &APIError{
		Status:  status,
		Code:    code,
		Message: message,
		Details: details,
	}
}

func NewBadRequest(code, message string, details map[string]any) *APIError {
	return New(http.StatusBadRequest, code, message, details)
}

func NewNotFound(code, message string, details map[string]any) *APIError {
	return New(http.StatusNotFound, code, message, details)
}

func NewConflict(code, message string, details map[string]any) *APIError {
	return New(http.StatusConflict, code, message, details)
}

func NewInternal(code, message string, details map[string]any) *APIError {
	return New(http.StatusInternalServerError, code, message, details)
}

func As(err error) (*APIError, bool) {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr, true
	}
	return nil, false
}

type responseEnvelope struct {
	Error errorEnvelope `json:"error"`
}

type errorEnvelope struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

// Write writes an APIError JSON payload.
func Write(w http.ResponseWriter, err error) {
	apiErr, ok := As(err)
	if !ok {
		apiErr = NewInternal(CodeInternalError, "Internal server error", nil)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(apiErr.Status)
	_ = json.NewEncoder(w).Encode(responseEnvelope{
		Error: errorEnvelope{
			Code:    apiErr.Code,
			Message: apiErr.Message,
			Details: apiErr.Details,
		},
	})
}
