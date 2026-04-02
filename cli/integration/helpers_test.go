//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/marginlab/margin-eval/runner/runner-core/runresults"
	"github.com/marginlab/margin-eval/runner/runner-core/store"
	"github.com/marginlab/margin-eval/runner/runner-core/testfixture"
	"github.com/marginlab/margin-eval/runner/runner-local/runfs"
)

const (
	envITMatrixFile = "MARGINLAB_IT_MATRIX_FILE"
	envITDotenvFile = "MARGINLAB_IT_DOTENV_FILE"

	envCodexVersions      = "MARGINLAB_IT_CODEX_VERSIONS"
	envClaudeCodeVersions = "MARGINLAB_IT_CLAUDE_CODE_VERSIONS"
	envGeminiCLIVersions  = "MARGINLAB_IT_GEMINI_CLI_VERSIONS"
	envOpencodeVersions   = "MARGINLAB_IT_OPENCODE_VERSIONS"
	envPiVersions         = "MARGINLAB_IT_PI_VERSIONS"

	envOpenAIKey     = "OPENAI_API_KEY"
	envAnthropicKey  = "ANTHROPIC_API_KEY"
	envGeminiKey     = "GEMINI_API_KEY"
	colimaDockerHost = "unix:///Users/josebouza/.colima/default/docker.sock"

	fakeImageTag = "marginlab-fake-agent-server:it"
	realImageTag = "marginlab-agent-server:it"
)

var (
	integrationEnvOnce sync.Once
	marginBuildOnce    sync.Once
	fakeBuildOnce      sync.Once
	realBuildOnce      sync.Once
	marginBinaryOnce   sync.Once
	fakeBinaryOnce     sync.Once
	realBinaryOnce     sync.Once

	marginBinaryPath string
	fakeBinaryPath   string
	realBinaryPath   string
)

type agentMatrixCase struct {
	DefinitionName string `json:"definition_name"`
	ConfigName     string `json:"config_name"`
}

type versionedMatrixCase struct {
	DefinitionName string
	ConfigName     string
	AgentVersion   string
}

type marginCommandResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

type cliRunSummary struct {
	RunID  string
	State  string
	RunDir string
}

type persistedRun struct {
	Results   runresults.Summary
	Artifacts []store.Artifact
}

func ensureIntegrationEnv(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker is required for integration tests")
	}
	integrationEnvOnce.Do(func() {})
}

func ensureMarginBinaryBuilt(t *testing.T) string {
	t.Helper()
	marginBuildOnce.Do(func() {
		runCmd(t, repoRoot(t), nil, "bash", filepath.Join("scripts", "prepare-embedded-agent-server.sh"))
	})
	marginBinaryOnce.Do(func() {
		outPath := filepath.Join(repoRoot(t), ".marginlab-local", "integration-bin", "margin")
		if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
			t.Fatalf("create margin output dir: %v", err)
		}
		runCmd(t, cliRoot(t), nil, "go", "build", "-o", outPath, "./cmd/margin")
		marginBinaryPath = outPath
	})
	return marginBinaryPath
}

func ensureFakeImageBuilt(t *testing.T) {
	t.Helper()
	fakeBuildOnce.Do(func() {
		runCmd(t, filepath.Join(repoRoot(t), "tools", "fake-agent-server"), nil, "docker", "build", "-t", fakeImageTag, ".")
	})
}

func ensureRealImageBuilt(t *testing.T) {
	t.Helper()
	realBuildOnce.Do(func() {
		runCmd(t, repoRoot(t), nil, "docker", "build", "-f", filepath.Join("agent-server", "integration", "testdata", "Dockerfile"), "-t", realImageTag, ".")
	})
}

