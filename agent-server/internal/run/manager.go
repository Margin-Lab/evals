package run

import (
	"context"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/marginlab/margin-eval/agent-server/internal/agentruntime"
	"github.com/marginlab/margin-eval/agent-server/internal/apperr"
	"github.com/marginlab/margin-eval/agent-server/internal/config"
	"github.com/marginlab/margin-eval/agent-server/internal/fsutil"
	"github.com/marginlab/margin-eval/agent-server/internal/logutil"
	"github.com/marginlab/margin-eval/agent-server/internal/ptyws"
	"github.com/marginlab/margin-eval/agent-server/internal/state"
	"github.com/google/uuid"
)

type Manager struct {
	cfg     config.Config
	store   *state.Store
	runtime *agentruntime.Runtime

	launcher    *launcher
	supervisor  *supervisor
	collector   *collector
	snapshotter *snapshotter

	mu           sync.Mutex
	active       *activeRun
	shuttingDown bool
	snapshotMu   sync.Mutex
}

func NewManager(cfg config.Config, store *state.Store, runtime *agentruntime.Runtime) *Manager {
	return &Manager{
		cfg:         cfg,
		store:       store,
		runtime:     runtime,
		launcher:    newLauncher(cfg, runtime),
		supervisor:  newSupervisor(cfg),
		collector:   newCollector(cfg, runtime),
		snapshotter: newSnapshotter(cfg),
	}
}

func (m *Manager) IsShuttingDown() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.shuttingDown
}

func (m *Manager) StartRun(ctx context.Context, req StartRequest) (StartResponse, error) {
	if strings.TrimSpace(req.CWD) == "" {
		return StartResponse{}, apperr.NewBadRequest(apperr.CodeInvalidCWD, "cwd is required", nil)
	}
	if strings.TrimSpace(req.InitialPrompt) == "" {
		return StartResponse{}, apperr.NewBadRequest(apperr.CodeInvalidInitialPrompt, "initial_prompt is required", nil)
	}
	validatedCWD, err := fsutil.ValidateExistingDirUnderRoot(req.CWD, m.cfg.WorkspacesDir)
	if err != nil {
		return StartResponse{}, apperr.NewBadRequest(apperr.CodeInvalidCWD, err.Error(), nil)
	}
	req.CWD = validatedCWD

	m.mu.Lock()
	if m.shuttingDown {
		m.mu.Unlock()
		return StartResponse{}, apperr.New(http.StatusServiceUnavailable, apperr.CodeServerShuttingDown, "server is shutting down", nil)
	}
	m.mu.Unlock()

	runID := "r_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	startedAt := time.Now().UTC()

	currentState, err := m.store.Update(func(s *state.ServerState) error {
		if err := state.ValidateStartRunTransition(*s); err != nil {
			return err
		}
		if err := state.ValidateRunStateTransition(s.Run.State, state.RunStateStarting); err != nil {
			return apperr.NewConflict(apperr.CodeInvalidRunState, err.Error(), nil)
		}
		trajectoryStatus := initialTrajectoryStatus(s.Agent)
		if req.DryRun {
			trajectoryStatus = state.TrajectoryStatusNone
		}
		s.Run = state.RunRecord{
			State:            state.RunStateStarting,
			RunID:            runID,
			StartedAt:        &startedAt,
			CWD:              req.CWD,
			Env:              cloneStringMap(req.Env),
			AuthFiles:        cloneRunAuthFileRecords(req.AuthFiles),
			TrajectoryStatus: trajectoryStatus,
		}
		return nil
	})
	if err != nil {
		return StartResponse{}, err
	}

	launchInput := launchInput{
		RunID:      runID,
		StartedAt:  startedAt,
		Request:    req,
		AgentState: currentState.Agent,
	}
	prepared, launchErr := m.launcher.Prepare(ctx, launchInput)
	if launchErr != nil {
		if rollbackErr := m.rollbackStartingRun(runID); rollbackErr != nil {
			logutil.Error("run.rollback_starting_failed", map[string]any{"run_id": runID, "error": rollbackErr.Error()})
		}
		logutil.Error("run.launch_failed", map[string]any{"run_id": runID, "error": launchErr.Error()})
		return StartResponse{}, mapRunError(launchErr)
	}
	if req.DryRun {
		endedAt := time.Now().UTC()
		if err := m.transitionRunToExited(runID, endedAt, 0, nil, state.TrajectoryStatusNone); err != nil {
			if rollbackErr := m.rollbackStartingRun(runID); rollbackErr != nil {
				logutil.Error("run.rollback_starting_failed", map[string]any{"run_id": runID, "error": rollbackErr.Error()})
			}
			return StartResponse{}, err
		}
		logDryRun(currentState.Agent, prepared.runCtx)
		return buildStartResponse(runID, state.RunStateExited, startedAt, 0), nil
	}
	active, launchErr := m.launcher.startPrepared(launchInput, prepared)
	if launchErr != nil {
		if rollbackErr := m.rollbackStartingRun(runID); rollbackErr != nil {
			logutil.Error("run.rollback_starting_failed", map[string]any{"run_id": runID, "error": rollbackErr.Error()})
		}
		logutil.Error("run.launch_failed", map[string]any{"run_id": runID, "error": launchErr.Error()})
		return StartResponse{}, mapRunError(launchErr)
	}

	if err := m.transitionRunToRunning(runID, active.cmd.Process.Pid); err != nil {
		_ = m.supervisor.forceKill(active)
		_ = active.ptyFile.Close()
		_ = active.ptyLogFile.Close()
		if rollbackErr := m.rollbackStartingRun(runID); rollbackErr != nil {
			logutil.Error("run.rollback_starting_failed", map[string]any{"run_id": runID, "error": rollbackErr.Error()})
		}
		return StartResponse{}, err
	}

	m.mu.Lock()
	m.active = active
	shuttingDown := m.shuttingDown
	m.mu.Unlock()

	go m.supervisor.streamPTYOutput(active)
	go m.finalizeRun(active)

	if shuttingDown {
		stopCtx, cancel := context.WithTimeout(context.Background(), m.cfg.StopGraceTimeout+5*time.Second)
		defer cancel()
		_ = m.supervisor.stop(stopCtx, active)
		<-active.finalizedCh
		return StartResponse{}, apperr.New(http.StatusServiceUnavailable, apperr.CodeServerShuttingDown, "server is shutting down", nil)
	}

	logAgentStart(active)

	return buildStartResponse(runID, state.RunStateRunning, startedAt, active.cmd.Process.Pid), nil
}

