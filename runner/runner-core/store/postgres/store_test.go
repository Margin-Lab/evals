package postgres

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/marginlab/margin-eval/runner/runner-core/domain"
	"github.com/marginlab/margin-eval/runner/runner-core/runbundle"
	"github.com/marginlab/margin-eval/runner/runner-core/store"
	"github.com/marginlab/margin-eval/runner/runner-core/testfixture"
	"github.com/marginlab/margin-eval/runner/runner-core/usage"
)

type scanFn func(dest ...any) error

func (f scanFn) Scan(dest ...any) error { return f(dest...) }

func TestScanRunIncludeBundle(t *testing.T) {
	bundle := runbundle.Bundle{
		SchemaVersion: runbundle.SchemaVersionV1,
		BundleID:      "bun_1",
		CreatedAt:     time.Date(2026, 2, 26, 12, 0, 0, 0, time.UTC),
		Source: runbundle.Source{
			Kind:            runbundle.SourceKindLocalFiles,
			SubmitProjectID: "proj_1",
		},
		ResolvedSnapshot: runbundle.ResolvedSnapshot{
			Name: "suite",
			Execution: runbundle.Execution{Mode: runbundle.ExecutionModeFull,
				MaxConcurrency:        1,
				FailFast:              false,
				InstanceTimeoutSecond: 120,
			},
			Agent:       testfixture.MinimalAgent(),
			RunDefaults: runbundle.RunDefault{Cwd: "/work", Env: map[string]string{}, PTY: runbundle.PTY{Cols: 120, Rows: 40}},
			Cases: []runbundle.Case{{
				CaseID:            "case_1",
				Image:             "ghcr.io/acme/repo@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
				InitialPrompt:     "hello",
				TestCommand:       []string{"bash", "-lc", "true"},
				TestCwd:           "/work",
				TestTimeoutSecond: 30,
				TestAssets:        testfixture.MinimalTestAssets(),
			}},
		},
	}
	raw, err := json.Marshal(bundle)
	if err != nil {
		t.Fatalf("marshal bundle: %v", err)
	}
	now := time.Date(2026, 2, 26, 13, 0, 0, 0, time.UTC)

	row := scanFn(func(dest ...any) error {
		*dest[0].(*string) = "run_1"
		*dest[1].(*string) = "proj_1"
		*dest[2].(*string) = "user_1"
		name := "smoke"
		*dest[3].(**string) = &name
		*dest[4].(*string) = string(domain.RunStateRunning)
		*dest[5].(*string) = string(runbundle.SourceKindLocalFiles)
		*dest[6].(*string) = "hash_1"
		*dest[7].(*[]byte) = raw
		cancelAt := now
		*dest[8].(**time.Time) = &cancelAt
		startedAt := now
		*dest[9].(**time.Time) = &startedAt
		*dest[10].(**time.Time) = nil
		*dest[11].(*time.Time) = now
		*dest[12].(*int) = 1
		*dest[13].(*int) = 2
		*dest[14].(*int) = 3
		*dest[15].(*int) = 4
		*dest[16].(*int) = 5
		*dest[17].(*int) = 6
		return nil
	})

	run, err := scanRun(row, true)
	if err != nil {
		t.Fatalf("scan run: %v", err)
	}
	if run.RunID != "run_1" || run.ProjectID != "proj_1" {
		t.Fatalf("unexpected run identity: %+v", run)
	}
	if !run.CancelRequested {
		t.Fatalf("expected cancel requested=true")
	}
	if run.Bundle.BundleID != "bun_1" {
		t.Fatalf("expected bundle decoded")
	}
	if run.Counts.Pending != 1 || run.Counts.Running != 2 || run.Counts.Succeeded != 3 || run.Counts.TestFailed != 4 || run.Counts.InfraFailed != 5 || run.Counts.Canceled != 6 {
		t.Fatalf("unexpected counts: %+v", run.Counts)
	}
}

func TestScanRunNoRows(t *testing.T) {
	row := scanFn(func(_ ...any) error { return pgx.ErrNoRows })
	_, err := scanRun(row, false)
	if err == nil {
		t.Fatalf("expected not found error")
	}
	if err != store.ErrNotFound {
		t.Fatalf("expected store.ErrNotFound, got %v", err)
	}
}

