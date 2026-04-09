package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/marginlab/margin-eval/runner/runner-core/domain"
	"github.com/marginlab/margin-eval/runner/runner-core/runbundle"
	"github.com/marginlab/margin-eval/runner/runner-core/store"
	"github.com/marginlab/margin-eval/runner/runner-core/testfixture"
)

type fakeExecutor struct {
	result store.InstanceResult
	err    error
}

type scriptedExecutor struct {
	results []store.InstanceResult
	errs    []error
	calls   int
}

type blockingExecutor struct{}

func (f fakeExecutor) ExecuteInstance(_ context.Context, _ store.Run, _ store.Instance, updateState func(domain.InstanceState) error, _ func(string) error) (store.InstanceResult, []store.Artifact, error) {
	for _, state := range []domain.InstanceState{
		domain.InstanceStateAgentServerInstalling,
		domain.InstanceStateBooting,
		domain.InstanceStateAgentConfiguring,
		domain.InstanceStateAgentInstalling,
		domain.InstanceStateAgentRunning,
		domain.InstanceStateAgentCollecting,
		domain.InstanceStateTesting,
		domain.InstanceStateCollecting,
	} {
		if err := updateState(state); err != nil {
			return store.InstanceResult{}, nil, err
		}
	}
	return f.result, []store.Artifact{{Role: "test_stdout", StoreKey: "k", URI: "u"}}, f.err
}

func (s *scriptedExecutor) ExecuteInstance(_ context.Context, _ store.Run, _ store.Instance, updateState func(domain.InstanceState) error, _ func(string) error) (store.InstanceResult, []store.Artifact, error) {
	for _, state := range []domain.InstanceState{
		domain.InstanceStateAgentServerInstalling,
		domain.InstanceStateBooting,
		domain.InstanceStateAgentConfiguring,
		domain.InstanceStateAgentInstalling,
		domain.InstanceStateAgentRunning,
		domain.InstanceStateAgentCollecting,
		domain.InstanceStateTesting,
		domain.InstanceStateCollecting,
	} {
		if err := updateState(state); err != nil {
			return store.InstanceResult{}, nil, err
		}
	}
	idx := s.calls
	s.calls++
	var result store.InstanceResult
	if idx < len(s.results) {
		result = s.results[idx]
	}
	var err error
	if idx < len(s.errs) {
		err = s.errs[idx]
	}
	return result, []store.Artifact{{Role: "test_stdout", StoreKey: "k", URI: "u"}}, err
}

func (blockingExecutor) ExecuteInstance(ctx context.Context, _ store.Run, _ store.Instance, _ func(domain.InstanceState) error, _ func(string) error) (store.InstanceResult, []store.Artifact, error) {
	<-ctx.Done()
	return store.InstanceResult{}, nil, ctx.Err()
}

func testBundle() runbundle.Bundle {
	return runbundle.Bundle{
		SchemaVersion: runbundle.SchemaVersionV1,
		BundleID:      "bun_t",
		CreatedAt:     time.Now().UTC(),
		Source:        runbundle.Source{Kind: runbundle.SourceKindLocalFiles},
		ResolvedSnapshot: runbundle.ResolvedSnapshot{
			Name:        "smoke",
			Execution:   runbundle.Execution{Mode: runbundle.ExecutionModeFull, MaxConcurrency: 1, InstanceTimeoutSecond: 60},
			Agent:       testfixture.MinimalAgent(),
			RunDefaults: runbundle.RunDefault{Env: map[string]string{}, PTY: runbundle.PTY{}},
			Cases: []runbundle.Case{{
				CaseID:            "c1",
				Image:             "ghcr.io/acme/repo@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
				InitialPrompt:     "hello",
				AgentCwd:          "/workspace",
				TestCommand:       []string{"bash", "-lc", "true"},
				TestCwd:           "/work",
				TestTimeoutSecond: 20,
				TestAssets:        testfixture.MinimalTestAssets(),
			}},
		},
	}
}