func (m *Manager) GetRun() (state.RunRecord, error) {
	st, err := m.store.Read()
	if err != nil {
		return state.RunRecord{}, apperr.NewInternal(apperr.CodeInternalError, "failed to read state", map[string]any{"error": err.Error()})
	}
	if st.Run.State == state.RunStateIdle && !st.Run.HasRun() {
		return state.RunRecord{}, apperr.NewNotFound(apperr.CodeRunNotActive, "no active run", nil)
	}
	return st.Run, nil
}

func (m *Manager) GetTrajectory() ([]byte, error) {
	st, err := m.store.Read()
	if err != nil {
		return nil, apperr.NewInternal(apperr.CodeInternalError, "failed to read state", map[string]any{"error": err.Error()})
	}
	if st.Run.State == state.RunStateIdle && !st.Run.HasRun() {
		return nil, apperr.NewNotFound(apperr.CodeRunNotActive, "no active run", nil)
	}
	if st.Run.State != state.RunStateExited {
		return nil, apperr.NewConflict(apperr.CodeInvalidRunState, "run trajectory is only available after exit", map[string]any{
			"run_state": st.Run.State,
		})
	}
	if st.Run.TrajectoryStatus != state.TrajectoryStatusComplete {
		return nil, apperr.NewNotFound(apperr.CodeTrajectoryUnavailable, "trajectory is not available for the current run", map[string]any{
			"trajectory_status": st.Run.TrajectoryStatus,
		})
	}

	path := trajectoryPathForRun(m.cfg.StateDir, st.Run.RunID)
	payload, err := os.ReadFile(path)
	if err != nil {
		return nil, apperr.NewInternal(apperr.CodeInternalError, "failed to read trajectory artifact", map[string]any{
			"path":  path,
			"error": err.Error(),
		})
	}
	return payload, nil
}

