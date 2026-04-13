package plaincontrol

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/marginlab/margin-eval/runner/runner-core/domain"
	"github.com/marginlab/margin-eval/runner/runner-core/runbundle"
	"github.com/marginlab/margin-eval/runner/runner-core/runnerapi"
	"github.com/marginlab/margin-eval/runner/runner-core/store"
)

type fakeSource struct {
	snapshots []runnerapi.RunSnapshot
	err       error
	index     int
}

func (f *fakeSource) GetRunSnapshot(_ context.Context, _ string, _ runnerapi.SnapshotOptions) (runnerapi.RunSnapshot, error) {
	if f.err != nil {
		return runnerapi.RunSnapshot{}, f.err
	}
	if len(f.snapshots) == 0 {
		return runnerapi.RunSnapshot{}, errors.New("no snapshots")
	}
	if f.index >= len(f.snapshots) {
		return f.snapshots[len(f.snapshots)-1], nil
	}
	snapshot := f.snapshots[f.index]
	f.index++
	return snapshot, nil
}

func TestRunPrintsProgressFailuresAndSummary(t *testing.T) {
	start := time.Date(2026, 3, 12, 12, 0, 0, 0, time.UTC)
	end := start.Add(75 * time.Second)
	snapshots := []runnerapi.RunSnapshot{
		{
			Run: store.Run{
				RunID:     "run_test_1",
				State:     domain.RunStateRunning,
				CreatedAt: start,
				StartedAt: &start,
				Counts: domain.RunCounts{
					Pending:   1,
					Running:   1,
					Succeeded: 0,
				},
			},
			Instances: []runnerapi.InstanceSnapshot{
				{
					Instance: store.Instance{
						InstanceID: "inst_1",
						Ordinal:    1,
						State:      domain.InstanceStateAgentRunning,
						Case:       runbundle.Case{CaseID: "case-1"},
					},
				},
				{
					Instance: store.Instance{
						InstanceID: "inst_2",
						Ordinal:    2,
						State:      domain.InstanceStatePending,
					},
				},
			},
		},
		{
			Run: store.Run{
				RunID:     "run_test_1",
				State:     domain.RunStateRunning,
				CreatedAt: start,
				StartedAt: &start,
				Counts: domain.RunCounts{
					Pending:    0,
					Running:    0,
					Succeeded:  1,
					TestFailed: 1,
				},
			},
			Instances: []runnerapi.InstanceSnapshot{
				{
					Instance: store.Instance{
						InstanceID: "inst_1",
						Ordinal:    1,
						State:      domain.InstanceStateSucceeded,
						Case:       runbundle.Case{CaseID: "case-1"},
					},
				},
				{
					Instance: store.Instance{
						InstanceID: "inst_2",
						Ordinal:    2,
						State:      domain.InstanceStateTestFailed,
					},
					Result: &store.StoredInstanceResult{
						TestExitCode: intPtr(1),
						ErrorMessage: "assertion failed in test.sh",
					},
				},
			},
		},
		{
			Run: store.Run{
				RunID:     "run_test_1",
				State:     domain.RunStateCompleted,
				CreatedAt: start,
				StartedAt: &start,
				EndedAt:   &end,
				Counts: domain.RunCounts{
					Succeeded:  1,
					TestFailed: 1,
				},
			},
			Instances: []runnerapi.InstanceSnapshot{
				{
					Instance: store.Instance{
						InstanceID: "inst_1",
						Ordinal:    1,
						State:      domain.InstanceStateSucceeded,
					},
				},
				{
					Instance: store.Instance{
						InstanceID: "inst_2",
						Ordinal:    2,
						State:      domain.InstanceStateTestFailed,
					},
					Result: &store.StoredInstanceResult{
						TestExitCode: intPtr(1),
						ErrorMessage: "assertion failed in test.sh",
					},
				},
			},
		},
	}

	var out bytes.Buffer
	_, err := Run(context.Background(), Config{
		RunID:             "run_test_1",
		RunDir:            "/tmp/run_test_1",
		Source:            &fakeSource{snapshots: snapshots},
		Out:               &out,
		PollInterval:      time.Millisecond,
		HeartbeatInterval: time.Hour,
		Now: func() time.Time {
			return end
		},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	text := out.String()
	for _, want := range []string{
		"[run] started run_id=run_test_1 total=2 run_dir=/tmp/run_test_1",
		"progress 0/2 | running 1 | pending 1 | pass 0 | test_fail 0 | infra_fail 0",
		"fail #002 (no-case-id) type=test_failed test_exit=1",
		"[1m15s] finished state=completed elapsed=1m15s",
		"[1m15s] summary total=2 pass=1 test_fail=1 infra_fail=0 canceled=0",
		"[1m15s] failures #002 (no-case-id)",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected output to contain %q, got:\n%s", want, text)
		}
	}
	if strings.Count(text, "fail #002") != 1 {
		t.Fatalf("expected one failure line, got:\n%s", text)
	}
}

func TestRunReturnsErrorWhenInitialSnapshotFails(t *testing.T) {
	_, err := Run(context.Background(), Config{
		RunID:  "run_test_1",
		Source: &fakeSource{err: errors.New("boom")},
	})
	if err == nil || !strings.Contains(err.Error(), "load run snapshot") {
		t.Fatalf("expected snapshot load error, got %v", err)
	}
}

func intPtr(v int) *int {
	return &v
}
