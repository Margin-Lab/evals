//go:build integration && integration_model

package integration

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/marginlab/margin-eval/agent-server/internal/apperr"
	"github.com/marginlab/margin-eval/runner/runner-core/testfixture"
	"nhooyr.io/websocket"
)

var (
	ansiCSIRegexp = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)
	ansiOSCRegexp = regexp.MustCompile(`\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)`)
)

type runSnapshotResponse struct {
	RunID           string `json:"run_id"`
	Agent           string `json:"agent"`
	RunState        string `json:"run_state"`
	CapturedAt      string `json:"captured_at"`
	ContentType     string `json:"content_type"`
	ContentEncoding string `json:"content_encoding"`
	Content         string `json:"content"`
	Truncated       bool   `json:"truncated"`
}

type ptyExitResult struct {
	ExitCode int
	Output   []byte
}

func TestServerRunAndPTYModelMatrix(t *testing.T) {
	ensureDockerProviderHealthy(t)

	for _, tc := range expandVersionedMatrixCases(t) {
		tc := tc
		requireProviderEnvForAgent(t, tc.DefinitionName)

		t.Run(caseName(tc.DefinitionName, tc.ConfigName, tc.AgentVersion), func(t *testing.T) {
			runModelSuite(t, tc.DefinitionName, tc.ConfigName, tc.AgentVersion)
		})
	}
}

func runModelSuite(t *testing.T, agentName, configName, version string) {
	expectedResponse := testfixture.IntegrationInstructionKeywordsForAgent(agentName).ExpectedResponse()
	extraEnv := modelProviderEnv()
	if agentName == "opencode" {
		extraEnv["AGENT_SERVER_TRAJECTORY_COLLECT_TIMEOUT"] = "25s"
	}
	server := startServerContainer(t, extraEnv)

	resp := server.requestJSON(t, http.MethodPut, "/v1/agent-definition", repoOwnedDefinitionRequest(t, agentName))
	assertStatusCode(t, resp, http.StatusCreated)

	resp = server.requestJSON(t, http.MethodPut, "/v1/agent-config", repoOwnedInstructionConfigRequest(t, agentName, configName, version))
	assertStatusCode(t, resp, http.StatusCreated)

	resp = server.requestJSON(t, http.MethodPost, "/v1/agent/install", nil)
	assertStatusCode(t, resp, http.StatusOK)

	workspace := server.mkdirWorkspace(t, "run-model-"+agentName+"-"+configName+"-"+version)
	startPayload := modelStartPayload(workspace)

	startRespHTTP := server.requestJSON(t, http.MethodPost, "/v1/run", startPayload)
	assertStatusCode(t, startRespHTTP, http.StatusCreated)
	startResp := decodeJSON[startRunResponse](t, startRespHTTP.Body)
	if startResp.RunID == "" || startResp.PID == nil || *startResp.PID <= 0 {
		t.Fatalf("start response = %+v", startResp)
	}
	defer server.exportRunArtifacts(t, startResp.RunID)

	_ = server.waitForRunState(t, 20*time.Second, "starting", "running", "collecting")

	resp = server.requestJSON(t, http.MethodPost, "/v1/run", startPayload)
	assertAPIError(t, resp, http.StatusConflict, apperr.CodeRunAlreadyActive)

	resp = server.requestJSON(t, http.MethodPut, "/v1/agent-definition", repoOwnedDefinitionRequest(t, agentName))
	assertAPIError(t, resp, http.StatusConflict, apperr.CodeRunAlreadyActive)

	resp = server.requestJSON(t, http.MethodPut, "/v1/agent-config", repoOwnedInstructionConfigRequest(t, agentName, configName, version))
	assertAPIError(t, resp, http.StatusConflict, apperr.CodeRunAlreadyActive)

	resp = server.requestJSON(t, http.MethodPost, "/v1/agent/install", nil)
	assertAPIError(t, resp, http.StatusConflict, apperr.CodeRunAlreadyActive)

	resp = server.requestJSON(t, http.MethodGet, "/v1/run/pty?run_id=wrong", nil)
	assertAPIErrorCodeOneOf(t, resp, http.StatusConflict, apperr.CodeRunIDMismatch, apperr.CodeRunNotActive)

	conn := dialPTYWithRetry(t, server, startResp.RunID, 30*time.Second)
	defer func() {
		_ = conn.Close(websocket.StatusNormalClosure, "test done")
	}()

	exitCtx, exitCancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer exitCancel()
	exitResultCh := make(chan ptyExitResult, 1)
	exitErrCh := make(chan error, 1)
	go func() {
		exitResult, err := waitForPTYExitMessageWithOutput(exitCtx, conn)
		if err != nil {
			exitErrCh <- err
			return
		}
		exitResultCh <- exitResult
	}()

	exited := server.waitForRunState(t, 150*time.Second, "exited")
	assertExitedRun(t, server, exited, startResp.RunID, agentName)
	runOutput := assertWSExitCodeMatchesRun(t, *exited.ExitCode, exitResultCh, exitErrCh, 15*time.Second)
	assertTerminalOutputContains(t, runOutput, expectedResponse, agentName+" PTY output")

	supportsSnapshot := agentName == "codex" || agentName == "claude-code"
	if supportsSnapshot {
		snapshotOutput := captureSnapshotArtifact(t, server, startResp.RunID, agentName)
		assertTerminalOutputContains(t, snapshotOutput, expectedResponse, agentName+" snapshot output")
	} else {
		resp = server.requestJSON(t, http.MethodPost, "/v1/run/snapshot", map[string]any{
			"run_id": startResp.RunID,
			"pty":    map[string]int{"cols": 120, "rows": 40},
		})
		assertAPIError(t, resp, http.StatusBadRequest, apperr.CodeSnapshotUnsupported)
	}

	resp = server.requestJSON(t, http.MethodDelete, "/v1/run", nil)
	assertStatusCode(t, resp, http.StatusOK)
	deleted := decodeJSON[deleteRunResponse](t, resp.Body)
	if deleted.State != "idle" {
		t.Fatalf("delete state = %q, want %q", deleted.State, "idle")
	}

	resp = server.requestJSON(t, http.MethodGet, "/v1/state", nil)
	assertStatusCode(t, resp, http.StatusOK)
	finalState := decodeJSON[stateResponse](t, resp.Body)
	if finalState.Run.State != "idle" || finalState.Run.RunID != nil {
		t.Fatalf("final run state = %+v, want idle", finalState.Run)
	}
}

