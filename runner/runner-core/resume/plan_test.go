package resume

import (
	"testing"
	"time"

	"github.com/marginlab/margin-eval/runner/runner-core/domain"
	"github.com/marginlab/margin-eval/runner/runner-core/runbundle"
	"github.com/marginlab/margin-eval/runner/runner-core/store"
	"github.com/marginlab/margin-eval/runner/runner-core/testfixture"
)

func testBundle() runbundle.Bundle {
	cases := []runbundle.Case{
		{
			CaseID:            "case-1",
			Image:             "img-1",
			InitialPrompt:     "one",
			AgentCwd:          "/workspace",
			TestCommand:       []string{"true"},
			TestCwd:           "/work",
			TestTimeoutSecond: 30,
			TestAssets:        testfixture.MinimalTestAssets(),
		},
		{
			CaseID:            "case-2",
			Image:             "img-2",
			InitialPrompt:     "two",
			AgentCwd:          "/workspace",
			TestCommand:       []string{"true"},
			TestCwd:           "/work",
			TestTimeoutSecond: 30,
			TestAssets:        testfixture.MinimalTestAssets(),
		},
	}
	return runbundle.Bundle{
		SchemaVersion: runbundle.SchemaVersionV1,
		BundleID:      "bun_1",
		CreatedAt:     time.Date(2026, 2, 27, 0, 0, 0, 0, time.UTC),
		Source:        runbundle.Source{Kind: runbundle.SourceKindLocalFiles},
		ResolvedSnapshot: runbundle.ResolvedSnapshot{
			Name: "smoke",
			Execution: runbundle.Execution{Mode: runbundle.ExecutionModeFull,
				MaxConcurrency:        1,
				FailFast:              false,
				InstanceTimeoutSecond: 120,
				SamplesPerCase:        1,
			},
			Agent:       testfixture.MinimalAgent(),
			RunDefaults: runbundle.RunDefault{Env: map[string]string{}, PTY: runbundle.PTY{Cols: 120, Rows: 40}},
			Cases:       cases,
			Instances:   runbundle.BuildInstanceSpecs(cases, 1),
		},
	}
}

func TestBuildPlanResumeModeCarriesAllTerminalCases(t *testing.T) {
	bundle := testBundle()
	snap := Snapshot{
		RunID:        "run_src",
		BundleHash:   "hash_1",
		InstanceKeys: []string{"case-1#1", "case-2#1"},
		Completed: map[string]CompletedInstance{
			"case-1#1": {
				CaseID:           "case-1",
				SourceRunID:      "run_src",
				SourceInstanceID: "run_src-inst-0001",
				Result: store.StoredInstanceResult{
					FinalState: domain.InstanceStateSucceeded,
				},
			},
			"case-2#1": {
				CaseID:           "case-2",
				SourceRunID:      "run_src",
				SourceInstanceID: "run_src-inst-0002",
				Result: store.StoredInstanceResult{
					FinalState: domain.InstanceStateTestFailed,
				},
			},
		},
	}
	plan, err := BuildPlan(bundle, "hash_1", snap, DefaultMode(), BundlePolicyExact)
	if err != nil {
		t.Fatalf("build plan: %v", err)
	}
	if plan.OriginRunID != "run_src" {
		t.Fatalf("unexpected origin run id: %s", plan.OriginRunID)
	}
	if len(plan.CarryByInstance) != 2 {
		t.Fatalf("expected 2 carried cases, got %d", len(plan.CarryByInstance))
	}
	if _, ok := plan.CarryByInstance["case-1#1"]; !ok {
		t.Fatalf("expected case-1 to be carried")
	}
	if _, ok := plan.CarryByInstance["case-2#1"]; !ok {
		t.Fatalf("expected case-2 to be carried")
	}
	if plan.HasBundleMismatch() {
		t.Fatalf("expected exact matching plan to have no mismatch")
	}
}

