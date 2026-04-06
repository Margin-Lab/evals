package agentruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/marginlab/margin-eval/agent-server/internal/config"
	"github.com/marginlab/margin-eval/agent-server/internal/logutil"
	"github.com/marginlab/margin-eval/agent-server/internal/noderuntime"
	"github.com/marginlab/margin-eval/agent-server/internal/state"
	"github.com/marginlab/margin-eval/runner/runner-core/agentdef"
	"github.com/marginlab/margin-eval/runner/runner-core/runbundle"
	"github.com/marginlab/margin-eval/runner/runner-core/testassets"
	"github.com/marginlab/margin-eval/runner/runner-core/testfixture"
	"github.com/marginlab/margin-eval/runner/runner-core/trajectory"
)

type fakeManagedNodeRuntime struct {
	ensureCalls int
	ensureSpecs []noderuntime.Spec
	info        noderuntime.Info
	err         error
}

func (f *fakeManagedNodeRuntime) Ensure(_ context.Context, spec noderuntime.Spec) (noderuntime.Info, error) {
	f.ensureCalls++
	f.ensureSpecs = append(f.ensureSpecs, spec)
	if f.err != nil {
		return noderuntime.Info{}, f.err
	}
	return f.info, nil
}

func runtimeTestNodeToolchainSpec() agentdef.ToolchainSpec {
	return agentdef.ToolchainSpec{
		Node: &agentdef.NodeToolchainSpec{Minimum: "20", Preferred: "24"},
	}
}

func runtimeTestManagedNodeInfo(binDir, nodePath, npmPath, npxPath string) noderuntime.Info {
	return noderuntime.Info{
		BinDir:        binDir,
		Environment:   map[string]string{"NODE_EXTRA_CA_CERTS": "/managed/ca.pem", "NPM_CONFIG_CAFILE": "/managed/ca.pem"},
		NodePath:      nodePath,
		NPMPath:       npmPath,
		NPXPath:       npxPath,
		Version:       "24",
		InstallMethod: "archive",
	}
}

func TestMaterializeDefinitionCreatesMissingParentDirectories(t *testing.T) {
	sourceDir := t.TempDir()
	hooksDir := filepath.Join(sourceDir, "hooks")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatalf("mkdir hooks dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(hooksDir, "run.sh"), []byte("#!/usr/bin/env bash\nprintf '{}\\n'\n"), 0o755); err != nil {
		t.Fatalf("write hook: %v", err)
	}
	pkg, err := testassets.PackDir(sourceDir)
	if err != nil {
		t.Fatalf("pack dir: %v", err)
	}

	definitionDir := filepath.Join(t.TempDir(), "state", "agent-definitions", pkg.ArchiveTGZSHA256, "src")
	if err := materializeDefinition(agentdef.DefinitionSnapshot{
		Manifest: agentdef.Manifest{
			Kind: "agent_definition",
			Name: "fixture",
			Run:  agentdef.RunSpec{PrepareHook: agentdef.HookRef{Path: "hooks/run.sh"}},
		},
		Package: pkg,
	}, definitionDir); err != nil {
		t.Fatalf("materializeDefinition() error = %v", err)
	}

	if _, err := os.Stat(filepath.Join(definitionDir, "hooks", "run.sh")); err != nil {
		t.Fatalf("materialized hook missing: %v", err)
	}
}

func TestInstallHookUsesManagedNodeToolchainPATH(t *testing.T) {
	stateDir := t.TempDir()
	definitionDir := t.TempDir()
	binDir := filepath.Join(t.TempDir(), "managed-bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir managed bin dir: %v", err)
	}
	fakeNPM := filepath.Join(binDir, "npm")
	if err := os.WriteFile(fakeNPM, []byte("#!/usr/bin/env bash\nprintf '9.9.9\\n'\n"), 0o755); err != nil {
		t.Fatalf("write fake npm: %v", err)
	}
	installHook := filepath.Join(definitionDir, "install.js")
	if err := os.WriteFile(installHook, []byte(`#!/usr/bin/env node
const { spawnSync } = require("node:child_process");

const result = spawnSync("npm", ["--version"], {
  encoding: "utf8",
});
if (result.status !== 0) {
  process.exit(result.status || 1);
}
if (result.stdout) {
  process.stderr.write(result.stdout);
}
if (result.stderr) {
  process.stderr.write(result.stderr);
}
process.stdout.write(JSON.stringify({
  installed: true,
  node_extra_ca_certs: process.env.NODE_EXTRA_CA_CERTS,
  npm_config_cafile: process.env.NPM_CONFIG_CAFILE,
}) + "\n");
`), 0o755); err != nil {
		t.Fatalf("write install hook: %v", err)
	}

	fakeNode := &fakeManagedNodeRuntime{
		info: runtimeTestManagedNodeInfo(binDir, filepath.Join(binDir, "node"), fakeNPM, filepath.Join(binDir, "npx")),
	}
	runtime := &Runtime{
		cfg: config.Config{
			StateDir: stateDir,
		},
		nodeRuntime: fakeNode,
	}
	agent := state.AgentRecord{
		Definition: &state.DefinitionRecord{
			Snapshot: agentdef.DefinitionSnapshot{
				Manifest: agentdef.Manifest{
					Kind:       "agent_definition",
					Name:       "codex",
					Toolchains: runtimeTestNodeToolchainSpec(),
					Run:        agentdef.RunSpec{PrepareHook: agentdef.HookRef{Path: "install.js"}},
					Install: agentdef.InstallSpec{
						RunHook: &agentdef.HookRef{Path: "install.js"},
					},
				},
			},
			DefinitionDir: definitionDir,
			InstallDir:    filepath.Join(t.TempDir(), "install"),
		},
	}

	info, err := runtime.Install(context.Background(), agent)
	if err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	if fakeNode.ensureCalls != 1 {
		t.Fatalf("Ensure() calls = %d, want 1", fakeNode.ensureCalls)
	}
	if installed, _ := info.Result["installed"].(bool); !installed {
		t.Fatalf("install result = %#v", info.Result)
	}
	if got, _ := info.Result["node_extra_ca_certs"].(string); got != "/managed/ca.pem" {
		t.Fatalf("node_extra_ca_certs = %q", got)
	}
	if got, _ := info.Result["npm_config_cafile"].(string); got != "/managed/ca.pem" {
		t.Fatalf("npm_config_cafile = %q", got)
	}
}

func TestPrepareRunInjectsManagedNodePATHIntoHookRunEnv(t *testing.T) {
	stateDir := t.TempDir()
	definitionDir := t.TempDir()
	runHook := filepath.Join(definitionDir, "run.js")
	if err := os.WriteFile(runHook, []byte(`#!/usr/bin/env node
const fs = require("node:fs");

const ctx = JSON.parse(fs.readFileSync(process.env.AGENT_CONTEXT_JSON, "utf8"));
process.stdout.write(JSON.stringify({
  path: "/bin/echo",
  args: ["ok"],
  env: ctx.run.env,
  dir: ctx.run.cwd,
}) + "\n");
`), 0o755); err != nil {
		t.Fatalf("write run hook: %v", err)
	}

	fakeNode := &fakeManagedNodeRuntime{
		info: runtimeTestManagedNodeInfo(filepath.Join(t.TempDir(), "managed-bin"), "/managed/bin/node", "/managed/bin/npm", "/managed/bin/npx"),
	}
	runtime := &Runtime{
		cfg: config.Config{
			StateDir: stateDir,
		},
		nodeRuntime: fakeNode,
	}
	agent := state.AgentRecord{
		Definition: &state.DefinitionRecord{
			Snapshot: agentdef.DefinitionSnapshot{
				Manifest: agentdef.Manifest{
					Kind:       "agent_definition",
					Name:       "codex",
					Toolchains: runtimeTestNodeToolchainSpec(),
					Run: agentdef.RunSpec{
						PrepareHook: agentdef.HookRef{Path: "run.js"},
					},
				},
			},
			DefinitionDir: definitionDir,
			InstallDir:    filepath.Join(t.TempDir(), "install"),
		},
	}
	execSpec, err := runtime.PrepareRun(context.Background(), agent, RunContext{
		RunID:         "run_1",
		SessionID:     "session_1",
		CWD:           "/workspace",
		RunHome:       "/home/run",
		ArtifactsDir:  "/artifacts",
		Env:           map[string]string{"PATH": "/usr/bin"},
		InitialPrompt: "hello",
	})
	if err != nil {
		t.Fatalf("PrepareRun() error = %v", err)
	}
	if fakeNode.ensureCalls != 1 {
		t.Fatalf("Ensure() calls = %d, want 1", fakeNode.ensureCalls)
	}
	gotEnv := map[string]string{}
	for _, pair := range execSpec.Env {
		key, value, ok := strings.Cut(pair, "=")
		if ok {
			gotEnv[key] = value
		}
	}
	if gotEnv["PATH"] != fakeNode.info.BinDir+string(os.PathListSeparator)+"/usr/bin" {
		t.Fatalf("PATH = %q", gotEnv["PATH"])
	}
	if gotEnv["NODE_EXTRA_CA_CERTS"] != "/managed/ca.pem" {
		t.Fatalf("NODE_EXTRA_CA_CERTS = %q", gotEnv["NODE_EXTRA_CA_CERTS"])
	}
	if gotEnv["NPM_CONFIG_CAFILE"] != "/managed/ca.pem" {
		t.Fatalf("NPM_CONFIG_CAFILE = %q", gotEnv["NPM_CONFIG_CAFILE"])
	}
}

