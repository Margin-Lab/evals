//go:build integration

package integration

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"nhooyr.io/websocket"
)

const (
	envOpenAIKey    = "OPENAI_API_KEY"
	envAnthropicKey = "ANTHROPIC_API_KEY"
	envArtifactsDir = "AGENT_SERVER_IT_ARTIFACTS_DIR"
	envITPrefix     = "AGENT_SERVER_IT_"
)

type responseEnvelope struct {
	Error struct {
		Code    string         `json:"code"`
		Message string         `json:"message"`
		Details map[string]any `json:"details"`
	} `json:"error"`
}

type runStateResponse struct {
	RunID            string `json:"run_id"`
	State            string `json:"state"`
	PID              *int   `json:"pid"`
	ExitCode         *int   `json:"exit_code"`
	TrajectoryStatus string `json:"trajectory_status"`
}

type httpResponse struct {
	StatusCode int
	Body       []byte
	Header     http.Header
}

type serverContainer struct {
	container testcontainers.Container
	httpBase  string
	wsBase    string
	client    *http.Client
}

func ensureDockerProviderHealthy(t *testing.T) {
	t.Helper()
	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("docker provider unavailable: %v", recovered)
		}
	}()

	provider, err := testcontainers.ProviderDocker.GetProvider()
	if err != nil {
		t.Fatalf("docker provider unavailable: %v", err)
	}
	defer func() { _ = provider.Close() }()

	if err := provider.Health(context.Background()); err != nil {
		t.Fatalf("docker provider health check failed: %v", err)
	}
}

func startServerContainer(t *testing.T, extraEnv map[string]string) *serverContainer {
	t.Helper()

	ctx := context.Background()
	env := integrationContainerEnv(extraEnv)

	req := testcontainers.ContainerRequest{
		FromDockerfile: testcontainers.FromDockerfile{
			Context:    repositoryRoot(),
			Dockerfile: filepath.ToSlash(filepath.Join("agent-server", "integration", "testdata", "Dockerfile")),
			Repo:       "agent-server-integration",
			Tag:        "local",
			KeepImage:  true,
		},
		ExposedPorts: []string{"8080/tcp"},
		Env:          env,
		WaitingFor: wait.ForHTTP("/healthz").
			WithPort("8080/tcp").
			WithStartupTimeout(6 * time.Minute),
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("start container: %v", err)
	}
	testcontainers.CleanupContainer(t, container)
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		dumpContainerLogs(t, container)
	})

	endpoint, err := container.Endpoint(ctx, "")
	if err != nil {
		t.Fatalf("resolve container endpoint: %v", err)
	}
	httpBase, wsBase, err := endpointBaseURLs(endpoint)
	if err != nil {
		t.Fatalf("parse endpoint %q: %v", endpoint, err)
	}

	return &serverContainer{
		container: container,
		httpBase:  httpBase,
		wsBase:    wsBase,
		client: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

func repositoryRoot() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return filepath.Clean(filepath.Join("..", ".."))
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func integrationContainerEnv(extraEnv map[string]string) map[string]string {
	env := map[string]string{
		"AGENT_SERVER_LISTEN":                     ":8080",
		"AGENT_SERVER_STOP_GRACE_TIMEOUT":         "4s",
		"AGENT_SERVER_TRAJECTORY_COLLECT_TIMEOUT": "8s",
		"AGENT_SERVER_TRAJECTORY_POLL_INTERVAL":   "200ms",
	}

	for _, pair := range os.Environ() {
		key, value, ok := strings.Cut(pair, "=")
		if !ok || !strings.HasPrefix(key, envITPrefix) {
			continue
		}
		mappedKey := strings.TrimSpace(strings.TrimPrefix(key, envITPrefix))
		if mappedKey == "" {
			continue
		}
		if isProviderCredentialEnvKey(mappedKey) {
			continue
		}
		env[mappedKey] = value
	}

	for key, value := range extraEnv {
		env[key] = value
	}
	return env
}

func isProviderCredentialEnvKey(key string) bool {
	normalized := strings.ToUpper(strings.TrimSpace(key))
	return normalized == envOpenAIKey || normalized == envAnthropicKey
}

func endpointBaseURLs(endpoint string) (string, string, error) {
	normalized := strings.TrimSpace(endpoint)
	if normalized == "" {
		return "", "", fmt.Errorf("empty endpoint")
	}
	if !strings.Contains(normalized, "://") {
		normalized = "http://" + normalized
	}
	parsed, err := url.Parse(normalized)
	if err != nil {
		return "", "", err
	}
	if parsed.Host == "" {
		return "", "", fmt.Errorf("endpoint host is empty")
	}

	wsScheme := "ws"
	if parsed.Scheme == "https" {
		wsScheme = "wss"
	}
	httpBase := fmt.Sprintf("%s://%s", parsed.Scheme, parsed.Host)
	wsBase := fmt.Sprintf("%s://%s", wsScheme, parsed.Host)
	return httpBase, wsBase, nil
}

func dumpContainerLogs(t *testing.T, container testcontainers.Container) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	reader, err := container.Logs(ctx)
	if err != nil {
		t.Logf("failed to fetch container logs: %v", err)
		return
	}
	defer func() { _ = reader.Close() }()

	data, err := io.ReadAll(reader)
	if err != nil {
		t.Logf("failed to read container logs: %v", err)
		return
	}
	t.Logf("container logs:\n%s", string(data))
}

