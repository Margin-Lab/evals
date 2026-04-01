package api

import (
	"encoding/base64"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/marginlab/margin-eval/agent-server/internal/agentruntime"
	"github.com/marginlab/margin-eval/agent-server/internal/apperr"
	"github.com/marginlab/margin-eval/agent-server/internal/config"
	"github.com/marginlab/margin-eval/agent-server/internal/fsutil"
	"github.com/marginlab/margin-eval/agent-server/internal/run"
	"github.com/marginlab/margin-eval/agent-server/internal/state"
	"github.com/marginlab/margin-eval/runner/runner-core/agentdef"
	"nhooyr.io/websocket"
)

type Server struct {
	cfg        config.Config
	store      *state.Store
	runtime    *agentruntime.Runtime
	runManager *run.Manager

	mutationMu sync.Mutex
}

const (
	readyStatusReady    = "ready"
	readyStatusNotReady = "not_ready"

	readyReasonServerShuttingDown         = "SERVER_SHUTTING_DOWN"
	readyReasonStateDirNotWritable        = "STATE_DIR_NOT_WRITABLE"
	readyReasonBinDirNotWritable          = "BIN_DIR_NOT_WRITABLE"
	readyReasonWorkspacesDirNotAccessible = "WORKSPACES_DIR_NOT_ACCESSIBLE"
	readyReasonStateStoreNotReadable      = "STATE_STORE_NOT_READABLE"
	readyReasonDefinitionUnavailable      = "DEFINITION_UNAVAILABLE"
)

type readyFailure struct {
	reasonCode string
	message    string
}

func NewServer(cfg config.Config, st *state.Store, runtime *agentruntime.Runtime, runManager *run.Manager) *Server {
	return &Server{
		cfg:        cfg,
		store:      st,
		runtime:    runtime,
		runManager: runManager,
	}
}

func (s *Server) Router() http.Handler {
	r := chi.NewRouter()

	r.Get("/healthz", s.handleHealthz)
	r.Get("/readyz", s.handleReadyz)

	r.Route("/v1", func(r chi.Router) {
		r.Get("/state", s.handleGetState)
		r.Put("/agent-definition", s.handlePutAgentDefinition)
		r.Put("/agent-config", s.handlePutAgentConfig)
		r.Post("/agent/install", s.handlePostAgentInstall)
		r.Post("/run", s.handlePostRun)
		r.Get("/run", s.handleGetRun)
		r.Get("/run/trajectory", s.handleGetRunTrajectory)
		r.Delete("/run", s.handleDeleteRun)
		r.Post("/run/snapshot", s.handlePostRunSnapshot)
		r.Get("/run/pty", s.handleRunPTY)
	})

	return r
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, healthResponse{Status: "ok"})
}

func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	checks := make(map[string]readyCheck)
	failures := make([]readyFailure, 0, 4)
	markReady := func(name string) {
		checks[name] = readyCheck{Status: readyStatusReady}
	}
	markNotReady := func(name, reasonCode, message string, details map[string]any) {
		checks[name] = readyCheck{
			Status:     readyStatusNotReady,
			ReasonCode: reasonCode,
			Message:    message,
			Details:    details,
		}
		failures = append(failures, readyFailure{reasonCode: reasonCode, message: message})
	}

	if s.runManager.IsShuttingDown() {
		markNotReady("server.lifecycle", readyReasonServerShuttingDown, "server is shutting down", nil)
	} else {
		markReady("server.lifecycle")
	}
	if err := fsutil.IsWritableDir(s.cfg.StateDir); err != nil {
		markNotReady("storage.state_dir", readyReasonStateDirNotWritable, "state dir is not writable", map[string]any{
			"path":  s.cfg.StateDir,
			"error": err.Error(),
		})
	} else {
		markReady("storage.state_dir")
	}
	if err := fsutil.IsWritableDir(s.cfg.BinDir); err != nil {
		markNotReady("storage.bin_dir", readyReasonBinDirNotWritable, "bin dir is not writable", map[string]any{
			"path":  s.cfg.BinDir,
			"error": err.Error(),
		})
	} else {
		markReady("storage.bin_dir")
	}
	if _, err := os.Stat(s.cfg.WorkspacesDir); err != nil {
		markNotReady("storage.workspaces_dir", readyReasonWorkspacesDirNotAccessible, "workspaces dir is not accessible", map[string]any{
			"path":  s.cfg.WorkspacesDir,
			"error": err.Error(),
		})
	} else {
		markReady("storage.workspaces_dir")
	}

	st, err := s.store.Read()
	if err != nil {
		markNotReady("storage.state_store", readyReasonStateStoreNotReadable, "state store is not readable", map[string]any{"error": err.Error()})
	} else {
		markReady("storage.state_store")
		if st.Agent.Definition != nil {
			if info, statErr := os.Stat(st.Agent.Definition.DefinitionDir); statErr != nil || !info.IsDir() {
				if statErr == nil {
					statErr = os.ErrNotExist
				}
				markNotReady("agent.definition_dir", readyReasonDefinitionUnavailable, "loaded agent definition files are unavailable", map[string]any{
					"path":  st.Agent.Definition.DefinitionDir,
					"error": statErr.Error(),
				})
			} else {
				markReady("agent.definition_dir")
			}
		}
	}

	if len(failures) > 0 {
		summary, reasonCode := summarizeReadyFailures(failures)
		writeJSON(w, http.StatusServiceUnavailable, readyResponse{
			Status:     readyStatusNotReady,
			Summary:    summary,
			ReasonCode: reasonCode,
			Checks:     checks,
		})
		return
	}

	writeJSON(w, http.StatusOK, readyResponse{
		Status:  readyStatusReady,
		Summary: "all readiness checks passed",
		Checks:  checks,
	})
}

