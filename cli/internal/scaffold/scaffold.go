package scaffold

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	toml "github.com/pelletier/go-toml/v2"
)

type suiteConfigFile struct {
	Kind        string   `toml:"kind"`
	Name        string   `toml:"name"`
	Description string   `toml:"description"`
	Cases       []string `toml:"cases"`
}

func InitSuite(suitePath, name string) error {
	suiteDir, err := absPathRequired(suitePath, "suite")
	if err != nil {
		return err
	}
	suiteName := strings.TrimSpace(name)
	if suiteName == "" {
		suiteName = filepath.Base(suiteDir)
	}

	if err := os.MkdirAll(filepath.Join(suiteDir, "cases"), 0o755); err != nil {
		return fmt.Errorf("create suite directories: %w", err)
	}

	suiteToml := fmt.Sprintf(`kind = "test_suite"
name = %q
description = "Fast pre-merge suite"

cases = []
`, suiteName)

	if err := writeFileNew(filepath.Join(suiteDir, "suite.toml"), []byte(suiteToml), 0o644); err != nil {
		return err
	}
	return nil
}

func InitCase(suitePath, caseName string) (string, error) {
	suiteDir, err := absPathRequired(suitePath, "suite")
	if err != nil {
		return "", err
	}
	suiteTomlPath := filepath.Join(suiteDir, "suite.toml")
	suiteCfg, err := readSuiteConfig(suiteTomlPath)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(suiteCfg.Kind) != "test_suite" {
		return "", fmt.Errorf("%s kind must be %q", suiteTomlPath, "test_suite")
	}

	name := strings.TrimSpace(caseName)
	if name == "" {
		name, err = nextGeneratedCaseName(suiteCfg.Cases, filepath.Join(suiteDir, "cases"))
		if err != nil {
			return "", err
		}
	}
	for _, listedCase := range suiteCfg.Cases {
		if listedCase == name {
			return "", fmt.Errorf("suite already contains case %q", name)
		}
	}

	caseDir := filepath.Join(suiteDir, "cases", name)
	if err := os.MkdirAll(filepath.Join(caseDir, "tests"), 0o755); err != nil {
		return "", fmt.Errorf("create case directories: %w", err)
	}

	caseToml := fmt.Sprintf(`kind = "test_case"
name = %q
description = "Describe what this case validates"

image = "ghcr.io/acme/repo@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
agent_cwd = "/work"
test_cwd = "/work"
test_timeout_seconds = 900
`, name)

	prompt := "Describe the task and expected behavior for this case.\n"
	testScript := "#!/usr/bin/env bash\nset -euo pipefail\n\n# TODO: implement assertions for this case\ntrue\n"

	if err := writeFileNew(filepath.Join(caseDir, "case.toml"), []byte(caseToml), 0o644); err != nil {
		return "", err
	}
	if err := writeFileNew(filepath.Join(caseDir, "prompt.md"), []byte(prompt), 0o644); err != nil {
		return "", err
	}
	if err := writeFileNew(filepath.Join(caseDir, "tests", "test.sh"), []byte(testScript), 0o755); err != nil {
		return "", err
	}

	suiteCfg.Cases = append(suiteCfg.Cases, name)
	if err := writeSuiteConfig(suiteTomlPath, suiteCfg); err != nil {
		return "", err
	}
	return name, nil
}

func InitAgentDefinition(definitionPath, name string) error {
	definitionDir, err := absPathRequired(definitionPath, "agent definition")
	if err != nil {
		return err
	}
	definitionName := strings.TrimSpace(name)
	if definitionName == "" {
		definitionName = filepath.Base(definitionDir)
	}
	if err := os.MkdirAll(filepath.Join(definitionDir, "hooks"), 0o755); err != nil {
		return fmt.Errorf("create agent definition directories: %w", err)
	}
	if err := writeFileNew(filepath.Join(definitionDir, "definition.toml"), []byte(renderDefinitionToml(definitionName)), 0o644); err != nil {
		return err
	}
	if err := writeFileNew(filepath.Join(definitionDir, "schema.json"), []byte(renderDefinitionSchema()), 0o644); err != nil {
		return err
	}
	for relPath, body := range renderDefinitionHooks() {
		if err := writeFileNew(filepath.Join(definitionDir, filepath.FromSlash(relPath)), []byte(body), 0o755); err != nil {
			return err
		}
	}
	return nil
}

func InitAgentConfig(agentConfigPath, name, definitionRef string) error {
	configDir, err := absPathRequired(agentConfigPath, "agent config")
	if err != nil {
		return err
	}
	configName := strings.TrimSpace(name)
	if configName == "" {
		configName = filepath.Base(configDir)
	}
	resolvedDefinitionRef := strings.TrimSpace(definitionRef)
	if resolvedDefinitionRef == "" {
		return fmt.Errorf("agent config definition reference is required")
	}
	if filepath.IsAbs(resolvedDefinitionRef) {
		relPath, err := filepath.Rel(configDir, resolvedDefinitionRef)
		if err != nil {
			return fmt.Errorf("compute relative definition path: %w", err)
		}
		resolvedDefinitionRef = relPath
	}
	resolvedDefinitionRef = filepath.ToSlash(resolvedDefinitionRef)
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		return fmt.Errorf("create agent config directory: %w", err)
	}
	if err := writeFileNew(filepath.Join(configDir, "config.toml"), []byte(renderAgentConfigToml(configName, resolvedDefinitionRef)), 0o644); err != nil {
		return err
	}
	return nil
}

