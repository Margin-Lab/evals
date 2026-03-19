package run

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/marginlab/margin-eval/agent-server/internal/apperr"
)

// TestRunErrorString verifies run error string rendering for nil, code-only, message-only, and combined forms.
func TestRunErrorString(t *testing.T) {
	var nilErr *Error
	if got := nilErr.Error(); got != "" {
		t.Fatalf("nil Error(). got %q", got)
	}

	tests := []struct {
		name string
		err  *Error
		want string
	}{
		{
			name: "code_and_message",
			err:  &Error{Code: "RUN_FAIL", Message: "run failed"},
			want: "RUN_FAIL: run failed",
		},
		{
			name: "code_only",
			err:  &Error{Code: "RUN_FAIL"},
			want: "RUN_FAIL",
		},
		{
			name: "message_only",
			err:  &Error{Message: "run failed"},
			want: "run failed",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.err.Error(); got != tc.want {
				t.Fatalf("Error() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestRunErrorUnwrap verifies Error.Unwrap returns wrapped causes and handles nil receiver safely.
func TestRunErrorUnwrap(t *testing.T) {
	cause := errors.New("root cause")
	err := &Error{Err: cause}
	if got := err.Unwrap(); !errors.Is(got, cause) {
		t.Fatalf("Unwrap() = %v, want %v", got, cause)
	}

	var nilErr *Error
	if got := nilErr.Unwrap(); got != nil {
		t.Fatalf("nil Unwrap() = %v, want nil", got)
	}
}

// TestAsError verifies asError extracts wrapped run errors and rejects non-run errors.
func TestAsError(t *testing.T) {
	cause := errors.New("boom")
	base := conflictError("RUN_BUSY", "run already active", nil, cause)
	wrapped := fmt.Errorf("wrapped: %w", base)

	got, ok := asError(wrapped)
	if !ok {
		t.Fatalf("asError() expected ok")
	}
	if got.Kind != ErrorKindConflict {
		t.Fatalf("kind = %q, want %q", got.Kind, ErrorKindConflict)
	}
	if !errors.Is(got, cause) {
		t.Fatalf("errors.Is(got, cause) = false")
	}

	if _, ok := asError(errors.New("plain")); ok {
		t.Fatalf("asError(plain) expected false")
	}
}

// TestMapRunError verifies run-layer errors are translated to the expected API status/code/message triples.
func TestMapRunError(t *testing.T) {
	tests := []struct {
		name       string
		in         error
		wantStatus int
		wantCode   string
		wantMsg    string
	}{
		{
			name:       "nil",
			in:         nil,
			wantStatus: 0,
		},
		{
			name:       "already_api_error",
			in:         apperr.NewBadRequest(apperr.CodeInvalidRequest, "bad", nil),
			wantStatus: http.StatusBadRequest,
			wantCode:   apperr.CodeInvalidRequest,
			wantMsg:    "bad",
		},
		{
			name:       "invalid_run_error",
			in:         invalidError("RUN_INVALID", "invalid run request", map[string]any{"k": "v"}, nil),
			wantStatus: http.StatusBadRequest,
			wantCode:   "RUN_INVALID",
			wantMsg:    "invalid run request",
		},
		{
			name:       "conflict_run_error",
			in:         conflictError("RUN_CONFLICT", "conflict", nil, nil),
			wantStatus: http.StatusConflict,
			wantCode:   "RUN_CONFLICT",
			wantMsg:    "conflict",
		},
		{
			name:       "unavailable_run_error",
			in:         unavailableError("RUN_UNAVAILABLE", "temporarily unavailable", nil, nil),
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   "RUN_UNAVAILABLE",
			wantMsg:    "temporarily unavailable",
		},
		{
			name:       "internal_run_error",
			in:         internalError("RUN_INTERNAL", "internal failure", nil, nil),
			wantStatus: http.StatusInternalServerError,
			wantCode:   "RUN_INTERNAL",
			wantMsg:    "internal failure",
		},
		{
			name:       "plain_error_maps_internal",
			in:         errors.New("plain"),
			wantStatus: http.StatusInternalServerError,
			wantCode:   apperr.CodeInternalError,
			wantMsg:    "run operation failed",
		},
		{
			name:       "run_error_default_code_message",
			in:         &Error{Kind: ErrorKindInvalid},
			wantStatus: http.StatusBadRequest,
			wantCode:   apperr.CodeInternalError,
			wantMsg:    "run operation failed",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := mapRunError(tc.in)
			if tc.in == nil {
				if got != nil {
					t.Fatalf("mapRunError(nil) = %v, want nil", got)
				}
				return
			}

			apiErr, ok := apperr.As(got)
			if !ok {
				t.Fatalf("mapRunError() returned non-API error: %T %v", got, got)
			}
			if apiErr.Status != tc.wantStatus {
				t.Fatalf("status = %d, want %d", apiErr.Status, tc.wantStatus)
			}
			if apiErr.Code != tc.wantCode {
				t.Fatalf("code = %q, want %q", apiErr.Code, tc.wantCode)
			}
			if apiErr.Message != tc.wantMsg {
				t.Fatalf("message = %q, want %q", apiErr.Message, tc.wantMsg)
			}
		})
	}
}