func requireProviderEnvForAgent(t *testing.T, agentName string) {
	t.Helper()
	for _, envName := range testfixture.RepoOwnedRequiredEnv(agentName) {
		switch envName {
		case envOpenAIKey:
			if openAIAPIKey() == "" {
				t.Fatalf("%s is required for %s model integration tests", envOpenAIKey, agentName)
			}
		case envAnthropicKey:
			if anthropicAPIKey() == "" {
				t.Fatalf("%s is required for %s model integration tests", envAnthropicKey, agentName)
			}
		case envGeminiKey:
			if geminiAPIKey() == "" {
				t.Fatalf("%s is required for %s model integration tests", envGeminiKey, agentName)
			}
		}
	}
}

func modelStartPayload(workspace string) map[string]any {
	return map[string]any{
		"cwd":            workspace,
		"initial_prompt": testfixture.IntegrationInstructionPrompt(),
		"env": map[string]string{
			"TERM": "xterm-256color",
		},
		"pty": map[string]int{
			"cols": 120,
			"rows": 40,
		},
	}
}

func modelProviderEnv() map[string]string {
	out := map[string]string{}
	if value := openAIAPIKey(); value != "" {
		out[envOpenAIKey] = value
	}
	if value := anthropicAPIKey(); value != "" {
		out[envAnthropicKey] = value
	}
	if value := geminiAPIKey(); value != "" {
		out[envGeminiKey] = value
	}
	return out
}

func assertExitedRun(t *testing.T, server *serverContainer, exited runStateResponse, expectedRunID, expectedAgent string) {
	t.Helper()
	if exited.RunID != expectedRunID {
		t.Fatalf("run_id = %q, want %q", exited.RunID, expectedRunID)
	}
	if exited.ExitCode == nil {
		t.Fatalf("exit_code is nil, want %d", 0)
	}
	if got := *exited.ExitCode; got != 0 {
		t.Fatalf("exit_code = %d, want %d", got, 0)
	}
	if exited.TrajectoryStatus != "complete" {
		t.Fatalf("trajectory_status = %q, want %q", exited.TrajectoryStatus, "complete")
	}
	resp := server.requestJSON(t, http.MethodGet, "/v1/run/trajectory", nil)
	assertStatusCode(t, resp, http.StatusOK)
	var traj map[string]any
	if err := json.Unmarshal(resp.Body, &traj); err != nil {
		t.Fatalf("decode trajectory: %v", err)
	}
	if schema, ok := traj["schema_version"].(string); !ok || schema != "ATIF-v1.6" {
		t.Fatalf("trajectory.schema_version = %#v, want %q", traj["schema_version"], "ATIF-v1.6")
	}
	if sessionID, ok := traj["session_id"].(string); !ok || strings.TrimSpace(sessionID) == "" {
		t.Fatalf("trajectory.session_id = %#v, want non-empty string", traj["session_id"])
	}
	agent, ok := traj["agent"].(map[string]any)
	if !ok {
		t.Fatalf("trajectory.agent = %#v, want object", traj["agent"])
	}
	if name, ok := agent["name"].(string); !ok || name != expectedAgent {
		t.Fatalf("trajectory.agent.name = %#v, want %q", agent["name"], expectedAgent)
	}
}