func TestRepoOwnedCodexPrepareRunUsesExecWithoutBrokenProfile(t *testing.T) {
	stateDir := t.TempDir()
	definitionDir := filepath.Join(stateDir, "definition")
	installDir := filepath.Join(stateDir, "install")
	runHome := filepath.Join(stateDir, "run-home")
	if err := os.MkdirAll(installDir, 0o755); err != nil {
		t.Fatalf("mkdir install dir: %v", err)
	}

	codex := testfixture.RepoOwnedAgent("codex")
	if err := materializeDefinition(codex.Definition, definitionDir); err != nil {
		t.Fatalf("materializeDefinition() error = %v", err)
	}

	fakeNode := &fakeManagedNodeRuntime{
		info: runtimeTestManagedNodeInfo(filepath.Join(t.TempDir(), "managed-bin"), "/managed/bin/node", "/managed/bin/npm", "/managed/bin/npx"),
	}
	runtime := &Runtime{
		cfg: config.Config{
			StateDir: stateDir,
		},
		nodeRuntime: fakeNode,
	}
	definitionRecord := state.DefinitionRecord{
		Snapshot:      codex.Definition,
		PackageHash:   codex.Definition.Package.ArchiveTGZSHA256,
		DefinitionDir: definitionDir,
		InstallDir:    installDir,
	}
	configSnapshot, err := runtime.ValidateConfig(definitionRecord, codex.Config)
	if err != nil {
		t.Fatalf("ValidateConfig() error = %v", err)
	}
	agent := state.AgentRecord{
		Definition: &definitionRecord,
		Config:     &state.ConfigRecord{Snapshot: configSnapshot},
		Install: &state.InstallInfo{
			InstalledAt: time.Now().UTC(),
			Result: map[string]any{
				"bin_path": filepath.Join(installDir, "bin", "codex"),
			},
		},
	}

	execSpec, err := runtime.PrepareRun(context.Background(), agent, RunContext{
		RunID:         "run_1",
		SessionID:     "session_1",
		CWD:           "/workspace",
		RunHome:       runHome,
		ArtifactsDir:  "/artifacts",
		Env:           map[string]string{"PATH": "/usr/bin"},
		InitialPrompt: "fix the bug",
	})
	if err != nil {
		t.Fatalf("PrepareRun() error = %v", err)
	}
	if len(execSpec.Args) < 3 {
		t.Fatalf("args = %#v", execSpec.Args)
	}
	if execSpec.Args[0] != "exec" {
		t.Fatalf("args[0] = %q, want %q", execSpec.Args[0], "exec")
	}
	if execSpec.Args[1] != "--dangerously-bypass-approvals-and-sandbox" {
		t.Fatalf("args[1] = %q, want %q", execSpec.Args[1], "--dangerously-bypass-approvals-and-sandbox")
	}
	if slices.Contains(execSpec.Args, "--no-alt-screen") {
		t.Fatalf("args unexpectedly contain main-run alt-screen flag: %#v", execSpec.Args)
	}
	if slices.Contains(execSpec.Args, "ci") {
		t.Fatalf("args unexpectedly contain broken profile reference: %#v", execSpec.Args)
	}
	if execSpec.Args[len(execSpec.Args)-1] != "fix the bug" {
		t.Fatalf("prompt arg = %q", execSpec.Args[len(execSpec.Args)-1])
	}
}

func TestRepoOwnedUnifiedConfigsTranslateToDirectSnapshots(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		agentName  string
		assertions func(t *testing.T, snapshot agentdef.ConfigSnapshot)
	}{
		{
			name:      "codex",
			agentName: "codex",
			assertions: func(t *testing.T, snapshot agentdef.ConfigSnapshot) {
				t.Helper()
				if snapshot.Unified == nil || snapshot.Unified.Model != "gpt-5" {
					t.Fatalf("unexpected unified payload: %#v", snapshot.Unified)
				}
				if got := snapshot.Input["codex_version"]; got != "latest" {
					t.Fatalf("codex_version = %#v", got)
				}
				if got := snapshot.Input["config_toml"]; !strings.Contains(fmt.Sprint(got), `model = "gpt-5"`) {
					t.Fatalf("config_toml = %v", got)
				}
			},
		},
		{
			name:      "claude-code",
			agentName: "claude-code",
			assertions: func(t *testing.T, snapshot agentdef.ConfigSnapshot) {
				t.Helper()
				startupArgs, ok := snapshot.Input["startup_args"].([]any)
				if !ok {
					t.Fatalf("startup_args = %#v", snapshot.Input["startup_args"])
				}
				if !slices.Equal(startupArgs, []any{"--model", "sonnet", "--effort", "medium"}) {
					t.Fatalf("startup_args = %#v", startupArgs)
				}
				if got := snapshot.Input["settings_json"]; !strings.Contains(fmt.Sprint(got), `"permissionMode": "acceptEdits"`) {
					t.Fatalf("settings_json = %v", got)
				}
			},
		},
		{
			name:      "opencode",
			agentName: "opencode",
			assertions: func(t *testing.T, snapshot agentdef.ConfigSnapshot) {
				t.Helper()
				if got := snapshot.Input["opencode_version"]; got != "latest" {
					t.Fatalf("opencode_version = %#v", got)
				}
				configJSONC := fmt.Sprint(snapshot.Input["config_jsonc"])
				if !strings.Contains(configJSONC, `"model": "openai/gpt-5"`) {
					t.Fatalf("config_jsonc = %v", configJSONC)
				}
				if !strings.Contains(configJSONC, `"reasoningEffort": "medium"`) {
					t.Fatalf("config_jsonc = %v", configJSONC)
				}
				if strings.Contains(configJSONC, `"variant": "medium"`) {
					t.Fatalf("config_jsonc unexpectedly contains variant mapping: %v", configJSONC)
				}
			},
		},
		{
			name:      "gemini-cli",
			agentName: "gemini-cli",
			assertions: func(t *testing.T, snapshot agentdef.ConfigSnapshot) {
				t.Helper()
				if snapshot.Unified == nil || snapshot.Unified.Model != "gemini-3-flash-preview" {
					t.Fatalf("unexpected unified payload: %#v", snapshot.Unified)
				}
				if got := snapshot.Input["gemini_version"]; got != "latest" {
					t.Fatalf("gemini_version = %#v", got)
				}
				if got := snapshot.Input["approval_mode"]; got != "yolo" {
					t.Fatalf("approval_mode = %#v", got)
				}
				if got := snapshot.Input["model"]; got != "gemini-3-flash-preview" {
					t.Fatalf("model = %#v", got)
				}
			},
		},
		{
			name:      "pi",
			agentName: "pi",
			assertions: func(t *testing.T, snapshot agentdef.ConfigSnapshot) {
				t.Helper()
				if snapshot.Unified == nil || snapshot.Unified.Model != "openai/gpt-5" {
					t.Fatalf("unexpected unified payload: %#v", snapshot.Unified)
				}
				if got := snapshot.Input["pi_version"]; got != "latest" {
					t.Fatalf("pi_version = %#v", got)
				}
				if got := snapshot.Input["provider"]; got != "openai" {
					t.Fatalf("provider = %#v", got)
				}
				if got := snapshot.Input["model"]; got != "gpt-5" {
					t.Fatalf("model = %#v", got)
				}
				if got := snapshot.Input["thinking"]; got != "medium" {
					t.Fatalf("thinking = %#v", got)
				}
			},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			stateDir := t.TempDir()
			definitionDir := filepath.Join(stateDir, "definition")
			installDir := filepath.Join(stateDir, "install")
			if err := os.MkdirAll(installDir, 0o755); err != nil {
				t.Fatalf("mkdir install dir: %v", err)
			}

			agent := testfixture.RepoOwnedUnifiedAgent(tc.agentName)
			if err := materializeDefinition(agent.Definition, definitionDir); err != nil {
				t.Fatalf("materializeDefinition() error = %v", err)
			}

			runtime := &Runtime{
				cfg: config.Config{StateDir: stateDir},
				nodeRuntime: &fakeManagedNodeRuntime{
					info: runtimeTestManagedNodeInfo(filepath.Join(t.TempDir(), "managed-bin"), "/managed/bin/node", "/managed/bin/npm", "/managed/bin/npx"),
				},
			}
			definitionRecord := state.DefinitionRecord{
				Snapshot:      agent.Definition,
				PackageHash:   agent.Definition.Package.ArchiveTGZSHA256,
				DefinitionDir: definitionDir,
				InstallDir:    installDir,
			}

			snapshot, err := runtime.ValidateConfig(definitionRecord, agent.Config)
			if err != nil {
				t.Fatalf("ValidateConfig() error = %v", err)
			}
			if snapshot.Mode != agentdef.ConfigModeUnified {
				t.Fatalf("mode = %q", snapshot.Mode)
			}
			if snapshot.Input == nil {
				t.Fatalf("translated input missing")
			}
			tc.assertions(t, snapshot)
		})
	}
}

