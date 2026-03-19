package apperr

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestConstructors verifies helper constructors set expected HTTP statuses and core fields.
func TestConstructors(t *testing.T) {
	bad := NewBadRequest("BAD", "bad request", map[string]any{"k": "v"})
	if bad.Status != http.StatusBadRequest || bad.Code != "BAD" || bad.Message != "bad request" {
		t.Fatalf("unexpected bad request error: %+v", bad)
	}

	notFound := NewNotFound("NF", "not found", nil)
	if notFound.Status != http.StatusNotFound {
		t.Fatalf("status = %d", notFound.Status)
	}

	conflict := NewConflict("CONFLICT", "conflict", nil)
	if conflict.Status != http.StatusConflict {
		t.Fatalf("status = %d", conflict.Status)
	}

	internal := NewInternal("INT", "internal", nil)
	if internal.Status != http.StatusInternalServerError {
		t.Fatalf("status = %d", internal.Status)
	}
}

// TestAs verifies API error type extraction succeeds for APIError and fails for generic errors.
func TestAs(t *testing.T) {
	apiErr := NewBadRequest("BAD", "bad request", nil)
	got, ok := As(apiErr)
	if !ok || got != apiErr {
		t.Fatalf("As(APIError) = (%v, %v)", got, ok)
	}

	if _, ok := As(errors.New("nope")); ok {
		t.Fatalf("As(non-APIError) expected false")
	}
}

// TestWriteWithAPIError verifies Write serializes provided APIError values as-is.
func TestWriteWithAPIError(t *testing.T) {
	rr := httptest.NewRecorder()
	Write(rr, New(http.StatusTeapot, "TEAPOT", "short and stout", map[string]any{"spout": true}))

	if rr.Code != http.StatusTeapot {
		t.Fatalf("status = %d", rr.Code)
	}

	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	errObj, ok := body["error"].(map[string]any)
	if !ok {
		t.Fatalf("missing error envelope: %v", body)
	}
	if errObj["code"] != "TEAPOT" || errObj["message"] != "short and stout" {
		t.Fatalf("unexpected envelope: %v", errObj)
	}
}

// TestWriteWithGenericErrorFallsBackToInternal verifies Write wraps unknown errors in INTERNAL_ERROR responses.
func TestWriteWithGenericErrorFallsBackToInternal(t *testing.T) {
	rr := httptest.NewRecorder()
	Write(rr, errors.New("boom"))

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", rr.Code)
	}

	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	errObj, ok := body["error"].(map[string]any)
	if !ok {
		t.Fatalf("missing error envelope: %v", body)
	}
	if errObj["code"] != CodeInternalError {
		t.Fatalf("code = %v, want %q", errObj["code"], CodeInternalError)
	}
}