func ensureFakeAgentServerBinaryBuilt(t *testing.T) string {
	t.Helper()
	fakeBinaryOnce.Do(func() {
		outPath := filepath.Join(repoRoot(t), ".marginlab-local", "integration-bin", "fake-agent-server-"+runtime.GOARCH)
		if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
			t.Fatalf("create fake-agent-server output dir: %v", err)
		}
		runCmd(t, filepath.Join(repoRoot(t), "tools", "fake-agent-server"), map[string]string{
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
		runCmd(t, filepath.Join(repoRoot(t), "agent-server"), map[string]string{
			"CGO_ENABLED": "0",
			"GOOS":        "linux",
			"GOARCH":      runtime.GOARCH,
		}, "go", "build", "-o", outPath, "./cmd/agent-server")
		realBinaryPath = outPath
	})
	return realBinaryPath
}

func repoRoot(t *testing.T) string {
	t.Helper()
	return repoRootFromCaller()
}

func repoRootFromCaller() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "."
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func cliRoot(t *testing.T) string {
	t.Helper()
	return filepath.Join(repoRoot(t), "cli")
}

func runCmd(t *testing.T, dir string, env map[string]string, name string, args ...string) string {
	t.Helper()
	output, err := runCmdNoFail(dir, env, name, args...)
	if err != nil {
		t.Fatalf("command failed: %s %s\noutput:\n%s\nerr:%v", name, strings.Join(args, " "), output, err)
	}
	return output
}

func runCmdNoFail(dir string, env map[string]string, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = mergedEnv(env)
	data, err := cmd.CombinedOutput()
	return string(data), err
}

func mergedEnv(extra map[string]string) []string {
	envMap := map[string]string{}
	for _, item := range os.Environ() {
		key, value, ok := strings.Cut(item, "=")
		if !ok {
			continue
		}
		envMap[key] = value
	}
	for key, value := range integrationCommandEnv() {
		envMap[key] = value
	}
	for key, value := range extra {
		envMap[key] = value
	}
	out := make([]string, 0, len(envMap))
	for key, value := range envMap {
		out = append(out, key+"="+value)
	}
	return out
}

func integrationCommandEnv() map[string]string {
	out := map[string]string{}
	if strings.TrimSpace(os.Getenv("DOCKER_HOST")) == "" {
		if _, err := os.Stat(strings.TrimPrefix(colimaDockerHost, "unix://")); err == nil {
			out["DOCKER_HOST"] = colimaDockerHost
		}
	}
	return out
}

func runMargin(t *testing.T, timeout time.Duration, extraEnv map[string]string, args ...string) marginCommandResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, ensureMarginBinaryBuilt(t), args...)
	cmd.Dir = repoRoot(t)
	cmd.Env = mergedEnv(extraEnv)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()

	exitCode := 0
	if err != nil {
		exitCode = -1
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else if ctx.Err() != nil {
			t.Fatalf("margin command timed out after %s\nstdout:\n%s\nstderr:\n%s", timeout, stdout.String(), stderr.String())
		} else {
			t.Fatalf("run margin: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
		}
	}

	return marginCommandResult{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: exitCode,
	}
}

func parseCLIRunSummary(t *testing.T, stdout string) cliRunSummary {
	t.Helper()
	var summary cliRunSummary
	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "run_id: "):
			summary.RunID = strings.TrimSpace(strings.TrimPrefix(line, "run_id: "))
		case strings.HasPrefix(line, "state: "):
			summary.State = strings.TrimSpace(strings.TrimPrefix(line, "state: "))
		case strings.HasPrefix(line, "run_dir: "):
			summary.RunDir = strings.TrimSpace(strings.TrimPrefix(line, "run_dir: "))
		}
	}
	if summary.RunID == "" || summary.State == "" || summary.RunDir == "" {
		t.Fatalf("missing cli run summary in stdout:\n%s", stdout)
	}
	return summary
}

func loadPersistedRun(t *testing.T, rootDir string, summary cliRunSummary) persistedRun {
	t.Helper()
	expectedRunDir := runfs.RunDir(rootDir, summary.RunID)
	if summary.RunDir != expectedRunDir {
		t.Fatalf("run_dir = %q, want %q", summary.RunDir, expectedRunDir)
	}
	for _, path := range []string{
		runfs.ResultsPath(rootDir, summary.RunID),
		runfs.ManifestPath(rootDir, summary.RunID),
		runfs.EventsPath(rootDir, summary.RunID),
		runfs.ArtifactsIndexPath(rootDir, summary.RunID),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected %s: %v", path, err)
		}
	}

	resultsRaw, err := os.ReadFile(runfs.ResultsPath(rootDir, summary.RunID))
	if err != nil {
		t.Fatalf("read results.json: %v", err)
	}
	var results runresults.Summary
	if err := json.Unmarshal(resultsRaw, &results); err != nil {
		t.Fatalf("decode results.json: %v", err)
	}

	artifactsRaw, err := os.ReadFile(runfs.ArtifactsIndexPath(rootDir, summary.RunID))
	if err != nil {
		t.Fatalf("read artifacts.json: %v", err)
	}
	var artifacts []store.Artifact
	if err := json.Unmarshal(artifactsRaw, &artifacts); err != nil {
		t.Fatalf("decode artifacts.json: %v", err)
	}

	return persistedRun{
		Results:   results,
		Artifacts: artifacts,
	}
}