func TestRepoOwnedAgentsMaterializeSkillsIntoDiscoveryRoots(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		agentName string
		skillsDir string
	}{
		{name: "codex", agentName: "codex", skillsDir: ".agents/skills"},
		{name: "claude-code", agentName: "claude-code", skillsDir: ".claude/skills"},
		{name: "gemini-cli", agentName: "gemini-cli", skillsDir: ".agents/skills"},
		{name: "opencode", agentName: "opencode", skillsDir: ".config/opencode/skills"},
		{name: "pi", agentName: "pi", skillsDir: ".agents/skills"},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			skill := runtimeTestSkill(t, "db-migration", "Use when creating or reviewing database migrations.")
			stateDir := t.TempDir()
			definitionDir := filepath.Join(stateDir, "definition")
			installDir := filepath.Join(stateDir, "install")
			runHome := filepath.Join(stateDir, "run-home")
			if err := os.MkdirAll(installDir, 0o755); err != nil {
				t.Fatalf("mkdir install dir: %v", err)
			}

			agent := testfixture.RepoOwnedAgent(tc.agentName)
			agent.Config.Skills = []agentdef.SkillSpec{skill}
			if err := materializeDefinition(agent.Definition, definitionDir); err != nil {
				t.Fatalf("materializeDefinition() error = %v", err)
			}

			runtime := &Runtime{
				cfg: config.Config{StateDir: stateDir},
				nodeRuntime: &fakeManagedNodeRuntime{
					info: runtimeTestManagedNodeInfo(filepath.Join(t.TempDir(), "managed-bin"), "/managed/bin/node", "/managed/bin/npm", "/managed/bin/npx"),
				},
			}
			definitionRecord := state.DefinitionRecord{
				Snapshot:      agent.Definition,
				PackageHash:   agent.Definition.Package.ArchiveTGZSHA256,
				DefinitionDir: definitionDir,
				InstallDir:    installDir,
			}
			configSnapshot, err := runtime.ValidateConfig(definitionRecord, agent.Config)
			if err != nil {
				t.Fatalf("ValidateConfig() error = %v", err)
			}
			runAgent := state.AgentRecord{
				Definition: &definitionRecord,
				Config:     &state.ConfigRecord{Snapshot: configSnapshot},
				Install: &state.InstallInfo{
					InstalledAt: time.Now().UTC(),
					Result: map[string]any{
						"bin_path": filepath.Join(installDir, "bin", tc.agentName),
					},
				},
			}

			if _, err := runtime.PrepareRun(context.Background(), runAgent, RunContext{
				RunID:         "run_1",
				SessionID:     "session_1",
				CWD:           "/workspace",
				RunHome:       runHome,
				ArtifactsDir:  "/artifacts",
				Env:           map[string]string{"PATH": "/usr/bin"},
				InitialPrompt: "fix the bug",
			}); err != nil {
				t.Fatalf("PrepareRun() error = %v", err)
			}

			skillRoot := filepath.Join(runHome, tc.skillsDir, "db-migration")
			if _, err := os.Stat(filepath.Join(skillRoot, "SKILL.md")); err != nil {
				t.Fatalf("materialized SKILL.md missing: %v", err)
			}
			toolBytes, err := os.ReadFile(filepath.Join(skillRoot, "tools", "check.sh"))
			if err != nil {
				t.Fatalf("read materialized nested skill file: %v", err)
			}
			if !strings.Contains(string(toolBytes), "echo ok") {
				t.Fatalf("materialized nested skill file = %q", string(toolBytes))
			}
		})
	}
}

func TestRepoOwnedAgentsMaterializeAgentsMDIntoStartDirectory(t *testing.T) {
	tests := []struct {
		name      string
		agentName string
		filename  string
	}{
		{name: "codex", agentName: "codex", filename: "AGENTS.md"},
		{name: "claude-code", agentName: "claude-code", filename: "CLAUDE.md"},
		{name: "gemini-cli", agentName: "gemini-cli", filename: "AGENTS.md"},
		{name: "opencode", agentName: "opencode", filename: "AGENTS.md"},
		{name: "pi", agentName: "pi", filename: "AGENTS.md"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			stateDir := t.TempDir()
			workspacesDir := filepath.Join(stateDir, "workspaces")
			runCWD := filepath.Join(workspacesDir, "repo")
			definitionDir := filepath.Join(stateDir, "definition")
			installDir := filepath.Join(stateDir, "install")
			runHome := filepath.Join(stateDir, "run-home")
			if err := os.MkdirAll(runCWD, 0o755); err != nil {
				t.Fatalf("mkdir run cwd: %v", err)
			}
			if err := os.MkdirAll(installDir, 0o755); err != nil {
				t.Fatalf("mkdir install dir: %v", err)
			}

			agent := testfixture.RepoOwnedAgent(tc.agentName)
			agent.Config.AgentsMD = &agentdef.AgentsMDSpec{Content: "Project instructions.\n"}
			if err := materializeDefinition(agent.Definition, definitionDir); err != nil {
				t.Fatalf("materializeDefinition() error = %v", err)
			}

			runtime := &Runtime{
				cfg: config.Config{
					StateDir:      stateDir,
					WorkspacesDir: workspacesDir,
				},
				nodeRuntime: &fakeManagedNodeRuntime{
					info: runtimeTestManagedNodeInfo(filepath.Join(t.TempDir(), "managed-bin"), "/managed/bin/node", "/managed/bin/npm", "/managed/bin/npx"),
				},
			}
			definitionRecord := state.DefinitionRecord{
				Snapshot:      agent.Definition,
				PackageHash:   agent.Definition.Package.ArchiveTGZSHA256,
				DefinitionDir: definitionDir,
				InstallDir:    installDir,
			}
			configSnapshot, err := runtime.ValidateConfig(definitionRecord, agent.Config)
			if err != nil {
				t.Fatalf("ValidateConfig() error = %v", err)
			}
			runAgent := state.AgentRecord{
				Definition: &definitionRecord,
				Config:     &state.ConfigRecord{Snapshot: configSnapshot},
				Install: &state.InstallInfo{
					InstalledAt: time.Now().UTC(),
					Result: map[string]any{
						"bin_path": filepath.Join(installDir, "bin", tc.agentName),
					},
				},
			}

			if _, err := runtime.PrepareRun(context.Background(), runAgent, RunContext{
				RunID:         "run_1",
				SessionID:     "session_1",
				CWD:           runCWD,
				RunHome:       runHome,
				ArtifactsDir:  "/artifacts",
				Env:           map[string]string{"PATH": "/usr/bin"},
				InitialPrompt: "fix the bug",
			}); err != nil {
				t.Fatalf("PrepareRun() error = %v", err)
			}

			body, err := os.ReadFile(filepath.Join(runCWD, tc.filename))
			if err != nil {
				t.Fatalf("read materialized %s: %v", tc.filename, err)
			}
			if string(body) != "Project instructions.\n" {
				t.Fatalf("%s = %q", tc.filename, string(body))
			}
		})
	}
}