func TestScanArtifact(t *testing.T) {
	now := time.Date(2026, 2, 26, 14, 0, 0, 0, time.UTC)
	meta, err := json.Marshal(map[string]any{"k": "v"})
	if err != nil {
		t.Fatalf("marshal metadata: %v", err)
	}

	row := scanFn(func(dest ...any) error {
		*dest[0].(*string) = "art_1"
		*dest[1].(*string) = "run_1"
		*dest[2].(*string) = "run_1-inst-0001"
		*dest[3].(*string) = "attempt_1"
		*dest[4].(*string) = "trajectory"
		*dest[5].(*int) = 0
		*dest[6].(*string) = "runs/run_1/meta.json"
		*dest[7].(*string) = "s3://bucket/runs/run_1/meta.json"
		contentType := "application/json"
		*dest[8].(**string) = &contentType
		size := int64(12)
		*dest[9].(**int64) = &size
		sha := "abc"
		*dest[10].(**string) = &sha
		*dest[11].(*[]byte) = meta
		*dest[12].(*time.Time) = now
		return nil
	})

	artifact, err := scanArtifact(row)
	if err != nil {
		t.Fatalf("scan artifact: %v", err)
	}
	if artifact.ArtifactID != "art_1" || artifact.StoreKey != "runs/run_1/meta.json" {
		t.Fatalf("unexpected artifact identity: %+v", artifact)
	}
	if artifact.ContentType != "application/json" || artifact.ByteSize != 12 || artifact.SHA256 != "abc" {
		t.Fatalf("unexpected artifact metadata: %+v", artifact)
	}
	if artifact.Metadata["k"] != "v" {
		t.Fatalf("expected decoded metadata, got %+v", artifact.Metadata)
	}
}

func TestScanStoredInstanceResultIncludesUsage(t *testing.T) {
	now := time.Date(2026, 2, 26, 15, 0, 0, 0, time.UTC)
	errorDetails, err := json.Marshal(map[string]any{"kind": "boom"})
	if err != nil {
		t.Fatalf("marshal error details: %v", err)
	}

	row := scanFn(func(dest ...any) error {
		*dest[0].(*string) = "inst_1"
		*dest[1].(*string) = "attempt_1"
		*dest[2].(*string) = string(domain.InstanceStateInfraFailed)
		providerRef := "provider://test"
		*dest[3].(**string) = &providerRef
		agentRunID := "agent_run_1"
		*dest[4].(**string) = &agentRunID
		agentExitCode := 1
		*dest[5].(**int) = &agentExitCode
		trajectoryRef := "runs/run_1/instances/inst_1/trajectory.json"
		*dest[6].(**string) = &trajectoryRef
		inputTokens := int64(12)
		*dest[7].(**int64) = &inputTokens
		outputTokens := int64(4)
		*dest[8].(**int64) = &outputTokens
		toolCalls := int64(2)
		*dest[9].(**int64) = &toolCalls
		testExitCode := 3
		*dest[10].(**int) = &testExitCode
		testStdoutRef := "stdout.txt"
		*dest[11].(**string) = &testStdoutRef
		testStderrRef := "stderr.txt"
		*dest[12].(**string) = &testStderrRef
		errorCode := "EXECUTOR_ERROR"
		*dest[13].(**string) = &errorCode
		errorMessage := "failed"
		*dest[14].(**string) = &errorMessage
		*dest[15].(*[]byte) = errorDetails
		provisionedAt := now.Add(1 * time.Second)
		*dest[16].(**time.Time) = &provisionedAt
		agentStartedAt := now.Add(2 * time.Second)
		*dest[17].(**time.Time) = &agentStartedAt
		agentEndedAt := now.Add(3 * time.Second)
		*dest[18].(**time.Time) = &agentEndedAt
		testStartedAt := now.Add(4 * time.Second)
		*dest[19].(**time.Time) = &testStartedAt
		testEndedAt := now.Add(5 * time.Second)
		*dest[20].(**time.Time) = &testEndedAt
		*dest[21].(*time.Time) = now
		return nil
	})

	result, err := scanStoredInstanceResult(row)
	if err != nil {
		t.Fatalf("scan stored instance result: %v", err)
	}
	if result.InstanceID != "inst_1" || result.TrajectoryRef != "runs/run_1/instances/inst_1/trajectory.json" {
		t.Fatalf("unexpected stored result identity: %+v", result)
	}
	wantUsage := &usage.Metrics{InputTokens: int64Ptr(12), OutputTokens: int64Ptr(4), ToolCalls: int64Ptr(2)}
	if result.Usage == nil || result.Usage.InputTokens == nil || *result.Usage.InputTokens != *wantUsage.InputTokens {
		t.Fatalf("unexpected usage metrics: %+v", result.Usage)
	}
	if result.ErrorDetails["kind"] != "boom" {
		t.Fatalf("unexpected error details: %+v", result.ErrorDetails)
	}
}

func int64Ptr(v int64) *int64 {
	return &v
}
