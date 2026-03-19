package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type fakeServer struct {
	mu sync.Mutex

	agentName    string
	agentVersion string
	agentState   string
	requiredEnv  []string

	run *runState
}

type runState struct {
	RunID         string
	Cwd           string
	InitialPrompt string
	DryRun        bool
	StartedAt     time.Time
	EndedAt       *time.Time
	Duration      time.Duration
	ExitCode      *int

	ProvenancePath string
}

type putAgentDefinitionRequest struct {
	Definition struct {
		Manifest struct {
			Name string `json:"name"`
			Auth struct {
				RequiredEnv []string `json:"required_env"`
			} `json:"auth"`
		} `json:"manifest"`
	} `json:"definition"`
}

type putConfigRequest struct {
	Config struct {
		Name  string         `json:"name"`
		Input map[string]any `json:"input"`
	} `json:"config"`
}

type startRunRequest struct {
	Cwd           string `json:"cwd"`
	InitialPrompt string `json:"initial_prompt"`
	DryRun        bool   `json:"dry_run"`
}

func main() {
	addr := strings.TrimSpace(os.Getenv("FAKE_AGENT_LISTEN"))
	if addr == "" {
		addr = ":8080"
	}

	srv := &fakeServer{}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", srv.handleHealthz)
	mux.HandleFunc("/readyz", srv.handleReadyz)
	mux.HandleFunc("/v1/state", srv.handleState)
	mux.HandleFunc("/v1/agent-definition", srv.handlePutAgentDefinition)
	mux.HandleFunc("/v1/agent/install", srv.handleInstall)
	mux.HandleFunc("/v1/agent-config", srv.handlePutConfig)
	mux.HandleFunc("/v1/run", srv.handleRun)
	mux.HandleFunc("/v1/run/trajectory", srv.handleGetRunTrajectory)

	httpServer := &http.Server{Addr: addr, Handler: mux}
	log.Printf("fake-agent-server listening on %s", addr)
	if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("listen failed: %v", err)
	}
}

func (s *fakeServer) handleHealthz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed", nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

func (s *fakeServer) handleReadyz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed", nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ready"})
}

func (s *fakeServer) handleState(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed", nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"agent": map[string]any{
			"state": s.agentStateOrDefault(),
			"definition": map[string]any{
				"snapshot": map[string]any{
					"manifest": map[string]any{
						"name": s.agentNameOrDefault(),
					},
				},
			},
		},
		"paths": map[string]any{
			"bin": "/marginlab/bin",
		},
		"capabilities": map[string]any{
			"supports_install":    true,
			"supports_snapshot":   false,
			"supports_trajectory": true,
			"required_env":        append([]string(nil), s.requiredEnv...),
		},
	})
}

func (s *fakeServer) handlePutAgentDefinition(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		writeAPIError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed", nil)
		return
	}
	var req putAgentDefinitionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "INVALID_REQUEST", fmt.Sprintf("decode request: %v", err), nil)
		return
	}
	name := strings.TrimSpace(req.Definition.Manifest.Name)
	if name == "" {
		writeAPIError(w, http.StatusBadRequest, "INVALID_AGENT", "definition.manifest.name is required", nil)
		return
	}

	s.mu.Lock()
	s.agentName = name
	s.agentVersion = "latest"
	s.agentState = "definition_loaded"
	s.requiredEnv = append([]string(nil), req.Definition.Manifest.Auth.RequiredEnv...)
	s.mu.Unlock()

	writeJSON(w, http.StatusCreated, map[string]any{
		"state": map[string]any{
			"state": "definition_loaded",
		},
		"definition": map[string]any{
			"snapshot": map[string]any{
				"manifest": map[string]any{
					"name": name,
				},
			},
			"install_dir":  "/marginlab/bin/fake",
			"package_hash": "fake-package",
		},
	})
}

func (s *fakeServer) handleInstall(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed", nil)
		return
	}
	s.mu.Lock()
	s.agentState = "installed"
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"state": "installed",
		"install": map[string]any{
			"installed_at": time.Now().UTC().Format(time.RFC3339Nano),
			"result": map[string]any{
				"installed": true,
				"version":   s.agentVersionOrDefault(),
			},
		},
	})
}