func InitEvalConfig(evalPath, name string) error {
	evalFilePath, err := absPathRequired(evalPath, "eval")
	if err != nil {
		return err
	}
	evalName := strings.TrimSpace(name)
	if evalName == "" {
		evalName = strings.TrimSuffix(filepath.Base(evalFilePath), filepath.Ext(evalFilePath))
	}

	content := fmt.Sprintf(`kind = "eval_config"
name = %q
description = "Eval runtime defaults"

max_concurrency = 1
fail_fast = false
retry_count = 1
instance_timeout_seconds = 1800
`, evalName)

	if err := writeFileNew(evalFilePath, []byte(content), 0o644); err != nil {
		return err
	}
	return nil
}

func renderDefinitionToml(name string) string {
	return fmt.Sprintf(`kind = "agent_definition"
name = %q
description = "Custom agent definition"

[auth]
required_env = []

[config]
schema = "schema.json"

[install]
check = "hooks/install-check.sh"
run = "hooks/install-run.sh"

[toolchains.node]
minimum = "20"
preferred = "24"

[run]
prepare = "hooks/run-prepare.js"
`, name)
}

func renderDefinitionSchema() string {
	return `{
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
    },
    "cwd": {
      "type": "string",
      "minLength": 1
    },
    "env": {
      "type": "object",
      "additionalProperties": {
        "type": "string"
      }
    }
  }
}
`
}

func renderDefinitionHooks() map[string]string {
	return map[string]string{
		"hooks/install-check.sh": `#!/usr/bin/env bash
set -euo pipefail

printf '{"installed":false}\n'
`,
		"hooks/install-run.sh": `#!/usr/bin/env bash
set -euo pipefail

printf '{"installed":true}\n'
`,
		"hooks/run-prepare.js": `#!/usr/bin/env node
"use strict";

const fs = require("fs");

function fail(message) {
  throw new Error(message);
}

const contextPath = process.env.AGENT_CONTEXT_JSON;
if (!contextPath) {
  fail("AGENT_CONTEXT_JSON is required");
}

const ctx = JSON.parse(fs.readFileSync(contextPath, "utf8"));
const config = ctx.config || {};
const inputCfg = config.input || {};
const command = inputCfg.command;
if (!Array.isArray(command) || command.length === 0) {
  fail("config.input.command must be a non-empty array");
}

const env = { ...((ctx.run || {}).env || {}) };
const customEnv = inputCfg.env || {};
if (customEnv === null || typeof customEnv !== "object" || Array.isArray(customEnv)) {
  fail("config.input.env must be an object");
}
for (const [key, value] of Object.entries(customEnv)) {
  env[String(key)] = String(value);
}

const cwd = inputCfg.cwd || ((ctx.run || {}).cwd);
if (typeof cwd !== "string" || cwd.length === 0) {
  fail("run cwd is required");
}

process.stdout.write(JSON.stringify({
  path: command[0],
  args: command.slice(1),
  env,
  dir: cwd,
}) + "\n");
`,
	}
}

func renderAgentConfigToml(name, definitionRef string) string {
	return fmt.Sprintf(`kind = "agent_config"
name = %q
description = "Default config profile"
definition = %q
mode = "direct"

# Optional shared skills:
# [[skills]]
# path = "./skills/my-skill"

# Optional shared project instructions:
# [agents_md]
# path = "./AGENTS.md"

[input]
command = ["bash", "-lc", "echo custom agent ready"]
`, name, definitionRef)
}

func absPathRequired(path string, label string) (string, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return "", fmt.Errorf("%s path is required", label)
	}
	abs, err := filepath.Abs(trimmed)
	if err != nil {
		return "", fmt.Errorf("resolve %s path: %w", label, err)
	}
	return abs, nil
}

func writeFileNew(path string, body []byte, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create directory for %s: %w", path, err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		if os.IsExist(err) {
			return fmt.Errorf("refusing to overwrite existing file %s", path)
		}
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	if _, err := f.Write(body); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func readSuiteConfig(path string) (suiteConfigFile, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return suiteConfigFile{}, fmt.Errorf("read %s: %w", path, err)
	}
	var cfg suiteConfigFile
	if err := toml.Unmarshal(body, &cfg); err != nil {
		return suiteConfigFile{}, fmt.Errorf("decode TOML %s: %w", path, err)
	}
	trimmedCases := make([]string, 0, len(cfg.Cases))
	for _, caseName := range cfg.Cases {
		trimmed := strings.TrimSpace(caseName)
		if trimmed == "" {
			return suiteConfigFile{}, fmt.Errorf("%s cases must not contain empty values", path)
		}
		trimmedCases = append(trimmedCases, trimmed)
	}
	cfg.Cases = trimmedCases
	return cfg, nil
}

func writeSuiteConfig(path string, cfg suiteConfigFile) error {
	body, err := toml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("encode TOML %s: %w", path, err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func nextGeneratedCaseName(suiteCases []string, casesRoot string) (string, error) {
	used := make(map[string]struct{}, len(suiteCases))
	for _, caseName := range suiteCases {
		used[caseName] = struct{}{}
	}

	entries, err := os.ReadDir(casesRoot)
	if err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("read cases directory %s: %w", casesRoot, err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			used[entry.Name()] = struct{}{}
		}
	}

	maxN := 0
	for usedName := range used {
		if !strings.HasPrefix(usedName, "case_") {
			continue
		}
		n, err := strconv.Atoi(strings.TrimPrefix(usedName, "case_"))
		if err != nil || n <= 0 {
			continue
		}
		if n > maxN {
			maxN = n
		}
	}
	for i := 1; i <= maxN+1; i++ {
		candidate := fmt.Sprintf("case_%d", i)
		if _, exists := used[candidate]; !exists {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("failed to generate case name")
}