func TestBuildPlanRetryFailedCarriesSucceededOnly(t *testing.T) {
	bundle := testBundle()
	snap := Snapshot{
		RunID:        "run_src",
		BundleHash:   "hash_1",
		InstanceKeys: []string{"case-1#1", "case-2#1"},
		Completed: map[string]CompletedInstance{
			"case-1#1": {
				CaseID:           "case-1",
				SourceRunID:      "run_src",
				SourceInstanceID: "run_src-inst-0001",
				Result: store.StoredInstanceResult{
					FinalState: domain.InstanceStateSucceeded,
				},
			},
			"case-2#1": {
				CaseID:           "case-2",
				SourceRunID:      "run_src",
				SourceInstanceID: "run_src-inst-0002",
				Result: store.StoredInstanceResult{
					FinalState: domain.InstanceStateTestFailed,
				},
			},
		},
	}
	plan, err := BuildPlan(bundle, "hash_1", snap, ModeRetryFailed, BundlePolicyExact)
	if err != nil {
		t.Fatalf("build plan: %v", err)
	}
	if len(plan.CarryByInstance) != 1 {
		t.Fatalf("expected 1 carried case, got %d", len(plan.CarryByInstance))
	}
	if _, ok := plan.CarryByInstance["case-1#1"]; !ok {
		t.Fatalf("expected case-1 to be carried")
	}
	if _, ok := plan.CarryByInstance["case-2#1"]; ok {
		t.Fatalf("expected case-2 to be rerun")
	}
}

func TestBuildPlanCarriesIndividualSamples(t *testing.T) {
	bundle := testBundle()
	bundle.ResolvedSnapshot.Execution.SamplesPerCase = 2
	bundle.ResolvedSnapshot.Instances = runbundle.BuildInstanceSpecs(bundle.ResolvedSnapshot.Cases, 2)
	snap := Snapshot{
		RunID:        "run_src",
		BundleHash:   "hash_1",
		InstanceKeys: []string{"case-1#1", "case-1#2", "case-2#1", "case-2#2"},
		Completed: map[string]CompletedInstance{
			"case-1#1": {
				InstanceKey:      "case-1#1",
				CaseID:           "case-1",
				SampleIndex:      1,
				SourceRunID:      "run_src",
				SourceInstanceID: "run_src-inst-0001",
				Result:           store.StoredInstanceResult{FinalState: domain.InstanceStateSucceeded},
			},
			"case-2#2": {
				InstanceKey:      "case-2#2",
				CaseID:           "case-2",
				SampleIndex:      2,
				SourceRunID:      "run_src",
				SourceInstanceID: "run_src-inst-0004",
				Result:           store.StoredInstanceResult{FinalState: domain.InstanceStateSucceeded},
			},
		},
	}

	plan, err := BuildPlan(bundle, "hash_1", snap, DefaultMode(), BundlePolicyExact)
	if err != nil {
		t.Fatalf("build plan: %v", err)
	}
	if len(plan.CarryByInstance) != 2 {
		t.Fatalf("expected 2 carried samples, got %d", len(plan.CarryByInstance))
	}
	if _, ok := plan.CarryByInstance["case-1#1"]; !ok {
		t.Fatalf("expected case-1#1 to be carried")
	}
	if _, ok := plan.CarryByInstance["case-1#2"]; ok {
		t.Fatalf("did not expect incomplete case-1#2 to be carried")
	}
	if len(plan.RerunInstances) != 2 {
		t.Fatalf("expected 2 rerun samples, got %v", plan.RerunInstances)
	}
}

