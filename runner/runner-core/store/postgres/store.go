package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/marginlab/margin-eval/runner/runner-core/domain"
	"github.com/marginlab/margin-eval/runner/runner-core/runbundle"
	"github.com/marginlab/margin-eval/runner/runner-core/store"
	"github.com/marginlab/margin-eval/runner/runner-core/usage"
)

type Config struct {
	DSN               string
	MaxConns          int32
	MinConns          int32
	MaxConnLifetime   time.Duration
	MaxConnIdleTime   time.Duration
	HealthCheckPeriod time.Duration
}

type Store struct {
	pool *pgxpool.Pool
}

var digestImagePattern = regexp.MustCompile(`^[^\s@]+@sha256:[0-9a-f]{64}$`)

type rowQueryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

type rowScanner interface {
	Scan(...any) error
}

func Open(ctx context.Context, cfg Config) (*Store, error) {
	if strings.TrimSpace(cfg.DSN) == "" {
		return nil, fmt.Errorf("postgres DSN is required")
	}
	pcfg, err := pgxpool.ParseConfig(cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("parse postgres DSN: %w", err)
	}
	if cfg.MaxConns > 0 {
		pcfg.MaxConns = cfg.MaxConns
	}
	if cfg.MinConns > 0 {
		pcfg.MinConns = cfg.MinConns
	}
	if cfg.MaxConnLifetime > 0 {
		pcfg.MaxConnLifetime = cfg.MaxConnLifetime
	}
	if cfg.MaxConnIdleTime > 0 {
		pcfg.MaxConnIdleTime = cfg.MaxConnIdleTime
	}
	if cfg.HealthCheckPeriod > 0 {
		pcfg.HealthCheckPeriod = cfg.HealthCheckPeriod
	}
	pool, err := pgxpool.NewWithConfig(ctx, pcfg)
	if err != nil {
		return nil, fmt.Errorf("create postgres pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	return &Store{pool: pool}, nil
}

func New(pool *pgxpool.Pool) (*Store, error) {
	if pool == nil {
		return nil, fmt.Errorf("postgres pool is required")
	}
	return &Store{pool: pool}, nil
}

func (s *Store) Close() {
	if s.pool != nil {
		s.pool.Close()
	}
}

func (s *Store) Ping(ctx context.Context) error {
	if s.pool == nil {
		return fmt.Errorf("postgres pool is nil")
	}
	return s.pool.Ping(ctx)
}

func (s *Store) CreateRun(ctx context.Context, in store.CreateRunInput) (store.Run, error) {
	if strings.TrimSpace(in.RunID) == "" {
		return store.Run{}, fmt.Errorf("run id is required")
	}
	if strings.TrimSpace(in.ProjectID) == "" {
		return store.Run{}, fmt.Errorf("project id is required")
	}
	if in.At.IsZero() {
		in.At = time.Now().UTC()
	}
	if err := runbundle.Validate(in.Bundle); err != nil {
		return store.Run{}, fmt.Errorf("validate run bundle: %w", err)
	}
	if strings.TrimSpace(in.BundleHash) == "" {
		h, err := runbundle.HashSHA256(in.Bundle)
		if err != nil {
			return store.Run{}, err
		}
		in.BundleHash = h
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return store.Run{}, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	run, err := s.createRunTx(ctx, tx, in)
	if err != nil {
		return store.Run{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return store.Run{}, fmt.Errorf("commit tx: %w", err)
	}
	return run, nil
}

func (s *Store) RerunExact(ctx context.Context, sourceRunID, newRunID, createdByUser, newName string, at time.Time) (store.Run, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return store.Run{}, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	source, err := s.getRunTx(ctx, tx, sourceRunID, true)
	if err != nil {
		return store.Run{}, err
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	bundle := runbundle.CloneForRerunExact(source.Bundle, "bun-"+newRunID, at, sourceRunID)
	hash, err := runbundle.HashSHA256(bundle)
	if err != nil {
		return store.Run{}, err
	}

	created, err := s.createRunTx(ctx, tx, store.CreateRunInput{
		RunID:         newRunID,
		ProjectID:     source.ProjectID,
		CreatedByUser: createdByUser,
		Name:          coalesce(newName, source.Name),
		SourceKind:    runbundle.SourceKindRunSnapshot,
		Bundle:        bundle,
		BundleHash:    hash,
		At:            at,
	})
	if err != nil {
		return store.Run{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return store.Run{}, fmt.Errorf("commit tx: %w", err)
	}
	return created, nil
}

func (s *Store) createRunTx(ctx context.Context, tx pgx.Tx, in store.CreateRunInput) (store.Run, error) {
	name := coalesce(in.Name, in.Bundle.ResolvedSnapshot.Name)
	sourceKind := in.SourceKind
	if strings.TrimSpace(string(sourceKind)) == "" {
		sourceKind = in.Bundle.Source.Kind
	}
	bundleJSON, err := json.Marshal(in.Bundle)
	if err != nil {
		return store.Run{}, fmt.Errorf("marshal run bundle: %w", err)
	}
	_, err = tx.Exec(ctx, `
INSERT INTO runs (
  run_id, project_id, created_by_user_id, name,
  state, source_kind, run_bundle_json, run_bundle_hash,
  created_at, updated_at
) VALUES (
  $1, $2, $3, $4,
  'queued', $5::run_source_kind_t, $6::jsonb, $7,
  $8, $8
)`, in.RunID, in.ProjectID, in.CreatedByUser, nullableName(name), string(sourceKind), bundleJSON, in.BundleHash, in.At)
	if err != nil {
		return store.Run{}, fmt.Errorf("insert run: %w", err)
	}

	for i, c := range in.Bundle.ResolvedSnapshot.Cases {
		instanceID := fmt.Sprintf("%s-inst-%04d", in.RunID, i+1)
		_, err = tx.Exec(ctx, `
INSERT INTO run_instances (
  instance_id, run_id, ordinal,
  case_id, image, initial_prompt, agent_cwd, test_command, test_cwd, test_timeout_seconds,
  state, created_at, updated_at
) VALUES (
  $1, $2, $3,
  $4, $5, $6, $7, $8, $9, $10,
  'pending', $11, $11
)`, instanceID, in.RunID, i, c.CaseID, c.Image, c.InitialPrompt, c.AgentCwd, c.TestCommand, c.TestCwd, c.TestTimeoutSecond, in.At)
		if err != nil {
			return store.Run{}, fmt.Errorf("insert run instance: %w", err)
		}
	}

	_, err = tx.Exec(ctx, `
INSERT INTO run_events (run_id, source, from_state, to_state, details, created_at)
VALUES ($1, 'api', NULL, 'queued', NULL, $2)
`, in.RunID, in.At)
	if err != nil {
		return store.Run{}, fmt.Errorf("insert run event: %w", err)
	}

	if err := s.refreshRunStateTx(ctx, tx, in.RunID, in.At); err != nil {
		return store.Run{}, err
	}
	return s.getRunTx(ctx, tx, in.RunID, false)
}

func (s *Store) ListRuns(ctx context.Context, projectID string, filter store.ListRunsFilter) ([]store.Run, error) {
	var args []any
	args = append(args, projectID)

	query := `
SELECT
  r.run_id,
  r.project_id,
  r.created_by_user_id,
  r.name,
  r.state::text,
  r.source_kind::text,
  r.run_bundle_hash,
  r.run_bundle_json,
  r.cancel_requested_at,
  r.started_at,
  r.ended_at,
  r.created_at,
  COALESCE(c.pending_count, 0) AS pending_count,
  COALESCE(c.running_count, 0) AS running_count,
  COALESCE(c.succeeded_count, 0) AS succeeded_count,
  COALESCE(c.test_failed_count, 0) AS test_failed_count,
  COALESCE(c.infra_failed_count, 0) AS infra_failed_count,
  COALESCE(c.canceled_count, 0) AS canceled_count
FROM runs r
LEFT JOIN run_instance_counts c ON c.run_id = r.run_id
WHERE r.project_id = $1`

	if filter.State != nil {
		args = append(args, string(*filter.State))
		query += fmt.Sprintf("\n  AND r.state = $%d::run_state_t", len(args))
	}
	if filter.SourceKind != nil {
		args = append(args, string(*filter.SourceKind))
		query += fmt.Sprintf("\n  AND r.source_kind = $%d::run_source_kind_t", len(args))
	}
	if filter.CreatedByUserID != nil {
		args = append(args, *filter.CreatedByUserID)
		query += fmt.Sprintf("\n  AND r.created_by_user_id = $%d", len(args))
	}
	query += "\nORDER BY r.created_at DESC"

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list runs query: %w", err)
	}
	defer rows.Close()

	out := make([]store.Run, 0)
	for rows.Next() {
		r, err := scanRun(rows, false)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if rows.Err() != nil {
		return nil, fmt.Errorf("list runs rows: %w", rows.Err())
	}
	return out, nil
}

func (s *Store) GetRun(ctx context.Context, runID string, includeBundle bool) (store.Run, error) {
	return s.getRunTx(ctx, s.pool, runID, includeBundle)
}

func (s *Store) getRunTx(ctx context.Context, q rowQueryer, runID string, includeBundle bool) (store.Run, error) {
	row := q.QueryRow(ctx, `
SELECT
  r.run_id,
  r.project_id,
  r.created_by_user_id,
  r.name,
  r.state::text,
  r.source_kind::text,
  r.run_bundle_hash,
  r.run_bundle_json,
  r.cancel_requested_at,
  r.started_at,
  r.ended_at,
  r.created_at,
  COALESCE(c.pending_count, 0) AS pending_count,
  COALESCE(c.running_count, 0) AS running_count,
  COALESCE(c.succeeded_count, 0) AS succeeded_count,
  COALESCE(c.test_failed_count, 0) AS test_failed_count,
  COALESCE(c.infra_failed_count, 0) AS infra_failed_count,
  COALESCE(c.canceled_count, 0) AS canceled_count
FROM runs r
LEFT JOIN run_instance_counts c ON c.run_id = r.run_id
WHERE r.run_id = $1
`, runID)
	return scanRun(row, includeBundle)
}

func scanRun(row interface{ Scan(...any) error }, includeBundle bool) (store.Run, error) {
	var runID string
	var projectID string
	var createdBy string
	var name *string
	var state string
	var sourceKind string
	var bundleHash string
	var bundleRaw []byte
	var cancelRequestedAt *time.Time
	var startedAt *time.Time
	var endedAt *time.Time
	var createdAt time.Time
	var pending, running, succeeded, testFailed, infraFailed, canceled int

	err := row.Scan(
		&runID,
		&projectID,
		&createdBy,
		&name,
		&state,
		&sourceKind,
		&bundleHash,
		&bundleRaw,
		&cancelRequestedAt,
		&startedAt,
		&endedAt,
		&createdAt,
		&pending,
		&running,
		&succeeded,
		&testFailed,
		&infraFailed,
		&canceled,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return store.Run{}, store.ErrNotFound
		}
		return store.Run{}, fmt.Errorf("scan run: %w", err)
	}

	r := store.Run{
		RunID:           runID,
		ProjectID:       projectID,
		CreatedByUser:   createdBy,
		Name:            derefString(name),
		State:           domain.RunState(state),
		SourceKind:      runbundle.SourceKind(sourceKind),
		BundleHash:      bundleHash,
		CreatedAt:       createdAt,
		StartedAt:       startedAt,
		EndedAt:         endedAt,
		CancelRequested: cancelRequestedAt != nil,
		Counts: domain.RunCounts{
			Pending:     pending,
			Running:     running,
			Succeeded:   succeeded,
			TestFailed:  testFailed,
			InfraFailed: infraFailed,
			Canceled:    canceled,
		},
	}
	if includeBundle {
		var bundle runbundle.Bundle
		if err := json.Unmarshal(bundleRaw, &bundle); err != nil {
			return store.Run{}, fmt.Errorf("decode run bundle: %w", err)
		}
		r.Bundle = bundle
	}
	return r, nil
}

func (s *Store) CancelRun(ctx context.Context, runID, reasonCode, reasonMessage string, at time.Time) (store.Run, error) {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return store.Run{}, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	var stateText string
	var cancelRequestedAt *time.Time
	err = tx.QueryRow(ctx, `
SELECT state::text, cancel_requested_at
FROM runs
WHERE run_id = $1
FOR UPDATE
`, runID).Scan(&stateText, &cancelRequestedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return store.Run{}, store.ErrNotFound
		}
		return store.Run{}, fmt.Errorf("lock run: %w", err)
	}

	_, err = tx.Exec(ctx, `
UPDATE runs
SET cancel_requested_at = COALESCE(cancel_requested_at, $2), updated_at = $2
WHERE run_id = $1
`, runID, at)
	if err != nil {
		return store.Run{}, fmt.Errorf("mark run cancel requested: %w", err)
	}

	counts, err := s.loadRunCountsTx(ctx, tx, runID)
	if err != nil {
		return store.Run{}, err
	}
	before := domain.RunState(stateText)
	after := domain.NextRunState(before, counts, true)
	if after != before {
		_, err = tx.Exec(ctx, `
UPDATE runs
SET
  state = $2::run_state_t,
  ended_at = CASE
    WHEN $2::run_state_t IN ('completed','failed','canceled') AND ended_at IS NULL THEN $3
    ELSE ended_at
  END,
  updated_at = $3
WHERE run_id = $1
`, runID, string(after), at)
		if err != nil {
			return store.Run{}, fmt.Errorf("update run state on cancel: %w", err)
		}
		details := map[string]any{}
		if strings.TrimSpace(reasonCode) != "" {
			details["reason_code"] = reasonCode
		}
		if strings.TrimSpace(reasonMessage) != "" {
			details["reason_message"] = reasonMessage
		}
		detailsRaw, err := marshalJSONMap(details)
		if err != nil {
			return store.Run{}, err
		}
		_, err = tx.Exec(ctx, `
INSERT INTO run_events (run_id, source, from_state, to_state, details, created_at)
VALUES ($1, 'api', $2::run_state_t, $3::run_state_t, $4::jsonb, $5)
`, runID, string(before), string(after), detailsRaw, at)
		if err != nil {
			return store.Run{}, fmt.Errorf("insert cancel run event: %w", err)
		}
	}

	if err := s.refreshRunStateTx(ctx, tx, runID, at); err != nil {
		return store.Run{}, err
	}

	run, err := s.getRunTx(ctx, tx, runID, false)
	if err != nil {
		return store.Run{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return store.Run{}, fmt.Errorf("commit tx: %w", err)
	}
	return run, nil
}

func (s *Store) ListInstances(ctx context.Context, runID string, state *domain.InstanceState) ([]store.Instance, error) {
	var exists bool
	err := s.pool.QueryRow(ctx, `SELECT true FROM runs WHERE run_id = $1`, runID).Scan(&exists)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, store.ErrNotFound
		}
		return nil, fmt.Errorf("check run exists: %w", err)
	}

	query := `
SELECT
  ri.instance_id,
  ri.run_id,
  ri.ordinal,
  ri.case_id,
  ri.image,
  ri.initial_prompt,
  ri.agent_cwd,
  ri.test_command,
  ri.test_cwd,
  ri.test_timeout_seconds,
  jsonb_extract_path(r.run_bundle_json, 'resolved_snapshot', 'cases', ri.ordinal::text, 'test_assets') AS test_assets,
  ri.state::text,
  ri.created_at,
  ri.updated_at
FROM run_instances ri
JOIN runs r ON r.run_id = ri.run_id
WHERE ri.run_id = $1`
	args := []any{runID}
	if state != nil {
		args = append(args, string(*state))
		query += fmt.Sprintf("\n  AND ri.state = $%d::instance_state_t", len(args))
	}
	query += "\nORDER BY ri.ordinal ASC"

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list run instances query: %w", err)
	}
	defer rows.Close()

	out := make([]store.Instance, 0)
	for rows.Next() {
		inst, err := scanInstance(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, inst)
	}
	if rows.Err() != nil {
		return nil, fmt.Errorf("list run instances rows: %w", rows.Err())
	}
	return out, nil
}

func (s *Store) GetInstance(ctx context.Context, instanceID string) (store.Instance, error) {
	row := s.pool.QueryRow(ctx, `
SELECT
  ri.instance_id,
  ri.run_id,
  ri.ordinal,
  ri.case_id,
  ri.image,
  ri.initial_prompt,
  ri.agent_cwd,
  ri.test_command,
  ri.test_cwd,
  ri.test_timeout_seconds,
  jsonb_extract_path(r.run_bundle_json, 'resolved_snapshot', 'cases', ri.ordinal::text, 'test_assets') AS test_assets,
  ri.state::text,
  ri.created_at,
  ri.updated_at
FROM run_instances ri
JOIN runs r ON r.run_id = ri.run_id
WHERE ri.instance_id = $1
`, instanceID)
	inst, err := scanInstance(row)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return store.Instance{}, err
		}
		return store.Instance{}, fmt.Errorf("get instance: %w", err)
	}
	return inst, nil
}

