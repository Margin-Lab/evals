package localrunner

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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
	"github.com/marginlab/margin-eval/runner/runner-local/runfs"
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
	runDir := testRunDir(tmp, "fresh-submit")
	svc, err := NewService(Config{
		Executor: fakeExecutor{result: store.InstanceResult{FinalState: domain.InstanceStateSucceeded}},
		Now:      fixedNow,
		IDFunc:   fixedIDFunc(),
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	_, err = svc.SubmitRun(context.Background(), freshSubmitInput("run_submit", "smoke", validBundle(), runDir))
	if err != nil {
		t.Fatalf("submit run: %v", err)
	}

	bundlePath := runfs.BundlePath(runDir)
	if _, err := os.Stat(bundlePath); err != nil {
		t.Fatalf("expected bundle.json at %s: %v", bundlePath, err)
	}
}

func TestServiceRunsAndPersistsSnapshot(t *testing.T) {
	tmp := t.TempDir()
	runDir := testRunDir(tmp, "persisted-snapshot")
	svc, err := NewService(Config{
		Executor: fakeExecutor{result: store.InstanceResult{
			FinalState:       domain.InstanceStateSucceeded,
			InstalledVersion: "3.4.5",
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

	run, err := svc.SubmitRun(context.Background(), freshSubmitInput("run_snapshot", "smoke", validBundle(), runDir))
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

	manifestPath := runfs.ManifestPath(runDir)
	resultsPath := runfs.ResultsPath(runDir)
	eventsPath := runfs.EventsPath(runDir)
	artifactsPath := runfs.ArtifactsIndexPath(runDir)
	instanceResultPath := runfs.InstanceResultPath(runDir, run.RunID+"-inst-0001")
	for _, path := range []string{manifestPath, resultsPath, eventsPath, artifactsPath, instanceResultPath} {
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
	if summary.InstalledVersion != "3.4.5" {
		t.Fatalf("unexpected summary installed version: %q", summary.InstalledVersion)
	}
	if len(summary.Instances) != 1 || summary.Instances[0].InstalledVersion != "3.4.5" {
		t.Fatalf("unexpected per-instance installed version summary: %+v", summary.Instances)
	}

	raw, err = os.ReadFile(instanceResultPath)
	if err != nil {
		t.Fatalf("read instance result: %v", err)
	}
	var instanceResult instanceResultFile
	if err := json.Unmarshal(raw, &instanceResult); err != nil {
		t.Fatalf("unmarshal instance result: %v", err)
	}
	if instanceResult.InstalledVersion != "3.4.5" {
		t.Fatalf("unexpected instance result installed version: %q", instanceResult.InstalledVersion)
	}
}

func TestServicePropagatesExecutorErrorAsFailedRun(t *testing.T) {
	tmp := t.TempDir()
	svc, err := NewService(Config{
		Executor:     fakeExecutor{err: errors.New("boom")},
		EngineConfig: defaultEngineConfig(),
		Now:          fixedNow,
		IDFunc:       fixedIDFunc(),
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	run, err := svc.SubmitRun(context.Background(), freshSubmitInput("run_failed", "smoke", validBundle(), testRunDir(tmp, "failed-run")))
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
	runDir := testRunDir(tmp, "progress")
	svc, err := NewService(Config{
		Executor:     fakeExecutor{result: store.InstanceResult{FinalState: domain.InstanceStateSucceeded}},
		EngineConfig: defaultEngineConfig(),
		Now:          fixedNow,
		IDFunc:       fixedIDFunc(),
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	run, err := svc.SubmitRun(context.Background(), freshSubmitInput("run_progress", "smoke", validBundle(), runDir))
	if err != nil {
		t.Fatalf("submit run: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	svc.Start(ctx)
	if _, err := svc.WaitForTerminalRun(ctx, run.RunID, 20*time.Millisecond); err != nil {
		t.Fatalf("wait for terminal run: %v", err)
	}

	progressPath := runfs.ProgressPath(runDir)
	if _, err := os.Stat(progressPath); err != nil {
		t.Fatalf("expected progress.json at %s: %v", progressPath, err)
	}
}

func TestServicePersistsTerminalSnapshotWithoutWaitForTerminalRun(t *testing.T) {
	tmp := t.TempDir()
	runDir := testRunDir(tmp, "terminal-without-wait")
	svc, err := NewService(Config{
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

	run, err := svc.SubmitRun(context.Background(), freshSubmitInput("run_terminal_no_wait", "smoke", validBundle(), runDir))
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
	resultsPath := runfs.ResultsPath(runDir)
	manifestPath := runfs.ManifestPath(runDir)
	for _, path := range []string{manifestPath, resultsPath} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected file %s after terminal snapshot persistence: %v", path, err)
		}
	}
}

func TestServiceResumesFromProgressAcrossRestart(t *testing.T) {
	tmp := t.TempDir()
	sourceRunDir := testRunDir(tmp, "resume-source")
	resumedRunDir := testRunDir(tmp, "resume-target")

	firstSvc, err := NewService(Config{
		Executor:     fakeExecutor{result: store.InstanceResult{FinalState: domain.InstanceStateSucceeded}},
		EngineConfig: defaultEngineConfig(),
		Now:          fixedNow,
		IDFunc:       fixedIDFunc(),
	})
	if err != nil {
		t.Fatalf("new first service: %v", err)
	}
	sourceRun, err := firstSvc.SubmitRun(context.Background(), freshSubmitInput("run_resume_source", "smoke", validBundle(), sourceRunDir))
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
		Executor:     fakeExecutor{err: errors.New("should not execute resumed completed cases")},
		EngineConfig: defaultEngineConfig(),
		Now:          fixedNow,
		IDFunc:       fixedIDFunc(),
	})
	if err != nil {
		t.Fatalf("new second service: %v", err)
	}
	resumedRun, err := secondSvc.SubmitRun(context.Background(), resumeSubmitInput("run_resume_target", "smoke-resumed", validBundle(), resumedRunDir, sourceRunDir, runnerapi.ResumeModeResume, runnerapi.ResumeBundlePolicyExact))
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
	sourceRunDir := testRunDir(tmp, "carry-failed-source")
	resumedRunDir := testRunDir(tmp, "carry-failed-target")

	firstSvc, err := NewService(Config{
		Executor:     fakeExecutor{result: store.InstanceResult{FinalState: domain.InstanceStateTestFailed}},
		EngineConfig: defaultEngineConfig(),
		Now:          fixedNow,
		IDFunc:       fixedIDFunc(),
	})
	if err != nil {
		t.Fatalf("new first service: %v", err)
	}
	sourceRun, err := firstSvc.SubmitRun(context.Background(), freshSubmitInput("run_carry_failed_source", "smoke", validBundle(), sourceRunDir))
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

	secondSvc, err := NewService(Config{
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
	resumedRun, err := secondSvc.SubmitRun(context.Background(), resumeSubmitInput("run_carry_failed_target", "smoke-resumed", validBundle(), resumedRunDir, sourceRunDir, runnerapi.ResumeModeResume, runnerapi.ResumeBundlePolicyExact))
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
	if resumedFinal.Counts.TestFailed != 1 {
		t.Fatalf("expected resumed run to carry 1 test_failed case, got %+v", resumedFinal.Counts)
	}
}

func TestServiceRetryFailedModeRerunsFailedAcrossRestart(t *testing.T) {
	tmp := t.TempDir()
	sourceRunDir := testRunDir(tmp, "retry-failed-source")
	resumedRunDir := testRunDir(tmp, "retry-failed-target")

	firstSvc, err := NewService(Config{
		Executor:     fakeExecutor{result: store.InstanceResult{FinalState: domain.InstanceStateTestFailed}},
		EngineConfig: defaultEngineConfig(),
		Now:          fixedNow,
		IDFunc:       fixedIDFunc(),
	})
	if err != nil {
		t.Fatalf("new first service: %v", err)
	}
	sourceRun, err := firstSvc.SubmitRun(context.Background(), freshSubmitInput("run_retry_failed_source", "smoke", validBundle(), sourceRunDir))
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

	executions := 0
	secondSvc, err := NewService(Config{
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
	resumedRun, err := secondSvc.SubmitRun(context.Background(), resumeSubmitInput("run_retry_failed_target", "smoke-retry-failed", validBundle(), resumedRunDir, sourceRunDir, runnerapi.ResumeModeRetryFailed, runnerapi.ResumeBundlePolicyExact))
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
	sourceRunDir := testRunDir(tmp, "resume-infra-source")
	resumedRunDir := testRunDir(tmp, "resume-infra-target")

	firstSvc, err := NewService(Config{
		Executor:     fakeExecutor{result: store.InstanceResult{FinalState: domain.InstanceStateInfraFailed}},
		EngineConfig: defaultEngineConfig(),
		Now:          fixedNow,
		IDFunc:       fixedIDFunc(),
	})
	if err != nil {
		t.Fatalf("new first service: %v", err)
	}
	sourceRun, err := firstSvc.SubmitRun(context.Background(), freshSubmitInput("run_resume_infra_source", "smoke", validBundle(), sourceRunDir))
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
	resumedRun, err := secondSvc.SubmitRun(context.Background(), resumeSubmitInput("run_resume_infra_target", "smoke-resume-infra", validBundle(), resumedRunDir, sourceRunDir, runnerapi.ResumeModeResume, runnerapi.ResumeBundlePolicyExact))
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

func TestServiceAllowMismatchCarriesIntersectingCasesAndRunsNewCases(t *testing.T) {
	tmp := t.TempDir()
	sourceRunDir := testRunDir(tmp, "allow-mismatch-source")
	resumedRunDir := testRunDir(tmp, "allow-mismatch-target")

	firstSvc, err := NewService(Config{
		Executor:     fakeExecutor{result: store.InstanceResult{FinalState: domain.InstanceStateSucceeded}},
		EngineConfig: defaultEngineConfig(),
		Now:          fixedNow,
		IDFunc:       fixedIDFunc(),
	})
	if err != nil {
		t.Fatalf("new first service: %v", err)
	}
	sourceRun, err := firstSvc.SubmitRun(context.Background(), freshSubmitInput("run_allow_mismatch_source", "smoke", validBundleWithCases("case_1"), sourceRunDir))
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

	executions := 0
	secondSvc, err := NewService(Config{
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
	resumedRun, err := secondSvc.SubmitRun(context.Background(), resumeSubmitInput("run_allow_mismatch_target", "smoke-override", validBundleWithCases("case_1", "case_2"), resumedRunDir, sourceRunDir, runnerapi.ResumeModeResume, runnerapi.ResumeBundlePolicyAllowMismatch))
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
	if resumedFinal.Counts.Succeeded != 2 {
		t.Fatalf("expected 2 succeeded cases, got %+v", resumedFinal.Counts)
	}
	if executions != 1 {
		t.Fatalf("expected exactly one new execution, got %d", executions)
	}
}

func TestServiceRequiresExplicitRunIDAndOutputDir(t *testing.T) {
	svc, err := NewService(Config{
		Executor: fakeExecutor{result: store.InstanceResult{FinalState: domain.InstanceStateSucceeded}},
		Now:      fixedNow,
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	if _, err := svc.SubmitRun(context.Background(), runnerapi.SubmitInput{
		ProjectID:     "proj_local",
		CreatedByUser: "user_local",
		Name:          "missing-run-id",
		Bundle:        validBundle(),
		OutputDir:     testRunDir(t.TempDir(), "missing-run-id"),
	}); err == nil || !strings.Contains(err.Error(), "run id is required") {
		t.Fatalf("expected run id validation error, got %v", err)
	}
	if _, err := svc.SubmitRun(context.Background(), runnerapi.SubmitInput{
		RunID:         "run_missing_output",
		ProjectID:     "proj_local",
		CreatedByUser: "user_local",
		Name:          "missing-output",
		Bundle:        validBundle(),
	}); err == nil || !strings.Contains(err.Error(), "output dir is required") {
		t.Fatalf("expected output dir validation error, got %v", err)
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

func testRunDir(baseDir, name string) string {
	return filepath.Join(baseDir, name)
}

func freshSubmitInput(runID, name string, bundle runbundle.Bundle, outputDir string) runnerapi.SubmitInput {
	return runnerapi.SubmitInput{
		RunID:         runID,
		OutputDir:     outputDir,
		ProjectID:     "proj_local",
		CreatedByUser: "user_local",
		Name:          name,
		Bundle:        bundle,
	}
}

func resumeSubmitInput(runID, name string, bundle runbundle.Bundle, outputDir, resumeFromDir string, mode runnerapi.ResumeMode, policy runnerapi.ResumeBundlePolicy) runnerapi.SubmitInput {
	in := freshSubmitInput(runID, name, bundle, outputDir)
	in.ResumeFromDir = resumeFromDir
	in.ResumeMode = mode
	in.ResumeBundlePolicy = policy
	return in
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
	return validBundleWithCases("case_1")
}

func validBundleWithCases(caseIDs ...string) runbundle.Bundle {
	cases := make([]runbundle.Case, 0, len(caseIDs))
	for _, caseID := range caseIDs {
		cases = append(cases, runbundle.Case{
			CaseID:            caseID,
			Image:             "ghcr.io/acme/repo@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			InitialPrompt:     "Fix tests",
			AgentCwd:          "/workspace",
			TestCommand:       []string{"bash", "-lc", "./test.sh"},
			TestCwd:           "/work",
			TestTimeoutSecond: 60,
			TestAssets:        testfixture.MinimalTestAssets(),
		})
	}
	return runbundle.Bundle{
		SchemaVersion: runbundle.SchemaVersionV1,
		BundleID:      "bun_1",
		CreatedAt:     time.Date(2026, 2, 26, 12, 0, 0, 0, time.UTC),
		Source:        runbundle.Source{Kind: runbundle.SourceKindLocalFiles, SubmitProjectID: "proj_local"},
		ResolvedSnapshot: runbundle.ResolvedSnapshot{
			Name:        "smoke",
			Execution:   runbundle.Execution{Mode: runbundle.ExecutionModeFull, MaxConcurrency: 1, FailFast: false, InstanceTimeoutSecond: 120},
			Agent:       testfixture.MinimalAgent(),
			RunDefaults: runbundle.RunDefault{Env: map[string]string{"TERM": "xterm-256color"}, PTY: runbundle.PTY{Cols: 120, Rows: 40}},
			Cases:       cases,
		},
	}
}

func int64Ptr(v int64) *int64 {
	return &v
}