func summarizeReadyFailures(failures []readyFailure) (string, string) {
	if len(failures) == 0 {
		return "all readiness checks passed", ""
	}
	if len(failures) == 1 {
		return failures[0].message, failures[0].reasonCode
	}
	return failures[0].message, failures[0].reasonCode
}

func (s *Server) handleGetState(w http.ResponseWriter, r *http.Request) {
	st, err := s.store.Read()
	if err != nil {
		apperr.Write(w, apperr.NewInternal(apperr.CodeInternalError, "failed to read state", map[string]any{"error": err.Error()}))
		return
	}

	var runID *string
	if st.Run.RunID != "" {
		runID = &st.Run.RunID
	}

	caps := stateCapabilities{}
	if st.Agent.Definition != nil {
		manifest := st.Agent.Definition.Snapshot.Manifest
		caps.SupportsInstall = manifest.Install.CheckHook != nil || manifest.Install.RunHook != nil
		caps.SupportsSnapshot = manifest.Snapshot != nil
		caps.SupportsTrajectory = manifest.Trajectory != nil
		caps.SupportsSkills = manifest.Skills != nil
		caps.SupportsAgentsMD = manifest.AgentsMD != nil
		caps.SupportsUnifiedConfig = manifest.Config.Unified != nil
		caps.RequiredEnv = agentdef.ResolveDefinitionRequiredEnv(st.Agent.Definition.Snapshot)
		if st.Agent.Config != nil {
			if required, err := agentdef.ResolveRequiredEnvForConfigSnapshot(st.Agent.Definition.Snapshot, st.Agent.Config.Snapshot); err == nil {
				caps.RequiredEnv = required
			}
		}
		if manifest.AgentsMD != nil {
			caps.AgentsMDFilename = manifest.AgentsMD.Filename
		}
		if manifest.Config.Unified != nil {
			caps.AllowedModels = append([]string(nil), manifest.Config.Unified.AllowedModels...)
			caps.AllowedReasoningLevels = append([]string(nil), manifest.Config.Unified.AllowedReasoningLevels...)
		}
	}

	writeJSON(w, http.StatusOK, getStateResponse{
		Agent: st.Agent,
		Run: stateRunSummary{
			State: st.Run.State,
			RunID: runID,
		},
		Paths: statePaths{
			Root:       s.cfg.RootDir,
			Bin:        s.cfg.BinDir,
			State:      s.cfg.StateDir,
			Workspaces: s.cfg.WorkspacesDir,
		},
		Capabilities: caps,
		ShuttingDown: s.runManager.IsShuttingDown(),
	})
}

