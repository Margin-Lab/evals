package localexecutor

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/marginlab/margin-eval/runner/runner-core/store"
	"github.com/marginlab/margin-eval/runner/runner-local/runfs"
)

var executionLogRoleOrder = []string{
	store.ArtifactRoleDockerBuild,
	store.ArtifactRoleAgentBoot,
	store.ArtifactRoleAgentRuntime,
	store.ArtifactRoleAgentPTY,
	store.ArtifactRoleAgentControl,
}

const executionLogStructuredVersion = 1

var structuredExecutionLogRoles = map[string]struct{}{
	store.ArtifactRoleDockerBuild:  {},
	store.ArtifactRoleAgentBoot:    {},
	store.ArtifactRoleAgentControl: {},
}

type executionLogs struct {
	runDir     string
	instanceID string

	mu                sync.Mutex
	files             map[string]*os.File
	paths             map[string]string
	storeKeys         map[string]string
	structuredWriters map[string]*structuredLogWriter
}

func newExecutionLogs(runDir, instanceID string) (*executionLogs, error) {
	resolvedRunDir := strings.TrimSpace(runDir)
	if resolvedRunDir == "" {
		return nil, fmt.Errorf("run dir is required")
	}
	if err := os.MkdirAll(runfs.InstanceDir(resolvedRunDir, instanceID), 0o755); err != nil {
		return nil, fmt.Errorf("create instance dir: %w", err)
	}
	return &executionLogs{
		runDir:            resolvedRunDir,
		instanceID:        instanceID,
		files:             map[string]*os.File{},
		paths:             map[string]string{},
		storeKeys:         map[string]string{},
		structuredWriters: map[string]*structuredLogWriter{},
	}, nil
}

func (l *executionLogs) Writer(role string) (io.Writer, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	trimmed := strings.TrimSpace(role)
	if isStructuredExecutionLogRole(trimmed) {
		if writer := l.structuredWriters[trimmed]; writer != nil {
			return writer, nil
		}
		if _, err := l.openLocked(trimmed); err != nil {
			return nil, err
		}
		writer := &structuredLogWriter{
			logs:   l,
			role:   trimmed,
			source: structuredOutputSourceForRole(trimmed),
		}
		l.structuredWriters[trimmed] = writer
		return writer, nil
	}
	return l.openLocked(trimmed)
}

func (l *executionLogs) Step(role, step, status, message string, details map[string]string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	trimmed := strings.TrimSpace(role)
	if !isStructuredExecutionLogRole(trimmed) {
		return fmt.Errorf("execution log role %q does not accept structured step records", trimmed)
	}
	record := structuredLogRecord{
		V:       executionLogStructuredVersion,
		Kind:    "step",
		TS:      timeNowUTC().Format(time.RFC3339Nano),
		Step:    sanitizeField(step),
		Status:  sanitizeField(status),
		Message: sanitizeField(message),
		Details: sanitizeDetails(details),
	}
	return l.appendStructuredRecordLocked(trimmed, record)
}

func (l *executionLogs) Append(role, text string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	trimmed := strings.TrimSpace(role)
	if isStructuredExecutionLogRole(trimmed) {
		return fmt.Errorf("execution log role %q requires structured records; plain append is not allowed", trimmed)
	}
	f, err := l.openLocked(trimmed)
	if err != nil {
		return err
	}
	if _, err := io.WriteString(f, text); err != nil {
		return fmt.Errorf("append execution log role %q: %w", trimmed, err)
	}
	return nil
}

func (l *executionLogs) Replace(role string, payload []byte) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	trimmed := strings.TrimSpace(role)
	if isStructuredExecutionLogRole(trimmed) {
		return fmt.Errorf("execution log role %q requires structured records; raw replace is not allowed", trimmed)
	}
	path, err := l.pathForRoleLocked(trimmed)
	if err != nil {
		return err
	}
	if writer := l.structuredWriters[trimmed]; writer != nil {
		if err := writer.flushLocked(); err != nil {
			return err
		}
		delete(l.structuredWriters, trimmed)
	}
	if f := l.files[trimmed]; f != nil {
		_ = f.Close()
		delete(l.files, trimmed)
	}
	if err := os.WriteFile(path, payload, 0o644); err != nil {
		return fmt.Errorf("write execution log role %q: %w", trimmed, err)
	}
	return nil
}

func (l *executionLogs) Artifacts() []store.Artifact {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.closeLocked()
	out := make([]store.Artifact, 0, len(l.paths))
	for idx, role := range executionLogRoleOrder {
		path, ok := l.paths[role]
		if !ok {
			continue
		}
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			continue
		}
		storeKey := l.storeKeys[role]
		if strings.TrimSpace(storeKey) == "" {
			continue
		}
		sum, err := fileSHA256(path)
		if err != nil {
			continue
		}
		out = append(out, store.Artifact{
			ArtifactID:  fmt.Sprintf("art-%s-%s", sanitizeID(l.instanceID), strings.ReplaceAll(role, "_", "-")),
			Role:        role,
			Ordinal:     idx,
			StoreKey:    storeKey,
			URI:         "file://" + path,
			ContentType: "text/plain",
			ByteSize:    info.Size(),
			SHA256:      sum,
		})
	}
	return out
}