func scanInstance(row interface{ Scan(...any) error }) (store.Instance, error) {
	var instanceID string
	var runID string
	var ordinal int
	var caseID string
	var image string
	var initialPrompt string
	var agentCwd string
	var testCommand []string
	var testCwd string
	var testTimeoutSeconds int
	var testAssetsRaw []byte
	var stateText string
	var createdAt time.Time
	var updatedAt time.Time

	err := row.Scan(
		&instanceID,
		&runID,
		&ordinal,
		&caseID,
		&image,
		&initialPrompt,
		&agentCwd,
		&testCommand,
		&testCwd,
		&testTimeoutSeconds,
		&testAssetsRaw,
		&stateText,
		&createdAt,
		&updatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return store.Instance{}, store.ErrNotFound
		}
		return store.Instance{}, fmt.Errorf("scan instance: %w", err)
	}
	testAssets, err := decodeCaseTestAssets(testAssetsRaw)
	if err != nil {
		return store.Instance{}, fmt.Errorf("scan instance: %w", err)
	}
	return store.Instance{
		InstanceID: instanceID,
		RunID:      runID,
		Ordinal:    ordinal,
		Case: runbundle.Case{
			CaseID:            caseID,
			Image:             image,
			InitialPrompt:     initialPrompt,
			AgentCwd:          agentCwd,
			TestCommand:       testCommand,
			TestCwd:           testCwd,
			TestTimeoutSecond: testTimeoutSeconds,
			TestAssets:        testAssets,
		},
		State:     domain.InstanceState(stateText),
		CreatedAt: createdAt,
		UpdatedAt: updatedAt,
	}, nil
}

func decodeCaseTestAssets(raw []byte) (runbundle.TestAssets, error) {
	if len(raw) == 0 {
		return runbundle.TestAssets{}, fmt.Errorf("missing case test_assets in run bundle")
	}
	var out runbundle.TestAssets
	if err := json.Unmarshal(raw, &out); err != nil {
		return runbundle.TestAssets{}, fmt.Errorf("decode case test_assets: %w", err)
	}
	return out, nil
}

