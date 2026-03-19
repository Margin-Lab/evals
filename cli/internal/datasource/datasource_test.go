package datasource

import (
	"context"
	"testing"
	"time"

	"github.com/marginlab/margin-eval/runner/runner-core/domain"
	"github.com/marginlab/margin-eval/runner/runner-core/runnerapi"
	"github.com/marginlab/margin-eval/runner/runner-core/store"
)

type fakeRunnerService struct {
	submitted bool
	waited    bool
}

func (f *fakeRunnerService) Start(_ context.Context) {}

func (f *fakeRunnerService) SubmitRun(_ context.Context, _ runnerapi.SubmitInput) (store.Run, error) {
	f.submitted = true
	return store.Run{RunID: "run_1"}, nil
}

func (f *fakeRunnerService) WaitForTerminalRun(_ context.Context, runID string, _ time.Duration) (store.Run, error) {
	f.waited = true
	return store.Run{RunID: runID, State: domain.RunStateCompleted}, nil
}

func (f *fakeRunnerService) GetRunSnapshot(_ context.Context, runID string, _ runnerapi.SnapshotOptions) (runnerapi.RunSnapshot, error) {
	return runnerapi.RunSnapshot{
		Run: store.Run{RunID: runID},
	}, nil
}

func (f *fakeRunnerService) GetInstanceSnapshot(_ context.Context, instanceID string, _ runnerapi.SnapshotOptions) (runnerapi.InstanceSnapshot, error) {
	return runnerapi.InstanceSnapshot{
		Instance: store.Instance{InstanceID: instanceID},
	}, nil
}

func TestNewRunnerServiceSource(t *testing.T) {
	if _, err := NewRunnerServiceSource(nil); err == nil {
		t.Fatalf("expected error for nil runner service")
	}

	fake := &fakeRunnerService{}
	src, err := NewRunnerServiceSource(fake)
	if err != nil {
		t.Fatalf("new source: %v", err)
	}

	run, err := src.SubmitRun(context.Background(), runnerapi.SubmitInput{})
	if err != nil {
		t.Fatalf("submit run: %v", err)
	}
	if run.RunID != "run_1" || !fake.submitted {
		t.Fatalf("unexpected submit behavior: run=%+v submitted=%v", run, fake.submitted)
	}

	finalRun, err := src.WaitForTerminalRun(context.Background(), run.RunID, 100*time.Millisecond)
	if err != nil {
		t.Fatalf("wait run: %v", err)
	}
	if finalRun.State != domain.RunStateCompleted || !fake.waited {
		t.Fatalf("unexpected wait behavior: run=%+v waited=%v", finalRun, fake.waited)
	}

	runSnapshot, err := src.GetRunSnapshot(context.Background(), run.RunID, runnerapi.SnapshotOptions{})
	if err != nil {
		t.Fatalf("get run snapshot: %v", err)
	}
	if runSnapshot.Run.RunID != run.RunID {
		t.Fatalf("unexpected run snapshot: %+v", runSnapshot.Run)
	}

	instanceSnapshot, err := src.GetInstanceSnapshot(context.Background(), "inst_1", runnerapi.SnapshotOptions{})
	if err != nil {
		t.Fatalf("get instance snapshot: %v", err)
	}
	if instanceSnapshot.Instance.InstanceID != "inst_1" {
		t.Fatalf("unexpected instance snapshot: %+v", instanceSnapshot.Instance)
	}
}
