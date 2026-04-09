package agentexecutor

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/marginlab/margin-eval/runner/runner-core/domain"
	"github.com/marginlab/margin-eval/runner/runner-core/runbundle"
	"github.com/marginlab/margin-eval/runner/runner-core/store"
	"github.com/marginlab/margin-eval/runner/runner-core/testfixture"
)

func TestExecuteInstanceDefinitionConfigInstallFlow(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	definitionReq := map[string]any{}
	configReq := map[string]any{}
	statusCalls := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/state":
			_ = json.NewEncoder(w).Encode(map[string]any{"paths": map[string]any{"root": "/marginlab"}})
		case r.Method == http.MethodPut && r.URL.Path == "/v1/agent-definition":
			_ = json.NewDecoder(r.Body).Decode(&definitionReq)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"state": "definition_loaded"})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/agent/install":
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"state": "installed"})
		case r.Method == http.MethodPut && r.URL.Path == "/v1/agent-config":
			_ = json.NewDecoder(r.Body).Decode(&configReq)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"state": "configured"})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/run":
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"run_id": "r_1", "state": "running"})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/run":
			mu.Lock()
			statusCalls++
			call := statusCalls
			mu.Unlock()
			if call < 2 {
				_ = json.NewEncoder(w).Encode(map[string]any{"state": "running"})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"state":             "exited",
				"exit_code":         0,
				"trajectory_status": "complete",
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/run/trajectory":
			_, _ = w.Write([]byte(`{
				"schema_version":"ATIF-v1.6",
				"session_id":"abc",
				"agent":{"name":"fixture-agent","version":"1.0.0"},
				"steps":[
					{"step_id":1,"source":"user","message":"hello"},
					{
						"step_id":2,
						"source":"agent",
						"message":"done",
						"tool_calls":[{"tool_call_id":"call-1","function_name":"shell","arguments":{"command":"pwd"}}],
						"observation":{"results":[{"source_call_id":"call-1","content":"ok"}]},
						"metrics":{"prompt_tokens":9,"completion_tokens":4}
					}
				],
				"final_metrics":{"total_prompt_tokens":9,"total_completion_tokens":4}
			}`))
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/run":
			_ = json.NewEncoder(w).Encode(map[string]any{"state": "idle"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	artifactRoot := t.TempDir()
	exec, err := New(Config{
		BaseURL:      server.URL,
		ArtifactRoot: artifactRoot,
		PollInterval: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("new executor: %v", err)
	}

	run := store.Run{RunID: "run_1", Bundle: validBundle()}
	inst := store.Instance{
		InstanceID: "inst-1",
		Case:       run.Bundle.ResolvedSnapshot.Cases[0],
	}

	result, artifacts, err := exec.ExecuteInstance(context.Background(), run, inst, func(domain.InstanceState) error { return nil })
	if err != nil {
		t.Fatalf("execute instance: %v", err)
	}
	if result.FinalState != domain.InstanceStateSucceeded {
		t.Fatalf("expected succeeded, got %s", result.FinalState)
	}
	if result.Trajectory == "" {
		t.Fatalf("expected trajectory ref")
	}
	if result.Usage == nil {
		t.Fatalf("expected usage metrics")
	}
	if result.Usage.InputTokens == nil || *result.Usage.InputTokens != 9 {
		t.Fatalf("unexpected input tokens: %#v", result.Usage.InputTokens)
	}
	if result.Usage.OutputTokens == nil || *result.Usage.OutputTokens != 4 {
		t.Fatalf("unexpected output tokens: %#v", result.Usage.OutputTokens)
	}
	if result.Usage.ToolCalls == nil || *result.Usage.ToolCalls != 1 {
		t.Fatalf("unexpected tool calls: %#v", result.Usage.ToolCalls)
	}
	if len(artifacts) != 1 {
		t.Fatalf("expected 1 artifact, got %d", len(artifacts))
	}
	if artifacts[0].StoreKey != "trajectory.json" {
		t.Fatalf("trajectory store key = %q, want trajectory.json", artifacts[0].StoreKey)
	}
	artifactPath := strings.TrimPrefix(artifacts[0].URI, "file://")
	if _, err := os.Stat(artifactPath); err != nil {
		t.Fatalf("stat artifact payload: %v", err)
	}
	if filepath.Base(artifactPath) != "trajectory.json" {
		t.Fatalf("trajectory artifact path = %q, want basename trajectory.json", artifactPath)
	}
	if got := nestedString(definitionReq, "definition", "manifest", "name"); got != "fixture-agent" {
		t.Fatalf("definition manifest name = %q", got)
	}
	if got := nestedString(configReq, "config", "name"); got != "fixture-agent-default" {
		t.Fatalf("config name = %q", got)
	}
}

func TestExecuteInstanceFetchesTrajectoryBeforeClearingRun(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	currentRunActive := false
	requests := make([]string, 0, 8)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r.Method+" "+r.URL.Path)
		mu.Unlock()
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/state":
			_ = json.NewEncoder(w).Encode(map[string]any{"paths": map[string]any{"root": "/marginlab"}})
		case r.Method == http.MethodPut && r.URL.Path == "/v1/agent-definition":
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"state": "definition_loaded"})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/agent/install":
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"state": "installed"})
		case r.Method == http.MethodPut && r.URL.Path == "/v1/agent-config":
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"state": "configured"})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/run":
			mu.Lock()
			currentRunActive = true
			mu.Unlock()
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"run_id": "r_1", "state": "running"})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/run":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"state":             "exited",
				"exit_code":         0,
				"trajectory_status": "complete",
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/run/trajectory":
			mu.Lock()
			active := currentRunActive
			mu.Unlock()
			if !active {
				http.Error(w, `{"error":{"code":"RUN_NOT_ACTIVE","message":"no active run"}}`, http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"schema_version":"ATIF-v1.6","session_id":"abc","agent":{"name":"fixture-agent","version":"1.0.0"},"steps":[]}`))
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/run":
			mu.Lock()
			currentRunActive = false
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"state": "idle"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	exec, err := New(Config{
		BaseURL:      server.URL,
		ArtifactRoot: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("new executor: %v", err)
	}

	run := store.Run{RunID: "run_1", Bundle: validBundle()}
	inst := store.Instance{
		InstanceID: "inst-1",
		Case:       run.Bundle.ResolvedSnapshot.Cases[0],
	}

	result, _, err := exec.ExecuteInstance(context.Background(), run, inst, func(domain.InstanceState) error { return nil })
	if err != nil {
		t.Fatalf("execute instance: %v\nrequests: %v", err, requests)
	}
	if result.Trajectory == "" {
		t.Fatalf("expected trajectory ref")
	}

	mu.Lock()
	defer mu.Unlock()
	trajectoryIdx := -1
	deleteIdx := -1
	for i, req := range requests {
		if req == "GET /v1/run/trajectory" && trajectoryIdx == -1 {
			trajectoryIdx = i
		}
		if req == "DELETE /v1/run" {
			deleteIdx = i
		}
	}
	if trajectoryIdx == -1 || deleteIdx == -1 {
		t.Fatalf("missing trajectory fetch or run delete in requests: %v", requests)
	}
	if deleteIdx < trajectoryIdx {
		t.Fatalf("expected trajectory fetch before run delete, got requests: %v", requests)
	}
}

func TestExecuteInstanceDryRunSkipsTrajectoryAndExitMetadata(t *testing.T) {
	t.Parallel()

	requests := make([]string, 0, 8)
	var runReq map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/state":
			_ = json.NewEncoder(w).Encode(map[string]any{"paths": map[string]any{"root": "/marginlab"}})
		case r.Method == http.MethodPut && r.URL.Path == "/v1/agent-definition":
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"state": "definition_loaded"})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/agent/install":
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"state": "installed"})
		case r.Method == http.MethodPut && r.URL.Path == "/v1/agent-config":
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"state": "configured"})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/run":
			_ = json.NewDecoder(r.Body).Decode(&runReq)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"run_id": "r_1", "state": "exited"})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/run":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"state":             "exited",
				"exit_code":         0,
				"trajectory_status": "none",
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/run/trajectory":
			t.Fatalf("trajectory endpoint should not be called in dry-run mode")
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/run":
			_ = json.NewEncoder(w).Encode(map[string]any{"state": "idle"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	exec, err := New(Config{
		BaseURL:      server.URL,
		ArtifactRoot: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("new executor: %v", err)
	}

	bundle := validBundle()
	bundle.ResolvedSnapshot.Execution.Mode = runbundle.ExecutionModeDryRun
	run := store.Run{RunID: "run_1", Bundle: bundle}
	inst := store.Instance{
		InstanceID: "inst-1",
		Case:       run.Bundle.ResolvedSnapshot.Cases[0],
	}

	result, artifacts, err := exec.ExecuteInstance(context.Background(), run, inst, func(domain.InstanceState) error { return nil })
	if err != nil {
		t.Fatalf("execute instance: %v", err)
	}
	if result.FinalState != domain.InstanceStateSucceeded {
		t.Fatalf("final state = %s, want %s", result.FinalState, domain.InstanceStateSucceeded)
	}
	if result.AgentRunID != "" || result.AgentExitCode != nil || result.Trajectory != "" {
		t.Fatalf("unexpected dry-run result metadata: %+v", result)
	}
	if len(artifacts) != 0 {
		t.Fatalf("expected no artifacts, got %d", len(artifacts))
	}
	if runReq["dry_run"] != true {
		t.Fatalf("dry_run request flag = %#v, want true", runReq["dry_run"])
	}
	for _, req := range requests {
		if req == "GET /v1/run/trajectory" {
			t.Fatalf("unexpected trajectory request: %v", requests)
		}
	}
}

func TestExecuteInstanceDoesNotUseCaseTestTimeoutForAgentRun(t *testing.T) {
	t.Parallel()

	events := make([]StepEvent, 0, 8)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/state":
			_ = json.NewEncoder(w).Encode(map[string]any{"paths": map[string]any{"root": "/marginlab"}})
		case r.Method == http.MethodPut && r.URL.Path == "/v1/agent-definition":
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"state": "definition_loaded"})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/agent/install":
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"state": "installed"})
		case r.Method == http.MethodPut && r.URL.Path == "/v1/agent-config":
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"state": "configured"})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/run":
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"run_id": "r_1", "state": "running"})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/run":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"state":             "exited",
				"exit_code":         0,
				"trajectory_status": "none",
			})
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/run":
			_ = json.NewEncoder(w).Encode(map[string]any{"state": "idle"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	exec, err := New(Config{
		BaseURL:      server.URL,
		ArtifactRoot: t.TempDir(),
		OnStep: func(ev StepEvent) {
			events = append(events, ev)
		},
	})
	if err != nil {
		t.Fatalf("new executor: %v", err)
	}

	bundle := validBundle()
	bundle.ResolvedSnapshot.Cases[0].TestTimeoutSecond = 0
	run := store.Run{RunID: "run_1", Bundle: bundle}
	inst := store.Instance{
		InstanceID: "inst-1",
		Case:       run.Bundle.ResolvedSnapshot.Cases[0],
	}

	result, _, err := exec.ExecuteInstance(context.Background(), run, inst, func(domain.InstanceState) error { return nil })
	if err != nil {
		t.Fatalf("execute instance: %v", err)
	}
	if result.FinalState != domain.InstanceStateSucceeded {
		t.Fatalf("final state = %s, want %s", result.FinalState, domain.InstanceStateSucceeded)
	}

	for _, ev := range events {
		if ev.Step == "run.wait_exit" && ev.Status == "start" {
			if ev.Details["instance_timeout_seconds"] != "120" {
				t.Fatalf("instance timeout detail = %q, want 120", ev.Details["instance_timeout_seconds"])
			}
			return
		}
	}
	t.Fatalf("missing run.wait_exit start event: %+v", events)
}

func TestExecuteInstanceWaitForExitStopsOnContextDeadline(t *testing.T) {
	t.Parallel()

	events := make([]StepEvent, 0, 8)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/state":
			_ = json.NewEncoder(w).Encode(map[string]any{"paths": map[string]any{"root": "/marginlab"}})
		case r.Method == http.MethodPut && r.URL.Path == "/v1/agent-definition":
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"state": "definition_loaded"})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/agent/install":
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"state": "installed"})
		case r.Method == http.MethodPut && r.URL.Path == "/v1/agent-config":
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"state": "configured"})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/run":
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"run_id": "r_1", "state": "running"})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/run":
			_ = json.NewEncoder(w).Encode(map[string]any{"state": "running"})
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/run":
			_ = json.NewEncoder(w).Encode(map[string]any{"state": "idle"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	exec, err := New(Config{
		BaseURL:      server.URL,
		ArtifactRoot: t.TempDir(),
		PollInterval: 10 * time.Millisecond,
		OnStep: func(ev StepEvent) {
			events = append(events, ev)
		},
	})
	if err != nil {
		t.Fatalf("new executor: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	run := store.Run{RunID: "run_1", Bundle: validBundle()}
	inst := store.Instance{
		InstanceID: "inst-1",
		Case:       run.Bundle.ResolvedSnapshot.Cases[0],
	}

	_, _, err = exec.ExecuteInstance(ctx, run, inst, func(domain.InstanceState) error { return nil })
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want context deadline exceeded", err)
	}

	for _, ev := range events {
		if ev.Step == "run.wait_exit" && ev.Status == "failed" {
			if !strings.Contains(ev.Details["error"], "context deadline exceeded") {
				t.Fatalf("run.wait_exit error detail = %q", ev.Details["error"])
			}
			return
		}
	}
	t.Fatalf("missing run.wait_exit failed event: %+v", events)
}

func TestExecuteInstanceFailsForInvalidDefinition(t *testing.T) {
	t.Parallel()

	exec, err := New(Config{BaseURL: "http://127.0.0.1", ArtifactRoot: t.TempDir()})
	if err != nil {
		t.Fatalf("new executor: %v", err)
	}

	bundle := validBundle()
	bundle.ResolvedSnapshot.Agent.Definition.Manifest.Run.PrepareHook.Path = ""
	_, _, err = exec.ExecuteInstance(
		context.Background(),
		store.Run{RunID: "run_1", Bundle: bundle},
		store.Instance{InstanceID: "inst-1", Case: bundle.ResolvedSnapshot.Cases[0]},
		func(domain.InstanceState) error { return nil },
	)
	if err == nil || !strings.Contains(err.Error(), "run.prepare") {
		t.Fatalf("expected definition validation error, got %v", err)
	}
}

func TestExecuteInstanceEmitsStepEvents(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/state":
			_ = json.NewEncoder(w).Encode(map[string]any{"paths": map[string]any{"root": "/marginlab"}})
		case r.Method == http.MethodPut && r.URL.Path == "/v1/agent-definition":
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"state": "definition_loaded"})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/agent/install":
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"state": "installed"})
		case r.Method == http.MethodPut && r.URL.Path == "/v1/agent-config":
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"state": "configured"})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/run":
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"run_id": "r_1", "state": "running"})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/run":
			_ = json.NewEncoder(w).Encode(map[string]any{"state": "exited", "exit_code": 0})
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/run":
			_ = json.NewEncoder(w).Encode(map[string]any{"state": "idle"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	events := make([]StepEvent, 0, 8)
	exec, err := New(Config{
		BaseURL:      server.URL,
		ArtifactRoot: t.TempDir(),
		OnStep: func(ev StepEvent) {
			events = append(events, ev)
		},
	})
	if err != nil {
		t.Fatalf("new executor: %v", err)
	}

	run := store.Run{RunID: "run_1", Bundle: validBundle()}
	inst := store.Instance{InstanceID: "inst-1", Case: run.Bundle.ResolvedSnapshot.Cases[0]}
	if _, _, err := exec.ExecuteInstance(context.Background(), run, inst, func(domain.InstanceState) error { return nil }); err != nil {
		t.Fatalf("execute instance: %v", err)
	}
	if len(events) == 0 {
		t.Fatalf("expected step events")
	}
	foundDefinition := false
	foundInstall := false
	foundConfig := false
	foundWait := false
	for _, ev := range events {
		if ev.Step == "agent_definition.load" && ev.Status == "completed" {
			foundDefinition = true
		}
		if ev.Step == "agent.install" && ev.Status == "completed" {
			foundInstall = true
		}
		if ev.Step == "agent.configure" && ev.Status == "completed" {
			foundConfig = true
		}
		if ev.Step == "run.wait_exit" && ev.Status == "completed" {
			foundWait = true
		}
	}
	if !foundDefinition || !foundInstall || !foundConfig || !foundWait {
		t.Fatalf("expected definition/install/config/wait completion events, got %+v", events)
	}
}

func TestExecuteInstanceIncludesAuthFilesInStartRunRequest(t *testing.T) {
	t.Parallel()

	var runReq map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/state":
			_ = json.NewEncoder(w).Encode(map[string]any{"paths": map[string]any{"root": "/marginlab"}})
		case r.Method == http.MethodPut && r.URL.Path == "/v1/agent-definition":
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"state": "definition_loaded"})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/agent/install":
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"state": "installed"})
		case r.Method == http.MethodPut && r.URL.Path == "/v1/agent-config":
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"state": "configured"})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/run":
			_ = json.NewDecoder(r.Body).Decode(&runReq)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"run_id": "r_1", "state": "running"})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/run":
			_ = json.NewEncoder(w).Encode(map[string]any{"state": "exited", "exit_code": 0})
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/run":
			_ = json.NewEncoder(w).Encode(map[string]any{"state": "idle"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	exec, err := New(Config{
		BaseURL:      server.URL,
		ArtifactRoot: t.TempDir(),
		AuthFiles: []StartRunAuthFile{{
			RequiredEnv:    "OPENAI_API_KEY",
			SourcePath:     "/tmp/marginlab/config/auth-files/OPENAI_API_KEY",
			RunHomeRelPath: ".codex/auth.json",
		}},
	})
	if err != nil {
		t.Fatalf("new executor: %v", err)
	}

	run := store.Run{RunID: "run_1", Bundle: validBundle()}
	inst := store.Instance{InstanceID: "inst-1", Case: run.Bundle.ResolvedSnapshot.Cases[0]}
	if _, _, err := exec.ExecuteInstance(context.Background(), run, inst, func(domain.InstanceState) error { return nil }); err != nil {
		t.Fatalf("execute instance: %v", err)
	}

	authFiles, ok := runReq["auth_files"].([]any)
	if !ok || len(authFiles) != 1 {
		t.Fatalf("auth_files = %#v", runReq["auth_files"])
	}
	item, ok := authFiles[0].(map[string]any)
	if !ok {
		t.Fatalf("auth file entry = %#v", authFiles[0])
	}
	if item["required_env"] != "OPENAI_API_KEY" {
		t.Fatalf("required_env = %#v", item["required_env"])
	}
	if item["source_path"] != "/tmp/marginlab/config/auth-files/OPENAI_API_KEY" {
		t.Fatalf("source_path = %#v", item["source_path"])
	}
	if item["run_home_rel_path"] != ".codex/auth.json" {
		t.Fatalf("run_home_rel_path = %#v", item["run_home_rel_path"])
	}
}

func validBundle() runbundle.Bundle {
	return runbundle.Bundle{
		SchemaVersion: runbundle.SchemaVersionV1,
		BundleID:      "bun_1",
		CreatedAt:     time.Now().UTC(),
		Source:        runbundle.Source{Kind: runbundle.SourceKindLocalFiles, SubmitProjectID: "proj_local"},
		ResolvedSnapshot: runbundle.ResolvedSnapshot{
			Name: "test",
			Execution: runbundle.Execution{
				Mode:                  runbundle.ExecutionModeFull,
				MaxConcurrency:        1,
				FailFast:              false,
				InstanceTimeoutSecond: 120,
			},
			Agent: testfixture.MinimalAgent(),
			RunDefaults: runbundle.RunDefault{
				Env: map[string]string{"TERM": "xterm-256color"},
				PTY: runbundle.PTY{Cols: 120, Rows: 40},
			},
			Cases: []runbundle.Case{{
				CaseID:            "case_1",
				Image:             "ghcr.io/acme/repo@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
				InitialPrompt:     "hello",
				AgentCwd:          "/workspace",
				TestCommand:       []string{"bash", "-lc", "true"},
				TestCwd:           "/work",
				TestTimeoutSecond: 60,
				TestAssets:        testfixture.MinimalTestAssets(),
			}},
		},
	}
}

func nestedString(root map[string]any, path ...string) string {
	current := any(root)
	for _, segment := range path {
		m, ok := current.(map[string]any)
		if !ok {
			return ""
		}
		current = m[segment]
	}
	s, _ := current.(string)
	return s
}
