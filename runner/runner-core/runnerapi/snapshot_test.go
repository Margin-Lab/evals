package runnerapi

import (
	"context"
	"testing"
	"time"

	"github.com/marginlab/margin-eval/runner/runner-core/domain"
	"github.com/marginlab/margin-eval/runner/runner-core/runbundle"
	"github.com/marginlab/margin-eval/runner/runner-core/store"
	"github.com/marginlab/margin-eval/runner/runner-core/testfixture"
	"github.com/marginlab/margin-eval/runner/runner-core/usage"
)

func TestBuildRunSnapshotIncludesRequestedFields(t *testing.T) {
	t.Parallel()

	runStore := store.NewMemoryStore()
	ctx := context.Background()
	at := time.Date(2026, 2, 27, 12, 0, 0, 0, time.UTC)

	run, err := runStore.CreateRun(ctx, store.CreateRunInput{
		RunID:         "run_1",
		ProjectID:     "proj_1",
		CreatedByUser: "user_1",
		SourceKind:    runbundle.SourceKindLocalFiles,
		Bundle:        snapshotFixtureBundle(),
		At:            at,
	})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}

	claim, ok, err := runStore.ClaimPendingInstance(ctx, "worker_1", 30*time.Second, at.Add(1*time.Second))
	if err != nil || !ok {
		t.Fatalf("claim pending: ok=%v err=%v", ok, err)
	}
	for _, st := range []domain.InstanceState{
		domain.InstanceStateAgentServerInstalling,
		domain.InstanceStateBooting,
		domain.InstanceStateAgentConfiguring,
		domain.InstanceStateAgentInstalling,
		domain.InstanceStateAgentRunning,
		domain.InstanceStateAgentCollecting,
		domain.InstanceStateTesting,
		domain.InstanceStateCollecting,
	} {
		if err := runStore.UpdateInstanceState(ctx, claim.Run.RunID, claim.Instance.InstanceID, claim.AttemptID, st, at.Add(2*time.Second)); err != nil {
			t.Fatalf("update state %s: %v", st, err)
		}
	}

	zero := 0
	if err := runStore.FinalizeAttempt(ctx, store.FinalizeInput{
		AttemptID:  claim.AttemptID,
		RunID:      claim.Run.RunID,
		InstanceID: claim.Instance.InstanceID,
		Result: store.InstanceResult{
			FinalState:    domain.InstanceStateSucceeded,
			AgentExitCode: &zero,
			TestExitCode:  &zero,
			Usage: &usage.Metrics{
				InputTokens:  int64Ptr(12),
				OutputTokens: int64Ptr(5),
				ToolCalls:    int64Ptr(1),
			},
		},
		Artifacts: []store.Artifact{{
			ArtifactID:  "art_1",
			Role:        "test_stdout",
			Ordinal:     0,
			StoreKey:    "runs/run_1/instances/run_1-inst-0001/test_stdout.txt",
			URI:         "file:///tmp/test_stdout.txt",
			ContentType: "text/plain",
		}},
	}, at.Add(3*time.Second)); err != nil {
		t.Fatalf("finalize attempt: %v", err)
	}

	full, err := BuildRunSnapshot(ctx, runStore, run.RunID, SnapshotOptions{
		IncludeBundle:            true,
		IncludeRunEvents:         true,
		IncludeInstanceAttempts:  true,
		IncludeInstanceEvents:    true,
		IncludeInstanceResults:   true,
		IncludeInstanceArtifacts: true,
		IncludeResultsSummary:    true,
	})
	if err != nil {
		t.Fatalf("build full run snapshot: %v", err)
	}
	if full.Run.Bundle.BundleID == "" {
		t.Fatalf("expected included bundle")
	}
	if len(full.Events) == 0 {
		t.Fatalf("expected run events")
	}
	if len(full.Instances) != 1 {
		t.Fatalf("expected 1 instance snapshot, got %d", len(full.Instances))
	}
	inst := full.Instances[0]
	if inst.Result == nil {
		t.Fatalf("expected instance result")
	}
	if len(inst.Attempts) == 0 {
		t.Fatalf("expected instance attempts")
	}
	if len(inst.Events) == 0 {
		t.Fatalf("expected instance events")
	}
	if len(inst.Artifacts) != 1 {
		t.Fatalf("expected 1 artifact, got %d", len(inst.Artifacts))
	}
	if full.Results == nil {
		t.Fatalf("expected results summary")
	}
	if full.Results.Usage.InputTokens != 12 || full.Results.Usage.OutputTokens != 5 || full.Results.Usage.ToolCalls != 1 {
		t.Fatalf("unexpected results usage: %+v", full.Results.Usage)
	}

	basic, err := BuildRunSnapshot(ctx, runStore, run.RunID, SnapshotOptions{})
	if err != nil {
		t.Fatalf("build basic run snapshot: %v", err)
	}
	if basic.Run.Bundle.BundleID != "" {
		t.Fatalf("did not expect bundle in basic snapshot")
	}
	if len(basic.Events) != 0 {
		t.Fatalf("did not expect run events in basic snapshot")
	}
	if len(basic.Instances) != 1 {
		t.Fatalf("expected 1 basic instance, got %d", len(basic.Instances))
	}
	if basic.Instances[0].Result != nil {
		t.Fatalf("did not expect result in basic instance snapshot")
	}
	if basic.Results != nil {
		t.Fatalf("did not expect results summary in basic snapshot")
	}
}

func TestBuildInstanceSnapshotNotFound(t *testing.T) {
	t.Parallel()

	_, err := BuildInstanceSnapshot(context.Background(), store.NewMemoryStore(), "missing_instance", SnapshotOptions{})
	if err == nil {
		t.Fatalf("expected not found error")
	}
}

func int64Ptr(v int64) *int64 {
	return &v
}

func snapshotFixtureBundle() runbundle.Bundle {
	return runbundle.Bundle{
		SchemaVersion: runbundle.SchemaVersionV1,
		BundleID:      "bun_1",
		CreatedAt:     time.Date(2026, 2, 27, 12, 0, 0, 0, time.UTC),
		Source:        runbundle.Source{Kind: runbundle.SourceKindLocalFiles, SubmitProjectID: "proj_1"},
		ResolvedSnapshot: runbundle.ResolvedSnapshot{
			Name:      "smoke",
			Execution: runbundle.Execution{Mode: runbundle.ExecutionModeFull, MaxConcurrency: 1, FailFast: false, InstanceTimeoutSecond: 120},
			Agent:     testfixture.MinimalAgent(),
			RunDefaults: runbundle.RunDefault{
				Cwd: "/work",
				Env: map[string]string{"TERM": "xterm-256color"},
				PTY: runbundle.PTY{Cols: 120, Rows: 40},
			},
			Cases: []runbundle.Case{{
				CaseID:            "case_1",
				Image:             "ghcr.io/acme/repo@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
				InitialPrompt:     "Fix tests",
				TestCommand:       []string{"bash", "-lc", "true"},
				TestCwd:           "/work",
				TestTimeoutSecond: 60,
				TestAssets:        testfixture.MinimalTestAssets(),
			}},
		},
	}
}