func (m *Manager) DeleteRun(ctx context.Context) (state.RunRecord, error) {
	st, err := m.store.Read()
	if err != nil {
		return state.RunRecord{}, apperr.NewInternal(apperr.CodeInternalError, "failed to read state", map[string]any{"error": err.Error()})
	}
	if st.Run.State == state.RunStateIdle && !st.Run.HasRun() {
		return st.Run, nil
	}
	if state.IsRunActive(st.Run.State) {
		m.mu.Lock()
		active := m.active
		m.mu.Unlock()
		if active == nil {
			return state.RunRecord{}, apperr.NewInternal(apperr.CodeInternalError, "active run handle missing", nil)
		}
		if err := m.supervisor.stop(ctx, active); err != nil {
			return state.RunRecord{}, mapRunError(err)
		}
		<-active.finalizedCh

		st, err = m.store.Read()
		if err != nil {
			return state.RunRecord{}, apperr.NewInternal(apperr.CodeInternalError, "failed to read state", map[string]any{"error": err.Error()})
		}
		if st.Run.State == state.RunStateIdle && !st.Run.HasRun() {
			return st.Run, nil
		}
	}

	expectedRunID := st.Run.RunID
	expectedRunState := st.Run.State
	updated, err := m.store.Update(func(s *state.ServerState) error {
		if s.Run.State == state.RunStateIdle && !s.Run.HasRun() {
			return nil
		}
		if s.Run.RunID != expectedRunID || s.Run.State != expectedRunState {
			return staleRunStateError(expectedRunID, s.Run.RunID)
		}
		if err := state.ValidateRunClearTransition(*s); err != nil {
			return err
		}
		s.Run = state.RunRecord{State: state.RunStateIdle}
		return nil
	})
	if err != nil {
		return state.RunRecord{}, err
	}
	return updated.Run, nil
}

func (m *Manager) GetPTYHub(runID string) (*ptyws.Hub, error) {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return nil, apperr.NewBadRequest(apperr.CodeInvalidRunID, "run_id is required", nil)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active == nil || m.active.runID != runID {
		return nil, apperr.NewConflict(apperr.CodeRunNotActive, "active run stream is unavailable", nil)
	}
	return m.active.hub, nil
}

func (m *Manager) Shutdown(ctx context.Context) error {
	m.mu.Lock()
	m.shuttingDown = true
	active := m.active
	m.mu.Unlock()

	logutil.Info("server.shutdown_begin", map[string]any{"shutting_down": true})
	if active != nil {
		if err := m.supervisor.stop(ctx, active); err != nil {
			return mapRunError(err)
		}
		<-active.finalizedCh
	}
	logutil.Info("server.shutdown_complete", map[string]any{"shutting_down": true})
	return nil
}

func (m *Manager) finalizeRun(active *activeRun) {
	defer close(active.finalizedCh)
	defer func() {
		if active.watchCancel != nil {
			active.watchCancel()
		}
		_ = active.ptyFile.Close()
		_ = active.ptyLogFile.Close()
		m.mu.Lock()
		if m.active != nil && m.active.runID == active.runID {
			m.active = nil
		}
		m.mu.Unlock()
	}()

	exitCode, signal, endedAt := m.supervisor.wait(active)
	if err := m.transitionRunToCollecting(active.runID, endedAt, exitCode, signal); err != nil {
		logutil.Error("run.transition_collecting_failed", map[string]any{"run_id": active.runID, "error": err.Error()})
		active.hub.NotifyExit(exitCode)
		return
	}

	logutil.Info("run.collecting_trajectory", map[string]any{"run_id": active.runID})
	payload, status, collectErr := m.collector.collect(active)
	if collectErr != nil {
		logutil.Error("run.collect_trajectory_failed", map[string]any{"run_id": active.runID, "error": collectErr.Error()})
	}
	if err := payload.persist(); err != nil {
		logutil.Error("run.persist_trajectory_artifact_failed", map[string]any{"run_id": active.runID, "error": err.Error()})
		status = state.TrajectoryStatusFailed
	}
	if err := m.persistFinalRunTrajectory(active.runID, status); err != nil {
		logutil.Error("run.persist_trajectory_failed", map[string]any{"run_id": active.runID, "error": err.Error()})
	}

	active.hub.NotifyExit(exitCode)
	logutil.Info("run.exited", map[string]any{
		"run_id":            active.runID,
		"exit_code":         exitCode,
		"signal":            signal,
		"trajectory_status": status,
	})
}

func (m *Manager) transitionRunToRunning(runID string, pid int) error {
	_, err := m.store.Update(func(s *state.ServerState) error {
		if s.Run.RunID != runID {
			return staleRunStateError(runID, s.Run.RunID)
		}
		if err := state.ValidateRunStateTransition(s.Run.State, state.RunStateRunning); err != nil {
			return apperr.NewConflict(apperr.CodeInvalidRunState, err.Error(), map[string]any{"run_state": s.Run.State})
		}
		s.Run.State = state.RunStateRunning
		s.Run.PID = &pid
		return nil
	})
	return err
}

