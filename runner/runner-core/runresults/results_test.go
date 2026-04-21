package runresults

import (
	"testing"
	"time"

	"github.com/marginlab/margin-eval/runner/runner-core/domain"
	"github.com/marginlab/margin-eval/runner/runner-core/runbundle"
	"github.com/marginlab/margin-eval/runner/runner-core/store"
	"github.com/marginlab/margin-eval/runner/runner-core/usage"
)

func TestBuildSummarizesResultsUsageAndRuntime(t *testing.T) {
	startedAt := time.Date(2026, 3, 11, 15, 0, 0, 0, time.UTC)
	endedAt := startedAt.Add(90 * time.Second)
	run := store.Run{
		RunID:     "run_1",
		State:     domain.RunStateCompleted,
		StartedAt: &startedAt,
		EndedAt:   &endedAt,
	}

	instances := []store.Instance{
		{
			InstanceID: "inst_2",
			Ordinal:    1,
			Case:       runbundle.Case{CaseID: "case_2"},
			State:      domain.InstanceStateTestFailed,
			CreatedAt:  startedAt.Add(10 * time.Second),
			UpdatedAt:  startedAt.Add(46 * time.Second),
		},
		{
			InstanceID: "inst_1",
			Ordinal:    0,
			Case:       runbundle.Case{CaseID: "case_1"},
			State:      domain.InstanceStateSucceeded,
			CreatedAt:  startedAt,
			UpdatedAt:  startedAt.Add(31 * time.Second),
		},
		{
			InstanceID: "inst_3",
			Ordinal:    2,
			Case:       runbundle.Case{CaseID: "case_3"},
			State:      domain.InstanceStateCanceled,
			CreatedAt:  startedAt.Add(20 * time.Second),
			UpdatedAt:  startedAt.Add(25 * time.Second),
		},
	}

	results := []store.StoredInstanceResult{
		{
			InstanceID:       "inst_1",
			FinalState:       domain.InstanceStateSucceeded,
			InstalledVersion: "1.0.0",
			ProvisionedAt:    timePtr(startedAt.Add(1 * time.Second)),
			TestEndedAt:      timePtr(startedAt.Add(31 * time.Second)),
			Usage:            metrics(100, 25, 2),
		},
		{
			InstanceID:       "inst_2",
			FinalState:       domain.InstanceStateTestFailed,
			InstalledVersion: "1.0.0",
			TestExitCode:     intPtr(1),
			ProvisionedAt:    timePtr(startedAt.Add(11 * time.Second)),
			AgentEndedAt:     timePtr(startedAt.Add(46 * time.Second)),
			Usage:            metrics(20, 5, 1),
		},
		{
			InstanceID:    "inst_3",
			FinalState:    domain.InstanceStateCanceled,
			ProvisionedAt: timePtr(startedAt.Add(21 * time.Second)),
			TestEndedAt:   timePtr(startedAt.Add(24 * time.Second)),
		},
	}

	summary := Build(run, instances, results)

	if summary.TotalInstances != 3 {
		t.Fatalf("unexpected total instances: %d", summary.TotalInstances)
	}
	if summary.Status.Succeeded.Count != 1 || summary.Status.Succeeded.Percentage != 33.33 {
		t.Fatalf("unexpected succeeded breakdown: %+v", summary.Status.Succeeded)
	}
	if summary.Status.TestFailed.Count != 1 || summary.Status.TestFailed.Percentage != 33.33 {
		t.Fatalf("unexpected test_failed breakdown: %+v", summary.Status.TestFailed)
	}
	if summary.Status.Canceled.Count != 1 || summary.Status.Canceled.Percentage != 33.33 {
		t.Fatalf("unexpected canceled breakdown: %+v", summary.Status.Canceled)
	}
	if len(summary.InfraFailureReasons) != 0 {
		t.Fatalf("expected no infra failure reasons, got %+v", summary.InfraFailureReasons)
	}
	if summary.Usage.InputTokens != 120 || summary.Usage.OutputTokens != 30 || summary.Usage.ToolCalls != 3 {
		t.Fatalf("unexpected aggregate usage: %+v", summary.Usage)
	}
	if summary.Usage.InstancesWithUsage != 2 || summary.Usage.InstancesWithoutUsage != 1 {
		t.Fatalf("unexpected usage coverage: %+v", summary.Usage)
	}
	if summary.Runtime.RunMS == nil || *summary.Runtime.RunMS != 90000 {
		t.Fatalf("unexpected run runtime: %#v", summary.Runtime.RunMS)
	}
	if len(summary.Instances) != 3 {
		t.Fatalf("expected 3 instance summaries, got %d", len(summary.Instances))
	}
	if summary.Instances[0].InstanceID != "inst_1" || summary.Instances[0].RuntimeMS != 30000 {
		t.Fatalf("unexpected first instance summary: %+v", summary.Instances[0])
	}
	if summary.Instances[0].InfraFailureReason != nil {
		t.Fatalf("expected nil infra failure reason for success, got %+v", summary.Instances[0].InfraFailureReason)
	}
	if summary.Instances[0].InstalledVersion != "1.0.0" {
		t.Fatalf("unexpected first instance installed version: %+v", summary.Instances[0])
	}
	if summary.Instances[1].InstanceID != "inst_2" || summary.Instances[1].RuntimeMS != 35000 {
		t.Fatalf("unexpected second instance summary: %+v", summary.Instances[1])
	}
	if summary.Instances[1].InfraFailureReason != nil {
		t.Fatalf("expected nil infra failure reason for test_failed, got %+v", summary.Instances[1].InfraFailureReason)
	}
	if summary.Instances[1].InstalledVersion != "1.0.0" {
		t.Fatalf("unexpected second instance installed version: %+v", summary.Instances[1])
	}
	if summary.Instances[2].InstanceID != "inst_3" || summary.Instances[2].RuntimeMS != 3000 {
		t.Fatalf("unexpected third instance summary: %+v", summary.Instances[2])
	}
	if summary.Instances[2].InfraFailureReason != nil {
		t.Fatalf("expected nil infra failure reason for canceled, got %+v", summary.Instances[2].InfraFailureReason)
	}
	if summary.InstalledVersion != "" {
		t.Fatalf("expected omitted top-level installed version when a terminal instance is missing it, got %q", summary.InstalledVersion)
	}
}

