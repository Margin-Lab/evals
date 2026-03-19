package datasource

import (
	"context"
	"fmt"
	"time"

	"github.com/marginlab/margin-eval/runner/runner-core/runnerapi"
	"github.com/marginlab/margin-eval/runner/runner-core/store"
)

// Source provides a polling-friendly read/write surface for run execution data.
type Source interface {
	SubmitRun(ctx context.Context, in runnerapi.SubmitInput) (store.Run, error)
	WaitForTerminalRun(ctx context.Context, runID string, pollInterval time.Duration) (store.Run, error)
	GetRunSnapshot(ctx context.Context, runID string, opts runnerapi.SnapshotOptions) (runnerapi.RunSnapshot, error)
	GetInstanceSnapshot(ctx context.Context, instanceID string, opts runnerapi.SnapshotOptions) (runnerapi.InstanceSnapshot, error)
}

type runnerServiceSource struct {
	service runnerapi.Service
}

func NewRunnerServiceSource(service runnerapi.Service) (Source, error) {
	if service == nil {
		return nil, fmt.Errorf("runner service is required")
	}
	return &runnerServiceSource{service: service}, nil
}

func (s *runnerServiceSource) SubmitRun(ctx context.Context, in runnerapi.SubmitInput) (store.Run, error) {
	return s.service.SubmitRun(ctx, in)
}

func (s *runnerServiceSource) WaitForTerminalRun(ctx context.Context, runID string, pollInterval time.Duration) (store.Run, error) {
	return s.service.WaitForTerminalRun(ctx, runID, pollInterval)
}

func (s *runnerServiceSource) GetRunSnapshot(ctx context.Context, runID string, opts runnerapi.SnapshotOptions) (runnerapi.RunSnapshot, error) {
	return s.service.GetRunSnapshot(ctx, runID, opts)
}

func (s *runnerServiceSource) GetInstanceSnapshot(ctx context.Context, instanceID string, opts runnerapi.SnapshotOptions) (runnerapi.InstanceSnapshot, error) {
	return s.service.GetInstanceSnapshot(ctx, instanceID, opts)
}
