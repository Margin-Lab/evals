package run

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/marginlab/margin-eval/agent-server/internal/agentruntime"
	"github.com/marginlab/margin-eval/agent-server/internal/apperr"
	"github.com/marginlab/margin-eval/agent-server/internal/state"
	"github.com/google/uuid"
)

func (m *Manager) CaptureSnapshot(ctx context.Context, req SnapshotRequest) (SnapshotResponse, error) {
	req.RunID = strings.TrimSpace(req.RunID)
	if req.RunID == "" {
		return SnapshotResponse{}, apperr.NewBadRequest(apperr.CodeInvalidRunID, "run_id is required", nil)
	}
	if req.PTY.Cols <= 0 || req.PTY.Rows <= 0 {
		return SnapshotResponse{}, apperr.NewBadRequest(apperr.CodeInvalidPTY, "pty cols/rows must be > 0", nil)
	}

	m.snapshotMu.Lock()
	defer m.snapshotMu.Unlock()

	st, err := m.store.Read()
	if err != nil {
		return SnapshotResponse{}, apperr.NewInternal(apperr.CodeInternalError, "failed to read state", map[string]any{"error": err.Error()})
	}
	if !st.Run.HasRun() {
		return SnapshotResponse{}, apperr.NewConflict(apperr.CodeRunNotActive, "no run is available for snapshot", nil)
	}
	if !state.IsRunActive(st.Run.State) && st.Run.State != state.RunStateExited {
		return SnapshotResponse{}, apperr.NewConflict(apperr.CodeRunNotActive, "run snapshot is only available for active or exited runs", map[string]any{
			"run_state": st.Run.State,
		})
	}
	if st.Run.RunID != req.RunID {
		return SnapshotResponse{}, apperr.NewConflict(apperr.CodeRunIDMismatch, "run_id does not match current run", map[string]any{
			"active_run_id": st.Run.RunID,
			"requested":     req.RunID,
		})
	}
	if !m.runtime.SupportsSnapshot(st.Agent) {
		return SnapshotResponse{}, apperr.NewBadRequest(apperr.CodeSnapshotUnsupported, "snapshot is not supported for selected agent", map[string]any{
			"agent": agentName(st.Agent),
		})
	}
	if st.Run.State == state.RunStateExited {
		content, err := m.readExitedRunPTYSnapshot(req.RunID)
		if err == nil && len(content) > 0 {
			return SnapshotResponse{
				RunID:      req.RunID,
				Agent:      agentName(st.Agent),
				RunState:   st.Run.State,
				CapturedAt: time.Now().UTC(),
				Content:    content,
				Truncated:  false,
			}, nil
		}
	}

	runCtx, err := m.snapshotRunContext(st)
	if err != nil {
		return SnapshotResponse{}, err
	}

	execSpec, err := m.runtime.PrepareSnapshot(ctx, st.Agent, runCtx)
	if err != nil {
		return SnapshotResponse{}, internalError(apperr.CodeSnapshotFailed, "prepare snapshot command failed", map[string]any{"error": err.Error()}, err)
	}

	out, err := m.snapshotter.Capture(ctx, snapshotCaptureInput{
		ExecSpec: execSpec,
		PTY:      req.PTY,
	})
	if err != nil {
		return SnapshotResponse{}, mapRunError(err)
	}

	return SnapshotResponse{
		RunID:      req.RunID,
		Agent:      agentName(st.Agent),
		RunState:   st.Run.State,
		CapturedAt: time.Now().UTC(),
		Content:    out.Content,
		Truncated:  out.Truncated,
	}, nil
}

func (m *Manager) readExitedRunPTYSnapshot(runID string) ([]byte, error) {
	ptyLogPath := filepath.Join(m.cfg.StateDir, "runs", runID, "artifacts", "pty.log")
	content, err := os.ReadFile(ptyLogPath)
	if err != nil {
		return nil, err
	}
	if len(content) == 0 {
		return nil, os.ErrNotExist
	}
	return content, nil
}

func (m *Manager) snapshotRunContext(st state.ServerState) (agentruntime.RunContext, error) {
	runID := st.Run.RunID

	if state.IsRunActive(st.Run.State) {
		m.mu.Lock()
		active := m.active
		m.mu.Unlock()
		if active != nil && active.runID == runID {
			return cloneRunContext(active.runContext), nil
		}
		return agentruntime.RunContext{}, apperr.NewConflict(apperr.CodeRunNotActive, "active run snapshot context is unavailable", nil)
	}
	if st.Agent.Definition == nil || st.Agent.Install == nil || st.Agent.Config == nil {
		return agentruntime.RunContext{}, apperr.NewConflict(apperr.CodeAgentNotConfigured, "agent state is incomplete for snapshot", nil)
	}

	cwd := strings.TrimSpace(st.Run.CWD)
	if cwd == "" {
		return agentruntime.RunContext{}, apperr.NewInternal(apperr.CodeSnapshotFailed, "run cwd is unavailable", nil)
	}
	if info, err := os.Stat(cwd); err != nil || !info.IsDir() {
		if err == nil {
			err = os.ErrNotExist
		}
		return agentruntime.RunContext{}, apperr.New(http.StatusServiceUnavailable, apperr.CodeSnapshotFailed, "run cwd is unavailable for snapshot", map[string]any{
			"cwd":   cwd,
			"error": err.Error(),
		})
	}

	runHome := filepath.Join(m.cfg.StateDir, "runs", runID, "home")
	if info, err := os.Stat(runHome); err != nil || !info.IsDir() {
		if err == nil {
			err = os.ErrNotExist
		}
		return agentruntime.RunContext{}, apperr.New(http.StatusServiceUnavailable, apperr.CodeSnapshotFailed, "run home is unavailable for snapshot", map[string]any{
			"run_home": runHome,
			"error":    err.Error(),
		})
	}

	requiredEnv, err := resolveRequiredEnv(m.runtime.RequiredEnv(st.Agent), st.Run.Env, authFilesFromRunRecord(st.Run.AuthFiles))
	if err != nil {
		return agentruntime.RunContext{}, mapRunError(err)
	}
	env := mergeEnvironment(os.Environ(), st.Run.Env)
	env = mergeEnvironmentMap(env, requiredEnv)
	applyRunEnvironmentDefaults(env, runHome)

	sessionID := uuid.NewSHA1(uuid.NameSpaceURL, []byte(runID)).String()
	return agentruntime.RunContext{
		RunID:             runID,
		SessionID:         sessionID,
		SnapshotSessionID: sessionID,
		CWD:               cwd,
		RunHome:           runHome,
		ArtifactsDir:      filepath.Join(m.cfg.StateDir, "runs", runID, "artifacts"),
		Env:               env,
	}, nil
}

func cloneRunContext(in agentruntime.RunContext) agentruntime.RunContext {
	out := in
	out.Env = cloneStringMap(in.Env)
	out.RunArgs = append([]string(nil), in.RunArgs...)
	if strings.TrimSpace(out.SnapshotSessionID) == "" {
		out.SnapshotSessionID = out.SessionID
	}
	return out
}