func requireArtifactRole(t *testing.T, runDir string, artifacts []store.Artifact, role string) store.Artifact {
	t.Helper()
	for _, artifact := range artifacts {
		if artifact.Role != role {
			continue
		}
		path := filepath.Join(runDir, filepath.FromSlash(strings.TrimSpace(artifact.StoreKey)))
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("artifact %s path %q missing: %v", role, path, err)
		}
		return artifact
	}
	t.Fatalf("artifact role %q not found", role)
	return store.Artifact{}
}

func forbidArtifactRoles(t *testing.T, artifacts []store.Artifact, roles ...string) {
	t.Helper()
	for _, artifact := range artifacts {
		for _, role := range roles {
			if artifact.Role == role {
				t.Fatalf("unexpected artifact role %q", role)
			}
		}
	}
}

func readArtifactBody(t *testing.T, runDir string, artifact store.Artifact) string {
	t.Helper()
	path := filepath.Join(runDir, filepath.FromSlash(strings.TrimSpace(artifact.StoreKey)))
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read artifact %q: %v", path, err)
	}
	return string(body)
}

func resolveDigestPinnedLocalImage(image string) string {
	trimmed := strings.TrimSpace(image)
	if trimmed == "" || runbundle.IsPinnedImageRef(trimmed) {
		return trimmed
	}
	output, err := runCmdNoFail(repoRootFromCaller(), nil, "docker", "image", "inspect", trimmed, "--format", "{{.Id}}")
	if err != nil {
		panic("resolve digest-pinned image " + trimmed + ": " + strings.TrimSpace(output))
	}
	digest := strings.TrimSpace(output)
	digest = strings.TrimPrefix(digest, "sha256:")
	if len(digest) != 64 {
		panic("unexpected image digest for " + trimmed + ": " + digest)
	}
	return "sha256:" + digest
}

func writeFakeRunFixture(t *testing.T, prompt, testScript string, dryRun bool) (suitePath, configPath, evalPath string) {
	t.Helper()
	root := t.TempDir()
	definitionPath := filepath.Join(root, "definitions", "fake-agent")
	writeTextFile(t, filepath.Join(definitionPath, "definition.toml"), `kind = "agent_definition"
name = "fake-agent"

[config]
schema = "schema.json"

[run]
prepare = "hooks/run-prepare.sh"
`)
	writeTextFile(t, filepath.Join(definitionPath, "schema.json"), `{
  "type": "object",
  "required": ["command"],
  "additionalProperties": false,
  "properties": {
    "command": {
      "type": "array",
      "minItems": 1,
      "items": {
        "type": "string",
        "minLength": 1
      }
    }
  }
}
`)
	writeTextFile(t, filepath.Join(definitionPath, "hooks", "run-prepare.sh"), "#!/usr/bin/env bash\nset -euo pipefail\nprintf '{}\\n'\n")

	configPath = filepath.Join(root, "configs", "agents", "fake-agent-default")
	writeTextFile(t, filepath.Join(configPath, "config.toml"), `kind = "agent_config"
name = "fake-agent-default"
definition = "../../../definitions/fake-agent"
mode = "direct"

[input]
command = ["bash", "-lc", "echo hello"]
`)

	image := resolveDigestPinnedLocalImage(fakeImageTag)
	suitePath = filepath.Join(root, "suite")
	writeTextFile(t, filepath.Join(suitePath, "suite.toml"), "kind = \"test_suite\"\nname = \"cli-it\"\ncases = [\n  \"case-1\"\n]\n")
	writeTextFile(t, filepath.Join(suitePath, "cases", "case-1", "case.toml"), fmt.Sprintf(`kind = "test_case"
name = "case-1"
image = %q
agent_cwd = "/workspace"
test_cwd = "/work"
test_timeout_seconds = 120
`, image))
	writeTextFile(t, filepath.Join(suitePath, "cases", "case-1", "prompt.md"), prompt+"\n")
	writeTextFile(t, filepath.Join(suitePath, "cases", "case-1", "tests", "test.sh"), testScript)

	evalPath = filepath.Join(root, "configs", "evals", "cli-it.toml")
	mode := ""
	if dryRun {
		mode = "\ndry_run = true"
	}
	writeTextFile(t, evalPath, `kind = "eval_config"
name = "cli-it"
max_concurrency = 1
fail_fast = false
instance_timeout_seconds = 180`+mode+`
`)
	return suitePath, configPath, evalPath
}