func (s *fakeServer) handlePutConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		writeAPIError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed", nil)
		return
	}
	var req putConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "INVALID_REQUEST", fmt.Sprintf("decode request: %v", err), nil)
		return
	}
	if strings.TrimSpace(req.Config.Name) == "" {
		writeAPIError(w, http.StatusBadRequest, "INVALID_REQUEST", "config.name is required", nil)
		return
	}
	if req.Config.Input == nil {
		writeAPIError(w, http.StatusBadRequest, "INVALID_REQUEST", "config.input is required", nil)
		return
	}
	s.mu.Lock()
	s.agentState = "configured"
	s.mu.Unlock()
	writeJSON(w, http.StatusCreated, map[string]any{
		"state":  "configured",
		"config": req.Config,
	})
}

func (s *fakeServer) handleRun(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		s.handleStartRun(w, r)
	case http.MethodGet:
		s.handleGetRun(w, r)
	case http.MethodDelete:
		s.handleDeleteRun(w, r)
	default:
		writeAPIError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed", nil)
	}
}

func (s *fakeServer) handleStartRun(w http.ResponseWriter, r *http.Request) {
	var req startRunRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "INVALID_REQUEST", fmt.Sprintf("decode request: %v", err), nil)
		return
	}
	if strings.TrimSpace(req.InitialPrompt) == "" {
		writeAPIError(w, http.StatusBadRequest, "INVALID_REQUEST", "initial_prompt is required", nil)
		return
	}
	if strings.TrimSpace(req.Cwd) == "" {
		writeAPIError(w, http.StatusBadRequest, "INVALID_REQUEST", "cwd is required", nil)
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.run != nil {
		writeAPIError(w, http.StatusConflict, "RUN_ALREADY_ACTIVE", "run already active", nil)
		return
	}

	runID, err := newRunID()
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "INTERNAL", fmt.Sprintf("generate run id: %v", err), nil)
		return
	}

	startedAt := time.Now().UTC()
	duration := parseRunDuration(req.InitialPrompt)
	var (
		provenancePath string
		endedAt        *time.Time
		exitCode       *int
	)
	if req.DryRun {
		now := startedAt
		code := 0
		endedAt = &now
		exitCode = &code
	} else {
		provenancePath, err = writeRunFiles(runID, req.InitialPrompt)
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, "INTERNAL", fmt.Sprintf("write run files: %v", err), nil)
			return
		}
	}

	s.run = &runState{
		RunID:          runID,
		Cwd:            req.Cwd,
		InitialPrompt:  req.InitialPrompt,
		DryRun:         req.DryRun,
		StartedAt:      startedAt,
		EndedAt:        endedAt,
		Duration:       duration,
		ExitCode:       exitCode,
		ProvenancePath: provenancePath,
	}

	resp := map[string]any{
		"run_id":     runID,
		"started_at": startedAt.Format(time.RFC3339Nano),
	}
	if req.DryRun {
		resp["state"] = "exited"
		resp["exit_code"] = 0
	} else {
		resp["state"] = "running"
		resp["pid"] = os.Getpid()
	}
	writeJSON(w, http.StatusCreated, resp)
}

func (s *fakeServer) handleGetRun(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.run == nil {
		writeAPIError(w, http.StatusNotFound, "RUN_NOT_ACTIVE", "no run exists", nil)
		return
	}
	s.maybeTransitionRunToExitedLocked()

	r := s.run
	if r.ExitCode == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"state":      "running",
			"run_id":     r.RunID,
			"started_at": r.StartedAt.Format(time.RFC3339Nano),
			"cwd":        r.Cwd,
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"state":             "exited",
		"run_id":            r.RunID,
		"started_at":        r.StartedAt.Format(time.RFC3339Nano),
		"ended_at":          r.EndedAt.Format(time.RFC3339Nano),
		"cwd":               r.Cwd,
		"exit_code":         *r.ExitCode,
		"trajectory_status": s.trajectoryStatus(r),
	})
}

