package compiler

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/marginlab/margin-eval/runner/runner-core/agentdef"
	"github.com/marginlab/margin-eval/runner/runner-core/runbundle"
)

func TestCompileAgentDefinitionAndConfigSuccess(t *testing.T) {
	t.Parallel()
	root := t.TempDir()

	suitePath := filepath.Join(root, "suites", "smoke")
	createSuiteWithCases(t, suitePath, []string{"repo-build", "lint"})

	definitionPath, configPath := createAgentDefinitionConfig(t, root, "shell-agent", `[input]
command = ["bash", "-lc", "echo hello"]
`)

	evalPath := filepath.Join(root, "configs", "evals", "smoke-local.toml")
	writeFile(t, evalPath, `kind = "eval_config"
name = "smoke-local"
max_concurrency = 2
fail_fast = false
instance_timeout_seconds = 600
`)

	bundle, err := Compile(CompileInput{
		SuitePath:       suitePath,
		AgentConfigPath: configPath,
		EvalPath:        evalPath,
		SubmitProjectID: "proj_it",
		BundleID:        "bun_test",
		CreatedAt:       time.Date(2026, 1, 1, 1, 2, 3, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	if bundle.BundleID != "bun_test" {
		t.Fatalf("BundleID = %q", bundle.BundleID)
	}
	if bundle.Source.SubmitProjectID != "proj_it" {
		t.Fatalf("SubmitProjectID = %q", bundle.Source.SubmitProjectID)
	}
	if bundle.ResolvedSnapshot.Execution.Mode != runbundle.ExecutionModeFull {
		t.Fatalf("Execution.Mode = %q, want %q", bundle.ResolvedSnapshot.Execution.Mode, runbundle.ExecutionModeFull)
	}
	if bundle.ResolvedSnapshot.Execution.RetryCount != 1 {
		t.Fatalf("Execution.RetryCount = %d, want 1", bundle.ResolvedSnapshot.Execution.RetryCount)
	}
	gotCases := []string{bundle.ResolvedSnapshot.Cases[0].CaseID, bundle.ResolvedSnapshot.Cases[1].CaseID}
	wantCases := []string{"repo-build", "lint"}
	if !reflect.DeepEqual(gotCases, wantCases) {
		t.Fatalf("case order = %#v, want %#v", gotCases, wantCases)
	}
	if !reflect.DeepEqual(bundle.ResolvedSnapshot.Cases[0].TestCommand, []string{"bash", "-c", "tests/test.sh"}) {
		t.Fatalf("unexpected test command: %#v", bundle.ResolvedSnapshot.Cases[0].TestCommand)
	}
	if bundle.ResolvedSnapshot.Agent.Definition.Manifest.Name != "shell-agent" {
		t.Fatalf("definition name = %q", bundle.ResolvedSnapshot.Agent.Definition.Manifest.Name)
	}
	if bundle.ResolvedSnapshot.Agent.Config.Name != "shell-agent-default" {
		t.Fatalf("config name = %q", bundle.ResolvedSnapshot.Agent.Config.Name)
	}
	commandRaw, ok := bundle.ResolvedSnapshot.Agent.Config.Input["command"]
	if !ok {
		t.Fatalf("config input missing command")
	}
	command, ok := commandRaw.([]any)
	if !ok {
		t.Fatalf("config input command = %#v", commandRaw)
	}
	if len(command) != 3 || command[0] != "bash" || command[2] != "echo hello" {
		t.Fatalf("config input command = %#v", command)
	}
	if _, err := os.Stat(filepath.Join(definitionPath, "definition.toml")); err != nil {
		t.Fatalf("definition.toml missing: %v", err)
	}
}

func TestCompileRejectsVersionField(t *testing.T) {
	t.Parallel()
	root := t.TempDir()

	suitePath := filepath.Join(root, "suite")
	createSuiteWithCases(t, suitePath, []string{"repo-build"})

	_, configPath := createAgentDefinitionConfig(t, root, "shell-agent", `[input]
command = ["bash", "-lc", "echo hello"]
`)

	evalPath := filepath.Join(root, "eval.toml")
	writeFile(t, evalPath, `kind = "eval_config"
name = "smoke"
version = 3
max_concurrency = 1
fail_fast = false
instance_timeout_seconds = 300
`)

	_, err := Compile(CompileInput{SuitePath: suitePath, AgentConfigPath: configPath, EvalPath: evalPath})
	if err == nil || !strings.Contains(err.Error(), "must not define \"version\"") {
		t.Fatalf("expected version validation error, got %v", err)
	}
}

func TestCompileRejectsNegativeRetryCount(t *testing.T) {
	t.Parallel()
	root := t.TempDir()

	suitePath := filepath.Join(root, "suite")
	createSuiteWithCases(t, suitePath, []string{"repo-build"})

	_, configPath := createAgentDefinitionConfig(t, root, "shell-agent", `[input]
command = ["bash", "-lc", "echo hello"]
`)

	evalPath := filepath.Join(root, "eval.toml")
	writeFile(t, evalPath, `kind = "eval_config"
name = "smoke"
max_concurrency = 1
fail_fast = false
retry_count = -1
instance_timeout_seconds = 300
`)

	_, err := Compile(CompileInput{SuitePath: suitePath, AgentConfigPath: configPath, EvalPath: evalPath})
	if err == nil || !strings.Contains(err.Error(), "retry_count must be >= 0") {
		t.Fatalf("expected retry_count validation error, got %v", err)
	}
}

func TestCompileRejectsMissingDefinitionRef(t *testing.T) {
	t.Parallel()
	root := t.TempDir()

	suitePath := filepath.Join(root, "suite")
	createSuiteWithCases(t, suitePath, []string{"repo-build"})

	configPath := filepath.Join(root, "agent")
	writeFile(t, filepath.Join(configPath, "config.toml"), `kind = "agent_config"
name = "broken-config"
mode = "direct"

[input]
command = ["bash"]
`)

	evalPath := filepath.Join(root, "eval.toml")
	writeFile(t, evalPath, `kind = "eval_config"
name = "smoke"
max_concurrency = 1
fail_fast = false
instance_timeout_seconds = 300
`)

	_, err := Compile(CompileInput{SuitePath: suitePath, AgentConfigPath: configPath, EvalPath: evalPath})
	if err == nil || !strings.Contains(err.Error(), "definition is required") {
		t.Fatalf("expected missing definition error, got %v", err)
	}
}

func TestCompileRejectsInvalidConfigInput(t *testing.T) {
	t.Parallel()
	root := t.TempDir()

	suitePath := filepath.Join(root, "suite")
	createSuiteWithCases(t, suitePath, []string{"repo-build"})

	_, configPath := createAgentDefinitionConfig(t, root, "shell-agent", `[input]
cwd = "/work"
`)

	evalPath := filepath.Join(root, "eval.toml")
	writeFile(t, evalPath, `kind = "eval_config"
name = "smoke"
max_concurrency = 1
fail_fast = false
instance_timeout_seconds = 300
`)

	_, err := Compile(CompileInput{SuitePath: suitePath, AgentConfigPath: configPath, EvalPath: evalPath})
	if err == nil || !strings.Contains(err.Error(), "config.input") {
		t.Fatalf("expected schema validation error, got %v", err)
	}
}

func TestCompileCaseWithoutImageOrDockerfileFails(t *testing.T) {
	t.Parallel()
	root := t.TempDir()

	suitePath := filepath.Join(root, "suite")
createSuiteCase(t, suitePath, "repo-build", `kind = "test_case"
name = "repo-build"
agent_cwd = "/workspace"
test_cwd = "/work"
test_timeout_seconds = 120
`)

	_, configPath := createAgentDefinitionConfig(t, root, "shell-agent", `[input]
command = ["bash", "-lc", "echo hello"]
`)

	evalPath := filepath.Join(root, "eval.toml")
	writeFile(t, evalPath, `kind = "eval_config"
name = "smoke"
max_concurrency = 1
fail_fast = false
instance_timeout_seconds = 300
`)

	_, err := Compile(CompileInput{SuitePath: suitePath, AgentConfigPath: configPath, EvalPath: evalPath})
	if err == nil || !strings.Contains(err.Error(), "must set image or include env/Dockerfile") {
		t.Fatalf("expected missing image/dockerfile error, got %v", err)
	}
}

func TestCompileUsesEnvDockerfileWhenImageOmitted(t *testing.T) {
	t.Parallel()
	root := t.TempDir()

	suitePath := filepath.Join(root, "suite")
createSuiteCase(t, suitePath, "repo-build", `kind = "test_case"
name = "repo-build"
agent_cwd = "/workspace"
test_cwd = "/work"
test_timeout_seconds = 120
`)

	_, configPath := createAgentDefinitionConfig(t, root, "shell-agent", `[input]
command = ["bash", "-lc", "echo hello"]
`)

	evalPath := filepath.Join(root, "eval.toml")
	writeFile(t, evalPath, `kind = "eval_config"
name = "smoke"
max_concurrency = 1
fail_fast = false
instance_timeout_seconds = 300
`)
	writeFile(t, filepath.Join(suitePath, "cases", "repo-build", "env", "Dockerfile"), "FROM alpine:3.20\n")
	writeFile(t, filepath.Join(suitePath, "cases", "repo-build", "env", "bootstrap.sh"), "#!/usr/bin/env bash\necho hi\n")

	bundle, err := Compile(CompileInput{SuitePath: suitePath, AgentConfigPath: configPath, EvalPath: evalPath})
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	caseSpec := bundle.ResolvedSnapshot.Cases[0]
	if caseSpec.Image != "" {
		t.Fatalf("case image = %q, want empty for lazy build", caseSpec.Image)
	}
	if caseSpec.ImageBuild == nil {
		t.Fatalf("expected image_build spec to be populated")
	}
	if caseSpec.ImageBuild.DockerfileRelPath != "Dockerfile" {
		t.Fatalf("dockerfile rel path = %q", caseSpec.ImageBuild.DockerfileRelPath)
	}
}

func TestCompilePrefersExplicitImageOverEnvBuild(t *testing.T) {
	t.Parallel()
	root := t.TempDir()

	suitePath := filepath.Join(root, "suite")
	const explicitImage = "ghcr.io/acme/repo@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
createSuiteCase(t, suitePath, "repo-build", `kind = "test_case"
name = "repo-build"
image = "`+explicitImage+`"
agent_cwd = "/workspace"
test_cwd = "/work"
test_timeout_seconds = 120
`)
	writeFile(t, filepath.Join(suitePath, "cases", "repo-build", "env", "Dockerfile"), "FROM alpine:3.20\n")

	_, configPath := createAgentDefinitionConfig(t, root, "shell-agent", `[input]
command = ["bash", "-lc", "echo hello"]
`)

	evalPath := filepath.Join(root, "eval.toml")
	writeFile(t, evalPath, `kind = "eval_config"
name = "smoke"
max_concurrency = 1
fail_fast = false
instance_timeout_seconds = 300
`)

	bundle, err := Compile(CompileInput{SuitePath: suitePath, AgentConfigPath: configPath, EvalPath: evalPath})
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	caseSpec := bundle.ResolvedSnapshot.Cases[0]
	if got := caseSpec.Image; got != explicitImage {
		t.Fatalf("case image = %q, want %q", got, explicitImage)
	}
	if caseSpec.ImageBuild != nil {
		t.Fatalf("explicit image should not emit image_build spec")
	}
}

func TestCompilePackagesConfiguredSkills(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	suitePath := filepath.Join(root, "suite")
	createSuiteWithCases(t, suitePath, []string{"repo-build"})

	_, configPath := createAgentDefinitionConfig(t, root, "shell-agent", `[[skills]]
path = "./skills/db-migration"

[input]
command = ["bash", "-lc", "echo hello"]
`)
	writeFile(t, filepath.Join(root, "configs", "agents", "shell-agent-default", "skills", "db-migration", "SKILL.md"), `---
name: db-migration
description: Use when creating or reviewing database migrations.
---

Follow the migration checklist.
`)
	writeFile(t, filepath.Join(root, "configs", "agents", "shell-agent-default", "skills", "db-migration", "tools", "check.sh"), "#!/usr/bin/env bash\necho ok\n")

	definitionTomlPath := filepath.Join(root, "definitions", "shell-agent", "definition.toml")
	writeFile(t, definitionTomlPath, `kind = "agent_definition"
name = "shell-agent"

[config]
schema = "schema.json"

[skills]
home_rel_dir = ".agents/skills"

[run]
prepare = "hooks/run-prepare.sh"
`)

	evalPath := filepath.Join(root, "eval.toml")
	writeFile(t, evalPath, `kind = "eval_config"
name = "smoke"
max_concurrency = 1
fail_fast = false
instance_timeout_seconds = 300
`)

	bundle, err := Compile(CompileInput{SuitePath: suitePath, AgentConfigPath: configPath, EvalPath: evalPath})
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	if len(bundle.ResolvedSnapshot.Agent.Config.Skills) != 1 {
		t.Fatalf("skills = %#v", bundle.ResolvedSnapshot.Agent.Config.Skills)
	}
	skill := bundle.ResolvedSnapshot.Agent.Config.Skills[0]
	if skill.Name != "db-migration" {
		t.Fatalf("skill name = %q", skill.Name)
	}
	if skill.Description != "Use when creating or reviewing database migrations." {
		t.Fatalf("skill description = %q", skill.Description)
	}
	body, err := agentdef.ReadPackageFile(skill.Package, "tools/check.sh")
	if err != nil {
		t.Fatalf("ReadPackageFile() error = %v", err)
	}
	if !strings.Contains(string(body), "echo ok") {
		t.Fatalf("packaged nested file missing, got %q", string(body))
	}
}

func TestCompileLoadsConfiguredAgentsMD(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	suitePath := filepath.Join(root, "suite")
	createSuiteWithCases(t, suitePath, []string{"repo-build"})

	_, configPath := createAgentDefinitionConfig(t, root, "shell-agent", `[agents_md]
path = "./AGENTS.md"

[input]
command = ["bash", "-lc", "echo hello"]
`)
	writeFile(t, filepath.Join(root, "configs", "agents", "shell-agent-default", "AGENTS.md"), "Project-wide instructions.\n\nBe concise.\n")

	definitionTomlPath := filepath.Join(root, "definitions", "shell-agent", "definition.toml")
	writeFile(t, definitionTomlPath, `kind = "agent_definition"
name = "shell-agent"

[config]
schema = "schema.json"

[agents_md]
filename = "AGENTS.md"

[run]
prepare = "hooks/run-prepare.sh"
`)

	evalPath := filepath.Join(root, "eval.toml")
	writeFile(t, evalPath, `kind = "eval_config"
name = "smoke"
max_concurrency = 1
fail_fast = false
instance_timeout_seconds = 300
`)

	bundle, err := Compile(CompileInput{SuitePath: suitePath, AgentConfigPath: configPath, EvalPath: evalPath})
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	if bundle.ResolvedSnapshot.Agent.Config.AgentsMD == nil {
		t.Fatalf("agents_md missing")
	}
	if got := bundle.ResolvedSnapshot.Agent.Config.AgentsMD.Content; got != "Project-wide instructions.\n\nBe concise.\n" {
		t.Fatalf("agents_md.content = %q", got)
	}
}

func createAgentDefinitionConfig(t *testing.T, root, definitionName, inputTOML string) (string, string) {
	t.Helper()
	definitionPath := filepath.Join(root, "definitions", definitionName)
	writeFile(t, filepath.Join(definitionPath, "definition.toml"), `kind = "agent_definition"
name = "`+definitionName+`"

[config]
schema = "schema.json"

[run]
prepare = "hooks/run-prepare.sh"
`)
	writeFile(t, filepath.Join(definitionPath, "schema.json"), `{
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
    }
  }
}
`)
	writeFile(t, filepath.Join(definitionPath, "hooks", "run-prepare.sh"), "#!/usr/bin/env bash\nset -euo pipefail\nprintf '{}\\n'\n")

	configPath := filepath.Join(root, "configs", "agents", definitionName+"-default")
	writeFile(t, filepath.Join(configPath, "config.toml"), `kind = "agent_config"
name = "`+definitionName+`-default"
definition = "../../../definitions/`+definitionName+`"
mode = "direct"

`+inputTOML)
	return definitionPath, configPath
}

func createSuiteWithCases(t *testing.T, suitePath string, cases []string) {
	t.Helper()
	caseList := ""
	for i, c := range cases {
		if i > 0 {
			caseList += ",\n  "
		}
		caseList += "\"" + c + "\""
	}
	writeFile(t, filepath.Join(suitePath, "suite.toml"), "kind = \"test_suite\"\nname = \"smoke\"\ncases = [\n  "+caseList+"\n]\n")

	for _, name := range cases {
		writeFile(t, filepath.Join(suitePath, "cases", name, "case.toml"), "kind = \"test_case\"\nname = \""+name+"\"\nimage = \"ghcr.io/acme/repo@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef\"\nagent_cwd = \"/workspace\"\ntest_cwd = \"/work\"\ntest_timeout_seconds = 120\n")
		writeFile(t, filepath.Join(suitePath, "cases", name, "prompt.md"), "run case "+name+"\n")
		writeFile(t, filepath.Join(suitePath, "cases", name, "tests", "test.sh"), "#!/usr/bin/env bash\ntrue\n")
	}
}

func createSuiteCase(t *testing.T, suitePath, caseName, caseToml string) {
	t.Helper()
	writeFile(t, filepath.Join(suitePath, "suite.toml"), "kind = \"test_suite\"\nname = \"smoke\"\ncases = [\n  \""+caseName+"\"\n]\n")
	writeFile(t, filepath.Join(suitePath, "cases", caseName, "case.toml"), caseToml)
	writeFile(t, filepath.Join(suitePath, "cases", caseName, "prompt.md"), "run case "+caseName+"\n")
	writeFile(t, filepath.Join(suitePath, "cases", caseName, "tests", "test.sh"), "#!/usr/bin/env bash\ntrue\n")
}

func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", path, err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
}