func writeModelRunFixture(t *testing.T, definitionName, configName, version string) (suitePath, configPath, evalPath string) {
	t.Helper()
	root := t.TempDir()
	configPath = writeRepoOwnedInstructionConfigFixture(t, root, definitionName, configName, version)
	image := resolveDigestPinnedLocalImage(realImageTag)
	suitePath = filepath.Join(root, "suite")
	writeTextFile(t, filepath.Join(suitePath, "suite.toml"), "kind = \"test_suite\"\nname = \"cli-model-it\"\ncases = [\n  \"case-1\"\n]\n")
	writeTextFile(t, filepath.Join(suitePath, "cases", "case-1", "case.toml"), fmt.Sprintf(`kind = "test_case"
name = "case-1"
image = %q
agent_cwd = "/workspace"
test_cwd = "/work"
test_timeout_seconds = 120
`, image))
	writeTextFile(t, filepath.Join(suitePath, "cases", "case-1", "prompt.md"), testfixture.IntegrationInstructionPrompt()+"\n")
	writeTextFile(t, filepath.Join(suitePath, "cases", "case-1", "tests", "test.sh"), "#!/usr/bin/env bash\ntrue\n")

	evalPath = filepath.Join(root, "configs", "evals", "cli-model-it.toml")
	writeTextFile(t, evalPath, `kind = "eval_config"
name = "cli-model-it"
max_concurrency = 1
fail_fast = false
instance_timeout_seconds = 240
`)
	return suitePath, configPath, evalPath
}

func writeRepoOwnedInstructionConfigFixture(t *testing.T, root, definitionName, configName, version string) string {
	t.Helper()
	sourceDir := filepath.Join(repoRoot(t), "configs", "example-agent-configs", configName)
	destDir := filepath.Join(root, "configs", "agents", configName)
	copyDir(t, sourceDir, destDir)

	configTomlPath := filepath.Join(destDir, "config.toml")
	body, err := os.ReadFile(configTomlPath)
	if err != nil {
		t.Fatalf("read %s: %v", configTomlPath, err)
	}
	updated := string(body)
	definitionPath := filepath.Join(repoRoot(t), "configs", "agent-definitions", definitionName)
	updated = replaceSingleMatch(t, updated, `(?m)^definition\s*=.*$`, fmt.Sprintf("definition = %q", filepath.ToSlash(definitionPath)))
	if versionField := versionFieldForDefinition(definitionName); versionField != "" && version != "" && configName == testfixture.RepoOwnedDefaultConfigName(definitionName) {
		updated = replaceSingleMatch(t, updated, `(?m)^`+regexp.QuoteMeta(versionField)+`\s*=.*$`, fmt.Sprintf("%s = %q", versionField, version))
	}

	keywords := testfixture.IntegrationInstructionKeywordsForAgent(definitionName)
	writeIntegrationInstructionSkill(t, filepath.Join(destDir, "skills", testfixture.IntegrationInstructionSkillName), keywords.Skill)
	writeTextFile(t, filepath.Join(destDir, "AGENTS.md"), fmt.Sprintf("The root instructions keyword for this run is %q.\nWhen asked to report both configured keywords, output the root instructions keyword first.\n", keywords.Root))
	updated += `

[[skills]]
path = "./skills/reply-with-keyword"

[agents_md]
path = "./AGENTS.md"
`
	writeTextFile(t, configTomlPath, updated)
	return destDir
}

func writeIntegrationInstructionSkill(t *testing.T, dir, keyword string) {
	t.Helper()
	writeTextFile(t, filepath.Join(dir, "SKILL.md"), fmt.Sprintf(`---
name: %s
description: Use when asked to determine the configured skill keyword and combine it with the root instructions keyword in the final response.
---

When asked to report both configured keywords:
1. Read the project root instructions file to find the root instructions keyword.
2. The skill keyword for this run is %q.
3. Respond with the root instructions keyword, then a single space, then %q.
4. Exit immediately after responding.
`, testfixture.IntegrationInstructionSkillName, keyword, keyword))
}

func copyDir(t *testing.T, srcDir, destDir string) {
	t.Helper()
	err := filepath.WalkDir(srcDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destDir, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode())
	})
	if err != nil {
		t.Fatalf("copy %s to %s: %v", srcDir, destDir, err)
	}
}

func replaceSingleMatch(t *testing.T, body, pattern, replacement string) string {
	t.Helper()
	re := regexp.MustCompile(pattern)
	if !re.MatchString(body) {
		t.Fatalf("pattern %q not found in body:\n%s", pattern, body)
	}
	return re.ReplaceAllString(body, replacement)
}

func versionFieldForDefinition(definitionName string) string {
	switch definitionName {
	case "codex":
		return "codex_version"
	case "claude-code":
		return "claude_version"
	case "gemini-cli":
		return "gemini_version"
	case "opencode":
		return "opencode_version"
	case "pi":
		return "pi_version"
	default:
		return ""
	}
}

func writeTextFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o755); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func loadMatrixCases(t *testing.T) []agentMatrixCase {
	t.Helper()
	matrixPath := matrixFilePath()
	body, err := os.ReadFile(matrixPath)
	if err != nil {
		t.Fatalf("read integration matrix file %q: %v", matrixPath, err)
	}
	var doc struct {
		Cases []agentMatrixCase `json:"cases"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("decode integration matrix file %q: %v", matrixPath, err)
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

func expandVersionedMatrixCases(t *testing.T) []versionedMatrixCase {
	t.Helper()
	base := loadMatrixCases(t)
	out := make([]versionedMatrixCase, 0, len(base)*2)
	for _, c := range base {
		versions := versionsForAgent(c.DefinitionName)
		if c.ConfigName == testfixture.RepoOwnedUnifiedConfigName(c.DefinitionName) {
			versions = []string{"latest"}
		}
		for _, version := range versions {
			out = append(out, versionedMatrixCase{
				DefinitionName: c.DefinitionName,
				ConfigName:     c.ConfigName,
				AgentVersion:   version,
			})
		}
	}
	return out
}

func matrixFilePath() string {
	if raw := strings.TrimSpace(os.Getenv(envITMatrixFile)); raw != "" {
		return raw
	}
	return filepath.Join(repoRootFromCaller(), "tools", "integration-matrix", "matrix.json")
}

func versionsForAgent(agent string) []string {
	switch agent {
	case "codex":
		return parseVersionsEnv(envCodexVersions)
	case "claude-code":
		return parseVersionsEnv(envClaudeCodeVersions)
	case "gemini-cli":
		return parseVersionsEnv(envGeminiCLIVersions)
	case "opencode":
		return parseVersionsEnv(envOpencodeVersions)
	case "pi":
		return parseVersionsEnv(envPiVersions)
	default:
		return []string{"latest"}
	}
}

func parseVersionsEnv(envName string) []string {
	raw := strings.TrimSpace(os.Getenv(envName))
	if raw == "" {
		return []string{"latest"}
	}
	parts := strings.Split(raw, ",")
	seen := make(map[string]struct{}, len(parts))
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		value := strings.TrimSpace(part)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	if len(out) == 0 {
		return []string{"latest"}
	}
	return out
}

func requireMatrixAPIKeys(t *testing.T, cases []agentMatrixCase) {
	t.Helper()
	needsOpenAI := false
	needsAnthropic := false
	needsGemini := false
	for _, c := range cases {
		for _, envName := range testfixture.RepoOwnedRequiredEnvForConfig(c.DefinitionName, c.ConfigName) {
			switch envName {
			case envOpenAIKey:
				needsOpenAI = true
			case envAnthropicKey:
				needsAnthropic = true
			case envGeminiKey:
				needsGemini = true
			}
		}
	}
	missing := []string{}
	if needsOpenAI && strings.TrimSpace(os.Getenv(envOpenAIKey)) == "" {
		missing = append(missing, envOpenAIKey)
	}
	if needsAnthropic && strings.TrimSpace(os.Getenv(envAnthropicKey)) == "" {
		missing = append(missing, envAnthropicKey)
	}
	if needsGemini && strings.TrimSpace(os.Getenv(envGeminiKey)) == "" {
		missing = append(missing, envGeminiKey)
	}
	if len(missing) > 0 {
		t.Fatalf("integration_model requires API keys for matrix cases; missing: %v", missing)
	}
}

func modelAgentEnvArgs() []string {
	return []string{
		"--agent-env", "AGENT_SERVER_LISTEN=:8080",
		"--agent-env", "AGENT_SERVER_STOP_GRACE_TIMEOUT=4s",
		"--agent-env", "AGENT_SERVER_TRAJECTORY_COLLECT_TIMEOUT=25s",
		"--agent-env", "AGENT_SERVER_TRAJECTORY_POLL_INTERVAL=200ms",
	}
}

func caseName(definitionName, configName, version string) string {
	return fmt.Sprintf("%s_%s_%s", sanitizeToken(definitionName), sanitizeToken(configName), sanitizeToken(version))
}

func sanitizeToken(value string) string {
	replacer := strings.NewReplacer("/", "_", "\\", "_", ":", "_", ".", "_", " ", "_", "-", "_")
	cleaned := replacer.Replace(value)
	cleaned = strings.Trim(cleaned, "_")
	if cleaned == "" {
		return "default"
	}
	return strings.ToLower(cleaned)
}