func (s *Store) ListInstanceAttempts(ctx context.Context, instanceID string) ([]store.Attempt, error) {
	rows, err := s.pool.Query(ctx, `
SELECT
  attempt_id::text,
  instance_id,
  worker_id,
  lease_token::text,
  created_at,
  last_heartbeat_at,
  lease_expires_at,
  ended_at
FROM instance_attempts
WHERE instance_id = $1
ORDER BY created_at DESC
`, instanceID)
	if err != nil {
		return nil, fmt.Errorf("list instance attempts query: %w", err)
	}
	defer rows.Close()

	out := make([]store.Attempt, 0)
	for rows.Next() {
		var attemptID string
		var iid string
		var workerID string
		var leaseToken string
		var createdAt time.Time
		var lastHeartbeatAt time.Time
		var leaseExpiresAt time.Time
		var endedAt *time.Time
		if err := rows.Scan(&attemptID, &iid, &workerID, &leaseToken, &createdAt, &lastHeartbeatAt, &leaseExpiresAt, &endedAt); err != nil {
			return nil, fmt.Errorf("scan instance attempt: %w", err)
		}
		out = append(out, store.Attempt{
			AttemptID:       attemptID,
			InstanceID:      iid,
			WorkerID:        workerID,
			LeaseToken:      leaseToken,
			CreatedAt:       createdAt,
			LastHeartbeatAt: lastHeartbeatAt,
			LeaseExpiresAt:  leaseExpiresAt,
			EndedAt:         endedAt,
		})
	}
	if rows.Err() != nil {
		return nil, fmt.Errorf("list instance attempts rows: %w", rows.Err())
	}
	return out, nil
}

func (s *Store) ListRunEvents(ctx context.Context, runID string) ([]store.RunEvent, error) {
	rows, err := s.pool.Query(ctx, `
SELECT
  event_id,
  run_id,
  source,
  from_state::text,
  to_state::text,
  details,
  created_at
FROM run_events
WHERE run_id = $1
ORDER BY created_at ASC, event_id ASC
`, runID)
	if err != nil {
		return nil, fmt.Errorf("list run events query: %w", err)
	}
	defer rows.Close()

	out := make([]store.RunEvent, 0)
	for rows.Next() {
		var eventID int64
		var rid string
		var source string
		var fromState *string
		var toState string
		var detailsRaw []byte
		var createdAt time.Time
		if err := rows.Scan(&eventID, &rid, &source, &fromState, &toState, &detailsRaw, &createdAt); err != nil {
			return nil, fmt.Errorf("scan run event: %w", err)
		}
		details, err := unmarshalJSONMap(detailsRaw)
		if err != nil {
			return nil, err
		}
		out = append(out, store.RunEvent{
			EventID:   eventID,
			RunID:     rid,
			Source:    source,
			FromState: toRunStatePtr(fromState),
			ToState:   domain.RunState(toState),
			Details:   details,
			CreatedAt: createdAt,
		})
	}
	if rows.Err() != nil {
		return nil, fmt.Errorf("list run events rows: %w", rows.Err())
	}
	return out, nil
}

func (s *Store) ListInstanceEvents(ctx context.Context, instanceID string) ([]store.InstanceEvent, error) {
	rows, err := s.pool.Query(ctx, `
SELECT
  event_id,
  instance_id,
  attempt_id::text,
  source,
  from_state::text,
  to_state::text,
  details,
  created_at
FROM instance_events
WHERE instance_id = $1
ORDER BY created_at ASC, event_id ASC
`, instanceID)
	if err != nil {
		return nil, fmt.Errorf("list instance events query: %w", err)
	}
	defer rows.Close()

	out := make([]store.InstanceEvent, 0)
	for rows.Next() {
		var eventID int64
		var iid string
		var attemptID *string
		var source string
		var fromState *string
		var toState string
		var detailsRaw []byte
		var createdAt time.Time
		if err := rows.Scan(&eventID, &iid, &attemptID, &source, &fromState, &toState, &detailsRaw, &createdAt); err != nil {
			return nil, fmt.Errorf("scan instance event: %w", err)
		}
		details, err := unmarshalJSONMap(detailsRaw)
		if err != nil {
			return nil, err
		}
		out = append(out, store.InstanceEvent{
			EventID:    eventID,
			InstanceID: iid,
			AttemptID:  derefString(attemptID),
			Source:     source,
			FromState:  toInstanceStatePtr(fromState),
			ToState:    domain.InstanceState(toState),
			Details:    details,
			CreatedAt:  createdAt,
		})
	}
	if rows.Err() != nil {
		return nil, fmt.Errorf("list instance events rows: %w", rows.Err())
	}
	return out, nil
}

func (s *Store) GetInstanceResult(ctx context.Context, instanceID string) (store.StoredInstanceResult, error) {
	row := s.pool.QueryRow(ctx, `
SELECT
  instance_id,
  attempt_id::text,
  final_state::text,
  provider_ref,
  agent_run_id,
  agent_exit_code,
  trajectory_ref,
  input_tokens,
  output_tokens,
  tool_calls,
  test_exit_code,
  test_stdout_ref,
  test_stderr_ref,
  error_code,
  error_message,
  error_details,
  provisioned_at,
  agent_started_at,
  agent_ended_at,
  test_started_at,
  test_ended_at,
  created_at
FROM instance_results
WHERE instance_id = $1
`, instanceID)

	result, err := scanStoredInstanceResult(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return store.StoredInstanceResult{}, store.ErrNotFound
		}
		return store.StoredInstanceResult{}, fmt.Errorf("get instance result: %w", err)
	}
	return result, nil
}

func (s *Store) ListInstanceResults(ctx context.Context, runID string) ([]store.StoredInstanceResult, error) {
	rows, err := s.pool.Query(ctx, `
SELECT
  r.instance_id,
  r.attempt_id::text,
  r.final_state::text,
  r.provider_ref,
  r.agent_run_id,
  r.agent_exit_code,
  r.trajectory_ref,
  r.input_tokens,
  r.output_tokens,
  r.tool_calls,
  r.test_exit_code,
  r.test_stdout_ref,
  r.test_stderr_ref,
  r.error_code,
  r.error_message,
  r.error_details,
  r.provisioned_at,
  r.agent_started_at,
  r.agent_ended_at,
  r.test_started_at,
  r.test_ended_at,
  r.created_at
FROM instance_results r
JOIN run_instances i ON i.instance_id = r.instance_id
WHERE i.run_id = $1
ORDER BY i.ordinal ASC
`, runID)
	if err != nil {
		return nil, fmt.Errorf("list instance results: %w", err)
	}
	defer rows.Close()

	out := make([]store.StoredInstanceResult, 0)
	for rows.Next() {
		result, err := scanStoredInstanceResult(rows)
		if err != nil {
			return nil, fmt.Errorf("scan instance result: %w", err)
		}
		out = append(out, result)
	}
	if rows.Err() != nil {
		return nil, fmt.Errorf("list instance results rows: %w", rows.Err())
	}
	return out, nil
}

func scanStoredInstanceResult(scanner rowScanner) (store.StoredInstanceResult, error) {
	var result store.StoredInstanceResult
	var finalState string
	var providerRef *string
	var agentRunID *string
	var agentExitCode *int
	var trajectoryRef *string
	var inputTokens *int64
	var outputTokens *int64
	var toolCalls *int64
	var testExitCode *int
	var testStdoutRef *string
	var testStderrRef *string
	var errorCode *string
	var errorMessage *string
	var errorDetailsRaw []byte
	var provisionedAt *time.Time
	var agentStartedAt *time.Time
	var agentEndedAt *time.Time
	var testStartedAt *time.Time
	var testEndedAt *time.Time

	if err := scanner.Scan(
		&result.InstanceID,
		&result.AttemptID,
		&finalState,
		&providerRef,
		&agentRunID,
		&agentExitCode,
		&trajectoryRef,
		&inputTokens,
		&outputTokens,
		&toolCalls,
		&testExitCode,
		&testStdoutRef,
		&testStderrRef,
		&errorCode,
		&errorMessage,
		&errorDetailsRaw,
		&provisionedAt,
		&agentStartedAt,
		&agentEndedAt,
		&testStartedAt,
		&testEndedAt,
		&result.CreatedAt,
	); err != nil {
		return store.StoredInstanceResult{}, err
	}
	result.FinalState = domain.InstanceState(finalState)
	result.ProviderRef = derefString(providerRef)
	result.AgentRunID = derefString(agentRunID)
	result.AgentExitCode = agentExitCode
	result.TrajectoryRef = derefString(trajectoryRef)
	result.Usage = usage.Clone(&usage.Metrics{
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
		ToolCalls:    toolCalls,
	})
	result.TestExitCode = testExitCode
	result.TestStdoutRef = derefString(testStdoutRef)
	result.TestStderrRef = derefString(testStderrRef)
	result.ErrorCode = derefString(errorCode)
	result.ErrorMessage = derefString(errorMessage)
	result.ProvisionedAt = provisionedAt
	result.AgentStartedAt = agentStartedAt
	result.AgentEndedAt = agentEndedAt
	result.TestStartedAt = testStartedAt
	result.TestEndedAt = testEndedAt
	if len(errorDetailsRaw) > 0 {
		decoded, err := unmarshalJSONMap(errorDetailsRaw)
		if err != nil {
			return store.StoredInstanceResult{}, err
		}
		result.ErrorDetails = decoded
	}
	return result, nil
}

