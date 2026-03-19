package agentexecutor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/marginlab/margin-eval/runner/runner-core/agentdef"
	"github.com/marginlab/margin-eval/runner/runner-core/domain"
	"github.com/marginlab/margin-eval/runner/runner-core/runbundle"
	"github.com/marginlab/margin-eval/runner/runner-core/store"
	"github.com/marginlab/margin-eval/runner/runner-core/trajectory"
	"github.com/marginlab/margin-eval/runner/runner-core/usage"
)

const defaultPollInterval = 300 * time.Millisecond

type Config struct {
	BaseURL      string
	HTTPClient   *http.Client
	ArtifactRoot string
	AuthFiles    []StartRunAuthFile
	PollInterval time.Duration
	OnStep       func(StepEvent)
}

type StartRunAuthFile struct {
	RequiredEnv    string `json:"required_env"`
	SourcePath     string `json:"source_path"`
	RunHomeRelPath string `json:"run_home_rel_path"`
}

type StepEvent struct {
	Time    time.Time
	Step    string
	Status  string
	Message string
	Details map[string]string
}

type Executor struct {
	baseURL      string
	client       *http.Client
	artifactRoot string
	authFiles    []StartRunAuthFile
	pollInterval time.Duration
	onStep       func(StepEvent)
}

func New(cfg Config) (*Executor, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if baseURL == "" {
		return nil, fmt.Errorf("base_url is required")
	}
	artifactRoot := strings.TrimSpace(cfg.ArtifactRoot)
	if artifactRoot == "" {
		return nil, fmt.Errorf("artifact_root is required")
	}
	absArtifactRoot, err := filepath.Abs(artifactRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve artifact_root: %w", err)
	}
	if err := os.MkdirAll(absArtifactRoot, 0o755); err != nil {
		return nil, fmt.Errorf("create artifact_root: %w", err)
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	pollInterval := cfg.PollInterval
	if pollInterval <= 0 {
		pollInterval = defaultPollInterval
	}
	return &Executor{
		baseURL:      baseURL,
		client:       client,
		artifactRoot: absArtifactRoot,
		authFiles:    append([]StartRunAuthFile(nil), cfg.AuthFiles...),
		pollInterval: pollInterval,
		onStep:       cfg.OnStep,
	}, nil
}

func (e *Executor) ExecuteInstance(ctx context.Context, run store.Run, inst store.Instance, updateState func(domain.InstanceState) error) (store.InstanceResult, []store.Artifact, error) {
	if updateState == nil {
		return store.InstanceResult{}, nil, fmt.Errorf("update_state callback is required")
	}

	agent := run.Bundle.ResolvedSnapshot.Agent
	if _, err := agentdef.ValidateAndNormalizeConfigSpec(agent.Definition, agent.Config); err != nil {
		return store.InstanceResult{}, nil, fmt.Errorf("validate run bundle agent definition/config: %w", err)
	}

	definitionName := strings.TrimSpace(agent.Definition.Manifest.Name)
	configName := strings.TrimSpace(agent.Config.Name)
	if definitionName == "" {
		return store.InstanceResult{}, nil, fmt.Errorf("run bundle agent definition name is required")
	}
	if configName == "" {
		return store.InstanceResult{}, nil, fmt.Errorf("run bundle agent config name is required")
	}

	e.emitStep("agent_server.get_state", "start", "Requesting current agent-server state via GET /v1/state before run orchestration.", map[string]string{
		"endpoint": "/v1/state",
	})
	if _, err := e.getState(ctx); err != nil {
		e.emitStep("agent_server.get_state", "failed", "GET /v1/state failed; unable to discover agent-server runtime state.", map[string]string{
			"endpoint": "/v1/state",
			"error":    err.Error(),
		})
		return store.InstanceResult{}, nil, err
	}
	e.emitStep("agent_server.get_state", "completed", "Fetched agent-server state snapshot from GET /v1/state.", map[string]string{
		"endpoint": "/v1/state",
	})

	definitionStepDetails := map[string]string{
		"definition_name":   definitionName,
		"package_sha256":    agent.Definition.Package.ArchiveTGZSHA256,
		"package_tgz_bytes": fmt.Sprintf("%d", agent.Definition.Package.ArchiveTGZBytes),
		"endpoint":          "/v1/agent-definition",
	}
	e.emitStep("agent_definition.load", "start", "Submitting PUT /v1/agent-definition with the resolved definition snapshot.", cloneStringMap(definitionStepDetails))
	if err := e.putAgentDefinition(ctx, putAgentDefinitionRequest{Definition: agent.Definition}); err != nil {
		failedDetails := cloneStringMap(definitionStepDetails)
		failedDetails["error"] = err.Error()
		e.emitStep("agent_definition.load", "failed", "PUT /v1/agent-definition failed; agent definition could not be loaded.", failedDetails)
		return store.InstanceResult{}, nil, err
	}
	e.emitStep("agent_definition.load", "completed", "PUT /v1/agent-definition completed; agent definition is now active.", cloneStringMap(definitionStepDetails))

	if err := updateState(domain.InstanceStateAgentConfiguring); err != nil {
		return store.InstanceResult{}, nil, err
	}
	configStepDetails := map[string]string{
		"config_name":   configName,
		"mode":          string(agent.Config.Mode),
		"input_keys":    fmt.Sprintf("%d", len(agent.Config.Input)),
		"has_unified":   fmt.Sprintf("%t", agent.Config.Unified != nil),
		"endpoint":      "/v1/agent-config",
		"definition":    definitionName,
		"description":   strings.TrimSpace(agent.Config.Description),
		"has_input_map": fmt.Sprintf("%t", agent.Config.Input != nil),
	}
	e.emitStep("agent.configure", "start", "Submitting PUT /v1/agent-config with the resolved configuration snapshot.", cloneStringMap(configStepDetails))
	if err := e.putAgentConfig(ctx, putAgentConfigRequest{Config: agent.Config}); err != nil {
		failedDetails := cloneStringMap(configStepDetails)
		failedDetails["error"] = err.Error()
		e.emitStep("agent.configure", "failed", "PUT /v1/agent-config failed; agent configuration was rejected by agent-server.", failedDetails)
		return store.InstanceResult{}, nil, err
	}
	e.emitStep("agent.configure", "completed", "PUT /v1/agent-config completed; agent configuration is now active.", cloneStringMap(configStepDetails))

	if err := updateState(domain.InstanceStateAgentInstalling); err != nil {
		return store.InstanceResult{}, nil, err
	}
	installStepDetails := map[string]string{
		"definition_name": definitionName,
		"config_name":     configName,
		"endpoint":        "/v1/agent/install",
	}
	e.emitStep("agent.install", "start", "Submitting POST /v1/agent/install to install the selected definition using the active config.", cloneStringMap(installStepDetails))
	if err := e.postAgentInstall(ctx); err != nil {
		failedDetails := cloneStringMap(installStepDetails)
		failedDetails["error"] = err.Error()
		e.emitStep("agent.install", "failed", "POST /v1/agent/install failed; agent installation could not be ensured.", failedDetails)
		return store.InstanceResult{}, nil, err
	}
	e.emitStep("agent.install", "completed", "POST /v1/agent/install completed; installation is ready for execution.", cloneStringMap(installStepDetails))

	runCleanupDetails := map[string]string{"endpoint": "/v1/run"}
	e.emitStep("run.cleanup_previous", "start", "Sending DELETE /v1/run to clear any stale active run state before launch.", cloneStringMap(runCleanupDetails))
	if err := e.deleteRun(context.Background()); err != nil {
		failedDetails := cloneStringMap(runCleanupDetails)
		failedDetails["error"] = err.Error()
		e.emitStep("run.cleanup_previous", "failed", "DELETE /v1/run failed while clearing stale pre-launch run state.", failedDetails)
		return store.InstanceResult{}, nil, err
	}
	e.emitStep("run.cleanup_previous", "completed", "DELETE /v1/run completed; stale pre-launch run state is cleared.", cloneStringMap(runCleanupDetails))

	if err := updateState(domain.InstanceStateAgentRunning); err != nil {
		return store.InstanceResult{}, nil, err
	}
	agentStartedAt := time.Now().UTC()
	runDefaults := run.Bundle.ResolvedSnapshot.RunDefaults
	executionMode := run.Bundle.ResolvedSnapshot.Execution.Mode
	if executionMode == "" {
		executionMode = runbundle.ExecutionModeFull
	}
	dryRun := executionMode == runbundle.ExecutionModeDryRun
	caseTimeout := time.Duration(inst.Case.TestTimeoutSecond) * time.Second
	if caseTimeout <= 0 {
		return store.InstanceResult{}, nil, fmt.Errorf("case test_timeout_seconds must be > 0")
	}
	startReq := startRunRequest{
		CWD:           inst.Case.TestCwd,
		InitialPrompt: inst.Case.InitialPrompt,
		Env:           cloneStringMap(runDefaults.Env),
		AuthFiles:     append([]StartRunAuthFile(nil), e.authFiles...),
		DryRun:        dryRun,
		PTY: ptyRequest{
			Cols: runDefaults.PTY.Cols,
			Rows: runDefaults.PTY.Rows,
		},
	}
	runStartDetails := map[string]string{
		"endpoint":               "/v1/run",
		"cwd":                    startReq.CWD,
		"pty_cols":               fmt.Sprintf("%d", startReq.PTY.Cols),
		"pty_rows":               fmt.Sprintf("%d", startReq.PTY.Rows),
		"initial_prompt_present": fmt.Sprintf("%t", strings.TrimSpace(startReq.InitialPrompt) != ""),
		"execution_mode":         string(executionMode),
		"dry_run":                fmt.Sprintf("%t", dryRun),
	}
	runStartMessage := "Submitting POST /v1/run to launch the agent process in a PTY session."
	if dryRun {
		runStartMessage = "Submitting POST /v1/run in dry-run mode to validate prelaunch setup without starting the agent PTY."
	}
	e.emitStep("run.start", "start", runStartMessage, cloneStringMap(runStartDetails))
	agentRunID, err := e.startRun(ctx, startReq)
	if err != nil {
		failedDetails := cloneStringMap(runStartDetails)
		failedDetails["error"] = err.Error()
		e.emitStep("run.start", "failed", "POST /v1/run failed; agent process launch request was not accepted.", failedDetails)
		return store.InstanceResult{}, nil, err
	}
	runStartCompletedDetails := cloneStringMap(runStartDetails)
	runStartCompletedDetails["agent_run_id"] = agentRunID
	runStartCompletedMessage := "POST /v1/run completed; agent process is now running under agent-server control."
	if dryRun {
		runStartCompletedMessage = "POST /v1/run completed in dry-run mode; agent-server accepted and completed prelaunch validation."
	}
	e.emitStep("run.start", "completed", runStartCompletedMessage, runStartCompletedDetails)

	runWaitDetails := map[string]string{
		"endpoint":        "/v1/run",
		"timeout_seconds": fmt.Sprintf("%d", int(caseTimeout/time.Second)),
	}
	e.emitStep("run.wait_exit", "start", "Polling GET /v1/run until the run exits or the case timeout is reached.", cloneStringMap(runWaitDetails))
	exitCode, trajectoryStatus, err := e.waitForExit(ctx, caseTimeout)
	if err != nil {
		failedDetails := cloneStringMap(runWaitDetails)
		failedDetails["error"] = err.Error()
		e.emitStep("run.wait_exit", "failed", "Polling GET /v1/run failed before the run reached exited state.", failedDetails)
		return store.InstanceResult{}, nil, err
	}
	runWaitCompletedDetails := cloneStringMap(runWaitDetails)
	runWaitCompletedDetails["exit_code"] = fmt.Sprintf("%d", exitCode)
	e.emitStep("run.wait_exit", "completed", "Run reached exited state; final exit code was collected from GET /v1/run.", runWaitCompletedDetails)

	agentEndedAt := time.Now().UTC()
	if err := updateState(domain.InstanceStateAgentCollecting); err != nil {
		return store.InstanceResult{}, nil, err
	}
	final := domain.InstanceStateSucceeded
	if exitCode != 0 {
		final = domain.InstanceStateInfraFailed
	}
	if dryRun {
		e.emitStep("run.dry_run", "completed", "Dry-run completed prelaunch validation; skipping trajectory collection.", map[string]string{
			"agent_run_id":    agentRunID,
			"execution_mode":  string(executionMode),
			"trajectory_mode": "skipped",
		})
		return store.InstanceResult{
			FinalState: final,
		}, nil, e.clearExitedRun(agentRunID)
	}
	result := store.InstanceResult{
		FinalState:     final,
		AgentRunID:     agentRunID,
		AgentExitCode:  intPtr(exitCode),
		AgentStartedAt: timePtr(agentStartedAt),
		AgentEndedAt:   timePtr(agentEndedAt),
	}

	if strings.TrimSpace(trajectoryStatus) != "complete" {
		e.emitStep("trajectory.persist", "completed", "Run exited without a complete trajectory; skipping trajectory artifact persistence.", map[string]string{
			"trajectory_status": strings.TrimSpace(trajectoryStatus),
		})
		return result, nil, e.clearExitedRun(agentRunID)
	}

	e.emitStep("trajectory.fetch", "start", "Fetching ATIF trajectory from GET /v1/run/trajectory after run completion.", map[string]string{
		"endpoint": "/v1/run/trajectory",
	})
	trajectoryRaw, err := e.getRunTrajectory(ctx)
	if err != nil {
		failedDetails := map[string]string{
			"endpoint": "/v1/run/trajectory",
		}
		failedDetails["error"] = err.Error()
		e.emitStep("trajectory.fetch", "failed", "Failed to fetch trajectory payload from agent-server.", failedDetails)
		return store.InstanceResult{}, nil, err
	}
	e.emitStep("trajectory.fetch", "completed", "Fetched ATIF trajectory payload from agent-server.", map[string]string{
		"trajectory_bytes": fmt.Sprintf("%d", len(trajectoryRaw)),
		"endpoint":         "/v1/run/trajectory",
	})
	if extracted, usageErr := extractTrajectoryUsage(trajectoryRaw); usageErr == nil {
		result.Usage = extracted
	}

	trajectoryDetails := map[string]string{
		"trajectory_bytes": fmt.Sprintf("%d", len(trajectoryRaw)),
	}
	e.emitStep("trajectory.persist", "start", "Persisting ATIF trajectory payload to a local trajectory artifact.", cloneStringMap(trajectoryDetails))
	artifact, trajectoryRef, err := e.persistTrajectory(trajectoryRaw)
	if err != nil {
		failedDetails := cloneStringMap(trajectoryDetails)
		failedDetails["error"] = err.Error()
		e.emitStep("trajectory.persist", "failed", "Failed to persist trajectory payload to the local artifact store.", failedDetails)
		return store.InstanceResult{}, nil, err
	}
	completedDetails := cloneStringMap(trajectoryDetails)
	completedDetails["store_key"] = trajectoryRef
	e.emitStep("trajectory.persist", "completed", "ATIF trajectory payload persisted and linked in the instance result.", completedDetails)
	result.Trajectory = trajectoryRef
	return result, []store.Artifact{artifact}, e.clearExitedRun(agentRunID)
}

func (e *Executor) clearExitedRun(agentRunID string) error {
	runStopDetails := map[string]string{
		"endpoint":     "/v1/run",
		"agent_run_id": agentRunID,
	}
	e.emitStep("run.stop", "start", "Sending DELETE /v1/run after exit to clear server-side run state.", cloneStringMap(runStopDetails))
	if err := e.deleteRun(context.Background()); err != nil {
		failedDetails := cloneStringMap(runStopDetails)
		failedDetails["error"] = err.Error()
		e.emitStep("run.stop", "failed", "DELETE /v1/run failed while clearing post-exit run state.", failedDetails)
		return err
	}
	e.emitStep("run.stop", "completed", "DELETE /v1/run completed; post-exit run state has been cleared.", cloneStringMap(runStopDetails))
	return nil
}

func extractTrajectoryUsage(raw []byte) (*usage.Metrics, error) {
	traj, err := trajectory.Decode(raw)
	if err != nil {
		return nil, err
	}
	return trajectory.ExtractUsageMetrics(traj), nil
}

type stateResponse struct {
	Paths struct {
		Root       string `json:"root"`
		Bin        string `json:"bin"`
		State      string `json:"state"`
		Workspaces string `json:"workspaces"`
	} `json:"paths"`
}

type putAgentDefinitionRequest struct {
	Definition agentdef.DefinitionSnapshot `json:"definition"`
}

type putAgentConfigRequest struct {
	Config agentdef.ConfigSpec `json:"config"`
}

type ptyRequest struct {
	Cols int `json:"cols"`
	Rows int `json:"rows"`
}

type startRunRequest struct {
	CWD           string             `json:"cwd"`
	InitialPrompt string             `json:"initial_prompt"`
	Env           map[string]string  `json:"env,omitempty"`
	AuthFiles     []StartRunAuthFile `json:"auth_files,omitempty"`
	DryRun        bool               `json:"dry_run,omitempty"`
	PTY           ptyRequest         `json:"pty"`
}

type runResponse struct {
	State            string `json:"state"`
	ExitCode         *int   `json:"exit_code"`
	TrajectoryStatus string `json:"trajectory_status"`
}

func (e *Executor) getState(ctx context.Context) (stateResponse, error) {
	var out stateResponse
	if err := e.doJSON(ctx, http.MethodGet, "/v1/state", nil, &out); err != nil {
		return stateResponse{}, err
	}
	return out, nil
}

func (e *Executor) putAgentDefinition(ctx context.Context, req putAgentDefinitionRequest) error {
	return e.doJSON(ctx, http.MethodPut, "/v1/agent-definition", req, nil)
}

func (e *Executor) putAgentConfig(ctx context.Context, req putAgentConfigRequest) error {
	return e.doJSON(ctx, http.MethodPut, "/v1/agent-config", req, nil)
}

func (e *Executor) postAgentInstall(ctx context.Context) error {
	return e.doJSON(ctx, http.MethodPost, "/v1/agent/install", nil, nil)
}

type startRunResponse struct {
	RunID string `json:"run_id"`
}

func (e *Executor) startRun(ctx context.Context, req startRunRequest) (string, error) {
	var out startRunResponse
	if err := e.doJSON(ctx, http.MethodPost, "/v1/run", req, &out); err != nil {
		return "", err
	}
	return strings.TrimSpace(out.RunID), nil
}

func (e *Executor) getRun(ctx context.Context) (runResponse, error) {
	var out runResponse
	if err := e.doJSON(ctx, http.MethodGet, "/v1/run", nil, &out); err != nil {
		return runResponse{}, err
	}
	return out, nil
}

func (e *Executor) deleteRun(ctx context.Context) error {
	return e.doJSON(ctx, http.MethodDelete, "/v1/run", nil, nil)
}

func (e *Executor) waitForExit(ctx context.Context, timeout time.Duration) (int, string, error) {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(e.pollInterval)
	defer ticker.Stop()

	for {
		status, err := e.getRun(ctx)
		if err == nil && status.State == "exited" {
			exitCode := 0
			if status.ExitCode != nil {
				exitCode = *status.ExitCode
			}
			return exitCode, strings.TrimSpace(status.TrajectoryStatus), nil
		}
		if err != nil {
			return 0, "", err
		}
		select {
		case <-ctx.Done():
			return 0, "", ctx.Err()
		case <-deadline.C:
			return 0, "", fmt.Errorf("timed out waiting for /v1/run to exit")
		case <-ticker.C:
		}
	}
}

func (e *Executor) getRunTrajectory(ctx context.Context) ([]byte, error) {
	return e.doRaw(ctx, http.MethodGet, "/v1/run/trajectory")
}

func (e *Executor) persistTrajectory(payload []byte) (store.Artifact, string, error) {
	if err := os.MkdirAll(e.artifactRoot, 0o755); err != nil {
		return store.Artifact{}, "", fmt.Errorf("create trajectory dir: %w", err)
	}
	fileName, _ := store.DefaultArtifactFilename(store.ArtifactRoleTrajectory)
	filePath := filepath.Join(e.artifactRoot, fileName)
	if err := os.WriteFile(filePath, payload, 0o644); err != nil {
		return store.Artifact{}, "", fmt.Errorf("write trajectory artifact: %w", err)
	}
	hashBytes := sha256.Sum256(payload)
	rel, err := filepath.Rel(e.artifactRoot, filePath)
	if err != nil {
		return store.Artifact{}, "", fmt.Errorf("compute trajectory relative path: %w", err)
	}
	storeKey := filepath.ToSlash(rel)
	artifact := store.Artifact{
		ArtifactID:  "art-trajectory",
		Role:        store.ArtifactRoleTrajectory,
		StoreKey:    storeKey,
		URI:         "file://" + filePath,
		ContentType: "application/json",
		ByteSize:    int64(len(payload)),
		SHA256:      hex.EncodeToString(hashBytes[:]),
	}
	return artifact, storeKey, nil
}

func (e *Executor) doRaw(ctx context.Context, method, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, e.baseURL+path, nil)
	if err != nil {
		return nil, fmt.Errorf("build request %s %s: %w", method, path, err)
	}
	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read %s %s response: %w", method, path, err)
	}
	if resp.StatusCode >= 400 {
		message := strings.TrimSpace(string(data))
		if message == "" {
			message = http.StatusText(resp.StatusCode)
		}
		return nil, fmt.Errorf("%s %s returned status %d: %s", method, path, resp.StatusCode, message)
	}
	return data, nil
}