func (l *executionLogs) Close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.closeLocked()
}

func (l *executionLogs) closeLocked() {
	for role, writer := range l.structuredWriters {
		_ = writer.flushLocked()
		delete(l.structuredWriters, role)
	}
	for role, f := range l.files {
		_ = f.Sync()
		_ = f.Close()
		delete(l.files, role)
	}
}

func (l *executionLogs) openLocked(role string) (*os.File, error) {
	path, err := l.pathForRoleLocked(role)
	if err != nil {
		return nil, err
	}
	if f := l.files[role]; f != nil {
		return f, nil
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open execution log role %q: %w", role, err)
	}
	l.files[role] = f
	return f, nil
}

func (l *executionLogs) pathForRoleLocked(role string) (string, error) {
	trimmed := strings.TrimSpace(role)
	if trimmed == "" {
		return "", fmt.Errorf("execution log role is required")
	}
	if path := l.paths[trimmed]; path != "" {
		return path, nil
	}
	path, storeKey, _, ok := runfs.AbsoluteArtifactPath(l.runDir, l.instanceID, trimmed)
	if !ok {
		return "", fmt.Errorf("unsupported execution log role %q", trimmed)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("create execution log dir for %q: %w", trimmed, err)
	}
	l.paths[trimmed] = path
	l.storeKeys[trimmed] = storeKey
	return path, nil
}

func fileSHA256(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %q: %w", path, err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func sanitizeField(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "-"
	}
	return strings.Join(strings.Fields(strings.ReplaceAll(strings.ReplaceAll(trimmed, "\r", " "), "\n", " ")), " ")
}

func sanitizeDetails(details map[string]string) map[string]string {
	if len(details) == 0 {
		return nil
	}
	out := make(map[string]string, len(details))
	for key, value := range details {
		k := sanitizeField(key)
		v := sanitizeField(value)
		if k == "-" {
			continue
		}
		out[k] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func isStructuredExecutionLogRole(role string) bool {
	_, ok := structuredExecutionLogRoles[strings.TrimSpace(role)]
	return ok
}

func structuredOutputSourceForRole(role string) string {
	switch strings.TrimSpace(role) {
	case store.ArtifactRoleDockerBuild, store.ArtifactRoleAgentBoot:
		return "docker"
	default:
		return "writer"
	}
}

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

type structuredLogWriter struct {
	logs    *executionLogs
	role    string
	source  string
	pending bytes.Buffer
}

func (w *structuredLogWriter) Write(p []byte) (int, error) {
	w.logs.mu.Lock()
	defer w.logs.mu.Unlock()
	if len(p) == 0 {
		return 0, nil
	}
	if _, err := w.pending.Write(p); err != nil {
		return 0, fmt.Errorf("buffer structured log write for role %q: %w", w.role, err)
	}
	for {
		raw := w.pending.Bytes()
		idx := bytes.IndexByte(raw, '\n')
		if idx < 0 {
			break
		}
		line := strings.TrimSuffix(string(raw[:idx]), "\r")
		record := structuredLogRecord{
			V:       executionLogStructuredVersion,
			Kind:    "output",
			TS:      timeNowUTC().Format(time.RFC3339Nano),
			Source:  w.source,
			Message: line,
		}
		if err := w.logs.appendStructuredRecordLocked(w.role, record); err != nil {
			return 0, err
		}
		w.pending.Next(idx + 1)
	}
	return len(p), nil
}

func (w *structuredLogWriter) flushLocked() error {
	if w.pending.Len() == 0 {
		return nil
	}
	line := strings.TrimSuffix(w.pending.String(), "\r")
	w.pending.Reset()
	record := structuredLogRecord{
		V:       executionLogStructuredVersion,
		Kind:    "output",
		TS:      timeNowUTC().Format(time.RFC3339Nano),
		Source:  w.source,
		Message: line,
	}
	return w.logs.appendStructuredRecordLocked(w.role, record)
}

func (l *executionLogs) appendStructuredRecordLocked(role string, record structuredLogRecord) error {
	f, err := l.openLocked(role)
	if err != nil {
		return err
	}
	body, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("marshal structured execution log role %q: %w", role, err)
	}
	if _, err := f.Write(append(body, '\n')); err != nil {
		return fmt.Errorf("append structured execution log role %q: %w", role, err)
	}
	return nil
}

var timeNowUTC = func() time.Time {
	return time.Now().UTC()
}