func TestPoolProcessesRunToCompletion(t *testing.T) {
	s := store.NewMemoryStore()
	_, err := s.CreateRun(context.Background(), store.CreateRunInput{
		RunID:         "run_ok",
		ProjectID:     "proj_1",
		CreatedByUser: "u1",
		SourceKind:    runbundle.SourceKindLocalFiles,
		Bundle:        testBundle(),
		At:            time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}

	pool := NewPool(s, fakeExecutor{result: store.InstanceResult{FinalState: domain.InstanceStateSucceeded}}, Config{
		WorkerID:          "w1",
		WorkerCount:       1,
		PollInterval:      5 * time.Millisecond,
		LeaseDuration:     2 * time.Second,
		HeartbeatInterval: 20 * time.Millisecond,
		ReaperInterval:    50 * time.Millisecond,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	pool.Start(ctx)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		r, err := s.GetRun(context.Background(), "run_ok", false)
		if err != nil {
			t.Fatalf("get run: %v", err)
		}
		if r.State == domain.RunStateCompleted {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("run did not complete")
}

func TestPoolMarksRunFailedOnExecutorError(t *testing.T) {
	s := store.NewMemoryStore()
	_, _ = s.CreateRun(context.Background(), store.CreateRunInput{
		RunID:         "run_fail",
		ProjectID:     "proj_1",
		CreatedByUser: "u1",
		SourceKind:    runbundle.SourceKindLocalFiles,
		Bundle:        testBundle(),
		At:            time.Now().UTC(),
	})

	pool := NewPool(s, fakeExecutor{err: errors.New("boom")}, Config{
		WorkerID:          "w1",
		WorkerCount:       1,
		PollInterval:      5 * time.Millisecond,
		LeaseDuration:     2 * time.Second,
		HeartbeatInterval: 20 * time.Millisecond,
		ReaperInterval:    50 * time.Millisecond,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	pool.Start(ctx)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		r, _ := s.GetRun(context.Background(), "run_fail", false)
		if r.State == domain.RunStateFailed {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("run did not fail")
}

func TestPoolRetriesInfraFailuresWithinRun(t *testing.T) {
	s := store.NewMemoryStore()
	bundle := testBundle()
	bundle.ResolvedSnapshot.Execution.RetryCount = 1
	_, err := s.CreateRun(context.Background(), store.CreateRunInput{
		RunID:         "run_retry",
		ProjectID:     "proj_1",
		CreatedByUser: "u1",
		SourceKind:    runbundle.SourceKindLocalFiles,
		Bundle:        bundle,
		At:            time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}

	exec := &scriptedExecutor{
		results: []store.InstanceResult{
			{FinalState: domain.InstanceStateInfraFailed, ErrorCode: "EXECUTOR_ERROR", ErrorMessage: "boom"},
			{FinalState: domain.InstanceStateSucceeded},
		},
	}
	pool := NewPool(s, exec, Config{
		WorkerID:          "w1",
		WorkerCount:       1,
		PollInterval:      5 * time.Millisecond,
		LeaseDuration:     2 * time.Second,
		HeartbeatInterval: 20 * time.Millisecond,
		ReaperInterval:    50 * time.Millisecond,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	pool.Start(ctx)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		r, err := s.GetRun(context.Background(), "run_retry", false)
		if err != nil {
			t.Fatalf("get run: %v", err)
		}
		if r.State == domain.RunStateCompleted {
			attempts, err := s.ListInstanceAttempts(context.Background(), "run_retry-inst-0001")
			if err != nil {
				t.Fatalf("list attempts: %v", err)
			}
			if len(attempts) != 2 {
				t.Fatalf("attempt count = %d, want 2", len(attempts))
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("run did not complete after retry")
}

func TestPoolAppliesInstanceTimeoutToEntireAttempt(t *testing.T) {
	s := store.NewMemoryStore()
	bundle := testBundle()
	bundle.ResolvedSnapshot.Execution.InstanceTimeoutSecond = 1
	_, err := s.CreateRun(context.Background(), store.CreateRunInput{
		RunID:         "run_timeout",
		ProjectID:     "proj_1",
		CreatedByUser: "u1",
		SourceKind:    runbundle.SourceKindLocalFiles,
		Bundle:        bundle,
		At:            time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}

	pool := NewPool(s, blockingExecutor{}, Config{
		WorkerID:          "w1",
		WorkerCount:       1,
		PollInterval:      5 * time.Millisecond,
		LeaseDuration:     2 * time.Second,
		HeartbeatInterval: 20 * time.Millisecond,
		ReaperInterval:    50 * time.Millisecond,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	pool.Start(ctx)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		r, err := s.GetRun(context.Background(), "run_timeout", false)
		if err != nil {
			t.Fatalf("get run: %v", err)
		}
		if r.State == domain.RunStateFailed {
			result, err := s.GetInstanceResult(context.Background(), "run_timeout-inst-0001")
			if err != nil {
				t.Fatalf("get instance result: %v", err)
			}
			if result.FinalState != domain.InstanceStateInfraFailed {
				t.Fatalf("final state = %s, want %s", result.FinalState, domain.InstanceStateInfraFailed)
			}
			if result.ErrorCode != "INSTANCE_TIMEOUT" {
				t.Fatalf("error code = %q, want INSTANCE_TIMEOUT", result.ErrorCode)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("run did not fail after instance timeout")
}