func (s *serverContainer) requestJSON(t *testing.T, method, path string, payload any) httpResponse {
	t.Helper()

	normalizedPath := path
	if !strings.HasPrefix(normalizedPath, "/") {
		normalizedPath = "/" + normalizedPath
	}

	var bodyReader io.Reader
	if payload != nil {
		body, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal request payload: %v", err)
		}
		bodyReader = bytes.NewReader(body)
	}

	req, err := http.NewRequest(method, s.httpBase+normalizedPath, bodyReader)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := s.client.Do(req)
	if err != nil {
		t.Fatalf("execute request %s %s: %v", method, normalizedPath, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	return httpResponse{
		StatusCode: resp.StatusCode,
		Body:       body,
		Header:     resp.Header.Clone(),
	}
}

func (s *serverContainer) exec(t *testing.T, cmd []string) (int, string) {
	t.Helper()

	exitCode, output, err := s.container.Exec(context.Background(), cmd)
	if err != nil {
		t.Fatalf("container exec %v failed: %v", cmd, err)
	}
	data, err := io.ReadAll(output)
	if err != nil {
		t.Fatalf("read exec output for %v: %v", cmd, err)
	}
	return exitCode, string(data)
}

func (s *serverContainer) writeRunArtifact(t *testing.T, runID string, name string, contents []byte) string {
	t.Helper()

	runID = strings.TrimSpace(runID)
	name = strings.TrimSpace(name)
	if runID == "" {
		t.Fatalf("write run artifact: run id is required")
	}
	if name == "" {
		t.Fatalf("write run artifact: artifact name is required")
	}
	if strings.Contains(name, "/") || strings.Contains(name, "\\") || name == "." || name == ".." {
		t.Fatalf("write run artifact: invalid artifact name %q", name)
	}

	artifactsDir := filepath.Join("/marginlab/state/runs", runID, "artifacts")
	exitCode, output := s.exec(t, []string{"mkdir", "-p", artifactsDir})
	if exitCode != 0 {
		t.Fatalf("write run artifact: mkdir %q failed (exit=%d): %s", artifactsDir, exitCode, output)
	}

	targetPath := filepath.Join(artifactsDir, name)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := s.container.CopyToContainer(ctx, contents, targetPath, 0o644); err != nil {
		t.Fatalf("write run artifact: copy to %q failed: %v", targetPath, err)
	}

	return targetPath
}

func (s *serverContainer) mkdirWorkspace(t *testing.T, suffix string) string {
	t.Helper()

	workspace := "/marginlab/workspaces/" + sanitizeToken(suffix)
	exitCode, output := s.exec(t, []string{"mkdir", "-p", workspace})
	if exitCode != 0 {
		t.Fatalf("mkdir workspace %q failed (exit=%d): %s", workspace, exitCode, output)
	}
	return workspace
}

func (s *serverContainer) dialPTY(ctx context.Context, runID string) (*websocket.Conn, *http.Response, error) {
	wsURL := fmt.Sprintf("%s/v1/run/pty?run_id=%s", s.wsBase, url.QueryEscape(runID))
	return websocket.Dial(ctx, wsURL, nil)
}

func waitForPTYExitMessage(ctx context.Context, conn *websocket.Conn) (int, error) {
	for {
		msgType, payload, err := conn.Read(ctx)
		if err != nil {
			return 0, err
		}
		if msgType != websocket.MessageText {
			continue
		}
		var control struct {
			Type     string `json:"type"`
			ExitCode int    `json:"exit_code"`
		}
		if err := json.Unmarshal(payload, &control); err != nil {
			continue
		}
		if control.Type == "exit" {
			return control.ExitCode, nil
		}
	}
}

func (s *serverContainer) waitForRunState(t *testing.T, timeout time.Duration, acceptable ...string) runStateResponse {
	t.Helper()

	wanted := make(map[string]struct{}, len(acceptable))
	for _, state := range acceptable {
		wanted[state] = struct{}{}
	}

	deadline := time.Now().Add(timeout)
	lastStatus := 0
	lastBody := ""
	for time.Now().Before(deadline) {
		resp := s.requestJSON(t, http.MethodGet, "/v1/run", nil)
		lastStatus = resp.StatusCode
		lastBody = string(resp.Body)
		if resp.StatusCode == http.StatusOK {
			record := decodeJSON[runStateResponse](t, resp.Body)
			if _, ok := wanted[record.State]; ok {
				return record
			}
		}
		time.Sleep(250 * time.Millisecond)
	}

	t.Fatalf("timed out waiting for run state in %v (acceptable=%v, last_status=%d, last_body=%s)", acceptable, acceptable, lastStatus, lastBody)
	return runStateResponse{}
}

func (s *serverContainer) exportRunArtifacts(t *testing.T, runID string) {
	t.Helper()

	root := strings.TrimSpace(os.Getenv(envArtifactsDir))
	if root == "" {
		return
	}
	if strings.TrimSpace(runID) == "" {
		t.Fatalf("export run artifacts: run id is required")
	}

	targetDir := filepath.Join(root, sanitizeToken(t.Name()), sanitizeToken(runID))
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		t.Fatalf("export run artifacts: create target directory %q failed: %v", targetDir, err)
	}

	runDir := filepath.Join("/marginlab/state/runs", runID)
	sourceDir := filepath.Join(runDir, "artifacts")
	containerTarPath := filepath.Join("/tmp", "agent-server-it-"+sanitizeToken(runID)+"-artifacts.tar")

	exitCode, output := s.exec(t, []string{"tar", "-C", runDir, "-cf", containerTarPath, "artifacts"})
	if exitCode != 0 {
		t.Fatalf("export run artifacts: create tar for %q failed (exit=%d): %s", sourceDir, exitCode, output)
	}

	defer func() {
		_, _ = s.execIgnoreError([]string{"rm", "-f", containerTarPath})
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	reader, err := s.container.CopyFileFromContainer(ctx, containerTarPath)
	if err != nil {
		t.Fatalf("export run artifacts: copy tar %q from container failed: %v", containerTarPath, err)
	}
	defer func() { _ = reader.Close() }()

	fileCount, err := extractTarToDir(reader, targetDir)
	if err != nil {
		t.Fatalf("export run artifacts: extract tar for %q into %q failed: %v", sourceDir, targetDir, err)
	}

	exportedRoot := filepath.Join(targetDir, "artifacts")
	t.Logf("exported run artifacts (files=%d): %s", fileCount, exportedRoot)
}

func (s *serverContainer) execIgnoreError(cmd []string) (int, string) {
	exitCode, output, err := s.container.Exec(context.Background(), cmd)
	if err != nil {
		return 1, err.Error()
	}
	data, readErr := io.ReadAll(output)
	if readErr != nil {
		return 1, readErr.Error()
	}
	return exitCode, string(data)
}

func extractTarToDir(reader io.Reader, targetDir string) (int, error) {
	tr := tar.NewReader(reader)
	files := 0

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return files, nil
		}
		if err != nil {
			return files, err
		}

		name := strings.TrimLeft(hdr.Name, "/")
		if name == "" || name == "." {
			continue
		}

		destination := filepath.Join(targetDir, filepath.Clean(name))
		rel, err := filepath.Rel(targetDir, destination)
		if err != nil {
			return files, fmt.Errorf("resolve target path %q: %w", name, err)
		}
		if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return files, fmt.Errorf("tar entry escapes target directory: %q", hdr.Name)
		}

		mode := os.FileMode(hdr.Mode)
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(destination, mode.Perm()); err != nil {
				return files, fmt.Errorf("create directory %q: %w", destination, err)
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
				return files, fmt.Errorf("create parent directory for %q: %w", destination, err)
			}
			file, err := os.OpenFile(destination, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode.Perm())
			if err != nil {
				return files, fmt.Errorf("open %q: %w", destination, err)
			}
			if _, err := io.Copy(file, tr); err != nil {
				_ = file.Close()
				return files, fmt.Errorf("write %q: %w", destination, err)
			}
			if err := file.Close(); err != nil {
				return files, fmt.Errorf("close %q: %w", destination, err)
			}
			files++
		default:
			// No special handling needed for links/devices in current artifacts.
			continue
		}
	}
}

