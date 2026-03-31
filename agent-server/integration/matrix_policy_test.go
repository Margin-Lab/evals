//go:build integration

package integration

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/marginlab/margin-eval/runner/runner-core/testfixture"
)

const (
	envITMatrixFile = "MARGINLAB_IT_MATRIX_FILE"
	envITDotenvFile = "MARGINLAB_IT_DOTENV_FILE"

	envCodexVersions      = "MARGINLAB_IT_CODEX_VERSIONS"
	envClaudeCodeVersions = "MARGINLAB_IT_CLAUDE_CODE_VERSIONS"
	envGeminiCLIVersions  = "MARGINLAB_IT_GEMINI_CLI_VERSIONS"
	envOpencodeVersions   = "MARGINLAB_IT_OPENCODE_VERSIONS"
	envPiVersions         = "MARGINLAB_IT_PI_VERSIONS"
)

type matrixCase struct {
	DefinitionName string `json:"definition_name"`
	ConfigName     string `json:"config_name"`
}

type matrixDoc struct {
	Cases []matrixCase `json:"cases"`
}

type versionedMatrixCase struct {
	DefinitionName string
	ConfigName     string
	AgentVersion   string
}

func loadMatrixCases(t *testing.T) []matrixCase {
	t.Helper()

	path := matrixFilePath()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read integration matrix file %q: %v", path, err)
	}

	var doc matrixDoc
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("decode integration matrix file %q: %v", path, err)
	}
	if len(doc.Cases) == 0 {
		t.Fatalf("integration matrix has no cases: %s", path)
	}

	out := make([]matrixCase, 0, len(doc.Cases))
	for _, c := range doc.Cases {
		c.DefinitionName = strings.TrimSpace(c.DefinitionName)
		c.ConfigName = strings.TrimSpace(c.ConfigName)
		if c.DefinitionName == "" {
			t.Fatalf("matrix definition_name is required in %s", path)
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

func matrixAgents(t *testing.T) []string {
	t.Helper()

	cases := loadMatrixCases(t)
	seen := map[string]struct{}{}
	out := make([]string, 0, len(cases))
	for _, c := range cases {
		if _, ok := seen[c.DefinitionName]; ok {
			continue
		}
		seen[c.DefinitionName] = struct{}{}
		out = append(out, c.DefinitionName)
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
		for _, v := range versions {
			out = append(out, versionedMatrixCase{
				DefinitionName: c.DefinitionName,
				ConfigName:     c.ConfigName,
				AgentVersion:   v,
			})
		}
	}
	return out
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

func matrixFilePath() string {
	override := strings.TrimSpace(os.Getenv(envITMatrixFile))
	if override != "" {
		return override
	}
	return filepath.Join(repositoryRootFromCaller(), "tools", "integration-matrix", "matrix.json")
}

func repositoryRootFromCaller() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "."
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func caseName(definitionName, configName, version string) string {
	return fmt.Sprintf("%s_%s_%s", sanitizeToken(definitionName), sanitizeToken(configName), sanitizeToken(version))
}