func TestPrepareRunOverwritesAgentsMDAndWarns(t *testing.T) {
	stateDir := t.TempDir()
	workspacesDir := filepath.Join(stateDir, "workspaces")
	runCWD := filepath.Join(workspacesDir, "repo")
	definitionDir := filepath.Join(stateDir, "definition")
	installDir := filepath.Join(stateDir, "install")
	runHome := filepath.Join(stateDir, "run-home")
	if err := os.MkdirAll(runCWD, 0o755); err != nil {
		t.Fatalf("mkdir run cwd: %v", err)
	}
	if err := os.MkdirAll(installDir, 0o755); err != nil {
		t.Fatalf("mkdir install dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(runCWD, "AGENTS.md"), []byte("old instructions\n"), 0o644); err != nil {
		t.Fatalf("write existing AGENTS.md: %v", err)
	}

	agent := testfixture.RepoOwnedAgent("codex")
	agent.Config.AgentsMD = &agentdef.AgentsMDSpec{Content: "new instructions\n"}
	if err := materializeDefinition(agent.Definition, definitionDir); err != nil {
		t.Fatalf("materializeDefinition() error = %v", err)
	}

	runtime := &Runtime{
		cfg: config.Config{
			StateDir:      stateDir,
			WorkspacesDir: workspacesDir,
		},
		nodeRuntime: &fakeManagedNodeRuntime{
			info: runtimeTestManagedNodeInfo(filepath.Join(t.TempDir(), "managed-bin"), "/managed/bin/node", "/managed/bin/npm", "/managed/bin/npx"),
		},
	}
	definitionRecord := state.DefinitionRecord{
		Snapshot:      agent.Definition,
		PackageHash:   agent.Definition.Package.ArchiveTGZSHA256,
		DefinitionDir: definitionDir,
		InstallDir:    installDir,
	}
	configSnapshot, err := runtime.ValidateConfig(definitionRecord, agent.Config)
	if err != nil {
		t.Fatalf("ValidateConfig() error = %v", err)
	}
	runAgent := state.AgentRecord{
		Definition: &definitionRecord,
		Config:     &state.ConfigRecord{Snapshot: configSnapshot},
		Install: &state.InstallInfo{
			InstalledAt: time.Now().UTC(),
			Result: map[string]any{
				"bin_path": filepath.Join(installDir, "bin", "codex"),
			},
		},
	}

	var logs bytes.Buffer
	restoreLogs := logutil.SetOutput(&logs)
	defer restoreLogs()

	if _, err := runtime.PrepareRun(context.Background(), runAgent, RunContext{
		RunID:         "run_1",
		SessionID:     "session_1",
		CWD:           runCWD,
		RunHome:       runHome,
		ArtifactsDir:  "/artifacts",
		Env:           map[string]string{"PATH": "/usr/bin"},
		InitialPrompt: "fix the bug",
	}); err != nil {
		t.Fatalf("PrepareRun() error = %v", err)
	}

	body, err := os.ReadFile(filepath.Join(runCWD, "AGENTS.md"))
	if err != nil {
		t.Fatalf("read overwritten AGENTS.md: %v", err)
	}
	if string(body) != "new instructions\n" {
		t.Fatalf("AGENTS.md = %q", string(body))
	}
	if !strings.Contains(logs.String(), `"status":"warn"`) || !strings.Contains(logs.String(), `"message":"agents_md_overwrite"`) {
		t.Fatalf("warning log missing: %q", logs.String())
	}
}

func TestPrepareRunLeavesMatchingAgentsMDWithoutWarning(t *testing.T) {
	stateDir := t.TempDir()
	workspacesDir := filepath.Join(stateDir, "workspaces")
	runCWD := filepath.Join(workspacesDir, "repo")
	definitionDir := filepath.Join(stateDir, "definition")
	installDir := filepath.Join(stateDir, "install")
	runHome := filepath.Join(stateDir, "run-home")
	if err := os.MkdirAll(runCWD, 0o755); err != nil {
		t.Fatalf("mkdir run cwd: %v", err)
	}
	if err := os.MkdirAll(installDir, 0o755); err != nil {
		t.Fatalf("mkdir install dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(runCWD, "AGENTS.md"), []byte("same instructions\n"), 0o644); err != nil {
		t.Fatalf("write existing AGENTS.md: %v", err)
	}

	agent := testfixture.RepoOwnedAgent("codex")
	agent.Config.AgentsMD = &agentdef.AgentsMDSpec{Content: "same instructions\n"}
	if err := materializeDefinition(agent.Definition, definitionDir); err != nil {
		t.Fatalf("materializeDefinition() error = %v", err)
	}

	runtime := &Runtime{
		cfg: config.Config{
			StateDir:      stateDir,
			WorkspacesDir: workspacesDir,
		},
		nodeRuntime: &fakeManagedNodeRuntime{
			info: runtimeTestManagedNodeInfo(filepath.Join(t.TempDir(), "managed-bin"), "/managed/bin/node", "/managed/bin/npm", "/managed/bin/npx"),
		},
	}
	definitionRecord := state.DefinitionRecord{
		Snapshot:      agent.Definition,
		PackageHash:   agent.Definition.Package.ArchiveTGZSHA256,
		DefinitionDir: definitionDir,
		InstallDir:    installDir,
	}
	configSnapshot, err := runtime.ValidateConfig(definitionRecord, agent.Config)
	if err != nil {
		t.Fatalf("ValidateConfig() error = %v", err)
	}
	runAgent := state.AgentRecord{
		Definition: &definitionRecord,
		Config:     &state.ConfigRecord{Snapshot: configSnapshot},
		Install: &state.InstallInfo{
			InstalledAt: time.Now().UTC(),
			Result: map[string]any{
				"bin_path": filepath.Join(installDir, "bin", "codex"),
			},
		},
	}

	var logs bytes.Buffer
	restoreLogs := logutil.SetOutput(&logs)
	defer restoreLogs()

	if _, err := runtime.PrepareRun(context.Background(), runAgent, RunContext{
		RunID:         "run_1",
		SessionID:     "session_1",
		CWD:           runCWD,
		RunHome:       runHome,
		ArtifactsDir:  "/artifacts",
		Env:           map[string]string{"PATH": "/usr/bin"},
		InitialPrompt: "fix the bug",
	}); err != nil {
		t.Fatalf("PrepareRun() error = %v", err)
	}

	if logs.Len() != 0 {
		t.Fatalf("unexpected warning log: %q", logs.String())
	}
}

func TestValidateConfigEnsuresManagedNode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		agent runbundle.Agent
	}{
		{
			name:  "direct",
			agent: testfixture.RepoOwnedAgent("codex"),
		},
		{
			name:  "unified",
			agent: testfixture.RepoOwnedUnifiedAgent("codex"),
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			stateDir := t.TempDir()
			definitionDir := filepath.Join(stateDir, "definition")
			installDir := filepath.Join(stateDir, "install")
			if err := os.MkdirAll(installDir, 0o755); err != nil {
				t.Fatalf("mkdir install dir: %v", err)
			}
			if err := materializeDefinition(tc.agent.Definition, definitionDir); err != nil {
				t.Fatalf("materializeDefinition() error = %v", err)
			}

			fakeNode := &fakeManagedNodeRuntime{
				info: runtimeTestManagedNodeInfo(filepath.Join(t.TempDir(), "managed-bin"), "/managed/bin/node", "/managed/bin/npm", "/managed/bin/npx"),
			}
			runtime := &Runtime{
				cfg:         config.Config{StateDir: stateDir},
				nodeRuntime: fakeNode,
			}
			definitionRecord := state.DefinitionRecord{
				Snapshot:      tc.agent.Definition,
				PackageHash:   tc.agent.Definition.Package.ArchiveTGZSHA256,
				DefinitionDir: definitionDir,
				InstallDir:    installDir,
			}

			if _, err := runtime.ValidateConfig(definitionRecord, tc.agent.Config); err != nil {
				t.Fatalf("ValidateConfig() error = %v", err)
			}
			if fakeNode.ensureCalls != 1 {
				t.Fatalf("Ensure() calls = %d, want 1", fakeNode.ensureCalls)
			}
		})
	}
}

func runtimeTestSkill(t *testing.T, name, description string) agentdef.SkillSpec {
	t.Helper()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "SKILL.md"), []byte(`---
name: `+name+`
description: `+description+`
---

Use this skill when relevant.
`), 0o644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "tools"), 0o755); err != nil {
		t.Fatalf("mkdir skill tools: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "tools", "check.sh"), []byte("#!/usr/bin/env bash\necho ok\n"), 0o755); err != nil {
		t.Fatalf("write skill nested file: %v", err)
	}
	skill, err := agentdef.LoadSkillSpecFromDir(root)
	if err != nil {
		t.Fatalf("LoadSkillSpecFromDir() error = %v", err)
	}
	return skill
}

func TestRepoOwnedClaudePrepareRunSetsSkipDangerousPromptFlag(t *testing.T) {
	stateDir := t.TempDir()
	definitionDir := filepath.Join(stateDir, "definition")
	installDir := filepath.Join(stateDir, "install")
	runHome := filepath.Join(stateDir, "run-home")
	artifactsDir := filepath.Join(stateDir, "artifacts")
	if err := os.MkdirAll(installDir, 0o755); err != nil {
		t.Fatalf("mkdir install dir: %v", err)
	}
	if err := os.MkdirAll(artifactsDir, 0o755); err != nil {
		t.Fatalf("mkdir artifacts dir: %v", err)
	}

	claude := testfixture.RepoOwnedAgent("claude-code")
	if err := materializeDefinition(claude.Definition, definitionDir); err != nil {
		t.Fatalf("materializeDefinition() error = %v", err)
	}

	fakeNode := &fakeManagedNodeRuntime{
		info: runtimeTestManagedNodeInfo(filepath.Join(t.TempDir(), "managed-bin"), "/managed/bin/node", "/managed/bin/npm", "/managed/bin/npx"),
	}
	runtime := &Runtime{
		cfg: config.Config{
			StateDir: stateDir,
		},
		nodeRuntime: fakeNode,
	}
	definitionRecord := state.DefinitionRecord{
		Snapshot:      claude.Definition,
		PackageHash:   claude.Definition.Package.ArchiveTGZSHA256,
		DefinitionDir: definitionDir,
		InstallDir:    installDir,
	}
	configSnapshot, err := runtime.ValidateConfig(definitionRecord, claude.Config)
	if err != nil {
		t.Fatalf("ValidateConfig() error = %v", err)
	}
	agent := state.AgentRecord{
		Definition: &definitionRecord,
		Config:     &state.ConfigRecord{Snapshot: configSnapshot},
		Install: &state.InstallInfo{
			InstalledAt: time.Now().UTC(),
			Result: map[string]any{
				"bin_path": filepath.Join(installDir, "bin", "claude"),
			},
		},
	}

	execSpec, err := runtime.PrepareRun(context.Background(), agent, RunContext{
		RunID:         "run_1",
		SessionID:     "session_1",
		CWD:           "/workspace",
		RunHome:       runHome,
		ArtifactsDir:  artifactsDir,
		Env:           map[string]string{"PATH": "/usr/bin", "ANTHROPIC_API_KEY": "test-api-key-12345678901234567890"},
		InitialPrompt: "fix the bug",
	})
	if err != nil {
		t.Fatalf("PrepareRun() error = %v", err)
	}
	if execSpec.Path != "bash" {
		t.Fatalf("path = %q, want %q", execSpec.Path, "bash")
	}
	if len(execSpec.Args) != 2 || execSpec.Args[0] != "-c" {
		t.Fatalf("args = %#v", execSpec.Args)
	}
	command := execSpec.Args[1]
	for _, token := range []string{
		"--dangerously-skip-permissions",
		"--verbose",
		"--output-format=stream-json",
		"--session-id",
		"claude.stderr.log",
		"-p",
	} {
		if !strings.Contains(command, token) {
			t.Fatalf("command %q missing %q", command, token)
		}
	}
	if strings.Contains(command, "claude-stream.jsonl") {
		t.Fatalf("command %q unexpectedly writes a separate Claude stdout transcript", command)
	}

	settingsBytes, err := os.ReadFile(filepath.Join(runHome, ".claude", "settings.json"))
	if err != nil {
		t.Fatalf("read settings.json: %v", err)
	}
	var settings map[string]any
	if err := json.Unmarshal(settingsBytes, &settings); err != nil {
		t.Fatalf("unmarshal settings.json: %v", err)
	}
	if got, _ := settings["skipDangerousModePermissionPrompt"].(bool); !got {
		t.Fatalf("skipDangerousModePermissionPrompt = %#v, want true", settings["skipDangerousModePermissionPrompt"])
	}
}

