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
			},
			Agent:       testfixture.MinimalAgent(),
			RunDefaults: runbundle.RunDefault{Env: map[string]string{}, PTY: runbundle.PTY{Cols: 120, Rows: 40}},
			Cases: []runbundle.Case{
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
			},
		},
	}
}

func TestBuildPlanResumeModeCarriesAllTerminalCases(t *testing.T) {
	bundle := testBundle()
	snap := Snapshot{
		RunID:      "run_src",
		BundleHash: "hash_1",
		CaseIDs:    []string{"case-1", "case-2"},
		Completed: map[string]CompletedCase{
			"case-1": {
				CaseID:           "case-1",
				SourceRunID:      "run_src",
				SourceInstanceID: "run_src-inst-0001",
				Result: store.StoredInstanceResult{
					FinalState: domain.InstanceStateSucceeded,
				},
			},
			"case-2": {
				CaseID:           "case-2",
				SourceRunID:      "run_src",
				SourceInstanceID: "run_src-inst-0002",
				Result: store.StoredInstanceResult{
					FinalState: domain.InstanceStateTestFailed,
				},
			},
		},
	}
	plan, err := BuildPlan(bundle, "hash_1", snap, DefaultMode())
	if err != nil {
		t.Fatalf("build plan: %v", err)
	}
	if plan.OriginRunID != "run_src" {
		t.Fatalf("unexpected origin run id: %s", plan.OriginRunID)
	}
	if len(plan.CarryByCase) != 2 {
		t.Fatalf("expected 2 carried cases, got %d", len(plan.CarryByCase))
	}
	if _, ok := plan.CarryByCase["case-1"]; !ok {
		t.Fatalf("expected case-1 to be carried")
	}
	if _, ok := plan.CarryByCase["case-2"]; !ok {
		t.Fatalf("expected case-2 to be carried")
	}
}

func TestBuildPlanRetryFailedCarriesSucceededOnly(t *testing.T) {
	bundle := testBundle()
	snap := Snapshot{
		RunID:      "run_src",
		BundleHash: "hash_1",
		CaseIDs:    []string{"case-1", "case-2"},
		Completed: map[string]CompletedCase{
			"case-1": {
				CaseID:           "case-1",
				SourceRunID:      "run_src",
				SourceInstanceID: "run_src-inst-0001",
				Result: store.StoredInstanceResult{
					FinalState: domain.InstanceStateSucceeded,
				},
			},
			"case-2": {
				CaseID:           "case-2",
				SourceRunID:      "run_src",
				SourceInstanceID: "run_src-inst-0002",
				Result: store.StoredInstanceResult{
					FinalState: domain.InstanceStateTestFailed,
				},
			},
		},
	}
	plan, err := BuildPlan(bundle, "hash_1", snap, ModeRetryFailed)
	if err != nil {
		t.Fatalf("build plan: %v", err)
	}
	if len(plan.CarryByCase) != 1 {
		t.Fatalf("expected 1 carried case, got %d", len(plan.CarryByCase))
	}
	if _, ok := plan.CarryByCase["case-1"]; !ok {
		t.Fatalf("expected case-1 to be carried")
	}
	if _, ok := plan.CarryByCase["case-2"]; ok {
		t.Fatalf("expected case-2 to be rerun")
	}
}

func TestBuildPlanRejectsInvalidMode(t *testing.T) {
	_, err := BuildPlan(testBundle(), "hash_1", Snapshot{
		RunID:      "run_src",
		BundleHash: "hash_1",
		CaseIDs:    []string{"case-1", "case-2"},
	}, Mode(""))
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestBuildPlanRejectsHashMismatch(t *testing.T) {
	_, err := BuildPlan(testBundle(), "hash_new", Snapshot{
		RunID:      "run_src",
		BundleHash: "hash_old",
		CaseIDs:    []string{"case-1", "case-2"},
	}, DefaultMode())
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestBuildPlanRejectsCaseMismatch(t *testing.T) {
	_, err := BuildPlan(testBundle(), "hash_1", Snapshot{
		RunID:      "run_src",
		BundleHash: "hash_1",
		CaseIDs:    []string{"case-1"},
	}, DefaultMode())
	if err == nil {
		t.Fatalf("expected error")
	}
}