func (s *Store) ListArtifacts(ctx context.Context, instanceID string) ([]store.Artifact, error) {
	rows, err := s.pool.Query(ctx, `
SELECT
  artifact_id,
  run_id,
  instance_id,
  attempt_id::text,
  role,
  ordinal,
  store_key,
  uri,
  content_type,
  byte_size,
  sha256,
  metadata,
  created_at
FROM artifacts
WHERE instance_id = $1
ORDER BY role ASC, ordinal ASC, created_at ASC
`, instanceID)
	if err != nil {
		return nil, fmt.Errorf("list artifacts query: %w", err)
	}
	defer rows.Close()
	out := make([]store.Artifact, 0)
	for rows.Next() {
		a, err := scanArtifact(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	if rows.Err() != nil {
		return nil, fmt.Errorf("list artifacts rows: %w", rows.Err())
	}
	return out, nil
}

func (s *Store) GetArtifact(ctx context.Context, artifactID string) (store.Artifact, error) {
	row := s.pool.QueryRow(ctx, `
SELECT
  artifact_id,
  run_id,
  instance_id,
  attempt_id::text,
  role,
  ordinal,
  store_key,
  uri,
  content_type,
  byte_size,
  sha256,
  metadata,
  created_at
FROM artifacts
WHERE artifact_id = $1
`, artifactID)
	return scanArtifact(row)
}

func scanArtifact(row interface{ Scan(...any) error }) (store.Artifact, error) {
	var artifactID string
	var runID string
	var instanceID string
	var attemptID string
	var role string
	var ordinal int
	var storeKey string
	var uri string
	var contentType *string
	var byteSize *int64
	var sha256 *string
	var metadataRaw []byte
	var createdAt time.Time

	err := row.Scan(
		&artifactID,
		&runID,
		&instanceID,
		&attemptID,
		&role,
		&ordinal,
		&storeKey,
		&uri,
		&contentType,
		&byteSize,
		&sha256,
		&metadataRaw,
		&createdAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return store.Artifact{}, store.ErrNotFound
		}
		return store.Artifact{}, fmt.Errorf("scan artifact: %w", err)
	}
	metadata, err := unmarshalJSONMap(metadataRaw)
	if err != nil {
		return store.Artifact{}, err
	}
	art := store.Artifact{
		ArtifactID: artifactID,
		RunID:      runID,
		InstanceID: instanceID,
		AttemptID:  attemptID,
		Role:       role,
		Ordinal:    ordinal,
		StoreKey:   storeKey,
		URI:        uri,
		Metadata:   metadata,
		CreatedAt:  createdAt,
	}
	if contentType != nil {
		art.ContentType = *contentType
	}
	if byteSize != nil {
		art.ByteSize = *byteSize
	}
	if sha256 != nil {
		art.SHA256 = *sha256
	}
	return art, nil
}

func (s *Store) ClaimPendingInstance(ctx context.Context, workerID string, leaseDuration time.Duration, at time.Time) (store.ClaimedWork, bool, error) {
	if strings.TrimSpace(workerID) == "" {
		return store.ClaimedWork{}, false, fmt.Errorf("worker id is required")
	}
	if leaseDuration <= 0 {
		return store.ClaimedWork{}, false, fmt.Errorf("lease duration must be > 0")
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return store.ClaimedWork{}, false, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	candidateRow := tx.QueryRow(ctx, `
WITH running_counts AS (
  SELECT
    run_id,
    count(*) FILTER (WHERE state IN (
      'provisioning','image_building','agent_server_installing','booting','agent_installing','agent_configuring','agent_running','agent_collecting','testing','collecting_artifacts'
    )) AS running_count
  FROM run_instances
  GROUP BY run_id
)
SELECT
  r.run_id,
  r.project_id,
  r.created_by_user_id,
  r.name,
  r.state::text,
  r.source_kind::text,
  r.run_bundle_hash,
  r.run_bundle_json,
  r.cancel_requested_at,
  r.started_at,
  r.ended_at,
  r.created_at,
  ri.instance_id,
  ri.ordinal,
  ri.case_id,
  ri.image,
  ri.initial_prompt,
  ri.agent_cwd,
  ri.test_command,
  ri.test_cwd,
  ri.test_timeout_seconds,
  jsonb_extract_path(r.run_bundle_json, 'resolved_snapshot', 'cases', ri.ordinal::text, 'test_assets') AS test_assets,
  ri.state::text,
  ri.created_at,
  ri.updated_at
FROM run_instances ri
JOIN runs r ON r.run_id = ri.run_id
LEFT JOIN running_counts rc ON rc.run_id = r.run_id
WHERE ri.state = 'pending'
  AND r.cancel_requested_at IS NULL
  AND r.state IN ('queued', 'running')
  AND COALESCE(rc.running_count, 0) < GREATEST(1, ((r.run_bundle_json -> 'resolved_snapshot' -> 'execution' ->> 'max_concurrency')::int))
ORDER BY r.created_at ASC, ri.ordinal ASC
FOR UPDATE OF ri, r SKIP LOCKED
LIMIT 1
`)

	var runID string
	var projectID string
	var createdBy string
	var runName *string
	var runState string
	var sourceKind string
	var bundleHash string
	var bundleRaw []byte
	var cancelRequestedAt *time.Time
	var startedAt *time.Time
	var endedAt *time.Time
	var runCreatedAt time.Time
	var instanceID string
	var ordinal int
	var caseID string
	var image string
	var initialPrompt string
	var agentCwd string
	var testCommand []string
	var testCwd string
	var testTimeoutSeconds int
	var testAssetsRaw []byte
	var instanceState string
	var instanceCreatedAt time.Time
	var instanceUpdatedAt time.Time

	err = candidateRow.Scan(
		&runID,
		&projectID,
		&createdBy,
		&runName,
		&runState,
		&sourceKind,
		&bundleHash,
		&bundleRaw,
		&cancelRequestedAt,
		&startedAt,
		&endedAt,
		&runCreatedAt,
		&instanceID,
		&ordinal,
		&caseID,
		&image,
		&initialPrompt,
		&agentCwd,
		&testCommand,
		&testCwd,
		&testTimeoutSeconds,
		&testAssetsRaw,
		&instanceState,
		&instanceCreatedAt,
		&instanceUpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			if commitErr := tx.Commit(ctx); commitErr != nil {
				return store.ClaimedWork{}, false, fmt.Errorf("commit empty claim tx: %w", commitErr)
			}
			return store.ClaimedWork{}, false, nil
		}
		return store.ClaimedWork{}, false, fmt.Errorf("claim candidate query: %w", err)
	}

	attemptID := uuid.NewString()
	leaseToken := uuid.NewString()
	leaseExpiresAt := at.Add(leaseDuration)

	_, err = tx.Exec(ctx, `
INSERT INTO instance_attempts (
  attempt_id,
  instance_id,
  worker_id,
  lease_token,
  created_at,
  last_heartbeat_at,
  lease_expires_at
) VALUES (
  $1::uuid,
  $2,
  $3,
  $4::uuid,
  $5,
  $5,
  $6
)
`, attemptID, instanceID, workerID, leaseToken, at, leaseExpiresAt)
	if err != nil {
		return store.ClaimedWork{}, false, fmt.Errorf("insert instance attempt: %w", err)
	}

	_, err = tx.Exec(ctx, `
INSERT INTO instance_leases (
  instance_id,
  current_attempt_id,
  lease_token,
  leased_by_worker_id,
  lease_expires_at,
  last_heartbeat_at
) VALUES (
  $1,
  $2::uuid,
  $3::uuid,
  $4,
  $5,
  $6
)
ON CONFLICT (instance_id) DO UPDATE
SET
  current_attempt_id = EXCLUDED.current_attempt_id,
  lease_token = EXCLUDED.lease_token,
  leased_by_worker_id = EXCLUDED.leased_by_worker_id,
  lease_expires_at = EXCLUDED.lease_expires_at,
  last_heartbeat_at = EXCLUDED.last_heartbeat_at
`, instanceID, attemptID, leaseToken, workerID, leaseExpiresAt, at)
	if err != nil {
		return store.ClaimedWork{}, false, fmt.Errorf("upsert instance lease: %w", err)
	}

	oldInstanceState := domain.InstanceState(instanceState)
	_, err = tx.Exec(ctx, `
UPDATE run_instances
SET state = 'provisioning', updated_at = $2
WHERE instance_id = $1
`, instanceID, at)
	if err != nil {
		return store.ClaimedWork{}, false, fmt.Errorf("update instance to provisioning: %w", err)
	}

	_, err = tx.Exec(ctx, `
INSERT INTO instance_events (
  instance_id,
  attempt_id,
  source,
  from_state,
  to_state,
  details,
  created_at
) VALUES (
  $1,
  $2::uuid,
  'worker',
  $3::instance_state_t,
  'provisioning',
  NULL,
  $4
)
`, instanceID, attemptID, string(oldInstanceState), at)
	if err != nil {
		return store.ClaimedWork{}, false, fmt.Errorf("insert claim instance event: %w", err)
	}

	if domain.RunState(runState) == domain.RunStateQueued {
		_, err = tx.Exec(ctx, `
UPDATE runs
SET state = 'running', started_at = COALESCE(started_at, $2), updated_at = $2
WHERE run_id = $1
`, runID, at)
		if err != nil {
			return store.ClaimedWork{}, false, fmt.Errorf("update run to running: %w", err)
		}
		_, err = tx.Exec(ctx, `
INSERT INTO run_events (run_id, source, from_state, to_state, details, created_at)
VALUES ($1, 'worker', 'queued', 'running', NULL, $2)
`, runID, at)
		if err != nil {
			return store.ClaimedWork{}, false, fmt.Errorf("insert claim run event: %w", err)
		}
	}

	if err := s.refreshRunStateTx(ctx, tx, runID, at); err != nil {
		return store.ClaimedWork{}, false, err
	}

	run, err := s.getRunTx(ctx, tx, runID, true)
	if err != nil {
		return store.ClaimedWork{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return store.ClaimedWork{}, false, fmt.Errorf("commit claim tx: %w", err)
	}
	testAssets, err := decodeCaseTestAssets(testAssetsRaw)
	if err != nil {
		return store.ClaimedWork{}, false, err
	}

	return store.ClaimedWork{
		AttemptID:  attemptID,
		LeaseToken: leaseToken,
		Run:        run,
		Instance: store.Instance{
			InstanceID: instanceID,
			RunID:      runID,
			Ordinal:    ordinal,
			Case: runbundle.Case{
				CaseID:            caseID,
				Image:             image,
				InitialPrompt:     initialPrompt,
				AgentCwd:          agentCwd,
				TestCommand:       testCommand,
				TestCwd:           testCwd,
				TestTimeoutSecond: testTimeoutSeconds,
				TestAssets:        testAssets,
			},
			State:     domain.InstanceStateProvisioning,
			CreatedAt: instanceCreatedAt,
			UpdatedAt: at,
		},
	}, true, nil
}

func (s *Store) UpdateInstanceState(ctx context.Context, runID, instanceID, attemptID string, state domain.InstanceState, at time.Time) error {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	var fromState string
	err = tx.QueryRow(ctx, `
SELECT state::text
FROM run_instances
WHERE instance_id = $1 AND run_id = $2
FOR UPDATE
`, instanceID, runID).Scan(&fromState)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return store.ErrNotFound
		}
		return fmt.Errorf("lock instance: %w", err)
	}

	if err := s.assertLeaseTx(ctx, tx, instanceID, attemptID, "", ""); err != nil {
		return err
	}

	current := domain.InstanceState(fromState)
	if current.IsTerminal() {
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit tx: %w", err)
		}
		return nil
	}
	if !domain.ValidInstanceTransition(current, state) {
		return fmt.Errorf("invalid instance transition %q -> %q", current, state)
	}

	_, err = tx.Exec(ctx, `
UPDATE run_instances
SET state = $3::instance_state_t, updated_at = $4
WHERE instance_id = $1 AND run_id = $2
`, instanceID, runID, string(state), at)
	if err != nil {
		return fmt.Errorf("update instance state: %w", err)
	}

	_, err = tx.Exec(ctx, `
INSERT INTO instance_events (
  instance_id,
  attempt_id,
  source,
  from_state,
  to_state,
  details,
  created_at
) VALUES (
  $1,
  $2::uuid,
  'worker',
  $3::instance_state_t,
  $4::instance_state_t,
  NULL,
  $5
)
`, instanceID, attemptID, string(current), string(state), at)
	if err != nil {
		return fmt.Errorf("insert instance event: %w", err)
	}

	if err := s.refreshRunStateTx(ctx, tx, runID, at); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}

