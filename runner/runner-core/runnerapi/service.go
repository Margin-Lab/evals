package runnerapi

import (
	"context"
	"time"

	"github.com/marginlab/margin-eval/runner/runner-core/resume"
	"github.com/marginlab/margin-eval/runner/runner-core/runbundle"
	"github.com/marginlab/margin-eval/runner/runner-core/store"
)

type ResumeMode = resume.Mode

const (
	ResumeModeResume      ResumeMode = resume.ModeResume
	ResumeModeRetryFailed ResumeMode = resume.ModeRetryFailed
)

func DefaultResumeMode() ResumeMode {
	return resume.DefaultMode()
}

// SubmitInput is the shared runner submission payload across runner implementations.
type SubmitInput struct {
	RunID           string
	ProjectID       string
	CreatedByUser   string
	Name            string
	Bundle          runbundle.Bundle
	ResumeFromRunID string
	ResumeMode      ResumeMode
}

// Service is the shared runner service contract implemented by runner backends.
type Service interface {
	Start(ctx context.Context)
	SubmitRun(ctx context.Context, in SubmitInput) (store.Run, error)
	WaitForTerminalRun(ctx context.Context, runID string, pollInterval time.Duration) (store.Run, error)
	GetRunSnapshot(ctx context.Context, runID string, opts SnapshotOptions) (RunSnapshot, error)
	GetInstanceSnapshot(ctx context.Context, instanceID string, opts SnapshotOptions) (InstanceSnapshot, error)
}