func TestBuildClassifiesInfraFailureReasons(t *testing.T) {
	run := store.Run{RunID: "run_2"}
	instances := []store.Instance{
		{InstanceID: "inst_exec", Ordinal: 0, Case: runbundle.Case{CaseID: "case_exec"}, State: domain.InstanceStateInfraFailed},
		{InstanceID: "inst_timeout", Ordinal: 1, Case: runbundle.Case{CaseID: "case_timeout"}, State: domain.InstanceStateInfraFailed},
		{InstanceID: "inst_invalid", Ordinal: 2, Case: runbundle.Case{CaseID: "case_invalid"}, State: domain.InstanceStateInfraFailed},
		{InstanceID: "inst_unknown", Ordinal: 3, Case: runbundle.Case{CaseID: "case_unknown"}, State: domain.InstanceStateInfraFailed},
	}
	results := []store.StoredInstanceResult{
		{InstanceID: "inst_exec", FinalState: domain.InstanceStateInfraFailed, ErrorCode: "EXECUTOR_ERROR"},
		{InstanceID: "inst_timeout", FinalState: domain.InstanceStateInfraFailed, ErrorCode: "INSTANCE_TIMEOUT"},
		{InstanceID: "inst_invalid", FinalState: domain.InstanceStateInfraFailed, ErrorCode: "INVALID_FINAL_STATE"},
		{InstanceID: "inst_unknown", FinalState: domain.InstanceStateInfraFailed},
	}

	summary := Build(run, instances, results)

	reasons := map[string]int{}
	for _, item := range summary.InfraFailureReasons {
		reasons[item.Reason] = item.Count
	}
	if reasons[InfraFailureReasonExecutorError] != 1 {
		t.Fatalf("missing executor_error reason: %+v", summary.InfraFailureReasons)
	}
	if reasons[InfraFailureReasonInstanceTimeout] != 1 {
		t.Fatalf("missing instance_timeout reason: %+v", summary.InfraFailureReasons)
	}
	if reasons[InfraFailureReasonInvalidFinalState] != 1 {
		t.Fatalf("missing invalid_final_state reason: %+v", summary.InfraFailureReasons)
	}
	if reasons[InfraFailureReasonUnknownFailure] != 1 {
		t.Fatalf("missing unknown_failure reason: %+v", summary.InfraFailureReasons)
	}
}

func TestBuildSummarizesUniformInstalledVersion(t *testing.T) {
	run := store.Run{RunID: "run_uniform"}
	instances := []store.Instance{
		{InstanceID: "inst_1", Ordinal: 0, Case: runbundle.Case{CaseID: "case_1"}, State: domain.InstanceStateSucceeded},
		{InstanceID: "inst_2", Ordinal: 1, Case: runbundle.Case{CaseID: "case_2"}, State: domain.InstanceStateTestFailed},
	}
	results := []store.StoredInstanceResult{
		{InstanceID: "inst_1", FinalState: domain.InstanceStateSucceeded, InstalledVersion: "2.3.4"},
		{InstanceID: "inst_2", FinalState: domain.InstanceStateTestFailed, InstalledVersion: "2.3.4"},
	}

	summary := Build(run, instances, results)

	if summary.InstalledVersion != "2.3.4" {
		t.Fatalf("installed version = %q, want 2.3.4", summary.InstalledVersion)
	}
}

func TestBuildSummarizesMultipleInstalledVersions(t *testing.T) {
	run := store.Run{RunID: "run_multiple"}
	instances := []store.Instance{
		{InstanceID: "inst_1", Ordinal: 0, Case: runbundle.Case{CaseID: "case_1"}, State: domain.InstanceStateSucceeded},
		{InstanceID: "inst_2", Ordinal: 1, Case: runbundle.Case{CaseID: "case_2"}, State: domain.InstanceStateSucceeded},
	}
	results := []store.StoredInstanceResult{
		{InstanceID: "inst_1", FinalState: domain.InstanceStateSucceeded, InstalledVersion: "1.0.0"},
		{InstanceID: "inst_2", FinalState: domain.InstanceStateSucceeded, InstalledVersion: "2.0.0"},
	}

	summary := Build(run, instances, results)

	if summary.InstalledVersion != "multiple" {
		t.Fatalf("installed version = %q, want multiple", summary.InstalledVersion)
	}
}

func metrics(inputTokens, outputTokens, toolCalls int64) *usage.Metrics {
	return &usage.Metrics{
		InputTokens:  int64Ptr(inputTokens),
		OutputTokens: int64Ptr(outputTokens),
		ToolCalls:    int64Ptr(toolCalls),
	}
}

func intPtr(v int) *int {
	return &v
}

func int64Ptr(v int64) *int64 {
	return &v
}

func timePtr(v time.Time) *time.Time {
	return &v
}
