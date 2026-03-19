package store

import (
	"context"
	"testing"
	"time"

	"github.com/marginlab/margin-eval/runner/runner-core/domain"
	"github.com/marginlab/margin-eval/runner/runner-core/runbundle"
	"github.com/marginlab/margin-eval/runner/runner-core/testfixture"
	"github.com/marginlab/margin-eval/runner/runner-core/usage"
)

func fixtureBundle() runbundle.Bundle {
	return runbundle.Bundle{
		SchemaVersion: runbundle.SchemaVersionV1,
		BundleID:      "bun_1",
		CreatedAt:     time.Date(2026, 2, 26, 18, 0, 0, 0, time.UTC),
		Source:        runbundle.Source{Kind: runbundle.SourceKindLocalFiles},
		ResolvedSnapshot: runbundle.ResolvedSnapshot{
			Name: "smoke",
			Execution: runbundle.Execution{Mode: runbundle.ExecutionModeFull,
				MaxConcurrency:        1,
				FailFast:              false,
				InstanceTimeoutSecond: 120,
			},
			Agent:       testfixture.MinimalAgent(),
			RunDefaults: runbundle.RunDefault{Cwd: "/work", Env: map[string]string{"TERM": "xterm-256color"}, PTY: runbundle.PTY{Cols: 120, Rows: 40}},
			Cases: []runbundle.Case{{
				CaseID:            "case-1",
				Image:             "ghcr.io/acme/repo@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
				InitialPrompt:     "hello",
				TestCommand:       []string{"bash", "-lc", "true"},
				TestCwd:           "/work",
				TestTimeoutSecond: 30,
				TestAssets:        testfixture.MinimalTestAssets(),
			}},
		},
	}
}

