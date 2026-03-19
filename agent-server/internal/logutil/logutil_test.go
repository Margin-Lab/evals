package logutil

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// TestShouldRedactEnvKey verifies which environment variable keys are treated as secrets.
func TestShouldRedactEnvKey(t *testing.T) {
	tests := []struct {
		key  string
		want bool
	}{
		{key: "OPENAI_API_KEY", want: true},
		{key: "auth_token", want: true},
		{key: "my_credential", want: true},
		{key: "PASSWORD", want: true},
		{key: "PATH", want: false},
		{key: "HOME", want: false},
	}

	for _, tc := range tests {
		if got := shouldRedactEnvKey(tc.key); got != tc.want {
			t.Fatalf("shouldRedactEnvKey(%q) = %v, want %v", tc.key, got, tc.want)
		}
	}
}

// TestRedactEnv verifies redaction is applied only to sensitive keys and is deterministic.
func TestRedactEnv(t *testing.T) {
	env := map[string]string{
		"OPENAI_API_KEY": "secret",
		"PATH":           "/usr/bin",
	}

	redacted := RedactEnv(env)
	if redacted["PATH"] != "/usr/bin" {
		t.Fatalf("PATH = %q", redacted["PATH"])
	}
	value := redacted["OPENAI_API_KEY"]
	if !strings.HasPrefix(value, "[REDACTED:") || !strings.HasSuffix(value, "]") {
		t.Fatalf("unexpected redacted value %q", value)
	}
	if value == "secret" {
		t.Fatalf("expected redacted secret")
	}
	if value != redactValue("secret") {
		t.Fatalf("redaction should be deterministic")
	}
}

func TestInfoEmitsStructuredRecord(t *testing.T) {
	var buf bytes.Buffer
	prevOut := logOutput
	logOutput = &buf
	defer func() { logOutput = prevOut }()

	Info("run.started", map[string]any{
		"pid":   42,
		"args":  []string{"--a", "--b"},
		"env":   map[string]string{"HOME": "/tmp/home"},
		"notes": "line 1\nline 2",
	})

	raw := strings.TrimSpace(buf.String())
	if raw == "" {
		t.Fatalf("expected structured log output")
	}
	if raw[0] != '{' {
		t.Fatalf("expected JSON line without prefix, got %q", raw)
	}

	var rec structuredLogRecord
	if err := json.Unmarshal([]byte(raw), &rec); err != nil {
		t.Fatalf("decode structured log record: %v", err)
	}
	if rec.V != structuredLogVersion {
		t.Fatalf("v = %d, want %d", rec.V, structuredLogVersion)
	}
	if rec.Kind != logKindStep {
		t.Fatalf("kind = %q, want %q", rec.Kind, logKindStep)
	}
	if rec.Step != "run.started" {
		t.Fatalf("step = %q", rec.Step)
	}
	if rec.Message != "run.started" {
		t.Fatalf("message = %q", rec.Message)
	}
	if rec.Status != "info" {
		t.Fatalf("status = %q", rec.Status)
	}
	if rec.Source != logSourceAgentServer {
		t.Fatalf("source = %q", rec.Source)
	}
	if _, err := time.Parse(time.RFC3339Nano, rec.TS); err != nil {
		t.Fatalf("ts parse error: %v", err)
	}
	if rec.Details["pid"] != "42" {
		t.Fatalf("details.pid = %q", rec.Details["pid"])
	}
	if rec.Details["args"] != `["--a","--b"]` {
		t.Fatalf("details.args = %q", rec.Details["args"])
	}
	if rec.Details["env"] != `{"HOME":"/tmp/home"}` {
		t.Fatalf("details.env = %q", rec.Details["env"])
	}
	if rec.Details["notes"] != "line 1 line 2" {
		t.Fatalf("details.notes = %q", rec.Details["notes"])
	}
}

func TestErrorAndFatalStatus(t *testing.T) {
	var buf bytes.Buffer
	prevOut := logOutput
	logOutput = &buf
	defer func() { logOutput = prevOut }()

	Error("run.failed", map[string]any{"error": "boom"})
	Fatal("server.crash", map[string]any{"reason": "panic"})

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 log lines, got %d", len(lines))
	}
	var first structuredLogRecord
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatalf("decode first record: %v", err)
	}
	var second structuredLogRecord
	if err := json.Unmarshal([]byte(lines[1]), &second); err != nil {
		t.Fatalf("decode second record: %v", err)
	}
	if first.Status != "error" {
		t.Fatalf("first status = %q", first.Status)
	}
	if second.Status != "fatal" {
		t.Fatalf("second status = %q", second.Status)
	}
}

func TestStringifyFieldsDropsEmptyKeys(t *testing.T) {
	fields := map[string]any{
		"  ":      "ignored",
		" valid ": "ok",
	}
	got := stringifyFields(fields)
	if len(got) != 1 {
		t.Fatalf("expected one field, got %v", got)
	}
	if got["valid"] != "ok" {
		t.Fatalf("valid field = %q", got["valid"])
	}
}

func TestNewStdlibLoggerBridgesToStructuredRecord(t *testing.T) {
	var buf bytes.Buffer
	prevOut := logOutput
	logOutput = &buf
	defer func() { logOutput = prevOut }()

	logger := NewStdlibLogger("server.http_internal_error")
	logger.Printf("accept error: %s", "connection reset")

	raw := strings.TrimSpace(buf.String())
	if raw == "" {
		t.Fatalf("expected structured output from stdlib bridge")
	}
	var rec structuredLogRecord
	if err := json.Unmarshal([]byte(raw), &rec); err != nil {
		t.Fatalf("decode structured record: %v", err)
	}
	if rec.Step != "server.http_internal_error" {
		t.Fatalf("step = %q", rec.Step)
	}
	if rec.Status != "error" {
		t.Fatalf("status = %q", rec.Status)
	}
	if rec.Details["raw"] != "accept error: connection reset" {
		t.Fatalf("details.raw = %q", rec.Details["raw"])
	}
}