func (e *Executor) doJSON(ctx context.Context, method, path string, payload any, out any) error {
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("marshal %s %s request: %w", method, path, err)
		}
		body = strings.NewReader(string(encoded))
	}

	req, err := http.NewRequestWithContext(ctx, method, e.baseURL+path, body)
	if err != nil {
		return fmt.Errorf("build request %s %s: %w", method, path, err)
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := e.client.Do(req)
	if err != nil {
		return fmt.Errorf("request %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read %s %s response: %w", method, path, err)
	}
	if resp.StatusCode >= 400 {
		message := strings.TrimSpace(string(data))
		if message == "" {
			message = http.StatusText(resp.StatusCode)
		}
		return fmt.Errorf("%s %s returned status %d: %s", method, path, resp.StatusCode, message)
	}
	if out != nil && len(strings.TrimSpace(string(data))) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("decode %s %s response: %w", method, path, err)
		}
	}
	return nil
}

func cloneStringMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func sanitizeID(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "unknown"
	}
	parsed, err := url.PathUnescape(trimmed)
	if err == nil {
		trimmed = parsed
	}
	replacer := strings.NewReplacer("/", "-", "\\", "-", " ", "-")
	return replacer.Replace(trimmed)
}

func intPtr(v int) *int {
	return &v
}

func timePtr(v time.Time) *time.Time {
	return &v
}

func (e *Executor) emitStep(step, status, message string, details map[string]string) {
	if e.onStep == nil {
		return
	}
	e.onStep(StepEvent{
		Time:    time.Now().UTC(),
		Step:    strings.TrimSpace(step),
		Status:  strings.TrimSpace(status),
		Message: strings.TrimSpace(message),
		Details: cloneStringMap(details),
	})
}
