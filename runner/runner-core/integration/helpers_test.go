//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/marginlab/margin-eval/runner/runner-core/runbundle"
	"github.com/marginlab/margin-eval/runner/runner-core/store"
	"github.com/marginlab/margin-eval/runner/runner-core/testfixture"
)

const (
	fakeImageTag = "marginlab-fake-agent-server:it"
	realImageTag = "marginlab-agent-server:it"
)

var (
	integrationEnvOnce sync.Once
	fakeBuildOnce      sync.Once
	realBuildOnce      sync.Once
	fakeBinaryOnce     sync.Once
	realBinaryOnce     sync.Once
	fakeBinaryPath     string
	realBinaryPath     string
)

type agentMatrixCase struct {
	DefinitionName string `json:"definition_name"`
	ConfigName     string `json:"config_name"`
}

func ensureIntegrationEnv(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker is required for integration tests")
	}
	integrationEnvOnce.Do(func() {})
}

func ensureFakeImageBuilt(t *testing.T) {
	t.Helper()
	fakeBuildOnce.Do(func() {
		runCmd(t, filepath.Join(repoRoot(t), "tools", "fake-agent-server"), "docker", "build", "-t", fakeImageTag, ".")
	})
}

func ensureRealImageBuilt(t *testing.T) {
	t.Helper()
	realBuildOnce.Do(func() {
		runCmd(t, repoRoot(t), "docker", "build", "-f", filepath.Join("agent-server", "integration", "testdata", "Dockerfile"), "-t", realImageTag, ".")
	})
}

func ensureFakeAgentServerBinaryBuilt(t *testing.T) string {
	t.Helper()
	fakeBinaryOnce.Do(func() {
		outPath := filepath.Join(repoRoot(t), ".marginlab-local", "integration-bin", "fake-agent-server-"+runtime.GOARCH)
		if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
			t.Fatalf("create fake-agent-server output dir: %v", err)
		}
		runCmdWithEnv(t, filepath.Join(repoRoot(t), "tools", "fake-agent-server"), map[string]string{
			"CGO_ENABLED": "0",
			"GOOS":        "linux",
			"GOARCH":      runtime.GOARCH,
		}, "go", "build", "-o", outPath, ".")
		fakeBinaryPath = outPath
	})
	return fakeBinaryPath
}

func ensureRealAgentServerBinaryBuilt(t *testing.T) string {
	t.Helper()
	realBinaryOnce.Do(func() {
		outPath := filepath.Join(repoRoot(t), ".marginlab-local", "integration-bin", "agent-server-"+runtime.GOARCH)
		if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
			t.Fatalf("create agent-server output dir: %v", err)
		}
		runCmdWithEnv(t, filepath.Join(repoRoot(t), "agent-server"), map[string]string{
			"CGO_ENABLED": "0",
			"GOOS":        "linux",
			"GOARCH":      runtime.GOARCH,
		}, "go", "build", "-o", outPath, "./cmd/agent-server")
		realBinaryPath = outPath
	})
	return realBinaryPath
}

func runCmd(t *testing.T, workdir string, name string, args ...string) string {
	t.Helper()
	out, err := runCmdNoFail(workdir, name, args...)
	if err != nil {
		t.Fatalf("command failed: %s %s\noutput:\n%s\nerr:%v", name, strings.Join(args, " "), out, err)
	}
	return out
}

func runCmdWithEnv(t *testing.T, workdir string, env map[string]string, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = workdir
	cmd.Env = os.Environ()
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("command failed: %s %s\noutput:\n%s\nerr:%v", name, strings.Join(args, " "), string(out), err)
	}
	return string(out)
}