func (s *fakeServer) handleGetRunTrajectory(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.run == nil {
		writeAPIError(w, http.StatusNotFound, "RUN_NOT_ACTIVE", "no run exists", nil)
		return
	}
	s.maybeTransitionRunToExitedLocked()
	r := s.run
	if r.ExitCode == nil {
		writeAPIError(w, http.StatusConflict, "INVALID_RUN_STATE", "trajectory is only available after exit", map[string]any{
			"run_state": "running",
		})
		return
	}
	if r.DryRun {
		writeAPIError(w, http.StatusNotFound, "TRAJECTORY_UNAVAILABLE", "trajectory is not available for the current run", map[string]any{
			"trajectory_status": "none",
		})
		return
	}

	runtimeMS := int(r.EndedAt.Sub(r.StartedAt) / time.Millisecond)
	if runtimeMS < 0 {
		runtimeMS = 0
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"schema_version": "ATIF-v1.6",
		"session_id":     "fake-session-" + r.RunID,
		"agent": map[string]any{
			"name":    s.agentNameOrDefault(),
			"version": s.agentVersionOrDefault(),
		},
		"steps": []map[string]any{{
			"step_id": 1,
			"source":  "user",
			"message": r.InitialPrompt,
		}, {
			"step_id": 2,
			"source":  "agent",
			"message": "fake-agent-response",
			"tool_calls": []map[string]any{{
				"tool_call_id":  "call-1",
				"function_name": "fake.shell",
				"arguments": map[string]any{
					"command": "printf fake-agent-response",
				},
			}},
			"observation": map[string]any{
				"results": []map[string]any{{
					"source_call_id": "call-1",
					"content":        "fake-agent-tool-output",
				}},
			},
			"metrics": map[string]any{
				"prompt_tokens":     12,
				"completion_tokens": 5,
			},
		}},
		"final_metrics": map[string]any{
			"total_prompt_tokens":     12,
			"total_completion_tokens": 5,
			"total_steps":             2,
		},
		"extra": map[string]any{
			"agent": map[string]any{
				"runtime_ms": runtimeMS,
			},
			"provenance": map[string]any{
				"sources": []string{r.ProvenancePath},
			},
		},
	})
}

func (s *fakeServer) handleDeleteRun(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	s.run = nil
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"state": "idle"})
}

func (s *fakeServer) maybeTransitionRunToExitedLocked() {
	if s.run == nil || s.run.ExitCode != nil {
		return
	}
	if time.Since(s.run.StartedAt) < s.run.Duration {
		return
	}
	now := time.Now().UTC()
	exitCode := 0
	s.run.EndedAt = &now
	s.run.ExitCode = &exitCode
}

func (s *fakeServer) agentNameOrDefault() string {
	if strings.TrimSpace(s.agentName) == "" {
		return "fake-agent"
	}
	return s.agentName
}

func (s *fakeServer) agentVersionOrDefault() string {
	if strings.TrimSpace(s.agentVersion) == "" {
		return "latest"
	}
	return s.agentVersion
}

func (s *fakeServer) trajectoryStatus(run *runState) string {
	if run == nil || run.DryRun {
		return "none"
	}
	return "complete"
}

func (s *fakeServer) agentStateOrDefault() string {
	if strings.TrimSpace(s.agentState) == "" {
		return "empty"
	}
	return s.agentState
}

func parseRunDuration(initialPrompt string) time.Duration {
	if strings.Contains(initialPrompt, "[FAKE_LONG_RUN]") {
		return 2 * time.Minute
	}
	const tokenPrefix = "[FAKE_RUN_MS="
	start := strings.Index(initialPrompt, tokenPrefix)
	if start >= 0 {
		start += len(tokenPrefix)
		end := strings.Index(initialPrompt[start:], "]")
		if end > 0 {
			raw := initialPrompt[start : start+end]
			if v, err := strconv.Atoi(raw); err == nil && v > 0 {
				return time.Duration(v) * time.Millisecond
			}
		}
	}
	return 150 * time.Millisecond
}

func writeRunFiles(runID, initialPrompt string) (string, error) {
	if err := os.MkdirAll("/marginlab/state", 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile("/marginlab/state/fake-last-prompt.txt", []byte(initialPrompt), 0o644); err != nil {
		return "", err
	}
	artifactsDir := filepath.Join("/marginlab/state/runs", runID, "artifacts")
	if err := os.MkdirAll(artifactsDir, 0o755); err != nil {
		return "", err
	}
	provenancePath := filepath.Join(artifactsDir, "fake-provenance.jsonl")
	payload := fmt.Sprintf("{\"event\":\"fake_run\",\"run_id\":%q,\"prompt\":%q}\n", runID, initialPrompt)
	if err := os.WriteFile(provenancePath, []byte(payload), 0o644); err != nil {
		return "", err
	}
	return provenancePath, nil
}

func newRunID() (string, error) {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "r_" + hex.EncodeToString(buf), nil
}

func writeAPIError(w http.ResponseWriter, status int, code, message string, details map[string]any) {
	writeJSON(w, status, map[string]any{
		"error": map[string]any{
			"code":    code,
			"message": message,
			"details": details,
		},
	})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