func captureSnapshotArtifact(t *testing.T, server *serverContainer, runID string, agent string) []byte {
	t.Helper()

	resp := server.requestJSON(t, http.MethodPost, "/v1/run/snapshot", map[string]any{
		"run_id": runID,
		"pty": map[string]int{
			"cols": 120,
			"rows": 40,
		},
	})
	assertStatusCode(t, resp, http.StatusOK)

	snapshot := decodeJSON[runSnapshotResponse](t, resp.Body)
	content := assertSnapshotResponse(t, snapshot, runID, agent)
	artifactPath := server.writeRunArtifact(t, runID, "snapshot-pty.ansi", content)
	t.Logf("captured snapshot artifact: %s", artifactPath)
	return content
}

func assertSnapshotResponse(t *testing.T, snapshot runSnapshotResponse, runID string, agent string) []byte {
	t.Helper()

	if snapshot.RunID != runID {
		t.Fatalf("snapshot run_id = %q, want %q", snapshot.RunID, runID)
	}
	if snapshot.Agent != agent {
		t.Fatalf("snapshot agent = %q, want %q", snapshot.Agent, agent)
	}
	if snapshot.ContentType != "ansi" {
		t.Fatalf("snapshot content_type = %q, want %q", snapshot.ContentType, "ansi")
	}
	if snapshot.ContentEncoding != "base64" {
		t.Fatalf("snapshot content_encoding = %q, want %q", snapshot.ContentEncoding, "base64")
	}
	if snapshot.CapturedAt == "" || snapshot.Content == "" {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	content, err := base64.StdEncoding.DecodeString(snapshot.Content)
	if err != nil {
		t.Fatalf("decode snapshot content: %v", err)
	}
	if len(content) == 0 {
		t.Fatalf("decoded snapshot content should not be empty")
	}
	return content
}

func waitForPTYExitMessageWithOutput(ctx context.Context, conn *websocket.Conn) (ptyExitResult, error) {
	var output []byte
	for {
		msgType, payload, err := conn.Read(ctx)
		if err != nil {
			return ptyExitResult{}, err
		}
		switch msgType {
		case websocket.MessageBinary:
			output = append(output, payload...)
		case websocket.MessageText:
			var control struct {
				Type     string `json:"type"`
				ExitCode int    `json:"exit_code"`
			}
			if err := json.Unmarshal(payload, &control); err != nil {
				continue
			}
			if control.Type == "exit" {
				return ptyExitResult{
					ExitCode: control.ExitCode,
					Output:   append([]byte(nil), output...),
				}, nil
			}
		}
	}
}

func assertWSExitCodeMatchesRun(
	t *testing.T,
	expectedExitCode int,
	exitResultCh <-chan ptyExitResult,
	exitErrCh <-chan error,
	timeout time.Duration,
) []byte {
	t.Helper()

	select {
	case err := <-exitErrCh:
		t.Fatalf("waiting for websocket exit message failed: %v", err)
	case wsResult := <-exitResultCh:
		if wsResult.ExitCode != expectedExitCode {
			t.Fatalf("websocket exit_code = %d, want %d", wsResult.ExitCode, expectedExitCode)
		}
		if wsResult.ExitCode != 0 {
			t.Fatalf("websocket exit_code = %d, want %d", wsResult.ExitCode, 0)
		}
		return wsResult.Output
	case <-time.After(timeout):
		t.Fatalf("timed out waiting for websocket exit control message")
	}
	return nil
}

func assertTerminalOutputContains(t *testing.T, raw []byte, token string, label string) {
	t.Helper()

	normalized := normalizeTerminalOutput(raw)
	if strings.Contains(strings.ToLower(normalized), strings.ToLower(token)) {
		return
	}
	preview := normalized
	if len(preview) > 1200 {
		preview = preview[:1200]
	}
	t.Fatalf("%s does not contain %q\noutput preview:\n%s", label, token, preview)
}

func normalizeTerminalOutput(raw []byte) string {
	text := string(raw)
	text = ansiOSCRegexp.ReplaceAllString(text, "")
	text = ansiCSIRegexp.ReplaceAllString(text, "")
	text = strings.ReplaceAll(text, "\r", "")
	return text
}

func dialPTYWithRetry(t *testing.T, server *serverContainer, runID string, timeout time.Duration) *websocket.Conn {
	t.Helper()

	deadline := time.Now().Add(timeout)
	var lastErr error
	var lastStatus int
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		conn, resp, err := server.dialPTY(ctx, runID)
		cancel()
		if err == nil {
			return conn
		}
		lastErr = err
		lastStatus = 0
		if resp != nil {
			lastStatus = resp.StatusCode
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("timed out dialing PTY websocket (run_id=%s, last_status=%d, last_error=%v)", runID, lastStatus, lastErr)
	return nil
}