func runCmdNoFail(workdir string, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = workdir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func bundleWithCaseImage(bundle runbundle.Bundle, image string) runbundle.Bundle {
	if len(bundle.ResolvedSnapshot.Cases) == 0 {
		return bundle
	}
	bundle.ResolvedSnapshot.Cases[0].Image = resolveDigestPinnedLocalImage(image)
	return bundle
}

func resolveDigestPinnedLocalImage(image string) string {
	trimmed := strings.TrimSpace(image)
	if trimmed == "" || strings.Contains(trimmed, "@sha256:") {
		return trimmed
	}
	cmd := exec.Command("docker", "image", "inspect", trimmed, "--format", "{{.Id}}")
	output, err := cmd.CombinedOutput()
	if err != nil {
		panic("resolve digest-pinned image " + trimmed + ": " + strings.TrimSpace(string(output)))
	}
	digest := strings.TrimSpace(string(output))
	digest = strings.TrimPrefix(digest, "sha256:")
	if len(digest) != 64 {
		panic("unexpected image digest for " + trimmed + ": " + digest)
	}
	return trimmed + "@sha256:" + digest
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatalf("resolve caller path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
}

func buildBundle(prompt, definitionName, configName, version string) runbundle.Bundle {
	if strings.TrimSpace(definitionName) == "" {
		definitionName = "codex"
	}
	if strings.TrimSpace(configName) == "" {
		configName = testfixture.RepoOwnedDefaultConfigName(definitionName)
	}
	agent := testfixture.RepoOwnedAgentWithConfigVersion(definitionName, configName, version)
	if strings.TrimSpace(configName) != agent.Config.Name {
		panic("repo-owned config mismatch for definition " + definitionName)
	}
	return buildBundleWithAgent(prompt, agent)
}

func buildBundleWithAgent(prompt string, agent runbundle.Agent) runbundle.Bundle {
	return runbundle.Bundle{
		SchemaVersion: runbundle.SchemaVersionV1,
		BundleID:      "bun_it",
		CreatedAt:     time.Now().UTC(),
		Source:        runbundle.Source{Kind: runbundle.SourceKindLocalFiles, SubmitProjectID: "proj_it"},
		ResolvedSnapshot: runbundle.ResolvedSnapshot{
			Name: "it-run",
			Execution: runbundle.Execution{Mode: runbundle.ExecutionModeFull,
				MaxConcurrency:        1,
				FailFast:              false,
				InstanceTimeoutSecond: 240,
			},
			Agent:       agent,
			RunDefaults: runbundle.RunDefault{Cwd: "/marginlab/workspaces", Env: map[string]string{"TERM": "xterm-256color"}, PTY: runbundle.PTY{Cols: 120, Rows: 40}},
			Cases: []runbundle.Case{{
				CaseID:            "case_1",
				Image:             "ghcr.io/acme/repo@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
				InitialPrompt:     prompt,
				TestCommand:       []string{"bash", "-lc", "true"},
				TestCwd:           "/work",
				TestTimeoutSecond: 120,
				TestAssets:        testfixture.MinimalTestAssets(),
			}},
		},
	}
}

func buildInstructionBundle(t *testing.T, definitionName, configName, version string) runbundle.Bundle {
	t.Helper()
	if strings.TrimSpace(definitionName) == "" {
		definitionName = "codex"
	}
	if strings.TrimSpace(configName) == "" {
		configName = testfixture.RepoOwnedDefaultConfigName(definitionName)
	}
	agent, _, err := testfixture.RepoOwnedAgentWithInstructionFixtures(definitionName, configName, version)
	if err != nil {
		t.Fatalf("build repo-owned agent with instruction fixtures: %v", err)
	}
	if strings.TrimSpace(configName) != agent.Config.Name {
		t.Fatalf("repo-owned config mismatch for definition %s", definitionName)
	}
	return buildBundleWithAgent(testfixture.IntegrationInstructionPrompt(), agent)
}

func bundleWithFakeEval(bundle runbundle.Bundle) runbundle.Bundle {
	if len(bundle.ResolvedSnapshot.Cases) == 0 {
		return bundle
	}
	bundle.ResolvedSnapshot.Cases[0].TestCommand = []string{"bash", "-lc", "/marginlab/tests/run-eval"}
	return bundle
}

func waitForRunTerminal(t *testing.T, rs store.RunStore, runID string, timeout time.Duration) store.Run {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		r, err := rs.GetRun(context.Background(), runID, false)
		if err == nil && r.State.IsTerminal() {
			return r
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for terminal run: %s", runID)
	return store.Run{}
}

func createSecretsFile(t *testing.T, openAIKey, anthropicKey string) string {
	t.Helper()
	var lines []string
	if strings.TrimSpace(openAIKey) != "" {
		lines = append(lines, "OPENAI_API_KEY="+openAIKey)
	}
	if strings.TrimSpace(anthropicKey) != "" {
		lines = append(lines, "ANTHROPIC_API_KEY="+anthropicKey)
	}
	f, err := os.CreateTemp(repoRoot(t), ".agent-secrets-*.env")
	if err != nil {
		t.Fatalf("create temp secrets file: %v", err)
	}
	path := f.Name()
	if err := f.Close(); err != nil {
		t.Fatalf("close temp secrets file: %v", err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("write secrets file: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Remove(path)
	})
	return path
}

func loadMatrixCases(t *testing.T) []agentMatrixCase {
	t.Helper()
	matrixPath := filepath.Join(repoRoot(t), "tools", "integration-matrix", "matrix.json")
	body, err := os.ReadFile(matrixPath)
	if err != nil {
		t.Fatalf("read matrix file: %v", err)
	}
	var doc struct {
		Cases []agentMatrixCase `json:"cases"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("decode matrix file: %v", err)
	}
	if len(doc.Cases) == 0 {
		t.Fatalf("integration matrix has no cases: %s", matrixPath)
	}
	out := make([]agentMatrixCase, 0, len(doc.Cases))
	for _, c := range doc.Cases {
		c.DefinitionName = strings.TrimSpace(c.DefinitionName)
		c.ConfigName = strings.TrimSpace(c.ConfigName)
		if c.DefinitionName == "" {
			t.Fatalf("matrix definition_name is required in %s", matrixPath)
		}
		if c.ConfigName == "" {
			c.ConfigName = testfixture.RepoOwnedDefaultConfigName(c.DefinitionName)
		}
		resolvedDefinition := testfixture.RepoOwnedDefinitionNameForConfig(c.ConfigName)
		if resolvedDefinition != c.DefinitionName {
			t.Fatalf("matrix config_name %q belongs to %q, not %q", c.ConfigName, resolvedDefinition, c.DefinitionName)
		}
		out = append(out, c)
	}
	return out
}

func caseRequiresKey(c agentMatrixCase) string {
	requiredEnv := testfixture.RepoOwnedRequiredEnvForConfig(c.DefinitionName, c.ConfigName)
	if len(requiredEnv) == 0 {
		return ""
	}
	return requiredEnv[0]
}
