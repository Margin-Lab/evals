package store

import (
	"context"
	"errors"
	"time"

	"github.com/marginlab/margin-eval/runner/runner-core/domain"
	"github.com/marginlab/margin-eval/runner/runner-core/runbundle"
	"github.com/marginlab/margin-eval/runner/runner-core/usage"
)

var (
	ErrNotFound  = errors.New("not found")
	ErrLeaseLost = errors.New("lease lost")
)

type Run struct {
	RunID           string               `json:"run_id"`
	ProjectID       string               `json:"project_id"`
	CreatedByUser   string               `json:"created_by_user_id"`
	Name            string               `json:"name"`
	State           domain.RunState      `json:"state"`
	SourceKind      runbundle.SourceKind `json:"source_kind"`
	BundleHash      string               `json:"run_bundle_hash"`
	Bundle          runbundle.Bundle     `json:"run_bundle,omitempty"`
	CancelRequested bool                 `json:"-"`
	CreatedAt       time.Time            `json:"created_at"`
	StartedAt       *time.Time           `json:"started_at,omitempty"`
	EndedAt         *time.Time           `json:"ended_at,omitempty"`
	Counts          domain.RunCounts     `json:"counts"`
}

type Instance struct {
	InstanceID string               `json:"instance_id"`
	RunID      string               `json:"run_id"`
	Ordinal    int                  `json:"ordinal"`
	Case       runbundle.Case       `json:"case"`
	State      domain.InstanceState `json:"state"`
	CreatedAt  time.Time            `json:"created_at"`
	UpdatedAt  time.Time            `json:"updated_at"`
}

type Attempt struct {
	AttemptID       string     `json:"attempt_id"`
	InstanceID      string     `json:"instance_id"`
	WorkerID        string     `json:"worker_id"`
	LeaseToken      string     `json:"lease_token"`
	CreatedAt       time.Time  `json:"created_at"`
	LastHeartbeatAt time.Time  `json:"last_heartbeat_at"`
	LeaseExpiresAt  time.Time  `json:"lease_expires_at"`
	EndedAt         *time.Time `json:"ended_at,omitempty"`
}

type RunEvent struct {
	EventID   int64            `json:"event_id"`
	RunID     string           `json:"run_id"`
	Source    string           `json:"source"`
	FromState *domain.RunState `json:"from_state,omitempty"`
	ToState   domain.RunState  `json:"to_state"`
	Details   map[string]any   `json:"details,omitempty"`
	CreatedAt time.Time        `json:"created_at"`
}

type InstanceEvent struct {
	EventID    int64                 `json:"event_id"`
	InstanceID string                `json:"instance_id"`
	AttemptID  string                `json:"attempt_id,omitempty"`
	Source     string                `json:"source"`
	FromState  *domain.InstanceState `json:"from_state,omitempty"`
	ToState    domain.InstanceState  `json:"to_state"`
	Details    map[string]any        `json:"details,omitempty"`
	CreatedAt  time.Time             `json:"created_at"`
}

