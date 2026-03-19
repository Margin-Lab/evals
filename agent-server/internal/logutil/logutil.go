package logutil

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"reflect"
	"strings"
	"sync"
	"time"
)

const (
	structuredLogVersion = 1
	logKindStep          = "step"
	logSourceAgentServer = "agent_server"
)

type structuredLogRecord struct {
	V       int               `json:"v"`
	Kind    string            `json:"kind"`
	TS      string            `json:"ts"`
	Message string            `json:"message"`
	Step    string            `json:"step,omitempty"`
	Status  string            `json:"status,omitempty"`
	Source  string            `json:"source,omitempty"`
	Details map[string]string `json:"details,omitempty"`
}

var (
	logOutput   io.Writer = os.Stdout
	logOutputMu sync.Mutex
)

type stdlibBridgeWriter struct {
	event string
}

func Info(event string, fields map[string]any) {
	writeStepRecord("info", event, fields)
}

func Warn(event string, fields map[string]any) {
	writeStepRecord("warn", event, fields)
}

func Error(event string, fields map[string]any) {
	writeStepRecord("error", event, fields)
}

func Fatal(event string, fields map[string]any) {
	writeStepRecord("fatal", event, fields)
}

func NewStdlibLogger(event string) *log.Logger {
	return log.New(&stdlibBridgeWriter{event: sanitizeLine(event)}, "", 0)
}

func SetOutput(w io.Writer) func() {
	if w == nil {
		w = io.Discard
	}
	logOutputMu.Lock()
	prev := logOutput
	logOutput = w
	logOutputMu.Unlock()
	return func() {
		logOutputMu.Lock()
		logOutput = prev
		logOutputMu.Unlock()
	}
}

func (w *stdlibBridgeWriter) Write(p []byte) (int, error) {
	message := sanitizeLine(strings.TrimSpace(string(p)))
	if message != "-" {
		Error(w.event, map[string]any{"raw": message})
	}
	return len(p), nil
}

func writeStepRecord(status, event string, fields map[string]any) {
	record := structuredLogRecord{
		V:       structuredLogVersion,
		Kind:    logKindStep,
		TS:      time.Now().UTC().Format(time.RFC3339Nano),
		Message: sanitizeLine(event),
		Step:    sanitizeLine(event),
		Status:  sanitizeLine(status),
		Source:  logSourceAgentServer,
		Details: stringifyFields(fields),
	}
	body, err := json.Marshal(record)
	if err != nil {
		return
	}

	logOutputMu.Lock()
	defer logOutputMu.Unlock()
	_, _ = logOutput.Write(append(body, '\n'))
}

func RedactEnv(env map[string]string) map[string]string {
	out := make(map[string]string, len(env))
	for key, value := range env {
		if shouldRedactEnvKey(key) {
			out[key] = redactValue(value)
			continue
		}
		out[key] = value
	}
	return out
}

func shouldRedactEnvKey(key string) bool {
	upper := strings.ToUpper(key)
	if strings.Contains(upper, "KEY") || strings.Contains(upper, "TOKEN") || strings.Contains(upper, "SECRET") || strings.Contains(upper, "PASSWORD") {
		return true
	}
	if strings.Contains(upper, "AUTH") || strings.Contains(upper, "CREDENTIAL") {
		return true
	}
	return false
}

func redactValue(v string) string {
	h := sha256.Sum256([]byte(v))
	short := hex.EncodeToString(h[:4])
	return "[REDACTED:" + short + "]"
}

func stringifyFields(fields map[string]any) map[string]string {
	if len(fields) == 0 {
		return nil
	}
	out := make(map[string]string, len(fields))
	for key, value := range fields {
		k := sanitizeLine(key)
		if k == "-" {
			continue
		}
		out[k] = stringifyValue(value)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func stringifyValue(value any) string {
	if value == nil {
		return "null"
	}
	rv := reflect.ValueOf(value)
	for rv.Kind() == reflect.Interface || rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return "null"
		}
		rv = rv.Elem()
		value = rv.Interface()
	}
	switch rv.Kind() {
	case reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64,
		reflect.Complex64, reflect.Complex128,
		reflect.String:
		return sanitizeLine(fmt.Sprint(value))
	case reflect.Map, reflect.Slice, reflect.Array, reflect.Struct:
		body, err := json.Marshal(value)
		if err != nil {
			return sanitizeLine(fmt.Sprint(value))
		}
		return sanitizeLine(string(body))
	default:
		return sanitizeLine(fmt.Sprint(value))
	}
}

func sanitizeLine(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "-"
	}
	normalized := strings.ReplaceAll(trimmed, "\r", " ")
	normalized = strings.ReplaceAll(normalized, "\n", " ")
	return strings.Join(strings.Fields(normalized), " ")
}
