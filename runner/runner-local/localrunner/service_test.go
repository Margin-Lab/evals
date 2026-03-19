package localrunner

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/marginlab/margin-eval/runner/runner-core/domain"
	"github.com/marginlab/margin-eval/runner/runner-core/engine"
	"github.com/marginlab/margin-eval/runner/runner-core/runbundle"
	"github.com/marginlab/margin-eval/runner/runner-core/runnerapi"
	"github.com/marginlab/margin-eval/runner/runner-core/runresults"
	"github.com/marginlab/margin-eval/runner/runner-core/store"
	"github.com/marginlab/margin-eval/runner/runner-core/testfixture"
	"github.com/marginlab/margin-eval/runner/runner-core/usage"
)

type fakeExecutor struct {
	result    store.InstanceResult
	err       error
	onExecute func()
}

func (f fakeExecutor) ExecuteInstance(_ context.Context, run store.Run, inst store.Instance, updateState func(domain.InstanceState) error, updateResolvedImage func(string) error) (store.InstanceResult, []store.Artifact, error) {
	_ = run
	_ = inst
	_ = updateResolvedImage
	if f.onExecute != nil {
		f.onExecute()
	}
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
	return f.result, []store.Artifact{}, f.err
}

func TestServiceSubmitRunWritesBundle(t *testing.T) {
	tmp := t.TempDir()
	svc, err := NewService(Config{
		RootDir:  tmp,
		Executor: fakeExecutor{result: store.InstanceResult{FinalState: domain.InstanceStateSucceeded}},
		Now:      fixedNow,
		IDFunc:   fixedIDFunc(),
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	run, err := svc.SubmitRun(context.Background(), runnerapi.SubmitInput{
		ProjectID:     "proj_local",
		CreatedByUser: "user_local",
		Name:          "smoke",
		Bundle:        validBundle(),
	})
	if err != nil {
		t.Fatalf("submit run: %v", err)
	}

	bundlePath := filepath.Join(tmp, "runs", run.RunID, "bundle.json")
	if _, err := os.Stat(bundlePath); err != nil {
		t.Fatalf("expected bundle.json at %s: %v", bundlePath, err)
	}
}

func TestServiceRunsAndPersistsSnapshot(t *testing.T) {
	tmp := t.TempDir()
	svc, err := NewService(Config{
		RootDir: tmp,
		Executor: fakeExecutor{result: store.InstanceResult{
			FinalState: domain.InstanceStateSucceeded,
			Usage: &usage.Metrics{
				InputTokens:  int64Ptr(17),
				OutputTokens: int64Ptr(6),
				ToolCalls:    int64Ptr(1),
			},
		}},
		EngineConfig: defaultEngineConfig(),
		Now:          fixedNow,
		IDFunc:       fixedIDFunc(),
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	run, err := svc.SubmitRun(context.Background(), runnerapi.SubmitInput{
		ProjectID:     "proj_local",
		CreatedByUser: "user_local",
		Name:          "smoke",
		Bundle:        validBundle(),
	})
	if err != nil {
		t.Fatalf("submit run: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	svc.Start(ctx)

	finalRun, err := svc.WaitForTerminalRun(ctx, run.RunID, 20*time.Millisecond)
	if err != nil {
		t.Fatalf("wait for run: %v", err)
	}
	if finalRun.State != domain.RunStateCompleted {
		t.Fatalf("expected completed run, got %s", finalRun.State)
	}

	manifestPath := filepath.Join(tmp, "runs", run.RunID, "manifest.json")
	resultsPath := filepath.Join(tmp, "runs", run.RunID, "results.json")
	eventsPath := filepath.Join(tmp, "runs", run.RunID, "events.jsonl")
	artifactsPath := filepath.Join(tmp, "runs", run.RunID, "artifacts", "metadata.json")
	for _, path := range []string{manifestPath, resultsPath, eventsPath, artifactsPath} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected file %s: %v", path, err)
		}
	}

	raw, err := os.ReadFile(resultsPath)
	if err != nil {
		t.Fatalf("read results.json: %v", err)
	}
	var summary runresults.Summary
	if err := json.Unmarshal(raw, &summary); err != nil {
		t.Fatalf("unmarshal results.json: %v", err)
	}
	if summary.TotalInstances != 1 {
		t.Fatalf("unexpected total instances: %d", summary.TotalInstances)
	}
	if summary.Status.Succeeded.Count != 1 || summary.Status.Succeeded.Percentage != 100 {
		t.Fatalf("unexpected succeeded summary: %+v", summary.Status.Succeeded)
	}
	if summary.Usage.InputTokens != 17 || summary.Usage.OutputTokens != 6 || summary.Usage.ToolCalls != 1 {
		t.Fatalf("unexpected usage summary: %+v", summary.Usage)
	}
}

func TestServicePropagatesExecutorErrorAsFailedRun(t *testing.T) {
	tmp := t.TempDir()
	svc, err := NewService(Config{
		RootDir:      tmp,
		Executor:     fakeExecutor{err: errors.New("boom")},
		EngineConfig: defaultEngineConfig(),
		Now:          fixedNow,
		IDFunc:       fixedIDFunc(),
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	run, err := svc.SubmitRun(context.Background(), runnerapi.SubmitInput{
		ProjectID:     "proj_local",
		CreatedByUser: "user_local",
		Name:          "smoke",
		Bundle:        validBundle(),
	})
	if err != nil {
		t.Fatalf("submit run: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	svc.Start(ctx)

	finalRun, err := svc.WaitForTerminalRun(ctx, run.RunID, 20*time.Millisecond)
	if err != nil {
		t.Fatalf("wait for run: %v", err)
	}
	if finalRun.State != domain.RunStateFailed {
		t.Fatalf("expected failed run, got %s", finalRun.State)
	}
}

func TestServiceWritesProgressFile(t *testing.T) {
	tmp := t.TempDir()
	svc, err := NewService(Config{
		RootDir:      tmp,
		Executor:     fakeExecutor{result: store.InstanceResult{FinalState: domain.InstanceStateSucceeded}},
		EngineConfig: defaultEngineConfig(),
		Now:          fixedNow,
		IDFunc:       fixedIDFunc(),
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	run, err := svc.SubmitRun(context.Background(), runnerapi.SubmitInput{
		ProjectID:     "proj_local",
		CreatedByUser: "user_local",
		Name:          "smoke",
		Bundle:        validBundle(),
	})
	if err != nil {
		t.Fatalf("submit run: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	svc.Start(ctx)
	if _, err := svc.WaitForTerminalRun(ctx, run.RunID, 20*time.Millisecond); err != nil {
		t.Fatalf("wait for terminal run: %v", err)
	}

	progressPath := filepath.Join(tmp, "runs", run.RunID, "progress.json")
	if _, err := os.Stat(progressPath); err != nil {
		t.Fatalf("expected progress.json at %s: %v", progressPath, err)
	}
}

func TestServicePersistsTerminalSnapshotWithoutWaitForTerminalRun(t *testing.T) {
	tmp := t.TempDir()
	svc, err := NewService(Config{
		RootDir: tmp,
		Executor: fakeExecutor{result: store.InstanceResult{
			FinalState: domain.InstanceStateSucceeded,
		}},
		EngineConfig: defaultEngineConfig(),
		Now:          fixedNow,
		IDFunc:       fixedIDFunc(),
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	run, err := svc.SubmitRun(context.Background(), runnerapi.SubmitInput{
		ProjectID:     "proj_local",
		CreatedByUser: "user_local",
		Name:          "smoke",
		Bundle:        validBundle(),
	})
	if err != nil {
		t.Fatalf("submit run: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	svc.Start(ctx)

	var snapshot runnerapi.RunSnapshot
	for {
		snapshot, err = svc.GetRunSnapshot(ctx, run.RunID, runnerapi.SnapshotOptions{})
		if err != nil {
			t.Fatalf("get run snapshot: %v", err)
		}
		if snapshot.Run.State.IsTerminal() {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatalf("run did not reach terminal state: %v", ctx.Err())
		case <-time.After(20 * time.Millisecond):
		}
	}

	if snapshot.Run.State != domain.RunStateCompleted {
		t.Fatalf("expected completed run, got %s", snapshot.Run.State)
	}
	resultsPath := filepath.Join(tmp, "runs", run.RunID, "results.json")
	manifestPath := filepath.Join(tmp, "runs", run.RunID, "manifest.json")
	for _, path := range []string{manifestPath, resultsPath} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected file %s after terminal snapshot persistence: %v", path, err)
		}
	}
}

func TestServiceResumesFromProgressAcrossRestart(t *testing.T) {
	tmp := t.TempDir()

	firstSvc, err := NewService(Config{
		RootDir:      tmp,
		Executor:     fakeExecutor{result: store.InstanceResult{FinalState: domain.InstanceStateSucceeded}},
		EngineConfig: defaultEngineConfig(),
		Now:          fixedNow,
		IDFunc:       fixedIDFunc(),
	})
	if err != nil {
		t.Fatalf("new first service: %v", err)
	}
	sourceRun, err := firstSvc.SubmitRun(context.Background(), runnerapi.SubmitInput{
		ProjectID:     "proj_local",
		CreatedByUser: "user_local",
		Name:          "smoke",
		Bundle:        validBundle(),
	})
	if err != nil {
		t.Fatalf("submit source run: %v", err)
	}

	firstCtx, firstCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer firstCancel()
	firstSvc.Start(firstCtx)
	sourceFinal, err := firstSvc.WaitForTerminalRun(firstCtx, sourceRun.RunID, 20*time.Millisecond)
	if err != nil {
		t.Fatalf("wait source run: %v", err)
	}
	if sourceFinal.State != domain.RunStateCompleted {
		t.Fatalf("expected completed source run, got %s", sourceFinal.State)
	}

	// Simulate process restart with a fresh in-memory run store.
	secondSvc, err := NewService(Config{
		RootDir:      tmp,
		Executor:     fakeExecutor{err: errors.New("should not execute resumed completed cases")},
		EngineConfig: defaultEngineConfig(),
		Now:          fixedNow,
		IDFunc:       fixedIDFunc(),
	})
	if err != nil {
		t.Fatalf("new second service: %v", err)
	}
	resumedRun, err := secondSvc.SubmitRun(context.Background(), runnerapi.SubmitInput{
		ProjectID:       "proj_local",
		CreatedByUser:   "user_local",
		Name:            "smoke-resumed",
		Bundle:          validBundle(),
		ResumeFromRunID: sourceRun.RunID,
		ResumeMode:      runnerapi.ResumeModeResume,
	})
	if err != nil {
		t.Fatalf("submit resumed run: %v", err)
	}
	resumeCtx, resumeCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer resumeCancel()
	resumedFinal, err := secondSvc.WaitForTerminalRun(resumeCtx, resumedRun.RunID, 20*time.Millisecond)
	if err != nil {
		t.Fatalf("wait resumed run: %v", err)
	}
	if resumedFinal.State != domain.RunStateCompleted {
		t.Fatalf("expected completed resumed run, got %s", resumedFinal.State)
	}
	if resumedFinal.Counts.Succeeded != 1 {
		t.Fatalf("expected resumed run to carry 1 succeeded case, got %+v", resumedFinal.Counts)
	}
}

func TestServiceResumeModeCarriesFailedAcrossRestart(t *testing.T) {
	tmp := t.TempDir()

	firstSvc, err := NewService(Config{
		RootDir:      tmp,
		Executor:     fakeExecutor{result: store.InstanceResult{FinalState: domain.InstanceStateTestFailed}},
		EngineConfig: defaultEngineConfig(),
		Now:          fixedNow,
		IDFunc:       fixedIDFunc(),
	})
	if err != nil {
		t.Fatalf("new first service: %v", err)
	}
	sourceRun, err := firstSvc.SubmitRun(context.Background(), runnerapi.SubmitInput{
		ProjectID:     "proj_local",
		CreatedByUser: "user_local",
		Name:          "smoke",
		Bundle:        validBundle(),
	})
	if err != nil {
		t.Fatalf("submit source run: %v", err)
	}

	firstCtx, firstCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer firstCancel()
	firstSvc.Start(firstCtx)
	sourceFinal, err := firstSvc.WaitForTerminalRun(firstCtx, sourceRun.RunID, 20*time.Millisecond)
	if err != nil {
		t.Fatalf("wait source run: %v", err)
	}
	if sourceFinal.State != domain.RunStateFailed {
		t.Fatalf("expected failed source run, got %s", sourceFinal.State)
	}

	secondSvc, err := NewService(Config{
		RootDir: tmp,
		Executor: fakeExecutor{
			onExecute: func() {
				t.Fatalf("should not execute carried test_failed cases in resume mode")
			},
		},
		EngineConfig: defaultEngineConfig(),
		Now:          fixedNow,
		IDFunc:       fixedIDFunc(),
	})
	if err != nil {
		t.Fatalf("new second service: %v", err)
	}
	resumedRun, err := secondSvc.SubmitRun(context.Background(), runnerapi.SubmitInput{
		ProjectID:       "proj_local",
		CreatedByUser:   "user_local",
		Name:            "smoke-resumed",
		Bundle:          validBundle(),
		ResumeFromRunID: sourceRun.RunID,
		ResumeMode:      runnerapi.ResumeModeResume,
	})
	if err != nil {
		t.Fatalf("submit resumed run: %v", err)
	}
	resumeCtx, resumeCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer resumeCancel()
	resumedFinal, err := secondSvc.WaitForTerminalRun(resumeCtx, resumedRun.RunID, 20*time.Millisecond)
	if err != nil {
		t.Fatalf("wait resumed run: %v", err)
	}
	if resumedFinal.State != domain.RunStateFailed {
		t.Fatalf("expected failed resumed run, got %s", resumedFinal.State)
	}
	if resumedFinal.Counts.TestFailed != 1 {
		t.Fatalf("expected resumed run to carry 1 test_failed case, got %+v", resumedFinal.Counts)
	}
}

func TestServiceRetryFailedModeRerunsFailedAcrossRestart(t *testing.T) {
	tmp := t.TempDir()

	firstSvc, err := NewService(Config{
		RootDir:      tmp,
		Executor:     fakeExecutor{result: store.InstanceResult{FinalState: domain.InstanceStateTestFailed}},
		EngineConfig: defaultEngineConfig(),
		Now:          fixedNow,
		IDFunc:       fixedIDFunc(),
	})
	if err != nil {
		t.Fatalf("new first service: %v", err)
	}
	sourceRun, err := firstSvc.SubmitRun(context.Background(), runnerapi.SubmitInput{
		ProjectID:     "proj_local",
		CreatedByUser: "user_local",
		Name:          "smoke",
		Bundle:        validBundle(),
	})
	if err != nil {
		t.Fatalf("submit source run: %v", err)
	}

	firstCtx, firstCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer firstCancel()
	firstSvc.Start(firstCtx)
	sourceFinal, err := firstSvc.WaitForTerminalRun(firstCtx, sourceRun.RunID, 20*time.Millisecond)
	if err != nil {
		t.Fatalf("wait source run: %v", err)
	}
	if sourceFinal.State != domain.RunStateFailed {
		t.Fatalf("expected failed source run, got %s", sourceFinal.State)
	}

	executions := 0
	secondSvc, err := NewService(Config{
		RootDir: tmp,
		Executor: fakeExecutor{
			result: store.InstanceResult{FinalState: domain.InstanceStateSucceeded},
			onExecute: func() {
				executions++
			},
		},
		EngineConfig: defaultEngineConfig(),
		Now:          fixedNow,
		IDFunc:       fixedIDFunc(),
	})
	if err != nil {
		t.Fatalf("new second service: %v", err)
	}
	resumedRun, err := secondSvc.SubmitRun(context.Background(), runnerapi.SubmitInput{
		ProjectID:       "proj_local",
		CreatedByUser:   "user_local",
		Name:            "smoke-retry-failed",
		Bundle:          validBundle(),
		ResumeFromRunID: sourceRun.RunID,
		ResumeMode:      runnerapi.ResumeModeRetryFailed,
	})
	if err != nil {
		t.Fatalf("submit resumed run: %v", err)
	}
	resumeCtx, resumeCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer resumeCancel()
	secondSvc.Start(resumeCtx)
	resumedFinal, err := secondSvc.WaitForTerminalRun(resumeCtx, resumedRun.RunID, 20*time.Millisecond)
	if err != nil {
		t.Fatalf("wait resumed run: %v", err)
	}
	if resumedFinal.State != domain.RunStateCompleted {
		t.Fatalf("expected completed resumed run, got %s", resumedFinal.State)
	}
	if resumedFinal.Counts.Succeeded != 1 {
		t.Fatalf("expected resumed run to rerun and succeed, got %+v", resumedFinal.Counts)
	}
	if executions != 1 {
		t.Fatalf("expected exactly one rerun execution, got %d", executions)
	}
}

func TestServiceResumeModeRerunsInfraFailedAcrossRestart(t *testing.T) {
	tmp := t.TempDir()

	firstSvc, err := NewService(Config{
		RootDir:      tmp,
		Executor:     fakeExecutor{result: store.InstanceResult{FinalState: domain.InstanceStateInfraFailed}},
		EngineConfig: defaultEngineConfig(),
		Now:          fixedNow,
		IDFunc:       fixedIDFunc(),
	})
	if err != nil {
		t.Fatalf("new first service: %v", err)
	}
	sourceRun, err := firstSvc.SubmitRun(context.Background(), runnerapi.SubmitInput{
		ProjectID:     "proj_local",
		CreatedByUser: "user_local",
		Name:          "smoke",
		Bundle:        validBundle(),
	})
	if err != nil {
		t.Fatalf("submit source run: %v", err)
	}

	firstCtx, firstCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer firstCancel()
	firstSvc.Start(firstCtx)
	sourceFinal, err := firstSvc.WaitForTerminalRun(firstCtx, sourceRun.RunID, 20*time.Millisecond)
	if err != nil {
		t.Fatalf("wait source run: %v", err)
	}
	if sourceFinal.State != domain.RunStateFailed {
		t.Fatalf("expected failed source run, got %s", sourceFinal.State)
	}

	executions := 0
	secondSvc, err := NewService(Config{
		RootDir: tmp,
		Executor: fakeExecutor{
			result: store.InstanceResult{FinalState: domain.InstanceStateSucceeded},
			onExecute: func() {
				executions++
			},
		},
		EngineConfig: defaultEngineConfig(),
		Now:          fixedNow,
		IDFunc:       fixedIDFunc(),
	})
	if err != nil {
		t.Fatalf("new second service: %v", err)
	}
	resumedRun, err := secondSvc.SubmitRun(context.Background(), runnerapi.SubmitInput{
		ProjectID:       "proj_local",
		CreatedByUser:   "user_local",
		Name:            "smoke-resume-infra",
		Bundle:          validBundle(),
		ResumeFromRunID: sourceRun.RunID,
		ResumeMode:      runnerapi.ResumeModeResume,
	})
	if err != nil {
		t.Fatalf("submit resumed run: %v", err)
	}
	resumeCtx, resumeCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer resumeCancel()
	secondSvc.Start(resumeCtx)
	resumedFinal, err := secondSvc.WaitForTerminalRun(resumeCtx, resumedRun.RunID, 20*time.Millisecond)
	if err != nil {
		t.Fatalf("wait resumed run: %v", err)
	}
	if resumedFinal.State != domain.RunStateCompleted {
		t.Fatalf("expected completed resumed run, got %s", resumedFinal.State)
	}
	if resumedFinal.Counts.Succeeded != 1 {
		t.Fatalf("expected resumed run to rerun and succeed, got %+v", resumedFinal.Counts)
	}
	if executions != 1 {
		t.Fatalf("expected exactly one rerun execution, got %d", executions)
	}
}

func TestServiceDefaultRunIDContinuesAcrossRestart(t *testing.T) {
	tmp := t.TempDir()
	exec := fakeExecutor{result: store.InstanceResult{FinalState: domain.InstanceStateSucceeded}}

	firstSvc, err := NewService(Config{
		RootDir:  tmp,
		Executor: exec,
		Now:      fixedNow,
	})
	if err != nil {
		t.Fatalf("new first service: %v", err)
	}
	firstRun, err := firstSvc.SubmitRun(context.Background(), runnerapi.SubmitInput{
		ProjectID:     "proj_local",
		CreatedByUser: "user_local",
		Name:          "first",
		Bundle:        validBundle(),
	})
	if err != nil {
		t.Fatalf("submit first run: %v", err)
	}
	if firstRun.RunID != "run_000001" {
		t.Fatalf("expected first run id run_000001, got %s", firstRun.RunID)
	}

	secondSvc, err := NewService(Config{
		RootDir:  tmp,
		Executor: exec,
		Now:      fixedNow,
	})
	if err != nil {
		t.Fatalf("new second service: %v", err)
	}
	secondRun, err := secondSvc.SubmitRun(context.Background(), runnerapi.SubmitInput{
		ProjectID:     "proj_local",
		CreatedByUser: "user_local",
		Name:          "second",
		Bundle:        validBundle(),
	})
	if err != nil {
		t.Fatalf("submit second run: %v", err)
	}
	if secondRun.RunID != "run_000002" {
		t.Fatalf("expected second run id run_000002, got %s", secondRun.RunID)
	}
}

func fixedNow() time.Time {
	return time.Date(2026, 2, 26, 12, 0, 0, 0, time.UTC)
}

func fixedIDFunc() func(string) string {
	counts := map[string]int{}
	return func(prefix string) string {
		counts[prefix]++
		return prefix + "_" + strconv.Itoa(counts[prefix])
	}
}

func defaultEngineConfig() engine.Config {
	return engine.Config{
		WorkerID:          "local-worker",
		WorkerCount:       1,
		PollInterval:      5 * time.Millisecond,
		LeaseDuration:     2 * time.Second,
		HeartbeatInterval: 20 * time.Millisecond,
		ReaperInterval:    100 * time.Millisecond,
	}
}

func validBundle() runbundle.Bundle {
	return runbundle.Bundle{
		SchemaVersion: runbundle.SchemaVersionV1,
		BundleID:      "bun_1",
		CreatedAt:     time.Date(2026, 2, 26, 12, 0, 0, 0, time.UTC),
		Source:        runbundle.Source{Kind: runbundle.SourceKindLocalFiles, SubmitProjectID: "proj_local"},
		ResolvedSnapshot: runbundle.ResolvedSnapshot{
			Name:        "smoke",
			Execution:   runbundle.Execution{Mode: runbundle.ExecutionModeFull, MaxConcurrency: 1, FailFast: false, InstanceTimeoutSecond: 120},
			Agent:       testfixture.MinimalAgent(),
			RunDefaults: runbundle.RunDefault{Cwd: "/work", Env: map[string]string{"TERM": "xterm-256color"}, PTY: runbundle.PTY{Cols: 120, Rows: 40}},
			Cases: []runbundle.Case{{
				CaseID:            "case_1",
				Image:             "ghcr.io/acme/repo@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
				InitialPrompt:     "Fix tests",
				TestCommand:       []string{"bash", "-lc", "./test.sh"},
				TestCwd:           "/work",
				TestTimeoutSecond: 60,
				TestAssets:        testfixture.MinimalTestAssets(),
			}},
		},
	}
}

func int64Ptr(v int64) *int64 {
	return &v
}