func TestMemoryStoreCreateRunAndList(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()

	run, err := s.CreateRun(ctx, CreateRunInput{
		RunID:         "run_1",
		ProjectID:     "proj_1",
		CreatedByUser: "user_1",
		SourceKind:    runbundle.SourceKindLocalFiles,
		Bundle:        fixtureBundle(),
		At:            time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	if run.State != domain.RunStateQueued {
		t.Fatalf("expected queued, got %s", run.State)
	}

	runs, err := s.ListRuns(ctx, "proj_1", ListRunsFilter{})
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(runs) != 1 || runs[0].RunID != "run_1" {
		t.Fatalf("unexpected runs response: %+v", runs)
	}
}

func TestMemoryStoreClaimAndFinalizeTransitionsRun(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()
	_, err := s.CreateRun(ctx, CreateRunInput{
		RunID:         "run_2",
		ProjectID:     "proj_1",
		CreatedByUser: "user_1",
		SourceKind:    runbundle.SourceKindLocalFiles,
		Bundle:        fixtureBundle(),
		At:            time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}

	claim, ok, err := s.ClaimPendingInstance(ctx, "worker-a", 30*time.Second, time.Now().UTC())
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if !ok {
		t.Fatalf("expected claim")
	}
	if claim.Instance.State != domain.InstanceStateProvisioning {
		t.Fatalf("expected provisioning after claim, got %s", claim.Instance.State)
	}

	if err := s.UpdateInstanceState(ctx, claim.Run.RunID, claim.Instance.InstanceID, claim.AttemptID, domain.InstanceStateAgentServerInstalling, time.Now().UTC()); err != nil {
		t.Fatalf("update state: %v", err)
	}
	if err := s.UpdateInstanceState(ctx, claim.Run.RunID, claim.Instance.InstanceID, claim.AttemptID, domain.InstanceStateBooting, time.Now().UTC()); err != nil {
		t.Fatalf("update state: %v", err)
	}
	if err := s.UpdateInstanceState(ctx, claim.Run.RunID, claim.Instance.InstanceID, claim.AttemptID, domain.InstanceStateAgentConfiguring, time.Now().UTC()); err != nil {
		t.Fatalf("update state: %v", err)
	}
	if err := s.UpdateInstanceState(ctx, claim.Run.RunID, claim.Instance.InstanceID, claim.AttemptID, domain.InstanceStateAgentInstalling, time.Now().UTC()); err != nil {
		t.Fatalf("update state: %v", err)
	}
	if err := s.UpdateInstanceState(ctx, claim.Run.RunID, claim.Instance.InstanceID, claim.AttemptID, domain.InstanceStateAgentRunning, time.Now().UTC()); err != nil {
		t.Fatalf("update state: %v", err)
	}
	if err := s.UpdateInstanceState(ctx, claim.Run.RunID, claim.Instance.InstanceID, claim.AttemptID, domain.InstanceStateAgentCollecting, time.Now().UTC()); err != nil {
		t.Fatalf("update state: %v", err)
	}
	if err := s.UpdateInstanceState(ctx, claim.Run.RunID, claim.Instance.InstanceID, claim.AttemptID, domain.InstanceStateTesting, time.Now().UTC()); err != nil {
		t.Fatalf("update state: %v", err)
	}
	if err := s.UpdateInstanceState(ctx, claim.Run.RunID, claim.Instance.InstanceID, claim.AttemptID, domain.InstanceStateCollecting, time.Now().UTC()); err != nil {
		t.Fatalf("update state: %v", err)
	}

	if err := s.FinalizeAttempt(ctx, FinalizeInput{
		AttemptID:  claim.AttemptID,
		RunID:      claim.Run.RunID,
		InstanceID: claim.Instance.InstanceID,
		Result:     InstanceResult{FinalState: domain.InstanceStateSucceeded},
	}, time.Now().UTC()); err != nil {
		t.Fatalf("finalize: %v", err)
	}

	r, err := s.GetRun(ctx, claim.Run.RunID, false)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if r.State != domain.RunStateCompleted {
		t.Fatalf("expected completed run state, got %s", r.State)
	}
	if r.Counts.Succeeded != 1 {
		t.Fatalf("expected succeeded count 1, got %+v", r.Counts)
	}
}

func TestMemoryStoreCancelRun(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()
	_, _ = s.CreateRun(ctx, CreateRunInput{
		RunID:         "run_3",
		ProjectID:     "proj_1",
		CreatedByUser: "user_1",
		SourceKind:    runbundle.SourceKindLocalFiles,
		Bundle:        fixtureBundle(),
		At:            time.Now().UTC(),
	})

	r, err := s.CancelRun(ctx, "run_3", "USER_REQUEST", "stop", time.Now().UTC())
	if err != nil {
		t.Fatalf("cancel run: %v", err)
	}
	if r.State != domain.RunStateCanceling && r.State != domain.RunStateCanceled {
		t.Fatalf("expected canceling/canceled, got %s", r.State)
	}
}

func TestMemoryStoreUpdateInstanceImage(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()
	_, err := s.CreateRun(ctx, CreateRunInput{
		RunID:         "run_img",
		ProjectID:     "proj_1",
		CreatedByUser: "user_1",
		SourceKind:    runbundle.SourceKindLocalFiles,
		Bundle:        fixtureBundle(),
		At:            time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	claim, ok, err := s.ClaimPendingInstance(ctx, "worker-a", 30*time.Second, time.Now().UTC())
	if err != nil || !ok {
		t.Fatalf("claim: ok=%v err=%v", ok, err)
	}
	const resolved = "marginlab-local/case_1@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	if err := s.UpdateInstanceImage(ctx, claim.Run.RunID, claim.Instance.InstanceID, claim.AttemptID, resolved, time.Now().UTC()); err != nil {
		t.Fatalf("update instance image: %v", err)
	}
	inst, err := s.GetInstance(ctx, claim.Instance.InstanceID)
	if err != nil {
		t.Fatalf("get instance: %v", err)
	}
	if inst.Case.Image != resolved {
		t.Fatalf("instance image = %q, want %q", inst.Case.Image, resolved)
	}
}

func TestMemoryStoreRerunExact(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()
	_, _ = s.CreateRun(ctx, CreateRunInput{
		RunID:         "run_src",
		ProjectID:     "proj_1",
		CreatedByUser: "user_1",
		SourceKind:    runbundle.SourceKindLocalFiles,
		Bundle:        fixtureBundle(),
		At:            time.Now().UTC(),
	})

	re, err := s.RerunExact(ctx, "run_src", "run_new", "user_2", "rerun-name", time.Now().UTC())
	if err != nil {
		t.Fatalf("rerun exact: %v", err)
	}
	if re.RunID != "run_new" {
		t.Fatalf("unexpected rerun id: %s", re.RunID)
	}
	full, err := s.GetRun(ctx, "run_new", true)
	if err != nil {
		t.Fatalf("get rerun: %v", err)
	}
	if full.Bundle.Source.Kind != runbundle.SourceKindRunSnapshot {
		t.Fatalf("expected run_snapshot source kind, got %s", full.Bundle.Source.Kind)
	}
}

func TestMemoryStoreListRunsFiltersByCreatedByUser(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()
	_, _ = s.CreateRun(ctx, CreateRunInput{
		RunID:         "run_u1",
		ProjectID:     "proj_1",
		CreatedByUser: "user_1",
		SourceKind:    runbundle.SourceKindLocalFiles,
		Bundle:        fixtureBundle(),
		At:            time.Now().UTC(),
	})
	_, _ = s.CreateRun(ctx, CreateRunInput{
		RunID:         "run_u2",
		ProjectID:     "proj_1",
		CreatedByUser: "user_2",
		SourceKind:    runbundle.SourceKindLocalFiles,
		Bundle:        fixtureBundle(),
		At:            time.Now().UTC(),
	})
	user := "user_1"
	runs, err := s.ListRuns(ctx, "proj_1", ListRunsFilter{CreatedByUserID: &user})
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(runs) != 1 || runs[0].RunID != "run_u1" {
		t.Fatalf("unexpected filtered runs: %#v", runs)
	}
}

func TestMemoryStoreGetInstanceResult(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()
	_, err := s.CreateRun(ctx, CreateRunInput{
		RunID:         "run_result",
		ProjectID:     "proj_1",
		CreatedByUser: "user_1",
		SourceKind:    runbundle.SourceKindLocalFiles,
		Bundle:        fixtureBundle(),
		At:            time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	claim, ok, err := s.ClaimPendingInstance(ctx, "worker-a", 30*time.Second, time.Now().UTC())
	if err != nil || !ok {
		t.Fatalf("claim: ok=%v err=%v", ok, err)
	}
	states := []domain.InstanceState{
		domain.InstanceStateAgentServerInstalling,
		domain.InstanceStateBooting,
		domain.InstanceStateAgentConfiguring,
		domain.InstanceStateAgentInstalling,
		domain.InstanceStateAgentRunning,
		domain.InstanceStateAgentCollecting,
		domain.InstanceStateTesting,
		domain.InstanceStateCollecting,
	}
	for _, state := range states {
		if err := s.UpdateInstanceState(ctx, claim.Run.RunID, claim.Instance.InstanceID, claim.AttemptID, state, time.Now().UTC()); err != nil {
			t.Fatalf("update state %s: %v", state, err)
		}
	}
	if err := s.FinalizeAttempt(ctx, FinalizeInput{
		AttemptID:  claim.AttemptID,
		RunID:      claim.Run.RunID,
		InstanceID: claim.Instance.InstanceID,
		Result: InstanceResult{
			FinalState:   domain.InstanceStateSucceeded,
			Usage:        &usage.Metrics{InputTokens: int64Ptr(13), OutputTokens: int64Ptr(4), ToolCalls: int64Ptr(1)},
			TestExitCode: intPtr(0),
		},
	}, time.Now().UTC()); err != nil {
		t.Fatalf("finalize attempt: %v", err)
	}

	result, err := s.GetInstanceResult(ctx, claim.Instance.InstanceID)
	if err != nil {
		t.Fatalf("get instance result: %v", err)
	}
	if result.FinalState != domain.InstanceStateSucceeded {
		t.Fatalf("expected succeeded final state, got %s", result.FinalState)
	}
	if result.TestExitCode == nil || *result.TestExitCode != 0 {
		t.Fatalf("expected test exit code 0, got %#v", result.TestExitCode)
	}
	if result.Usage == nil || result.Usage.InputTokens == nil || *result.Usage.InputTokens != 13 {
		t.Fatalf("unexpected usage metrics: %+v", result.Usage)
	}

	results, err := s.ListInstanceResults(ctx, claim.Run.RunID)
	if err != nil {
		t.Fatalf("list instance results: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 instance result, got %d", len(results))
	}
	if results[0].Usage == nil || results[0].Usage.ToolCalls == nil || *results[0].Usage.ToolCalls != 1 {
		t.Fatalf("unexpected listed usage metrics: %+v", results[0].Usage)
	}
}

func TestMemoryStoreRequeueInfraFailure(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()
	_, err := s.CreateRun(ctx, CreateRunInput{
		RunID:         "run_retry",
		ProjectID:     "proj_1",
		CreatedByUser: "user_1",
		SourceKind:    runbundle.SourceKindLocalFiles,
		Bundle:        fixtureBundle(),
		At:            time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}

	claim, ok, err := s.ClaimPendingInstance(ctx, "worker-a", 30*time.Second, time.Now().UTC())
	if err != nil || !ok {
		t.Fatalf("claim: ok=%v err=%v", ok, err)
	}
	retried, err := s.RequeueInfraFailure(ctx, RequeueInfraFailureInput{
		AttemptID:     claim.AttemptID,
		RunID:         claim.Run.RunID,
		InstanceID:    claim.Instance.InstanceID,
		Artifacts:     []Artifact{{Role: "test_stdout", StoreKey: "retry.log", URI: "mem://retry.log"}},
		Result:        InstanceResult{FinalState: domain.InstanceStateInfraFailed, ErrorCode: "EXECUTOR_ERROR", ErrorMessage: "boom"},
		MaxRetryCount: 1,
	}, time.Now().UTC())
	if err != nil {
		t.Fatalf("requeue infra failure: %v", err)
	}
	if !retried {
		t.Fatalf("expected infra failure to be retried")
	}

	inst, err := s.GetInstance(ctx, claim.Instance.InstanceID)
	if err != nil {
		t.Fatalf("get instance: %v", err)
	}
	if inst.State != domain.InstanceStatePending {
		t.Fatalf("instance state = %s, want pending", inst.State)
	}
	if _, err := s.GetInstanceResult(ctx, claim.Instance.InstanceID); err != ErrNotFound {
		t.Fatalf("expected no stored result during retry, got %v", err)
	}
	artifacts, err := s.ListArtifacts(ctx, claim.Instance.InstanceID)
	if err != nil {
		t.Fatalf("list artifacts: %v", err)
	}
	if len(artifacts) != 1 || artifacts[0].AttemptID != claim.AttemptID {
		t.Fatalf("unexpected retry artifacts: %+v", artifacts)
	}

	events, err := s.ListInstanceEvents(ctx, claim.Instance.InstanceID)
	if err != nil {
		t.Fatalf("list instance events: %v", err)
	}
	if len(events) == 0 || events[len(events)-1].Source != "retry" {
		t.Fatalf("expected final retry event, got %+v", events)
	}

	nextClaim, ok, err := s.ClaimPendingInstance(ctx, "worker-a", 30*time.Second, time.Now().UTC())
	if err != nil || !ok {
		t.Fatalf("second claim: ok=%v err=%v", ok, err)
	}
	retried, err = s.RequeueInfraFailure(ctx, RequeueInfraFailureInput{
		AttemptID:     nextClaim.AttemptID,
		RunID:         nextClaim.Run.RunID,
		InstanceID:    nextClaim.Instance.InstanceID,
		Result:        InstanceResult{FinalState: domain.InstanceStateInfraFailed, ErrorCode: "EXECUTOR_ERROR", ErrorMessage: "boom again"},
		MaxRetryCount: 1,
	}, time.Now().UTC())
	if err != nil {
		t.Fatalf("second requeue infra failure: %v", err)
	}
	if retried {
		t.Fatalf("expected retry budget to be exhausted")
	}
}

func TestMemoryStoreCarryForwardInstance(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()
	createdAt := time.Now().UTC()
	run, err := s.CreateRun(ctx, CreateRunInput{
		RunID:         "run_cf",
		ProjectID:     "proj_1",
		CreatedByUser: "user_1",
		SourceKind:    runbundle.SourceKindLocalFiles,
		Bundle:        fixtureBundle(),
		At:            createdAt,
	})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	instances, err := s.ListInstances(ctx, run.RunID, nil)
	if err != nil {
		t.Fatalf("list instances: %v", err)
	}
	if len(instances) != 1 {
		t.Fatalf("expected one instance, got %d", len(instances))
	}
	instanceID := instances[0].InstanceID
	if err := s.CarryForwardInstance(ctx, CarryForwardInput{
		RunID:            run.RunID,
		InstanceID:       instanceID,
		SourceRunID:      "run_src",
		SourceInstanceID: "run_src-inst-0001",
		ProviderRef:      "provider://resume",
		Result: InstanceResult{
			FinalState:    domain.InstanceStateSucceeded,
			Trajectory:    "run_src/inst/trajectory.json",
			TestStdoutRef: "run_src/inst/test_stdout.txt",
			TestStderrRef: "run_src/inst/test_stderr.txt",
		},
		Artifacts: []Artifact{{
			ArtifactID:  "art-carry-trajectory",
			Role:        ArtifactRoleTrajectory,
			Ordinal:     0,
			StoreKey:    "run_src/inst/trajectory.json",
			URI:         "file:///tmp/trajectory.json",
			ContentType: "application/json",
		}},
	}, createdAt.Add(time.Second)); err != nil {
		t.Fatalf("carry-forward instance: %v", err)
	}

	finalRun, err := s.GetRun(ctx, run.RunID, false)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if finalRun.State != domain.RunStateCompleted {
		t.Fatalf("expected completed run, got %s", finalRun.State)
	}
	if finalRun.Counts.Succeeded != 1 {
		t.Fatalf("expected succeeded count 1, got %+v", finalRun.Counts)
	}
	result, err := s.GetInstanceResult(ctx, instanceID)
	if err != nil {
		t.Fatalf("get result: %v", err)
	}
	if result.FinalState != domain.InstanceStateSucceeded {
		t.Fatalf("expected succeeded result, got %s", result.FinalState)
	}
	if result.ProviderRef != "provider://resume" {
		t.Fatalf("unexpected provider ref: %s", result.ProviderRef)
	}
	if result.TrajectoryRef != "run_src/inst/trajectory.json" {
		t.Fatalf("unexpected trajectory ref: %s", result.TrajectoryRef)
	}
	arts, err := s.ListArtifacts(ctx, instanceID)
	if err != nil {
		t.Fatalf("list artifacts: %v", err)
	}
	if len(arts) != 1 {
		t.Fatalf("expected 1 artifact, got %d", len(arts))
	}

	// Idempotent on already-terminal carried instance.
	if err := s.CarryForwardInstance(ctx, CarryForwardInput{
		RunID:            run.RunID,
		InstanceID:       instanceID,
		SourceRunID:      "run_src",
		SourceInstanceID: "run_src-inst-0001",
		Result: InstanceResult{
			FinalState: domain.InstanceStateSucceeded,
		},
	}, createdAt.Add(2*time.Second)); err != nil {
		t.Fatalf("idempotent carry-forward: %v", err)
	}
}

func intPtr(v int) *int {
	return &v
}

func int64Ptr(v int64) *int64 {
	return &v
}