func TestRepoOwnedClaudePrepareRunOmitsAPIKeyCacheInOAuthMode(t *testing.T) {
	stateDir := t.TempDir()
	definitionDir := filepath.Join(stateDir, "definition")
	installDir := filepath.Join(stateDir, "install")
	runHome := filepath.Join(stateDir, "run-home")
	if err := os.MkdirAll(filepath.Join(runHome, ".claude"), 0o755); err != nil {
		t.Fatalf("mkdir claude dir: %v", err)
	}
	if err := os.MkdirAll(installDir, 0o755); err != nil {
		t.Fatalf("mkdir install dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(runHome, ".claude", ".credentials.json"), []byte(`{"token":"oauth"}`), 0o600); err != nil {
		t.Fatalf("write credentials file: %v", err)
	}

	claude := testfixture.RepoOwnedAgent("claude-code")
	if err := materializeDefinition(claude.Definition, definitionDir); err != nil {
		t.Fatalf("materializeDefinition() error = %v", err)
	}

	runtime := &Runtime{
		cfg: config.Config{StateDir: stateDir},
		nodeRuntime: &fakeManagedNodeRuntime{
			info: runtimeTestManagedNodeInfo(filepath.Join(t.TempDir(), "managed-bin"), "/managed/bin/node", "/managed/bin/npm", "/managed/bin/npx"),
		},
	}
	definitionRecord := state.DefinitionRecord{
		Snapshot:      claude.Definition,
		PackageHash:   claude.Definition.Package.ArchiveTGZSHA256,
		DefinitionDir: definitionDir,
		InstallDir:    installDir,
	}
	configSnapshot, err := runtime.ValidateConfig(definitionRecord, claude.Config)
	if err != nil {
		t.Fatalf("ValidateConfig() error = %v", err)
	}
	agent := state.AgentRecord{
		Definition: &definitionRecord,
		Config:     &state.ConfigRecord{Snapshot: configSnapshot},
		Install: &state.InstallInfo{
			InstalledAt: time.Now().UTC(),
			Result: map[string]any{
				"bin_path": filepath.Join(installDir, "bin", "claude"),
			},
		},
	}

	if _, err := runtime.PrepareRun(context.Background(), agent, RunContext{
		RunID:         "run_1",
		SessionID:     "session_1",
		CWD:           "/workspace",
		RunHome:       runHome,
		ArtifactsDir:  "/artifacts",
		Env:           map[string]string{"PATH": "/usr/bin"},
		InitialPrompt: "fix the bug",
	}); err != nil {
		t.Fatalf("PrepareRun() error = %v", err)
	}

	body, err := os.ReadFile(filepath.Join(runHome, ".claude", ".claude.json"))
	if err != nil {
		t.Fatalf("read .claude.json: %v", err)
	}
	var stateFile map[string]any
	if err := json.Unmarshal(body, &stateFile); err != nil {
		t.Fatalf("unmarshal .claude.json: %v", err)
	}
	if got, _ := stateFile["hasCompletedOnboarding"].(bool); !got {
		t.Fatalf("hasCompletedOnboarding = %#v, want true", stateFile["hasCompletedOnboarding"])
	}
	if _, exists := stateFile["customApiKeyResponses"]; exists {
		t.Fatalf("unexpected customApiKeyResponses in oauth mode: %#v", stateFile)
	}
}

func TestRepoOwnedClaudePrepareSnapshotUsesPrintResume(t *testing.T) {
	stateDir := t.TempDir()
	definitionDir := filepath.Join(stateDir, "definition")
	installDir := filepath.Join(stateDir, "install")
	runHome := filepath.Join(stateDir, "run-home")
	if err := os.MkdirAll(installDir, 0o755); err != nil {
		t.Fatalf("mkdir install dir: %v", err)
	}

	claude := testfixture.RepoOwnedAgent("claude-code")
	if err := materializeDefinition(claude.Definition, definitionDir); err != nil {
		t.Fatalf("materializeDefinition() error = %v", err)
	}

	runtime := &Runtime{
		cfg: config.Config{StateDir: stateDir},
		nodeRuntime: &fakeManagedNodeRuntime{
			info: runtimeTestManagedNodeInfo(filepath.Join(t.TempDir(), "managed-bin"), "/managed/bin/node", "/managed/bin/npm", "/managed/bin/npx"),
		},
	}
	definitionRecord := state.DefinitionRecord{
		Snapshot:      claude.Definition,
		PackageHash:   claude.Definition.Package.ArchiveTGZSHA256,
		DefinitionDir: definitionDir,
		InstallDir:    installDir,
	}
	configSnapshot, err := runtime.ValidateConfig(definitionRecord, claude.Config)
	if err != nil {
		t.Fatalf("ValidateConfig() error = %v", err)
	}
	agent := state.AgentRecord{
		Definition: &definitionRecord,
		Config:     &state.ConfigRecord{Snapshot: configSnapshot},
		Install: &state.InstallInfo{
			InstalledAt: time.Now().UTC(),
			Result: map[string]any{
				"bin_path": filepath.Join(installDir, "bin", "claude"),
			},
		},
	}

	execSpec, err := runtime.PrepareSnapshot(context.Background(), agent, RunContext{
		RunID:             "run_1",
		SessionID:         "session_1",
		SnapshotSessionID: "snapshot_session_1",
		CWD:               "/workspace",
		RunHome:           runHome,
		ArtifactsDir:      "/artifacts",
		Env:               map[string]string{"PATH": "/usr/bin"},
	})
	if err != nil {
		t.Fatalf("PrepareSnapshot() error = %v", err)
	}
	if !slices.Contains(execSpec.Args, "--resume") {
		t.Fatalf("args = %#v, want explicit resume", execSpec.Args)
	}
	if !slices.Contains(execSpec.Args, "-p") {
		t.Fatalf("args = %#v, want print mode", execSpec.Args)
	}
	if slices.Contains(execSpec.Args, "-c") {
		t.Fatalf("args unexpectedly contain interactive continue mode: %#v", execSpec.Args)
	}
}

func TestRepoOwnedGeminiPrepareRunWritesSettingsAndStreamsJSON(t *testing.T) {
	stateDir := t.TempDir()
	definitionDir := filepath.Join(stateDir, "definition")
	installDir := filepath.Join(stateDir, "install")
	runHome := filepath.Join(stateDir, "run-home")
	artifactsDir := filepath.Join(stateDir, "artifacts")
	if err := os.MkdirAll(installDir, 0o755); err != nil {
		t.Fatalf("mkdir install dir: %v", err)
	}
	if err := os.MkdirAll(artifactsDir, 0o755); err != nil {
		t.Fatalf("mkdir artifacts dir: %v", err)
	}

	agentBundle := testfixture.RepoOwnedUnifiedAgent("gemini-cli")
	agentBundle.Config.Unified.MCP = &agentdef.MCPConfig{
		Servers: []agentdef.MCPServer{{
			Name:      "filesystem",
			Transport: agentdef.MCPTransportSTDIO,
			Command:   []string{"npx", "-y", "@modelcontextprotocol/server-filesystem", "/workspace"},
			Env:       map[string]string{"FS_ROOT": "/workspace"},
		}},
	}
	if err := materializeDefinition(agentBundle.Definition, definitionDir); err != nil {
		t.Fatalf("materializeDefinition() error = %v", err)
	}

	runtime := &Runtime{
		cfg: config.Config{StateDir: stateDir},
		nodeRuntime: &fakeManagedNodeRuntime{
			info: runtimeTestManagedNodeInfo(filepath.Join(t.TempDir(), "managed-bin"), "/managed/bin/node", "/managed/bin/npm", "/managed/bin/npx"),
		},
	}
	definitionRecord := state.DefinitionRecord{
		Snapshot:      agentBundle.Definition,
		PackageHash:   agentBundle.Definition.Package.ArchiveTGZSHA256,
		DefinitionDir: definitionDir,
		InstallDir:    installDir,
	}
	configSnapshot, err := runtime.ValidateConfig(definitionRecord, agentBundle.Config)
	if err != nil {
		t.Fatalf("ValidateConfig() error = %v", err)
	}
	agent := state.AgentRecord{
		Definition: &definitionRecord,
		Config:     &state.ConfigRecord{Snapshot: configSnapshot},
		Install: &state.InstallInfo{
			InstalledAt: time.Now().UTC(),
			Result: map[string]any{
				"bin_path": filepath.Join(installDir, "bin", "gemini"),
			},
		},
	}

	execSpec, err := runtime.PrepareRun(context.Background(), agent, RunContext{
		RunID:         "run_1",
		SessionID:     "session_1",
		CWD:           "/workspace",
		RunHome:       runHome,
		ArtifactsDir:  artifactsDir,
		Env:           map[string]string{"PATH": "/usr/bin"},
		InitialPrompt: "inspect the repository",
	})
	if err != nil {
		t.Fatalf("PrepareRun() error = %v", err)
	}
	if execSpec.Path != "bash" {
		t.Fatalf("path = %q, want %q", execSpec.Path, "bash")
	}
	if len(execSpec.Args) != 2 || execSpec.Args[0] != "-c" {
		t.Fatalf("args = %#v", execSpec.Args)
	}
	command := execSpec.Args[1]
	for _, token := range []string{"--output-format stream-json", "--approval-mode yolo", "--model gemini-3-flash-preview", "gemini-stream.jsonl", "gemini.stderr.log", "-p"} {
		if !strings.Contains(command, token) {
			t.Fatalf("command %q missing %q", command, token)
		}
	}

	settingsBytes, err := os.ReadFile(filepath.Join(runHome, ".gemini", "settings.json"))
	if err != nil {
		t.Fatalf("read settings.json: %v", err)
	}
	var settings map[string]any
	if err := json.Unmarshal(settingsBytes, &settings); err != nil {
		t.Fatalf("unmarshal settings.json: %v", err)
	}
	contextBlock, ok := settings["context"].(map[string]any)
	if !ok {
		t.Fatalf("context = %#v", settings["context"])
	}
	fileNames, ok := contextBlock["fileName"].([]any)
	if !ok {
		t.Fatalf("context.fileName = %#v", contextBlock["fileName"])
	}
	if !slices.Equal(fileNames, []any{"AGENTS.md", "GEMINI.md"}) {
		t.Fatalf("context.fileName = %#v", fileNames)
	}
	mcpServers, ok := settings["mcpServers"].(map[string]any)
	if !ok {
		t.Fatalf("mcpServers = %#v", settings["mcpServers"])
	}
	fsServer, ok := mcpServers["filesystem"].(map[string]any)
	if !ok {
		t.Fatalf("filesystem server = %#v", mcpServers["filesystem"])
	}
	if fsServer["command"] != "npx" {
		t.Fatalf("filesystem command = %#v", fsServer["command"])
	}
}

func TestRepoOwnedOpencodePrepareRunKeepsJSONStreamOnStdout(t *testing.T) {
	stateDir := t.TempDir()
	definitionDir := filepath.Join(stateDir, "definition")
	installDir := filepath.Join(stateDir, "install")
	runHome := filepath.Join(stateDir, "run-home")
	artifactsDir := filepath.Join(stateDir, "artifacts")
	if err := os.MkdirAll(installDir, 0o755); err != nil {
		t.Fatalf("mkdir install dir: %v", err)
	}
	if err := os.MkdirAll(artifactsDir, 0o755); err != nil {
		t.Fatalf("mkdir artifacts dir: %v", err)
	}

	agentBundle := testfixture.RepoOwnedAgent("opencode")
	if err := materializeDefinition(agentBundle.Definition, definitionDir); err != nil {
		t.Fatalf("materializeDefinition() error = %v", err)
	}

	runtime := &Runtime{
		cfg: config.Config{StateDir: stateDir},
		nodeRuntime: &fakeManagedNodeRuntime{
			info: runtimeTestManagedNodeInfo(filepath.Join(t.TempDir(), "managed-bin"), "/managed/bin/node", "/managed/bin/npm", "/managed/bin/npx"),
		},
	}
	definitionRecord := state.DefinitionRecord{
		Snapshot:      agentBundle.Definition,
		PackageHash:   agentBundle.Definition.Package.ArchiveTGZSHA256,
		DefinitionDir: definitionDir,
		InstallDir:    installDir,
	}
	configSnapshot, err := runtime.ValidateConfig(definitionRecord, agentBundle.Config)
	if err != nil {
		t.Fatalf("ValidateConfig() error = %v", err)
	}
	agent := state.AgentRecord{
		Definition: &definitionRecord,
		Config:     &state.ConfigRecord{Snapshot: configSnapshot},
		Install: &state.InstallInfo{
			InstalledAt: time.Now().UTC(),
			Result: map[string]any{
				"bin_path": filepath.Join(installDir, "bin", "opencode"),
			},
		},
	}

	execSpec, err := runtime.PrepareRun(context.Background(), agent, RunContext{
		RunID:         "run_1",
		SessionID:     "session_1",
		CWD:           "/workspace",
		RunHome:       runHome,
		ArtifactsDir:  artifactsDir,
		Env:           map[string]string{"PATH": "/usr/bin"},
		InitialPrompt: "inspect the repository",
	})
	if err != nil {
		t.Fatalf("PrepareRun() error = %v", err)
	}
	if execSpec.Path != "bash" {
		t.Fatalf("path = %q, want %q", execSpec.Path, "bash")
	}
	if len(execSpec.Args) != 2 || execSpec.Args[0] != "-c" {
		t.Fatalf("args = %#v", execSpec.Args)
	}
	command := execSpec.Args[1]
	for _, token := range []string{"run", "--format=json", "opencode.jsonl", "opencode.stderr.log"} {
		if !strings.Contains(command, token) {
			t.Fatalf("command %q missing %q", command, token)
		}
	}
	if strings.Contains(command, "2>&1 | tee") {
		t.Fatalf("command %q unexpectedly merges stderr into stdout", command)
	}
}

func TestRepoOwnedPiPrepareRunUsesJSONModeAndSessionDir(t *testing.T) {
	stateDir := t.TempDir()
	definitionDir := filepath.Join(stateDir, "definition")
	installDir := filepath.Join(stateDir, "install")
	runHome := filepath.Join(stateDir, "run-home")
	artifactsDir := filepath.Join(stateDir, "artifacts")
	if err := os.MkdirAll(installDir, 0o755); err != nil {
		t.Fatalf("mkdir install dir: %v", err)
	}
	if err := os.MkdirAll(artifactsDir, 0o755); err != nil {
		t.Fatalf("mkdir artifacts dir: %v", err)
	}

	agentBundle := testfixture.RepoOwnedAgent("pi")
	if err := materializeDefinition(agentBundle.Definition, definitionDir); err != nil {
		t.Fatalf("materializeDefinition() error = %v", err)
	}

	runtime := &Runtime{
		cfg: config.Config{StateDir: stateDir},
		nodeRuntime: &fakeManagedNodeRuntime{
			info: runtimeTestManagedNodeInfo(filepath.Join(t.TempDir(), "managed-bin"), "/managed/bin/node", "/managed/bin/npm", "/managed/bin/npx"),
		},
	}
	definitionRecord := state.DefinitionRecord{
		Snapshot:      agentBundle.Definition,
		PackageHash:   agentBundle.Definition.Package.ArchiveTGZSHA256,
		DefinitionDir: definitionDir,
		InstallDir:    installDir,
	}
	configSnapshot, err := runtime.ValidateConfig(definitionRecord, agentBundle.Config)
	if err != nil {
		t.Fatalf("ValidateConfig() error = %v", err)
	}
	agent := state.AgentRecord{
		Definition: &definitionRecord,
		Config:     &state.ConfigRecord{Snapshot: configSnapshot},
		Install: &state.InstallInfo{
			InstalledAt: time.Now().UTC(),
			Result: map[string]any{
				"bin_path": filepath.Join(installDir, "bin", "pi"),
			},
		},
	}

	execSpec, err := runtime.PrepareRun(context.Background(), agent, RunContext{
		RunID:         "run_1",
		SessionID:     "session_1",
		CWD:           "/workspace",
		RunHome:       runHome,
		ArtifactsDir:  artifactsDir,
		Env:           map[string]string{"PATH": "/usr/bin"},
		InitialPrompt: "inspect the repository",
	})
	if err != nil {
		t.Fatalf("PrepareRun() error = %v", err)
	}
	if execSpec.Path != "bash" {
		t.Fatalf("path = %q, want %q", execSpec.Path, "bash")
	}
	if len(execSpec.Args) != 2 || execSpec.Args[0] != "-c" {
		t.Fatalf("args = %#v", execSpec.Args)
	}
	command := execSpec.Args[1]
	for _, token := range []string{"--mode json", "--session-dir", "--provider openai", "--model gpt-5", "--thinking medium", "pi-events.jsonl", "pi.stderr.log"} {
		if !strings.Contains(command, token) {
			t.Fatalf("command %q missing %q", command, token)
		}
	}
	gotEnv := map[string]string{}
	for _, pair := range execSpec.Env {
		key, value, ok := strings.Cut(pair, "=")
		if ok {
			gotEnv[key] = value
		}
	}
	if gotEnv["PI_CODING_AGENT_DIR"] != filepath.Join(runHome, ".pi", "agent") {
		t.Fatalf("PI_CODING_AGENT_DIR = %q", gotEnv["PI_CODING_AGENT_DIR"])
	}
}

func TestRepoOwnedCodexCollectTrajectoryProducesATIF(t *testing.T) {
	runtime, agent, runCtx := setupRepoOwnedTrajectoryTest(t, "codex")
	sessionFile := filepath.Join(runCtx.RunHome, ".codex", "sessions", "2026", "03", "10", "session", "codex.jsonl")
	writeJSONLLines(t, sessionFile, []string{
		`{"type":"session_meta","payload":{"id":"codex-session","cli_version":"0.42.0","cwd":"/workspace"}}`,
		`{"type":"turn_context","payload":{"model":"gpt-5-codex"}}`,
		`{"type":"response_item","timestamp":"2026-03-10T12:00:00Z","payload":{"type":"message","role":"user","content":[{"text":"Fix the bug"}]}}`,
		`{"type":"response_item","timestamp":"2026-03-10T12:00:01Z","payload":{"type":"reasoning","summary":["Investigating the repository"]}}`,
		`{"type":"response_item","timestamp":"2026-03-10T12:00:02Z","payload":{"type":"message","role":"assistant","content":[{"text":"I will inspect the files."}]}}`,
		`{"type":"response_item","timestamp":"2026-03-10T12:00:03Z","payload":{"type":"function_call","call_id":"call_1","name":"shell","arguments":"{\"command\":\"ls\"}"}}`,
		`{"type":"response_item","timestamp":"2026-03-10T12:00:04Z","payload":{"type":"function_call_output","call_id":"call_1","output":"{\"output\":\"file1\\nfile2\"}"}}`,
		`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":10,"output_tokens":5,"cached_input_tokens":2,"total_tokens":17},"last_token_usage":{"input_tokens":10,"output_tokens":5,"cached_input_tokens":2},"total_cost":0.12}}}`,
	})

	raw, err := runtime.CollectTrajectory(context.Background(), agent, runCtx)
	if err != nil {
		t.Fatalf("CollectTrajectory() error = %v", err)
	}
	traj, err := trajectory.Decode(raw)
	if err != nil {
		t.Fatalf("trajectory.Decode() error = %v", err)
	}
	if traj.SchemaVersion != trajectory.CurrentSchemaVersion {
		t.Fatalf("schema_version = %q", traj.SchemaVersion)
	}
	if traj.SessionID != "codex-session" {
		t.Fatalf("session_id = %q", traj.SessionID)
	}
	if traj.Agent.Name != "codex" || traj.Agent.Version != "1.0.0" {
		t.Fatalf("unexpected agent = %+v", traj.Agent)
	}
	if len(traj.Steps) != 3 {
		t.Fatalf("steps = %d, want 3", len(traj.Steps))
	}
	if traj.Steps[1].ReasoningContent != "Investigating the repository" {
		t.Fatalf("assistant reasoning = %q", traj.Steps[1].ReasoningContent)
	}
	if len(traj.Steps[2].ToolCalls) != 1 || traj.Steps[2].ToolCalls[0].ToolCallID != "call_1" {
		t.Fatalf("unexpected tool calls = %+v", traj.Steps[2].ToolCalls)
	}
	if traj.FinalMetrics == nil || traj.FinalMetrics.TotalPromptTokens == nil || *traj.FinalMetrics.TotalPromptTokens != 10 {
		t.Fatalf("unexpected final metrics = %+v", traj.FinalMetrics)
	}
	if traj.FinalMetrics.TotalCachedTokens == nil || *traj.FinalMetrics.TotalCachedTokens != 2 {
		t.Fatalf("unexpected cached tokens = %+v", traj.FinalMetrics)
	}
}

func TestRepoOwnedClaudeCollectTrajectoryProducesATIF(t *testing.T) {
	runtime, agent, runCtx := setupRepoOwnedTrajectoryTest(t, "claude-code")
	sessionFile := filepath.Join(runCtx.RunHome, ".claude", "projects", "project-a", "session.jsonl")
	writeJSONLLines(t, sessionFile, []string{
		`{"type":"user","timestamp":"2026-03-10T12:00:00Z","sessionId":"claude-session","version":"1.2.3","cwd":"/workspace","message":{"content":"Fix the bug"}}`,
		`{"type":"assistant","timestamp":"2026-03-10T12:00:01Z","sessionId":"claude-session","version":"1.2.3","message":{"id":"msg-1","role":"assistant","model":"sonnet","usage":{"input_tokens":20,"output_tokens":8,"cache_read_input_tokens":3,"service_tier":"standard"},"content":[{"type":"thinking","text":"Plan the fix"},{"type":"text","text":"Running a command"},{"type":"tool_use","id":"toolu_1","name":"Bash","input":{"command":"ls"}}]}}`,
		`{"type":"user","timestamp":"2026-03-10T12:00:02Z","sessionId":"claude-session","version":"1.2.3","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"file1\nfile2"}]}}`,
		`{"type":"assistant","timestamp":"2026-03-10T12:00:03Z","sessionId":"claude-session","version":"1.2.3","message":{"id":"msg-2","role":"assistant","model":"sonnet","usage":{"input_tokens":10,"output_tokens":4},"content":"Done"}}`,
	})

	raw, err := runtime.CollectTrajectory(context.Background(), agent, runCtx)
	if err != nil {
		t.Fatalf("CollectTrajectory() error = %v", err)
	}
	traj, err := trajectory.Decode(raw)
	if err != nil {
		t.Fatalf("trajectory.Decode() error = %v", err)
	}
	if traj.SessionID != "claude-session" {
		t.Fatalf("session_id = %q", traj.SessionID)
	}
	if traj.Agent.Name != "claude-code" || traj.Agent.Version != "1.2.3" {
		t.Fatalf("unexpected agent = %+v", traj.Agent)
	}
	if len(traj.Steps) != 4 {
		t.Fatalf("steps = %d, want 4", len(traj.Steps))
	}
	if traj.Steps[1].ReasoningContent != "Plan the fix" {
		t.Fatalf("reasoning_content = %q", traj.Steps[1].ReasoningContent)
	}
	if len(traj.Steps[2].ToolCalls) != 1 || traj.Steps[2].ToolCalls[0].FunctionName != "Bash" {
		t.Fatalf("unexpected tool call step = %+v", traj.Steps[2])
	}
	if traj.FinalMetrics == nil || traj.FinalMetrics.TotalPromptTokens == nil || *traj.FinalMetrics.TotalPromptTokens != 23 {
		t.Fatalf("unexpected prompt totals = %+v", traj.FinalMetrics)
	}
	if traj.FinalMetrics.TotalCompletionTokens == nil || *traj.FinalMetrics.TotalCompletionTokens != 12 {
		t.Fatalf("unexpected completion totals = %+v", traj.FinalMetrics)
	}
	if traj.FinalMetrics.TotalCachedTokens == nil || *traj.FinalMetrics.TotalCachedTokens != 3 {
		t.Fatalf("unexpected cached totals = %+v", traj.FinalMetrics)
	}
}

func TestRepoOwnedOpencodeCollectTrajectoryProducesATIF(t *testing.T) {
	runtime, agent, runCtx := setupRepoOwnedTrajectoryTest(t, "opencode")
	writeJSONLLines(t, filepath.Join(runCtx.ArtifactsDir, "pty.log"), []string{
		`{"type":"step_start","sessionID":"opencode-session","timestamp":1741608000000}`,
		`{"type":"text","part":{"type":"text","text":"Inspecting the repository"}}`,
		`{"type":"tool_use","part":{"type":"tool","callID":"call_1","tool":"bash","state":{"input":{"command":"ls"},"output":"file1\nfile2"}}}`,
		`{"type":"step_finish","part":{"tokens":{"input":12,"output":6,"reasoning":2,"cache":{"read":4,"write":1}},"cost":0.42}}`,
		`{"type":"step_start","sessionID":"opencode-session","timestamp":1741608001000}`,
		`{"type":"text","part":{"type":"text","text":"Applying the fix"}}`,
		`{"type":"step_finish","part":{"tokens":{"input":14,"output":3,"reasoning":0,"cache":{"read":5,"write":0}},"cost":0.11}}`,
	})

	raw, err := runtime.CollectTrajectory(context.Background(), agent, runCtx)
	if err != nil {
		t.Fatalf("CollectTrajectory() error = %v", err)
	}
	traj, err := trajectory.Decode(raw)
	if err != nil {
		t.Fatalf("trajectory.Decode() error = %v", err)
	}
	if traj.SessionID != "opencode-session" {
		t.Fatalf("session_id = %q", traj.SessionID)
	}
	if traj.Agent.Name != "opencode" {
		t.Fatalf("unexpected agent = %+v", traj.Agent)
	}
	if len(traj.Steps) != 3 {
		t.Fatalf("steps = %d, want 3", len(traj.Steps))
	}
	if text, ok := traj.Steps[0].Message.Text(); !ok || text != "fix the bug" {
		t.Fatalf("unexpected user message = %q", text)
	}
	if len(traj.Steps[1].ToolCalls) != 1 || traj.Steps[1].ToolCalls[0].FunctionName != "bash" {
		t.Fatalf("unexpected tool call step = %+v", traj.Steps[1])
	}
	if traj.Steps[1].Metrics == nil || traj.Steps[1].Metrics.PromptTokens == nil || *traj.Steps[1].Metrics.PromptTokens != 16 {
		t.Fatalf("unexpected first prompt snapshot = %+v", traj.Steps[1].Metrics)
	}
	if traj.Steps[2].Metrics == nil || traj.Steps[2].Metrics.PromptTokens == nil || *traj.Steps[2].Metrics.PromptTokens != 19 {
		t.Fatalf("unexpected second prompt snapshot = %+v", traj.Steps[2].Metrics)
	}
	if traj.FinalMetrics == nil {
		t.Fatalf("expected final metrics")
	}
	if traj.FinalMetrics.TotalPromptTokens == nil || *traj.FinalMetrics.TotalPromptTokens != 19 {
		t.Fatalf("unexpected prompt totals = %+v", traj.FinalMetrics)
	}
	if traj.FinalMetrics.TotalCompletionTokens == nil || *traj.FinalMetrics.TotalCompletionTokens != 9 {
		t.Fatalf("unexpected completion totals = %+v", traj.FinalMetrics)
	}
	if traj.FinalMetrics.TotalCachedTokens == nil || *traj.FinalMetrics.TotalCachedTokens != 5 {
		t.Fatalf("unexpected cached totals = %+v", traj.FinalMetrics)
	}
}

func TestRepoOwnedGeminiCollectTrajectoryProducesATIF(t *testing.T) {
	runtime, agent, runCtx := setupRepoOwnedTrajectoryTest(t, "gemini-cli")
	writeJSONLLines(t, filepath.Join(runCtx.ArtifactsDir, "gemini-stream.jsonl"), []string{
		`{"type":"init","timestamp":"2026-03-10T12:00:00Z","session_id":"gemini-session","model":"gemini-2.5-pro"}`,
		`{"type":"message","timestamp":"2026-03-10T12:00:00Z","role":"user","content":"fix the bug"}`,
		`{"type":"message","timestamp":"2026-03-10T12:00:01Z","role":"assistant","content":"Inspecting files"}`,
		`{"type":"tool_use","timestamp":"2026-03-10T12:00:02Z","tool_name":"ReadFile","tool_id":"tool-1","parameters":{"path":"main.go"}}`,
		`{"type":"tool_result","timestamp":"2026-03-10T12:00:03Z","tool_id":"tool-1","status":"success","output":"package main"}`,
		`{"type":"message","timestamp":"2026-03-10T12:00:04Z","role":"assistant","content":"Done"}`,
		`{"type":"result","timestamp":"2026-03-10T12:00:05Z","status":"success","stats":{"total_tokens":24,"input_tokens":14,"output_tokens":10,"cached":3,"input":11,"duration_ms":2500,"tool_calls":1,"models":{"gemini-2.5-pro":{"total_tokens":24,"input_tokens":14,"output_tokens":10,"cached":3,"input":11}}}}`,
	})

	raw, err := runtime.CollectTrajectory(context.Background(), agent, runCtx)
	if err != nil {
		t.Fatalf("CollectTrajectory() error = %v", err)
	}
	traj, err := trajectory.Decode(raw)
	if err != nil {
		t.Fatalf("trajectory.Decode() error = %v", err)
	}
	if traj.SessionID != "gemini-session" {
		t.Fatalf("session_id = %q", traj.SessionID)
	}
	if traj.Agent.Name != "gemini-cli" || traj.Agent.Version != "1.0.0" {
		t.Fatalf("unexpected agent = %+v", traj.Agent)
	}
	if len(traj.Steps) != 3 {
		t.Fatalf("steps = %d, want 3", len(traj.Steps))
	}
	if len(traj.Steps[1].ToolCalls) != 1 || traj.Steps[1].ToolCalls[0].FunctionName != "ReadFile" {
		t.Fatalf("unexpected tool call step = %+v", traj.Steps[1])
	}
	if traj.Steps[1].Observation == nil || len(traj.Steps[1].Observation.Results) != 1 {
		t.Fatalf("observation = %+v", traj.Steps[1].Observation)
	}
	if traj.FinalMetrics == nil || traj.FinalMetrics.TotalPromptTokens == nil || *traj.FinalMetrics.TotalPromptTokens != 14 {
		t.Fatalf("unexpected prompt totals = %+v", traj.FinalMetrics)
	}
	if traj.FinalMetrics.TotalCompletionTokens == nil || *traj.FinalMetrics.TotalCompletionTokens != 10 {
		t.Fatalf("unexpected completion totals = %+v", traj.FinalMetrics)
	}
	if traj.FinalMetrics.TotalCachedTokens == nil || *traj.FinalMetrics.TotalCachedTokens != 3 {
		t.Fatalf("unexpected cached totals = %+v", traj.FinalMetrics)
	}
}

func TestRepoOwnedPiCollectTrajectoryProducesATIF(t *testing.T) {
	runtime, agent, runCtx := setupRepoOwnedTrajectoryTest(t, "pi")
	imageData := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO7Z0b8AAAAASUVORK5CYII="
	writeJSONLLines(t, filepath.Join(runCtx.ArtifactsDir, "pi-events.jsonl"), []string{
		`{"type":"session","id":"pi-session","cwd":"/workspace"}`,
		`{"type":"agent_end","messages":[{"role":"user","content":"fix the bug","timestamp":1741608000000},{"role":"assistant","model":"openai/gpt-5","timestamp":1741608001000,"usage":{"input":12,"output":6,"cacheRead":2,"cacheWrite":1,"cost":{"total":0.42}},"content":[{"type":"thinking","thinking":"Plan the fix"},{"type":"text","text":"Inspecting the repository"},{"type":"toolCall","id":"toolu_1","name":"bash","arguments":{"command":"ls"}}]},{"role":"toolResult","toolCallId":"toolu_1","content":[{"type":"text","text":"file1\nfile2"},{"type":"image","mimeType":"image/png","data":"` + imageData + `"}]},{"role":"assistant","model":"openai/gpt-5","timestamp":1741608002000,"usage":{"input":14,"output":3,"cacheRead":5,"cacheWrite":0,"cost":{"total":0.11}},"content":[{"type":"text","text":"Done"}]}]}`,
	})

	raw, err := runtime.CollectTrajectory(context.Background(), agent, runCtx)
	if err != nil {
		t.Fatalf("CollectTrajectory() error = %v", err)
	}
	traj, err := trajectory.Decode(raw)
	if err != nil {
		t.Fatalf("trajectory.Decode() error = %v", err)
	}
	if traj.SessionID != "pi-session" {
		t.Fatalf("session_id = %q", traj.SessionID)
	}
	if traj.Agent.Name != "pi" || traj.Agent.Version != "1.0.0" {
		t.Fatalf("unexpected agent = %+v", traj.Agent)
	}
	if len(traj.Steps) != 3 {
		t.Fatalf("steps = %d, want 3", len(traj.Steps))
	}
	if traj.Steps[1].ReasoningContent != "Plan the fix" {
		t.Fatalf("reasoning_content = %q", traj.Steps[1].ReasoningContent)
	}
	if len(traj.Steps[1].ToolCalls) != 1 || traj.Steps[1].ToolCalls[0].FunctionName != "bash" {
		t.Fatalf("unexpected tool call step = %+v", traj.Steps[1])
	}
	if traj.Steps[1].Observation == nil || len(traj.Steps[1].Observation.Results) != 1 {
		t.Fatalf("observation = %+v", traj.Steps[1].Observation)
	}
	if _, err := os.Stat(filepath.Join(runCtx.ArtifactsDir, "images", "step_2_obs_0_img_0.png")); err != nil {
		t.Fatalf("materialized observation image missing: %v", err)
	}
	if traj.Steps[1].Metrics == nil || traj.Steps[1].Metrics.PromptTokens == nil || *traj.Steps[1].Metrics.PromptTokens != 14 {
		t.Fatalf("unexpected first prompt snapshot = %+v", traj.Steps[1].Metrics)
	}
	if traj.Steps[2].Metrics == nil || traj.Steps[2].Metrics.PromptTokens == nil || *traj.Steps[2].Metrics.PromptTokens != 19 {
		t.Fatalf("unexpected second prompt snapshot = %+v", traj.Steps[2].Metrics)
	}
	if traj.FinalMetrics == nil {
		t.Fatalf("expected final metrics")
	}
	if traj.FinalMetrics.TotalPromptTokens == nil || *traj.FinalMetrics.TotalPromptTokens != 19 {
		t.Fatalf("unexpected prompt totals = %+v", traj.FinalMetrics)
	}
	if traj.FinalMetrics.TotalCompletionTokens == nil || *traj.FinalMetrics.TotalCompletionTokens != 9 {
		t.Fatalf("unexpected completion totals = %+v", traj.FinalMetrics)
	}
	if traj.FinalMetrics.TotalCachedTokens == nil || *traj.FinalMetrics.TotalCachedTokens != 5 {
		t.Fatalf("unexpected cached totals = %+v", traj.FinalMetrics)
	}
}

func setupRepoOwnedTrajectoryTest(t *testing.T, agentName string) (*Runtime, state.AgentRecord, RunContext) {
	t.Helper()

	stateDir := t.TempDir()
	definitionDir := filepath.Join(stateDir, "definition")
	installDir := filepath.Join(stateDir, "install")
	runHome := filepath.Join(stateDir, "run-home")
	artifactsDir := filepath.Join(stateDir, "artifacts")
	if err := os.MkdirAll(installDir, 0o755); err != nil {
		t.Fatalf("mkdir install dir: %v", err)
	}
	if err := os.MkdirAll(runHome, 0o755); err != nil {
		t.Fatalf("mkdir run home: %v", err)
	}
	if err := os.MkdirAll(artifactsDir, 0o755); err != nil {
		t.Fatalf("mkdir artifacts dir: %v", err)
	}

	agentBundle := testfixture.RepoOwnedAgent(agentName)
	if err := materializeDefinition(agentBundle.Definition, definitionDir); err != nil {
		t.Fatalf("materializeDefinition() error = %v", err)
	}

	runtime := &Runtime{
		cfg: config.Config{StateDir: stateDir},
		nodeRuntime: &fakeManagedNodeRuntime{
			info: runtimeTestManagedNodeInfo(filepath.Join(t.TempDir(), "managed-bin"), "/managed/bin/node", "/managed/bin/npm", "/managed/bin/npx"),
		},
	}
	definitionRecord := state.DefinitionRecord{
		Snapshot:      agentBundle.Definition,
		PackageHash:   agentBundle.Definition.Package.ArchiveTGZSHA256,
		DefinitionDir: definitionDir,
		InstallDir:    installDir,
	}
	configSnapshot, err := runtime.ValidateConfig(definitionRecord, agentBundle.Config)
	if err != nil {
		t.Fatalf("ValidateConfig() error = %v", err)
	}
	agent := state.AgentRecord{
		Definition: &definitionRecord,
		Config:     &state.ConfigRecord{Snapshot: configSnapshot},
		Install: &state.InstallInfo{
			InstalledAt: time.Now().UTC(),
			Result: map[string]any{
				"bin_path":         filepath.Join(installDir, "bin", agentName),
				"resolved_version": "1.0.0",
				"version":          "1.0.0",
			},
		},
	}
	return runtime, agent, RunContext{
		RunID:         "run_1",
		SessionID:     "session_1",
		CWD:           "/workspace",
		RunHome:       runHome,
		ArtifactsDir:  artifactsDir,
		Env:           map[string]string{"PATH": "/usr/bin"},
		InitialPrompt: "fix the bug",
	}
}

func writeJSONLLines(t *testing.T, path string, lines []string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	body := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