func (s *Store) UpdateInstanceImage(ctx context.Context, runID, instanceID, attemptID, image string, at time.Time) error {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	resolvedImage := strings.TrimSpace(image)
	if !digestImagePattern.MatchString(resolvedImage) {
		return fmt.Errorf("image must be digest-pinned using @sha256")
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	var currentRunID string
	var currentState string
	var currentImage string
	err = tx.QueryRow(ctx, `
SELECT run_id, state::text, image
FROM run_instances
WHERE instance_id = $1
FOR UPDATE
`, instanceID).Scan(&currentRunID, &currentState, &currentImage)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return store.ErrNotFound
		}
		return fmt.Errorf("lock instance: %w", err)
	}
	if currentRunID != runID {
		return store.ErrNotFound
	}
	if err := s.assertLeaseTx(ctx, tx, instanceID, attemptID, "", ""); err != nil {
		return err
	}
	if domain.InstanceState(currentState).IsTerminal() || strings.TrimSpace(currentImage) == resolvedImage {
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit tx: %w", err)
		}
		return nil
	}

	_, err = tx.Exec(ctx, `
UPDATE run_instances
SET image = $3, updated_at = $4
WHERE instance_id = $1 AND run_id = $2
`, instanceID, runID, resolvedImage, at)
	if err != nil {
		return fmt.Errorf("update instance image: %w", err)
	}

	detailsRaw, err := marshalJSONMap(map[string]any{"resolved_image": resolvedImage})
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
INSERT INTO instance_events (
  instance_id,
  attempt_id,
  source,
  from_state,
  to_state,
  details,
  created_at
) VALUES (
  $1,
  $2::uuid,
  'worker',
  $3::instance_state_t,
  $3::instance_state_t,
  $4::jsonb,
  $5
)
`, instanceID, attemptID, currentState, detailsRaw, at)
	if err != nil {
		return fmt.Errorf("insert image resolution event: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}

func (s *Store) HeartbeatAttempt(ctx context.Context, instanceID, attemptID, leaseToken, workerID string, leaseDuration time.Duration, at time.Time) error {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	if err := s.assertLeaseTx(ctx, tx, instanceID, attemptID, leaseToken, workerID); err != nil {
		return err
	}
	leaseExpiresAt := at.Add(leaseDuration)

	_, err = tx.Exec(ctx, `
UPDATE instance_leases
SET
  lease_expires_at = $4,
  last_heartbeat_at = $5,
  leased_by_worker_id = $3
WHERE instance_id = $1
  AND current_attempt_id = $2::uuid
`, instanceID, attemptID, workerID, leaseExpiresAt, at)
	if err != nil {
		return fmt.Errorf("update instance lease heartbeat: %w", err)
	}

	_, err = tx.Exec(ctx, `
UPDATE instance_attempts
SET
  last_heartbeat_at = $2,
  lease_expires_at = $3
WHERE attempt_id = $1::uuid
`, attemptID, at, leaseExpiresAt)
	if err != nil {
		return fmt.Errorf("update instance attempt heartbeat: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}

func (s *Store) FinalizeAttempt(ctx context.Context, in store.FinalizeInput, at time.Time) error {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	var runID string
	var currentState string
	err = tx.QueryRow(ctx, `
SELECT run_id, state::text
FROM run_instances
WHERE instance_id = $1
FOR UPDATE
`, in.InstanceID).Scan(&runID, &currentState)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return store.ErrNotFound
		}
		return fmt.Errorf("lock instance: %w", err)
	}
	if runID != in.RunID {
		return store.ErrNotFound
	}

	if err := s.assertLeaseTx(ctx, tx, in.InstanceID, in.AttemptID, "", ""); err != nil {
		return err
	}

	final := in.Result.FinalState
	if !final.IsTerminal() {
		final = domain.InstanceStateInfraFailed
	}
	if !domain.ValidInstanceTransition(domain.InstanceState(currentState), final) {
		final = domain.InstanceStateInfraFailed
	}

	_, err = tx.Exec(ctx, `