func (m *Manager) transitionRunToExited(
	runID string,
	endedAt time.Time,
	exitCode int,
	signal *string,
	trajectoryStatus state.TrajectoryStatus,
) error {
	_, err := m.store.Update(func(s *state.ServerState) error {
		if s.Run.RunID != runID {
			return staleRunStateError(runID, s.Run.RunID)
		}
		if err := state.ValidateRunStateTransition(s.Run.State, state.RunStateExited); err != nil {
			return apperr.NewConflict(apperr.CodeInvalidRunState, err.Error(), map[string]any{"run_state": s.Run.State})
		}
		s.Run.State = state.RunStateExited
		s.Run.EndedAt = &endedAt
		s.Run.ExitCode = &exitCode
		s.Run.Signal = signal
		s.Run.TrajectoryStatus = trajectoryStatus
		return nil
	})
	return err
}

func (m *Manager) transitionRunToCollecting(runID string, endedAt time.Time, exitCode int, signal *string) error {
	_, err := m.store.Update(func(s *state.ServerState) error {
		if s.Run.RunID != runID {
			return staleRunStateError(runID, s.Run.RunID)
		}
		if err := state.ValidateRunStateTransition(s.Run.State, state.RunStateCollecting); err != nil {
			return apperr.NewConflict(apperr.CodeInvalidRunState, err.Error(), map[string]any{"run_state": s.Run.State})
		}
		s.Run.State = state.RunStateCollecting
		s.Run.EndedAt = &endedAt
		s.Run.ExitCode = &exitCode
		s.Run.Signal = signal
		if s.Run.TrajectoryStatus != state.TrajectoryStatusNone {
			s.Run.TrajectoryStatus = state.TrajectoryStatusCollecting
		}
		return nil
	})
	return err
}

func (m *Manager) persistFinalRunTrajectory(runID string, status state.TrajectoryStatus) error {
	_, err := m.store.Update(func(s *state.ServerState) error {
		if s.Run.RunID != runID {
			return staleRunStateError(runID, s.Run.RunID)
		}
		if err := state.ValidateRunStateTransition(s.Run.State, state.RunStateExited); err != nil {
			return apperr.NewConflict(apperr.CodeInvalidRunState, err.Error(), map[string]any{"run_state": s.Run.State})
		}
		s.Run.State = state.RunStateExited
		s.Run.TrajectoryStatus = status
		return nil
	})
	return err
}

func (m *Manager) rollbackStartingRun(runID string) error {
	_, err := m.store.Update(func(s *state.ServerState) error {
		if s.Run.RunID != runID {
			return staleRunStateError(runID, s.Run.RunID)
		}
		if s.Run.State != state.RunStateStarting {
			return apperr.NewConflict(apperr.CodeRunStateStale, "run state changed concurrently", map[string]any{
				"expected_run_id": runID,
				"actual_run_id":   s.Run.RunID,
				"run_state":       s.Run.State,
			})
		}
		if err := state.ValidateRunStateTransition(s.Run.State, state.RunStateIdle); err != nil {
			return apperr.NewConflict(apperr.CodeInvalidRunState, err.Error(), map[string]any{"run_state": s.Run.State})
		}
		s.Run = state.RunRecord{State: state.RunStateIdle}
		return nil
	})
	return err
}

func staleRunStateError(expected, actual string) error {
	return apperr.NewConflict(apperr.CodeRunStateStale, "run state changed concurrently", map[string]any{
		"expected_run_id": expected,
		"actual_run_id":   actual,
	})
}

func initialTrajectoryStatus(agent state.AgentRecord) state.TrajectoryStatus {
	if supportsTrajectory(agent) {
		return state.TrajectoryStatusPending
	}
	return state.TrajectoryStatusNone
}

func mapRunError(err error) error {
	if err == nil {
		return nil
	}
	if apiErr, ok := apperr.As(err); ok {
		return apiErr
	}
	rerr, ok := asError(err)
	if !ok {
		return apperr.NewInternal(apperr.CodeInternalError, "run operation failed", map[string]any{"error": err.Error()})
	}

	code := rerr.Code
	if code == "" {
		code = apperr.CodeInternalError
	}
	message := rerr.Message
	if message == "" {
		message = "run operation failed"
	}
	switch rerr.Kind {
	case ErrorKindInvalid:
		return apperr.NewBadRequest(code, message, rerr.Details)
	case ErrorKindConflict:
		return apperr.NewConflict(code, message, rerr.Details)
	case ErrorKindUnavailable:
		return apperr.New(http.StatusServiceUnavailable, code, message, rerr.Details)
	default:
		return apperr.NewInternal(code, message, rerr.Details)
	}
}
