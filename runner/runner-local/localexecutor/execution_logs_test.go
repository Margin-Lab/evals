package localexecutor

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marginlab/margin-eval/runner/runner-core/store"
)

func TestExecutionLogsProducesArtifacts(t *testing.T) {
	root := t.TempDir()
	logs, err := newExecutionLogs(root, "run_1", "inst_1")
	if err != nil {
		t.Fatalf("newExecutionLogs() error = %v", err)
	}
	defer logs.Close()

	bootWriter, err := logs.Writer(store.ArtifactRoleAgentBoot)
	if err != nil {
		t.Fatalf("Writer() error = %v", err)
	}
	if _, err := io.WriteString(bootWriter, "boot line 1\nboot line 2"); err != nil {
		t.Fatalf("write boot log error = %v", err)
	}
	if err := logs.Step(store.ArtifactRoleAgentBoot, "bootstrap.start", "start", "bootstrap started", map[string]string{"phase": "prepare"}); err != nil {
		t.Fatalf("Step() error = %v", err)
	}
	if err := logs.Step(store.ArtifactRoleAgentControl, "run.start", "completed", "started agent run", map[string]string{"agent_run_id": "run_agent_1"}); err != nil {
		t.Fatalf("Step() control error = %v", err)
	}
	if err := logs.Replace(store.ArtifactRoleAgentRuntime, []byte("runtime log")); err != nil {
		t.Fatalf("Replace() error = %v", err)
	}

	arts := logs.Artifacts()
	if len(arts) != 3 {
		t.Fatalf("expected 3 artifacts, got %d", len(arts))
	}
	for _, art := range arts {
		if art.ContentType != "text/plain" {
			t.Fatalf("unexpected content type for %s: %s", art.Role, art.ContentType)
		}
		if art.ByteSize <= 0 {
			t.Fatalf("expected non-zero byte size for %s", art.Role)
		}
		if art.SHA256 == "" {
			t.Fatalf("expected sha256 for %s", art.Role)
		}
		if filepath.Ext(art.StoreKey) != ".log" {
			t.Fatalf("expected .log store key for %s, got %s", art.Role, art.StoreKey)
		}
	}

	bootPath := artifactPathByRole(t, arts, store.ArtifactRoleAgentBoot)
	bootRecords := mustReadStructuredRecords(t, bootPath)
	if len(bootRecords) != 3 {
		t.Fatalf("expected 3 structured boot records, got %d", len(bootRecords))
	}
	assertHasStructuredOutputMessage(t, bootRecords, "boot line 1")
	assertHasStructuredOutputMessage(t, bootRecords, "boot line 2")
	assertHasStructuredStep(t, bootRecords, "bootstrap.start", "start", "bootstrap started", "phase", "prepare")

	controlPath := artifactPathByRole(t, arts, store.ArtifactRoleAgentControl)
	controlRecords := mustReadStructuredRecords(t, controlPath)
	if len(controlRecords) != 1 {
		t.Fatalf("expected 1 structured control record, got %d", len(controlRecords))
	}
	assertHasStructuredStep(t, controlRecords, "run.start", "completed", "started agent run", "agent_run_id", "run_agent_1")
}

func TestExecutionLogsRejectsPlainWritesToStructuredRoles(t *testing.T) {
	root := t.TempDir()
	logs, err := newExecutionLogs(root, "run_1", "inst_1")
	if err != nil {
		t.Fatalf("newExecutionLogs() error = %v", err)
	}
	defer logs.Close()

	if err := logs.Append(store.ArtifactRoleAgentControl, "plain text\n"); err == nil {
		t.Fatalf("expected Append() to fail for structured control role")
	}
	if err := logs.Replace(store.ArtifactRoleAgentControl, []byte("plain text")); err == nil {
		t.Fatalf("expected Replace() to fail for structured control role")
	}
	if err := logs.Step(store.ArtifactRoleAgentRuntime, "runtime.capture", "warning", "runtime capture failed", nil); err == nil {
		t.Fatalf("expected Step() to fail for raw runtime role")
	}
}

func TestExecutionLogsStructuredWriterFlushesPartialLineOnClose(t *testing.T) {
	root := t.TempDir()
	logs, err := newExecutionLogs(root, "run_1", "inst_1")
	if err != nil {
		t.Fatalf("newExecutionLogs() error = %v", err)
	}
	defer logs.Close()

	writer, err := logs.Writer(store.ArtifactRoleDockerBuild)
	if err != nil {
		t.Fatalf("Writer() error = %v", err)
	}
	if _, err := io.WriteString(writer, "tail-without-newline"); err != nil {
		t.Fatalf("write structured output error = %v", err)
	}
	arts := logs.Artifacts()
	dockerPath := artifactPathByRole(t, arts, store.ArtifactRoleDockerBuild)
	records := mustReadStructuredRecords(t, dockerPath)
	assertHasStructuredOutputMessage(t, records, "tail-without-newline")
}

func artifactPathByRole(t *testing.T, artifacts []store.Artifact, role string) string {
	t.Helper()
	for _, item := range artifacts {
		if item.Role == role {
			return strings.TrimPrefix(item.URI, "file://")
		}
	}
	t.Fatalf("artifact role %s not found", role)
	return ""
}

func mustReadStructuredRecords(t *testing.T, path string) []structuredLogRecord {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read structured log artifact %s: %v", path, err)
	}
	lines := strings.Split(strings.TrimSpace(string(body)), "\n")
	records := make([]structuredLogRecord, 0, len(lines))
	for idx, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var record structuredLogRecord
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("decode structured log line %d: %v", idx+1, err)
		}
		records = append(records, record)
	}
	return records
}

func assertHasStructuredOutputMessage(t *testing.T, records []structuredLogRecord, message string) {
	t.Helper()
	for _, record := range records {
		if record.Kind == "output" && record.Message == message {
			return
		}
	}
	t.Fatalf("expected output record with message %q", message)
}

func assertHasStructuredStep(t *testing.T, records []structuredLogRecord, step, status, message, detailKey, detailValue string) {
	t.Helper()
	for _, record := range records {
		if record.Kind != "step" {
			continue
		}
		if record.Step != step || record.Status != status || record.Message != message {
			continue
		}
		if detailKey == "" {
			return
		}
		if record.Details[detailKey] == detailValue {
			return
		}
	}
	t.Fatalf("expected step record step=%q status=%q message=%q details[%q]=%q", step, status, message, detailKey, detailValue)
}