func (s *Server) handlePutAgentDefinition(w http.ResponseWriter, r *http.Request) {
	unlock, err := s.beginMutation()
	if err != nil {
		apperr.Write(w, err)
		return
	}
	defer unlock()

	var req putAgentDefinitionRequest
	if err := decodeJSON(r, &req); err != nil {
		apperr.Write(w, apperr.NewBadRequest(apperr.CodeInvalidRequest, err.Error(), nil))
		return
	}
	req, err = validatePutAgentDefinitionRequest(req)
	if err != nil {
		apperr.Write(w, err)
		return
	}

	record, err := s.runtime.LoadDefinition(req.Definition)
	if err != nil {
		apperr.Write(w, internalMutationError(apperr.CodeInvalidAgent, "failed to materialize agent definition", err))
		return
	}

	statusCode := http.StatusCreated
	updated, err := s.store.Update(func(st *state.ServerState) error {
		if err := state.ValidateLoadDefinitionTransition(*st); err != nil {
			return err
		}
		if st.Agent.Definition != nil &&
			st.Agent.Definition.PackageHash == record.PackageHash &&
			agentName(st.Agent) == record.Snapshot.Manifest.Name {
			statusCode = http.StatusOK
		}
		st.Agent = state.AgentRecord{
			State:      state.AgentStateDefinitionLoaded,
			Definition: &record,
		}
		return nil
	})
	if err != nil {
		apperr.Write(w, err)
		return
	}

	writeJSON(w, statusCode, putAgentDefinitionResponse{
		State:      updated.Agent.State,
		Definition: updated.Agent.Definition,
	})
}

func (s *Server) handlePostAgentInstall(w http.ResponseWriter, r *http.Request) {
	unlock, err := s.beginMutation()
	if err != nil {
		apperr.Write(w, err)
		return
	}
	defer unlock()

	current, err := s.store.Read()
	if err != nil {
		apperr.Write(w, apperr.NewInternal(apperr.CodeInternalError, "failed to read state", map[string]any{"error": err.Error()}))
		return
	}
	if err := state.ValidateInstallTransition(current); err != nil {
		apperr.Write(w, err)
		return
	}

	installInfo, err := s.runtime.Install(r.Context(), current.Agent)
	if err != nil {
		apperr.Write(w, unavailableMutationError(apperr.CodeInstallFailed, "failed to install agent definition", err))
		return
	}
	expectedName := agentName(current.Agent)
	expectedState := current.Agent.State

	updated, err := s.store.Update(func(st *state.ServerState) error {
		if agentName(st.Agent) != expectedName || st.Agent.State != expectedState {
			return staleAgentStateError(expectedName, expectedState, st.Agent)
		}
		if err := state.ValidateInstallTransition(*st); err != nil {
			return err
		}
		targetState := state.AgentStateInstalled
		if st.Agent.Config != nil {
			targetState = state.AgentStateConfigured
		}
		if err := state.ValidateAgentStateTransition(st.Agent.State, targetState); err != nil {
			return apperr.NewConflict(apperr.CodeInvalidAgentState, err.Error(), nil)
		}
		st.Agent.Install = &installInfo
		st.Agent.State = targetState
		return nil
	})
	if err != nil {
		apperr.Write(w, err)
		return
	}

	writeJSON(w, http.StatusOK, postAgentInstallResponse{
		State:   updated.Agent.State,
		Install: updated.Agent.Install,
	})
}

func (s *Server) handlePutAgentConfig(w http.ResponseWriter, r *http.Request) {
	unlock, err := s.beginMutation()
	if err != nil {
		apperr.Write(w, err)
		return
	}
	defer unlock()

	var req putAgentConfigRequest
	if err := decodeJSON(r, &req); err != nil {
		apperr.Write(w, apperr.NewBadRequest(apperr.CodeInvalidRequest, err.Error(), nil))
		return
	}
	req, err = validatePutAgentConfigRequest(req)
	if err != nil {
		apperr.Write(w, err)
		return
	}

	current, err := s.store.Read()
	if err != nil {
		apperr.Write(w, apperr.NewInternal(apperr.CodeInternalError, "failed to read state", map[string]any{"error": err.Error()}))
		return
	}
	if err := state.ValidateConfigureTransition(current); err != nil {
		apperr.Write(w, err)
		return
	}
	if current.Agent.Definition == nil {
		apperr.Write(w, apperr.NewConflict(apperr.CodeAgentNotConfigured, "agent definition must be loaded before configuration", nil))
		return
	}

	normalized, err := s.runtime.ValidateConfig(*current.Agent.Definition, req.Config)
	if err != nil {
		apperr.Write(w, invalidMutationError(apperr.CodeConfigValidation, "agent config validation failed", err))
		return
	}

	expectedName := agentName(current.Agent)
	expectedState := current.Agent.State
	statusCode := http.StatusCreated
	if expectedState == state.AgentStateConfigured {
		statusCode = http.StatusOK
	}

	updated, err := s.store.Update(func(st *state.ServerState) error {
		if agentName(st.Agent) != expectedName || st.Agent.State != expectedState {
			return staleAgentStateError(expectedName, expectedState, st.Agent)
		}
		if err := state.ValidateConfigureTransition(*st); err != nil {
			return err
		}
		if err := state.ValidateAgentStateTransition(st.Agent.State, state.AgentStateConfigured); err != nil {
			return apperr.NewConflict(apperr.CodeInvalidAgentState, err.Error(), nil)
		}
		st.Agent.Config = &state.ConfigRecord{Snapshot: normalized}
		st.Agent.State = state.AgentStateConfigured
		return nil
	})
	if err != nil {
		apperr.Write(w, err)
		return
	}

	writeJSON(w, statusCode, putAgentConfigResponse{
		State:  updated.Agent.State,
		Config: updated.Agent.Config.Snapshot,
	})
}