type Artifact struct {
	ArtifactID  string         `json:"artifact_id"`
	RunID       string         `json:"run_id"`
	InstanceID  string         `json:"instance_id"`
	AttemptID   string         `json:"attempt_id"`
	Role        string         `json:"role"`
	Ordinal     int            `json:"ordinal"`
	StoreKey    string         `json:"store_key"`
	URI         string         `json:"uri"`
	ContentType string         `json:"content_type"`
	ByteSize    int64          `json:"byte_size"`
	SHA256      string         `json:"sha256"`
	Metadata    map[string]any `json:"metadata,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
}

type InstanceResult struct {
	FinalState      domain.InstanceState `json:"final_state"`
	AgentRunID      string               `json:"agent_run_id,omitempty"`
	AgentExitCode   *int                 `json:"agent_exit_code,omitempty"`
	Trajectory      string               `json:"trajectory_ref,omitempty"`
	Usage           *usage.Metrics       `json:"usage,omitempty"`
	OracleExitCode  *int                 `json:"oracle_exit_code,omitempty"`
	OracleStdoutRef string               `json:"oracle_stdout_ref,omitempty"`
	OracleStderrRef string               `json:"oracle_stderr_ref,omitempty"`
	TestExitCode    *int                 `json:"test_exit_code,omitempty"`
	TestStdoutRef   string               `json:"test_stdout_ref,omitempty"`
	TestStderrRef   string               `json:"test_stderr_ref,omitempty"`
	ErrorCode       string               `json:"error_code,omitempty"`
	ErrorMessage    string               `json:"error_message,omitempty"`
	ErrorDetails    map[string]any       `json:"error_details,omitempty"`
	ProvisionedAt   *time.Time           `json:"provisioned_at,omitempty"`
	AgentStartedAt  *time.Time           `json:"agent_started_at,omitempty"`
	AgentEndedAt    *time.Time           `json:"agent_ended_at,omitempty"`
	OracleStartedAt *time.Time           `json:"oracle_started_at,omitempty"`
	OracleEndedAt   *time.Time           `json:"oracle_ended_at,omitempty"`
	TestStartedAt   *time.Time           `json:"test_started_at,omitempty"`
	TestEndedAt     *time.Time           `json:"test_ended_at,omitempty"`
}

type StoredInstanceResult struct {
	InstanceID      string               `json:"instance_id"`
	AttemptID       string               `json:"attempt_id"`
	FinalState      domain.InstanceState `json:"final_state"`
	ProviderRef     string               `json:"provider_ref,omitempty"`
	AgentRunID      string               `json:"agent_run_id,omitempty"`
	AgentExitCode   *int                 `json:"agent_exit_code,omitempty"`
	TrajectoryRef   string               `json:"trajectory_ref,omitempty"`
	Usage           *usage.Metrics       `json:"usage,omitempty"`
	OracleExitCode  *int                 `json:"oracle_exit_code,omitempty"`
	OracleStdoutRef string               `json:"oracle_stdout_ref,omitempty"`
	OracleStderrRef string               `json:"oracle_stderr_ref,omitempty"`
	TestExitCode    *int                 `json:"test_exit_code,omitempty"`
	TestStdoutRef   string               `json:"test_stdout_ref,omitempty"`
	TestStderrRef   string               `json:"test_stderr_ref,omitempty"`
	ErrorCode       string               `json:"error_code,omitempty"`
	ErrorMessage    string               `json:"error_message,omitempty"`
	ErrorDetails    map[string]any       `json:"error_details,omitempty"`
	ProvisionedAt   *time.Time           `json:"provisioned_at,omitempty"`
	AgentStartedAt  *time.Time           `json:"agent_started_at,omitempty"`
	AgentEndedAt    *time.Time           `json:"agent_ended_at,omitempty"`
	OracleStartedAt *time.Time           `json:"oracle_started_at,omitempty"`
	OracleEndedAt   *time.Time           `json:"oracle_ended_at,omitempty"`
	TestStartedAt   *time.Time           `json:"test_started_at,omitempty"`
	TestEndedAt     *time.Time           `json:"test_ended_at,omitempty"`
	CreatedAt       time.Time            `json:"created_at"`
}

type ClaimedWork struct {
	AttemptID  string
	LeaseToken string
	Run        Run
	Instance   Instance
}

type FinalizeInput struct {
	AttemptID   string
	RunID       string
	InstanceID  string
	ProviderRef string
	Artifacts   []Artifact
	Result      InstanceResult
}

type RequeueInfraFailureInput struct {
	AttemptID     string
	RunID         string
	InstanceID    string
	ProviderRef   string
	Artifacts     []Artifact
	Result        InstanceResult
	MaxRetryCount int
}

type CarryForwardInput struct {
	RunID            string
	InstanceID       string
	SourceRunID      string
	SourceInstanceID string
	ProviderRef      string
	Artifacts        []Artifact
	Result           InstanceResult
}

type CreateRunInput struct {
	RunID         string
	ProjectID     string
	CreatedByUser string
	Name          string
	SourceKind    runbundle.SourceKind
	Bundle        runbundle.Bundle
	BundleHash    string
	At            time.Time
}

type ListRunsFilter struct {
	State           *domain.RunState
	SourceKind      *runbundle.SourceKind
	CreatedByUserID *string
}

type RunStore interface {
	CreateRun(ctx context.Context, in CreateRunInput) (Run, error)
	RerunExact(ctx context.Context, sourceRunID, newRunID, createdByUser, newName string, at time.Time) (Run, error)
	ListRuns(ctx context.Context, projectID string, filter ListRunsFilter) ([]Run, error)
	GetRun(ctx context.Context, runID string, includeBundle bool) (Run, error)
	CancelRun(ctx context.Context, runID, reasonCode, reasonMessage string, at time.Time) (Run, error)

	ListInstances(ctx context.Context, runID string, state *domain.InstanceState) ([]Instance, error)
	GetInstance(ctx context.Context, instanceID string) (Instance, error)
	ListInstanceAttempts(ctx context.Context, instanceID string) ([]Attempt, error)
	ListRunEvents(ctx context.Context, runID string) ([]RunEvent, error)
	ListInstanceEvents(ctx context.Context, instanceID string) ([]InstanceEvent, error)
	GetInstanceResult(ctx context.Context, instanceID string) (StoredInstanceResult, error)
	ListInstanceResults(ctx context.Context, runID string) ([]StoredInstanceResult, error)
	ListArtifacts(ctx context.Context, instanceID string) ([]Artifact, error)
	GetArtifact(ctx context.Context, artifactID string) (Artifact, error)

	ClaimPendingInstance(ctx context.Context, workerID string, leaseDuration time.Duration, at time.Time) (ClaimedWork, bool, error)
	UpdateInstanceState(ctx context.Context, runID, instanceID, attemptID string, state domain.InstanceState, at time.Time) error
	UpdateInstanceImage(ctx context.Context, runID, instanceID, attemptID, image string, at time.Time) error
	HeartbeatAttempt(ctx context.Context, instanceID, attemptID, leaseToken, workerID string, leaseDuration time.Duration, at time.Time) error
	FinalizeAttempt(ctx context.Context, in FinalizeInput, at time.Time) error
	RequeueInfraFailure(ctx context.Context, in RequeueInfraFailureInput, at time.Time) (bool, error)
	CarryForwardInstance(ctx context.Context, in CarryForwardInput, at time.Time) error
	RunCancelRequested(ctx context.Context, runID string) (bool, error)
	SweepCancelingRuns(ctx context.Context, at time.Time) (int, error)
	ReapExpiredLeases(ctx context.Context, at time.Time, limit int) (int, error)
}