UPDATE run_instances
SET state = $3::instance_state_t, updated_at = $4
WHERE instance_id = $1 AND run_id = $2
`, in.InstanceID, in.RunID, string(final), at)
	if err != nil {
		return fmt.Errorf("update finalized instance state: %w", err)
	}

	detailsRaw, err := marshalJSONMap(map[string]any{
		"provider_ref":  in.ProviderRef,
		"error_code":    in.Result.ErrorCode,
		"error_message": in.Result.ErrorMessage,
	})
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
INSERT INTO instance_events (
  instance_id,
  attempt_id,
  source,
  from_state,
  to_state,
  details,
  created_at
) VALUES (
  $1,
  $2::uuid,
  'worker',
  $3::instance_state_t,
  $4::instance_state_t,
  $5::jsonb,
  $6
)
`, in.InstanceID, in.AttemptID, currentState, string(final), detailsRaw, at)
	if err != nil {
		return fmt.Errorf("insert finalize instance event: %w", err)
	}

	_, err = tx.Exec(ctx, `
UPDATE instance_attempts
SET ended_at = COALESCE(ended_at, $2)
WHERE attempt_id = $1::uuid
`, in.AttemptID, at)
	if err != nil {
		return fmt.Errorf("mark attempt ended: %w", err)
	}

	_, err = tx.Exec(ctx, `DELETE FROM instance_leases WHERE instance_id = $1`, in.InstanceID)
	if err != nil {
		return fmt.Errorf("delete instance lease: %w", err)
	}

	errorDetailsRaw, err := marshalJSONMap(in.Result.ErrorDetails)
	if err != nil {
		return err
	}
	inputTokens, outputTokens, toolCalls := usageColumns(in.Result.Usage)
	_, err = tx.Exec(ctx, `
INSERT INTO instance_results (
  instance_id,
  attempt_id,
  final_state,
  provider_ref,
  agent_run_id,
  agent_exit_code,
  trajectory_ref,
  input_tokens,
  output_tokens,
  tool_calls,
  test_exit_code,
  test_stdout_ref,
  test_stderr_ref,
  error_code,
  error_message,
  error_details,
  provisioned_at,
  agent_started_at,
  agent_ended_at,
  test_started_at,
  test_ended_at,
  created_at
) VALUES (
  $1,
  $2::uuid,
  $3::instance_state_t,
  $4,
  $5,
  $6,
  $7,
  $8,
  $9,
  $10,
  $11,
  $12,
  $13,
  $14,
  $15,
  $16::jsonb,
  $17,
  $18,
  $19,
  $20,
  $21,
  $22
)
ON CONFLICT (instance_id) DO UPDATE SET
  attempt_id = EXCLUDED.attempt_id,
  final_state = EXCLUDED.final_state,
  provider_ref = EXCLUDED.provider_ref,
  agent_run_id = EXCLUDED.agent_run_id,
  agent_exit_code = EXCLUDED.agent_exit_code,
  trajectory_ref = EXCLUDED.trajectory_ref,
  input_tokens = EXCLUDED.input_tokens,
  output_tokens = EXCLUDED.output_tokens,
  tool_calls = EXCLUDED.tool_calls,
  test_exit_code = EXCLUDED.test_exit_code,
  test_stdout_ref = EXCLUDED.test_stdout_ref,
  test_stderr_ref = EXCLUDED.test_stderr_ref,
  error_code = EXCLUDED.error_code,
  error_message = EXCLUDED.error_message,
  error_details = EXCLUDED.error_details,
  provisioned_at = EXCLUDED.provisioned_at,
  agent_started_at = EXCLUDED.agent_started_at,
  agent_ended_at = EXCLUDED.agent_ended_at,
  test_started_at = EXCLUDED.test_started_at,
  test_ended_at = EXCLUDED.test_ended_at,
  created_at = EXCLUDED.created_at
`,
		in.InstanceID,
		in.AttemptID,
		string(final),
		nullableString(in.ProviderRef),
		nullableString(in.Result.AgentRunID),
		in.Result.AgentExitCode,
		nullableString(in.Result.Trajectory),
		inputTokens,
		outputTokens,
		toolCalls,
		in.Result.TestExitCode,
		nullableString(in.Result.TestStdoutRef),
		nullableString(in.Result.TestStderrRef),
		nullableString(in.Result.ErrorCode),
		nullableString(in.Result.ErrorMessage),
		errorDetailsRaw,
		in.Result.ProvisionedAt,
		in.Result.AgentStartedAt,
		in.Result.AgentEndedAt,
		in.Result.TestStartedAt,
		in.Result.TestEndedAt,
		at,
	)
	if err != nil {
		return fmt.Errorf("upsert instance result: %w", err)
	}

	for i := range in.Artifacts {
		art := in.Artifacts[i]
		if strings.TrimSpace(art.ArtifactID) == "" {
			art.ArtifactID = defaultArtifactID(in.InstanceID, in.AttemptID, i)
		}
		if art.CreatedAt.IsZero() {
			art.CreatedAt = at
		}
		metadataRaw, err := marshalJSONMap(art.Metadata)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `
INSERT INTO artifacts (
  artifact_id,
  run_id,
  instance_id,
  attempt_id,
  role,
  ordinal,
  store_key,
  uri,
  content_type,
  byte_size,
  sha256,
  metadata,
  created_at
) VALUES (
  $1,
  $2,
  $3,
  $4::uuid,
  $5,
  $6,
  $7,
  $8,
  $9,
  $10,
  $11,
  $12::jsonb,
  $13
)
`,
			art.ArtifactID,
			in.RunID,
			in.InstanceID,
			in.AttemptID,
			art.Role,
			art.Ordinal,
			art.StoreKey,
			art.URI,
			nullableString(art.ContentType),
			nullableInt64(art.ByteSize),
			nullableString(art.SHA256),
			metadataRaw,
			art.CreatedAt,
		)
		if err != nil {
			return fmt.Errorf("insert artifact: %w", err)
		}
	}

	if err := s.refreshRunStateTx(ctx, tx, in.RunID, at); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}

func (s *Store) RequeueInfraFailure(ctx context.Context, in store.RequeueInfraFailureInput, at time.Time) (bool, error) {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return false, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	var runID string
	var currentState string
	err = tx.QueryRow(ctx, `
SELECT run_id, state::text
FROM run_instances
WHERE instance_id = $1
FOR UPDATE
`, in.InstanceID).Scan(&runID, &currentState)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, store.ErrNotFound
		}
		return false, fmt.Errorf("lock instance: %w", err)
	}
	if runID != in.RunID {
		return false, store.ErrNotFound
	}
	if err := s.assertLeaseTx(ctx, tx, in.InstanceID, in.AttemptID, "", ""); err != nil {
		return false, err
	}
	if !in.Result.FinalState.IsInfraFailure() || in.MaxRetryCount <= 0 {
		if err := tx.Commit(ctx); err != nil {
			return false, fmt.Errorf("commit tx: %w", err)
		}
		return false, nil
	}

	var usedRetries int
	err = tx.QueryRow(ctx, `
SELECT count(*)
FROM instance_events
WHERE instance_id = $1 AND source = 'retry'
`, in.InstanceID).Scan(&usedRetries)
	if err != nil {
		return false, fmt.Errorf("count retry events: %w", err)
	}
	if usedRetries >= in.MaxRetryCount {
		if err := tx.Commit(ctx); err != nil {
			return false, fmt.Errorf("commit tx: %w", err)
		}
		return false, nil
	}

	_, err = tx.Exec(ctx, `
UPDATE instance_attempts
SET ended_at = COALESCE(ended_at, $2)
WHERE attempt_id = $1::uuid
`, in.AttemptID, at)
	if err != nil {
		return false, fmt.Errorf("mark retry attempt ended: %w", err)
	}

	_, err = tx.Exec(ctx, `DELETE FROM instance_leases WHERE instance_id = $1`, in.InstanceID)
	if err != nil {
		return false, fmt.Errorf("delete retry lease: %w", err)
	}

	_, err = tx.Exec(ctx, `
UPDATE run_instances
SET state = 'pending', updated_at = $2
WHERE instance_id = $1
`, in.InstanceID, at)
	if err != nil {
		return false, fmt.Errorf("reset instance to pending: %w", err)
	}

	retryIndex := usedRetries + 1
	detailsRaw, err := marshalJSONMap(map[string]any{
		"attempt_final_state": string(in.Result.FinalState),
		"retry_index":         retryIndex,
		"max_retry_count":     in.MaxRetryCount,
		"provider_ref":        in.ProviderRef,
		"error_code":          in.Result.ErrorCode,
		"error_message":       in.Result.ErrorMessage,
	})
	if err != nil {
		return false, err
	}
	_, err = tx.Exec(ctx, `
INSERT INTO instance_events (
  instance_id,
  attempt_id,
  source,
  from_state,
  to_state,
  details,
  created_at
) VALUES (
  $1,
  $2::uuid,
  'retry',
  $3::instance_state_t,
  'pending'::instance_state_t,
  $4::jsonb,
  $5
)
`, in.InstanceID, in.AttemptID, currentState, detailsRaw, at)
	if err != nil {
		return false, fmt.Errorf("insert retry instance event: %w", err)
	}

	for i := range in.Artifacts {
		art := in.Artifacts[i]
		if strings.TrimSpace(art.ArtifactID) == "" {
			art.ArtifactID = defaultArtifactID(in.InstanceID, in.AttemptID, i)
		}
		if art.CreatedAt.IsZero() {
			art.CreatedAt = at
		}
		metadataRaw, err := marshalJSONMap(art.Metadata)
		if err != nil {
			return false, err
		}
		_, err = tx.Exec(ctx, `
INSERT INTO artifacts (
  artifact_id,
  run_id,
  instance_id,
  attempt_id,
  role,
  ordinal,
  store_key,
  uri,
  content_type,
  byte_size,
  sha256,
  metadata,
  created_at
) VALUES (
  $1,
  $2,
  $3,
  $4::uuid,
  $5,
  $6,
  $7,
  $8,
  $9,
  $10,
  $11,
  $12::jsonb,
  $13
)
`,
			art.ArtifactID,
			in.RunID,
			in.InstanceID,
			in.AttemptID,
			art.Role,
			art.Ordinal,
			art.StoreKey,
			art.URI,
			nullableString(art.ContentType),
			nullableInt64(art.ByteSize),
			nullableString(art.SHA256),
			metadataRaw,
			art.CreatedAt,
		)
		if err != nil {
			return false, fmt.Errorf("insert retry artifact: %w", err)
		}
	}

	if err := s.refreshRunStateTx(ctx, tx, in.RunID, at); err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit tx: %w", err)
	}
	return true, nil
}