func (s *Server) handlePostRun(w http.ResponseWriter, r *http.Request) {
	unlock, err := s.beginMutation()
	if err != nil {
		apperr.Write(w, err)
		return
	}
	defer unlock()

	var req run.StartRequest
	if err := decodeJSON(r, &req); err != nil {
		apperr.Write(w, apperr.NewBadRequest(apperr.CodeInvalidRequest, err.Error(), nil))
		return
	}
	req, err = validateStartRunRequest(req, s.cfg)
	if err != nil {
		apperr.Write(w, err)
		return
	}

	resp, err := s.runManager.StartRun(r.Context(), req)
	if err != nil {
		apperr.Write(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, resp)
}

func (s *Server) handleGetRun(w http.ResponseWriter, r *http.Request) {
	runState, err := s.runManager.GetRun()
	if err != nil {
		apperr.Write(w, err)
		return
	}
	writeJSON(w, http.StatusOK, runState)
}

func (s *Server) handleGetRunTrajectory(w http.ResponseWriter, r *http.Request) {
	payload, err := s.runManager.GetTrajectory()
	if err != nil {
		apperr.Write(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(payload)
}

func (s *Server) handleDeleteRun(w http.ResponseWriter, r *http.Request) {
	unlock, err := s.beginMutation()
	if err != nil {
		apperr.Write(w, err)
		return
	}
	defer unlock()

	runState, err := s.runManager.DeleteRun(r.Context())
	if err != nil {
		apperr.Write(w, err)
		return
	}
	writeJSON(w, http.StatusOK, deleteRunResponse{State: runState.State})
}

func (s *Server) handlePostRunSnapshot(w http.ResponseWriter, r *http.Request) {
	var req postRunSnapshotRequest
	if err := decodeJSON(r, &req); err != nil {
		apperr.Write(w, apperr.NewBadRequest(apperr.CodeInvalidRequest, err.Error(), nil))
		return
	}
	req, err := validatePostRunSnapshotRequest(req)
	if err != nil {
		apperr.Write(w, err)
		return
	}

	resp, err := s.runManager.CaptureSnapshot(r.Context(), run.SnapshotRequest{
		RunID: req.RunID,
		PTY:   req.PTY,
	})
	if err != nil {
		apperr.Write(w, err)
		return
	}

	writeJSON(w, http.StatusOK, postRunSnapshotResponse{
		RunID:           resp.RunID,
		Agent:           resp.Agent,
		RunState:        resp.RunState,
		CapturedAt:      resp.CapturedAt.Format(time.RFC3339),
		ContentType:     "ansi",
		ContentEncoding: "base64",
		Content:         base64.StdEncoding.EncodeToString(resp.Content),
		Truncated:       resp.Truncated,
	})
}

func (s *Server) handleRunPTY(w http.ResponseWriter, r *http.Request) {
	runID, err := validateRunPTYQuery(r.URL.Query().Get("run_id"))
	if err != nil {
		apperr.Write(w, err)
		return
	}

	hub, err := s.runManager.GetPTYHub(runID)
	if err != nil {
		apperr.Write(w, err)
		return
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		CompressionMode: websocket.CompressionDisabled,
	})
	if err != nil {
		return
	}

	if err := hub.ServeConn(r.Context(), conn); err != nil {
		_ = conn.Close(websocket.StatusInternalError, "pty stream error")
		return
	}
}

func (s *Server) beginMutation() (func(), error) {
	s.mutationMu.Lock()
	if s.runManager.IsShuttingDown() {
		s.mutationMu.Unlock()
		return nil, apperr.New(http.StatusServiceUnavailable, apperr.CodeServerShuttingDown, "server is shutting down", nil)
	}
	return func() {
		s.mutationMu.Unlock()
	}, nil
}
