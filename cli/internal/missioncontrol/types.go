package missioncontrol

import (
	"context"
	"time"

	"github.com/marginlab/margin-eval/runner/runner-core/runnerapi"
	"github.com/marginlab/margin-eval/runner/runner-core/store"
)

const (
	// DefaultTextPreviewLimit bounds text previews loaded for logs/artifacts.
	DefaultTextPreviewLimit int64 = 256 * 1024
)

// Source is the backend-agnostic read surface consumed by the mission-control TUI.
type Source interface {
	GetRunSnapshot(ctx context.Context, runID string) (runnerapi.RunSnapshot, error)
	ReadArtifactText(ctx context.Context, artifact store.Artifact, maxBytes int64) (ArtifactText, error)
}

// ArtifactText is a bounded text payload for TUI rendering.
type ArtifactText struct {
	Text      string
	Truncated bool
}

// Config defines runtime wiring for the mission-control TUI.
type Config struct {
	RunID            string
	Source           Source
	PollInterval     time.Duration
	TextPreviewLimit int64
}

// Outcome captures how the mission-control TUI session ended.
type Outcome struct {
	FinalRun store.Run
	Aborted  bool
}