func (s *Store) CarryForwardInstance(ctx context.Context, in store.CarryForwardInput, at time.Time) error {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	var runID string
	var currentState string
	err = tx.QueryRow(ctx, `
SELECT run_id, state::text
FROM run_instances
WHERE instance_id = $1
FOR UPDATE
`, in.InstanceID).Scan(&runID, &currentState)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return store.ErrNotFound
		}
		return fmt.Errorf("lock instance: %w", err)
	}
	if runID != in.RunID {
		return store.ErrNotFound
	}
	if domain.InstanceState(currentState).IsTerminal() {
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit tx: %w", err)
		}
		return nil
	}
	if domain.InstanceState(currentState) != domain.InstanceStatePending {
		return fmt.Errorf("carry-forward requires pending instance, got %s", currentState)
	}
	final := in.Result.FinalState
	if !final.IsTerminal() {
		return fmt.Errorf("carry-forward result final state must be terminal")
	}

	attemptID := uuid.NewString()
	leaseToken := uuid.NewString()
	_, err = tx.Exec(ctx, `
INSERT INTO instance_attempts (
  attempt_id,
  instance_id,
  worker_id,
  lease_token,
  created_at,
  last_heartbeat_at,
  lease_expires_at,
  ended_at
) VALUES (
  $1::uuid,
  $2,
  'resume',
  $3::uuid,
  $4,
  $4,
  $4,
  $4
)
`, attemptID, in.InstanceID, leaseToken, at)
	if err != nil {
		return fmt.Errorf("insert carry-forward attempt: %w", err)
	}
	_, err = tx.Exec(ctx, `DELETE FROM instance_leases WHERE instance_id = $1`, in.InstanceID)
	if err != nil {
		return fmt.Errorf("delete carry-forward lease: %w", err)
	}

	_, err = tx.Exec(ctx, `
UPDATE run_instances
SET state = $3::instance_state_t, updated_at = $4
WHERE instance_id = $1 AND run_id = $2
`, in.InstanceID, in.RunID, string(final), at)
	if err != nil {
		return fmt.Errorf("update carry-forward instance state: %w", err)
	}

	detailsRaw, err := marshalJSONMap(map[string]any{
		"source_run_id":      in.SourceRunID,
		"source_instance_id": in.SourceInstanceID,
		"carried_forward":    true,
		"provider_ref":       in.ProviderRef,
		"error_code":         in.Result.ErrorCode,
		"error_message":      in.Result.ErrorMessage,
	})
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
INSERT INTO instance_events (
  instance_id,
  attempt_id,
  source,
  from_state,
  to_state,
  details,
  created_at
) VALUES (
  $1,
  $2::uuid,
  'resume',
  $3::instance_state_t,
  $4::instance_state_t,
  $5::jsonb,
  $6
)
`, in.InstanceID, attemptID, currentState, string(final), detailsRaw, at)
	if err != nil {
		return fmt.Errorf("insert carry-forward instance event: %w", err)
	}

	errorDetailsRaw, err := marshalJSONMap(in.Result.ErrorDetails)
	if err != nil {
		return err
	}
	inputTokens, outputTokens, toolCalls := usageColumns(in.Result.Usage)
	_, err = tx.Exec(ctx, `
INSERT INTO instance_results (
  instance_id,
  attempt_id,
  final_state,
  provider_ref,
  agent_run_id,
  agent_exit_code,
  trajectory_ref,
  input_tokens,
  output_tokens,
  tool_calls,
  test_exit_code,
  test_stdout_ref,
  test_stderr_ref,
  error_code,
  error_message,
  error_details,
  provisioned_at,
  agent_started_at,
  agent_ended_at,
  test_started_at,
  test_ended_at,
  created_at
) VALUES (
  $1,
  $2::uuid,
  $3::instance_state_t,
  $4,
  $5,
  $6,
  $7,
  $8,
  $9,
  $10,
  $11,
  $12,
  $13,
  $14,
  $15,
  $16::jsonb,
  $17,
  $18,
  $19,
  $20,
  $21,
  $22
)
ON CONFLICT (instance_id) DO UPDATE SET
  attempt_id = EXCLUDED.attempt_id,
  final_state = EXCLUDED.final_state,
  provider_ref = EXCLUDED.provider_ref,
  agent_run_id = EXCLUDED.agent_run_id,
  agent_exit_code = EXCLUDED.agent_exit_code,
  trajectory_ref = EXCLUDED.trajectory_ref,
  input_tokens = EXCLUDED.input_tokens,
  output_tokens = EXCLUDED.output_tokens,
  tool_calls = EXCLUDED.tool_calls,
  test_exit_code = EXCLUDED.test_exit_code,
  test_stdout_ref = EXCLUDED.test_stdout_ref,
  test_stderr_ref = EXCLUDED.test_stderr_ref,
  error_code = EXCLUDED.error_code,
  error_message = EXCLUDED.error_message,
  error_details = EXCLUDED.error_details,
  provisioned_at = EXCLUDED.provisioned_at,
  agent_started_at = EXCLUDED.agent_started_at,
  agent_ended_at = EXCLUDED.agent_ended_at,
  test_started_at = EXCLUDED.test_started_at,
  test_ended_at = EXCLUDED.test_ended_at,
  created_at = EXCLUDED.created_at
`,
		in.InstanceID,
		attemptID,
		string(final),
		nullableString(in.ProviderRef),
		nullableString(in.Result.AgentRunID),
		in.Result.AgentExitCode,
		nullableString(in.Result.Trajectory),
		inputTokens,
		outputTokens,
		toolCalls,
		in.Result.TestExitCode,
		nullableString(in.Result.TestStdoutRef),
		nullableString(in.Result.TestStderrRef),
		nullableString(in.Result.ErrorCode),
		nullableString(in.Result.ErrorMessage),
		errorDetailsRaw,
		in.Result.ProvisionedAt,
		in.Result.AgentStartedAt,
		in.Result.AgentEndedAt,
		in.Result.TestStartedAt,
		in.Result.TestEndedAt,
		at,
	)
	if err != nil {
		return fmt.Errorf("upsert carry-forward instance result: %w", err)
	}

	for i := range in.Artifacts {
		art := in.Artifacts[i]
		if strings.TrimSpace(art.ArtifactID) == "" {
			art.ArtifactID = defaultArtifactID(in.InstanceID, attemptID, i)
		}
		if art.CreatedAt.IsZero() {
			art.CreatedAt = at
		}
		metadataRaw, err := marshalJSONMap(art.Metadata)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `
INSERT INTO artifacts (
  artifact_id,
  run_id,
  instance_id,
  attempt_id,
  role,
  ordinal,
  store_key,
  uri,
  content_type,
  byte_size,
  sha256,
  metadata,
  created_at
) VALUES (
  $1,
  $2,
  $3,
  $4::uuid,
  $5,
  $6,
  $7,
  $8,
  $9,
  $10,
  $11,
  $12::jsonb,
  $13
)
`,
			art.ArtifactID,
			in.RunID,
			in.InstanceID,
			attemptID,
			art.Role,
			art.Ordinal,
			art.StoreKey,
			art.URI,
			nullableString(art.ContentType),
			nullableInt64(art.ByteSize),
			nullableString(art.SHA256),
			metadataRaw,
			art.CreatedAt,
		)
		if err != nil {
			return fmt.Errorf("insert carry-forward artifact: %w", err)
		}
	}

	if err := s.refreshRunStateTx(ctx, tx, in.RunID, at); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}

func (s *Store) RunCancelRequested(ctx context.Context, runID string) (bool, error) {
	var cancelRequestedAt *time.Time
	err := s.pool.QueryRow(ctx, `
SELECT cancel_requested_at
FROM runs
WHERE run_id = $1
`, runID).Scan(&cancelRequestedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, store.ErrNotFound
		}
		return false, fmt.Errorf("query run cancel requested: %w", err)
	}
	return cancelRequestedAt != nil, nil
}

func (s *Store) SweepCancelingRuns(ctx context.Context, at time.Time) (int, error) {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	rows, err := tx.Query(ctx, `
SELECT run_id, state::text, ended_at
FROM runs
WHERE cancel_requested_at IS NOT NULL
FOR UPDATE
`)
	if err != nil {
		return 0, fmt.Errorf("select canceling runs: %w", err)
	}
	defer rows.Close()

	type runStateRow struct {
		RunID   string
		State   domain.RunState
		EndedAt *time.Time
	}
	candidates := make([]runStateRow, 0)
	for rows.Next() {
		var runID string
		var state string
		var endedAt *time.Time
		if err := rows.Scan(&runID, &state, &endedAt); err != nil {
			return 0, fmt.Errorf("scan canceling run: %w", err)
		}
		candidates = append(candidates, runStateRow{RunID: runID, State: domain.RunState(state), EndedAt: endedAt})
	}
	if rows.Err() != nil {
		return 0, fmt.Errorf("canceling runs rows: %w", rows.Err())
	}

	updated := 0
	for _, c := range candidates {
		_, err = tx.Exec(ctx, `
WITH updated AS (
  UPDATE run_instances
  SET state = 'canceled', updated_at = $2
  WHERE run_id = $1
    AND state = 'pending'
  RETURNING instance_id
)
INSERT INTO instance_events (
  instance_id,
  attempt_id,
  source,
  from_state,
  to_state,
  details,
  created_at
)
SELECT
  u.instance_id,
  NULL,
  'sweeper',
  'pending',
  'canceled',
  '{"reason":"run_canceled"}'::jsonb,
  $2
FROM updated u
`, c.RunID, at)
		if err != nil {
			return 0, fmt.Errorf("cancel pending instances for run: %w", err)
		}

		counts, err := s.loadRunCountsTx(ctx, tx, c.RunID)
		if err != nil {
			return 0, err
		}
		after := domain.NextRunState(c.State, counts, true)
		if after == c.State {
			continue
		}
		_, err = tx.Exec(ctx, `
UPDATE runs
SET
  state = $2::run_state_t,
  ended_at = CASE
    WHEN $2::run_state_t IN ('completed','failed','canceled') AND ended_at IS NULL THEN $3
    ELSE ended_at
  END,
  updated_at = $3
WHERE run_id = $1
`, c.RunID, string(after), at)
		if err != nil {
			return 0, fmt.Errorf("update swept run state: %w", err)
		}
		_, err = tx.Exec(ctx, `
INSERT INTO run_events (run_id, source, from_state, to_state, details, created_at)
VALUES ($1, 'sweeper', $2::run_state_t, $3::run_state_t, NULL, $4)
`, c.RunID, string(c.State), string(after), at)
		if err != nil {
			return 0, fmt.Errorf("insert sweep run event: %w", err)
		}
		updated++
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit tx: %w", err)
	}
	return updated, nil
}

func (s *Store) ReapExpiredLeases(ctx context.Context, at time.Time, limit int) (int, error) {
	if limit <= 0 {
		limit = 200
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	rows, err := tx.Query(ctx, `
SELECT
  l.instance_id,
  l.current_attempt_id::text,
  ri.run_id,
  ri.state::text