func codexVersions() []string {
	return versionsForAgent("codex")
}

func claudeCodeVersions() []string {
	return versionsForAgent("claude-code")
}

func opencodeVersions() []string {
	return versionsForAgent("opencode")
}

func openAIAPIKey() string {
	return strings.TrimSpace(os.Getenv(envOpenAIKey))
}

func anthropicAPIKey() string {
	return strings.TrimSpace(os.Getenv(envAnthropicKey))
}

func sanitizeToken(value string) string {
	replacer := strings.NewReplacer(
		"/", "_",
		"\\", "_",
		":", "_",
		".", "_",
		" ", "_",
		"-", "_",
	)
	cleaned := replacer.Replace(value)
	cleaned = strings.Trim(cleaned, "_")
	if cleaned == "" {
		return "default"
	}
	return strings.ToLower(cleaned)
}

func decodeJSON[T any](t *testing.T, body []byte) T {
	t.Helper()
	var out T
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode JSON failed: %v\nbody: %s", err, string(body))
	}
	return out
}

func assertStatusCode(t *testing.T, resp httpResponse, want int) {
	t.Helper()
	if resp.StatusCode != want {
		t.Fatalf("status = %d, want %d\nbody: %s", resp.StatusCode, want, string(resp.Body))
	}
}

func assertAPIError(t *testing.T, resp httpResponse, wantStatus int, wantCode string) {
	t.Helper()
	if resp.StatusCode != wantStatus {
		t.Fatalf("status = %d, want %d\nbody: %s", resp.StatusCode, wantStatus, string(resp.Body))
	}
	envelope := decodeJSON[responseEnvelope](t, resp.Body)
	if envelope.Error.Code != wantCode {
		t.Fatalf("error code = %q, want %q\nbody: %s", envelope.Error.Code, wantCode, string(resp.Body))
	}
}

func assertAPIErrorCodeOneOf(t *testing.T, resp httpResponse, wantStatus int, wantCodes ...string) {
	t.Helper()
	if resp.StatusCode != wantStatus {
		t.Fatalf("status = %d, want %d\nbody: %s", resp.StatusCode, wantStatus, string(resp.Body))
	}
	envelope := decodeJSON[responseEnvelope](t, resp.Body)
	for _, wantCode := range wantCodes {
		if envelope.Error.Code == wantCode {
			return
		}
	}
	t.Fatalf("error code = %q, want one of %v\nbody: %s", envelope.Error.Code, wantCodes, string(resp.Body))
}