func TestBuildPlanRejectsInvalidMode(t *testing.T) {
	_, err := BuildPlan(testBundle(), "hash_1", Snapshot{
		RunID:        "run_src",
		BundleHash:   "hash_1",
		InstanceKeys: []string{"case-1#1", "case-2#1"},
	}, Mode(""), BundlePolicyExact)
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestBuildPlanRejectsHashMismatch(t *testing.T) {
	_, err := BuildPlan(testBundle(), "hash_new", Snapshot{
		RunID:        "run_src",
		BundleHash:   "hash_old",
		InstanceKeys: []string{"case-1#1", "case-2#1"},
	}, DefaultMode(), BundlePolicyExact)
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestBuildPlanRejectsCaseMismatch(t *testing.T) {
	_, err := BuildPlan(testBundle(), "hash_1", Snapshot{
		RunID:        "run_src",
		BundleHash:   "hash_1",
		InstanceKeys: []string{"case-1#1"},
	}, DefaultMode(), BundlePolicyExact)
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestBuildPlanAllowMismatchCarriesOnlyIntersectingCases(t *testing.T) {
	bundle := testBundle()
	bundle.ResolvedSnapshot.Cases = append(bundle.ResolvedSnapshot.Cases, runbundle.Case{
		CaseID:            "case-3",
		Image:             "img-3",
		InitialPrompt:     "three",
		AgentCwd:          "/workspace",
		TestCommand:       []string{"true"},
		TestCwd:           "/work",
		TestTimeoutSecond: 30,
		TestAssets:        testfixture.MinimalTestAssets(),
	})
	bundle.ResolvedSnapshot.Instances = runbundle.BuildInstanceSpecs(bundle.ResolvedSnapshot.Cases, 1)
	snap := Snapshot{
		RunID:        "run_src",
		BundleHash:   "hash_old",
		InstanceKeys: []string{"case-1#1", "case-removed#1"},
		Completed: map[string]CompletedInstance{
			"case-1#1": {
				CaseID:           "case-1",
				SourceRunID:      "run_src",
				SourceInstanceID: "run_src-inst-0001",
				Result: store.StoredInstanceResult{
					FinalState: domain.InstanceStateSucceeded,
				},
			},
			"case-removed#1": {
				CaseID:           "case-removed",
				SourceRunID:      "run_src",
				SourceInstanceID: "run_src-inst-0009",
				Result: store.StoredInstanceResult{
					FinalState: domain.InstanceStateSucceeded,
				},
			},
		},
	}
	plan, err := BuildPlan(bundle, "hash_new", snap, DefaultMode(), BundlePolicyAllowMismatch)
	if err != nil {
		t.Fatalf("build plan: %v", err)
	}
	if !plan.HasBundleMismatch() {
		t.Fatalf("expected mismatch for changed bundle")
	}
	if !plan.BundleHashMatch == false {
	}
	if len(plan.CarryByInstance) != 1 {
		t.Fatalf("expected 1 carried case, got %d", len(plan.CarryByInstance))
	}
	if _, ok := plan.CarryByInstance["case-1#1"]; !ok {
		t.Fatalf("expected case-1 to be carried")
	}
	if len(plan.AddedInstances) != 2 {
		t.Fatalf("expected 2 added cases, got %v", plan.AddedInstances)
	}
	if len(plan.DroppedInstances) != 1 || plan.DroppedInstances[0] != "case-removed#1" {
		t.Fatalf("unexpected dropped cases: %v", plan.DroppedInstances)
	}
	if len(plan.RerunInstances) != 2 {
		t.Fatalf("expected 2 rerun cases, got %v", plan.RerunInstances)
	}
}

func TestBuildPlanAllowMismatchStillUsesResumePolicy(t *testing.T) {
	bundle := testBundle()
	snap := Snapshot{
		RunID:        "run_src",
		BundleHash:   "hash_old",
		InstanceKeys: []string{"case-1#1", "case-2#1"},
		Completed: map[string]CompletedInstance{
			"case-1#1": {
				CaseID:           "case-1",
				SourceRunID:      "run_src",
				SourceInstanceID: "run_src-inst-0001",
				Result: store.StoredInstanceResult{
					FinalState: domain.InstanceStateSucceeded,
				},
			},
			"case-2#1": {
				CaseID:           "case-2",
				SourceRunID:      "run_src",
				SourceInstanceID: "run_src-inst-0002",
				Result: store.StoredInstanceResult{
					FinalState: domain.InstanceStateTestFailed,
				},
			},
		},
	}
	plan, err := BuildPlan(bundle, "hash_new", snap, ModeRetryFailed, BundlePolicyAllowMismatch)
	if err != nil {
		t.Fatalf("build plan: %v", err)
	}
	if len(plan.CarryByInstance) != 1 {
		t.Fatalf("expected 1 carried case, got %d", len(plan.CarryByInstance))
	}
	if _, ok := plan.CarryByInstance["case-2#1"]; ok {
		t.Fatalf("expected test_failed case to rerun under retry-failed")
	}
	if len(plan.RerunInstances) != 1 || plan.RerunInstances[0] != "case-2#1" {
		t.Fatalf("unexpected rerun cases: %v", plan.RerunInstances)
	}
}

func TestBuildPlanRejectsInvalidBundlePolicy(t *testing.T) {
	_, err := BuildPlan(testBundle(), "hash_1", Snapshot{
		RunID:        "run_src",
		BundleHash:   "hash_1",
		InstanceKeys: []string{"case-1#1", "case-2#1"},
	}, DefaultMode(), BundlePolicy(""))
	if err == nil {
		t.Fatalf("expected error")
	}
}