FROM instance_leases l
JOIN run_instances ri ON ri.instance_id = l.instance_id
WHERE l.lease_expires_at <= $1
ORDER BY l.lease_expires_at ASC
FOR UPDATE OF l, ri SKIP LOCKED
LIMIT $2
`, at, limit)
	if err != nil {
		return 0, fmt.Errorf("select expired leases: %w", err)
	}
	defer rows.Close()

	type leaseRow struct {
		InstanceID string
		AttemptID  string
		RunID      string
		State      domain.InstanceState
	}
	candidates := make([]leaseRow, 0)
	for rows.Next() {
		var r leaseRow
		var state string
		if err := rows.Scan(&r.InstanceID, &r.AttemptID, &r.RunID, &state); err != nil {
			return 0, fmt.Errorf("scan expired lease row: %w", err)
		}
		r.State = domain.InstanceState(state)
		candidates = append(candidates, r)
	}
	if rows.Err() != nil {
		return 0, fmt.Errorf("expired leases rows: %w", rows.Err())
	}

	reaped := 0
	for _, c := range candidates {
		if c.State.IsTerminal() {
			if _, err := tx.Exec(ctx, `DELETE FROM instance_leases WHERE instance_id = $1`, c.InstanceID); err != nil {
				return 0, fmt.Errorf("delete stale terminal lease: %w", err)
			}
			continue
		}
		if _, err := tx.Exec(ctx, `
UPDATE instance_attempts
SET ended_at = COALESCE(ended_at, $2)
WHERE attempt_id = $1::uuid
`, c.AttemptID, at); err != nil {
			return 0, fmt.Errorf("mark reaped attempt ended: %w", err)
		}
		if _, err := tx.Exec(ctx, `DELETE FROM instance_leases WHERE instance_id = $1`, c.InstanceID); err != nil {
			return 0, fmt.Errorf("delete expired lease: %w", err)
		}
		if _, err := tx.Exec(ctx, `
UPDATE run_instances
SET state = 'pending', updated_at = $2
WHERE instance_id = $1
`, c.InstanceID, at); err != nil {
			return 0, fmt.Errorf("reset reaped instance to pending: %w", err)
		}
		detailsRaw, err := marshalJSONMap(map[string]any{"reason": "lease_expired"})
		if err != nil {
			return 0, err
		}
		if _, err := tx.Exec(ctx, `
INSERT INTO instance_events (
  instance_id,
  attempt_id,
  source,
  from_state,
  to_state,
  details,
  created_at
) VALUES (
  $1,
  $2::uuid,
  'reaper',
  $3::instance_state_t,
  'pending',
  $4::jsonb,
  $5
)
`, c.InstanceID, c.AttemptID, string(c.State), detailsRaw, at); err != nil {
			return 0, fmt.Errorf("insert reaper instance event: %w", err)
		}
		if err := s.refreshRunStateTx(ctx, tx, c.RunID, at); err != nil {
			return 0, err
		}
		reaped++
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit tx: %w", err)
	}
	return reaped, nil
}

func (s *Store) assertLeaseTx(ctx context.Context, tx pgx.Tx, instanceID, attemptID, leaseToken, workerID string) error {
	var currentAttemptID *string
	var currentLeaseToken *string
	var currentWorkerID *string
	err := tx.QueryRow(ctx, `
SELECT current_attempt_id::text, lease_token::text, leased_by_worker_id
FROM instance_leases
WHERE instance_id = $1
FOR UPDATE
`, instanceID).Scan(&currentAttemptID, &currentLeaseToken, &currentWorkerID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return store.ErrLeaseLost
		}
		return fmt.Errorf("query lease: %w", err)
	}
	if strings.TrimSpace(derefString(currentAttemptID)) == "" {
		return store.ErrLeaseLost
	}
	if attemptID != "" && derefString(currentAttemptID) != attemptID {
		return store.ErrLeaseLost
	}
	if leaseToken != "" && derefString(currentLeaseToken) != leaseToken {
		return store.ErrLeaseLost
	}
	if workerID != "" && derefString(currentWorkerID) != workerID {
		return store.ErrLeaseLost
	}
	return nil
}

func (s *Store) refreshRunStateTx(ctx context.Context, tx pgx.Tx, runID string, at time.Time) error {
	var currentState string
	var cancelRequestedAt *time.Time
	var startedAt *time.Time
	var endedAt *time.Time
	err := tx.QueryRow(ctx, `
SELECT state::text, cancel_requested_at, started_at, ended_at
FROM runs
WHERE run_id = $1
FOR UPDATE
`, runID).Scan(&currentState, &cancelRequestedAt, &startedAt, &endedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return store.ErrNotFound
		}
		return fmt.Errorf("lock run for refresh: %w", err)
	}

	counts, err := s.loadRunCountsTx(ctx, tx, runID)
	if err != nil {
		return err
	}
	cancelRequested := cancelRequestedAt != nil
	before := domain.RunState(currentState)
	after := domain.NextRunState(before, counts, cancelRequested)

	if after != before {
		_, err = tx.Exec(ctx, `
UPDATE runs
SET
  state = $2::run_state_t,
  started_at = CASE
    WHEN $2::run_state_t = 'running' AND started_at IS NULL THEN $3
    ELSE started_at
  END,
  ended_at = CASE
    WHEN $2::run_state_t IN ('completed','failed','canceled') AND ended_at IS NULL THEN $3
    ELSE ended_at
  END,
  updated_at = $3
WHERE run_id = $1
`, runID, string(after), at)
		if err != nil {
			return fmt.Errorf("update refreshed run state: %w", err)
		}
		_, err = tx.Exec(ctx, `
INSERT INTO run_events (run_id, source, from_state, to_state, details, created_at)
VALUES ($1, 'orchestrator', $2::run_state_t, $3::run_state_t, NULL, $4)
`, runID, string(before), string(after), at)
		if err != nil {
			return fmt.Errorf("insert orchestrator run event: %w", err)
		}
		return nil
	}

	if (before == domain.RunStateRunning && startedAt == nil) || (before.IsTerminal() && endedAt == nil) {
		_, err = tx.Exec(ctx, `
UPDATE runs
SET
  started_at = CASE
    WHEN state = 'running' AND started_at IS NULL THEN $2
    ELSE started_at
  END,
  ended_at = CASE
    WHEN state IN ('completed','failed','canceled') AND ended_at IS NULL THEN $2
    ELSE ended_at
  END,
  updated_at = $2
WHERE run_id = $1
`, runID, at)
		if err != nil {
			return fmt.Errorf("update run terminal/start timestamps: %w", err)
		}
	}
	return nil
}

func (s *Store) loadRunCountsTx(ctx context.Context, q rowQueryer, runID string) (domain.RunCounts, error) {
	var pending int
	var running int
	var succeeded int
	var testFailed int
	var infraFailed int
	var canceled int
	err := q.QueryRow(ctx, `
SELECT
  count(*) FILTER (WHERE state = 'pending') AS pending_count,
  count(*) FILTER (WHERE state IN (
    'provisioning','image_building','agent_server_installing','booting','agent_installing','agent_configuring','agent_running','agent_collecting','testing','collecting_artifacts'
  )) AS running_count,
  count(*) FILTER (WHERE state = 'succeeded') AS succeeded_count,
  count(*) FILTER (WHERE state = 'test_failed') AS test_failed_count,
  count(*) FILTER (WHERE state = 'infra_failed') AS infra_failed_count,
  count(*) FILTER (WHERE state = 'canceled') AS canceled_count
FROM run_instances
WHERE run_id = $1
`, runID).Scan(&pending, &running, &succeeded, &testFailed, &infraFailed, &canceled)
	if err != nil {
		return domain.RunCounts{}, fmt.Errorf("query run counts: %w", err)
	}
	return domain.RunCounts{
		Pending:     pending,
		Running:     running,
		Succeeded:   succeeded,
		TestFailed:  testFailed,
		InfraFailed: infraFailed,
		Canceled:    canceled,
	}, nil
}

func marshalJSONMap(m map[string]any) ([]byte, error) {
	if len(m) == 0 {
		return nil, nil
	}
	b, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("marshal json map: %w", err)
	}
	return b, nil
}

func defaultArtifactID(instanceID, attemptID string, idx int) string {
	attempt := strings.TrimSpace(attemptID)
	if attempt == "" {
		return fmt.Sprintf("art-%s-%03d", instanceID, idx+1)
	}
	return fmt.Sprintf("art-%s-%s-%03d", instanceID, attempt, idx+1)
}

func unmarshalJSONMap(raw []byte) (map[string]any, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	out := map[string]any{}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("decode json map: %w", err)
	}
	return out, nil
}

func toRunStatePtr(v *string) *domain.RunState {
	if v == nil {
		return nil
	}
	s := domain.RunState(*v)
	return &s
}

func toInstanceStatePtr(v *string) *domain.InstanceState {
	if v == nil {
		return nil
	}
	s := domain.InstanceState(*v)
	return &s
}

func derefString(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

func nullableName(v string) any {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	return v
}

func nullableString(v string) any {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	return v
}

func usageColumns(metrics *usage.Metrics) (*int64, *int64, *int64) {
	if metrics == nil {
		return nil, nil, nil
	}
	return metrics.InputTokens, metrics.OutputTokens, metrics.ToolCalls
}

func nullableInt64(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}

func coalesce(v, fallback string) string {
	if strings.TrimSpace(v) != "" {
		return v
	}
	return fallback
}
