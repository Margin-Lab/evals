//go:build integration

package integration

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/marginlab/margin-eval/agent-server/internal/apperr"
	"github.com/marginlab/margin-eval/runner/runner-core/agentdef"
	"github.com/marginlab/margin-eval/runner/runner-core/testassets"
	"github.com/marginlab/margin-eval/runner/runner-core/testfixture"
)

type healthResponse struct {
	Status string `json:"status"`
}

type stateResponse struct {
	Agent struct {
		State      string `json:"state"`
		Definition *struct {
			Snapshot struct {
				Manifest struct {
					Name string `json:"name"`
				} `json:"manifest"`
			} `json:"snapshot"`
		} `json:"definition"`
		Config *struct {
			Snapshot struct {
				Name  string         `json:"name"`
				Input map[string]any `json:"input"`
			} `json:"snapshot"`
		} `json:"config"`
		Install *struct {
			Result map[string]any `json:"result"`
		} `json:"install"`
	} `json:"agent"`
	Run struct {
		State string  `json:"state"`
		RunID *string `json:"run_id"`
	} `json:"run"`
	Paths struct {
		Root       string `json:"root"`
		Bin        string `json:"bin"`
		State      string `json:"state"`
		Workspaces string `json:"workspaces"`
	} `json:"paths"`
	Capabilities struct {
		SupportsInstall    bool     `json:"supports_install"`
		SupportsSnapshot   bool     `json:"supports_snapshot"`
		SupportsTrajectory bool     `json:"supports_trajectory"`
		RequiredEnv        []string `json:"required_env"`
	} `json:"capabilities"`
	ShuttingDown bool `json:"shutting_down"`
}

type putAgentDefinitionResponse struct {
	State      string `json:"state"`
	Definition *struct {
		Snapshot struct {
			Manifest struct {
				Name string `json:"name"`
			} `json:"manifest"`
		} `json:"snapshot"`
	} `json:"definition"`
}

type putAgentConfigResponse struct {
	State  string `json:"state"`
	Config struct {
		Name  string         `json:"name"`
		Input map[string]any `json:"input"`
	} `json:"config"`
}

type postAgentInstallResponse struct {
	State   string `json:"state"`
	Install *struct {
		Result map[string]any `json:"result"`
	} `json:"install"`
}

type startRunResponse struct {
	RunID string `json:"run_id"`
	State string `json:"state"`
	PID   *int   `json:"pid"`
}

type deleteRunResponse struct {
	State string `json:"state"`
}

func TestLegacyAgentEndpointsRemoved(t *testing.T) {
	ensureDockerProviderHealthy(t)

	server := startServerContainer(t, nil)

	for _, path := range []string{"/v1/agent", "/v1/agent/config"} {
		resp := server.requestJSON(t, http.MethodPut, path, map[string]any{})
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("%s status = %d, want %d", path, resp.StatusCode, http.StatusNotFound)
		}
	}
}

func TestServerLifecycleWithFixtureDefinition(t *testing.T) {
	ensureDockerProviderHealthy(t)

	server := startServerContainer(t, nil)

	resp := server.requestJSON(t, http.MethodGet, "/healthz", nil)
	assertStatusCode(t, resp, http.StatusOK)
	health := decodeJSON[healthResponse](t, resp.Body)
	if health.Status != "ok" {
		t.Fatalf("health status = %q, want %q", health.Status, "ok")
	}

	resp = server.requestJSON(t, http.MethodGet, "/readyz", nil)
	assertStatusCode(t, resp, http.StatusOK)
	ready := decodeJSON[healthResponse](t, resp.Body)
	if ready.Status != "ready" {
		t.Fatalf("ready status = %q, want %q", ready.Status, "ready")
	}

	resp = server.requestJSON(t, http.MethodGet, "/v1/state", nil)
	assertStatusCode(t, resp, http.StatusOK)
	initial := decodeJSON[stateResponse](t, resp.Body)
	if initial.Agent.State != "empty" {
		t.Fatalf("initial agent state = %q, want %q", initial.Agent.State, "empty")
	}
	if initial.Run.State != "idle" || initial.Run.RunID != nil {
		t.Fatalf("initial run = %+v", initial.Run)
	}

	resp = server.requestJSON(t, http.MethodPut, "/v1/agent-config", fixtureConfigRequest())
	assertAPIError(t, resp, http.StatusConflict, apperr.CodeAgentNotConfigured)

	resp = server.requestJSON(t, http.MethodPost, "/v1/agent/install", nil)
	assertAPIError(t, resp, http.StatusConflict, apperr.CodeAgentNotConfigured)

	resp = server.requestJSON(t, http.MethodPut, "/v1/agent-definition", fixtureDefinitionRequest(t))
	assertStatusCode(t, resp, http.StatusCreated)
	definition := decodeJSON[putAgentDefinitionResponse](t, resp.Body)
	if definition.State != "definition_loaded" {
		t.Fatalf("definition state = %q, want %q", definition.State, "definition_loaded")
	}
	if definition.Definition == nil || definition.Definition.Snapshot.Manifest.Name != "fixture-agent" {
		t.Fatalf("loaded definition = %#v", definition.Definition)
	}

	resp = server.requestJSON(t, http.MethodPut, "/v1/agent-config", fixtureConfigRequest())
	assertStatusCode(t, resp, http.StatusCreated)
	configResp := decodeJSON[putAgentConfigResponse](t, resp.Body)
	if configResp.State != "configured" {
		t.Fatalf("config state = %q, want %q", configResp.State, "configured")
	}
	if configResp.Config.Name != "fixture-default" {
		t.Fatalf("config name = %q, want %q", configResp.Config.Name, "fixture-default")
	}

	startPayload := map[string]any{
		"cwd":            "/marginlab/workspaces",
		"initial_prompt": "fixture run",
		"env":            map[string]string{"TERM": "xterm-256color"},
		"pty":            map[string]int{"cols": 120, "rows": 40},
	}

	resp = server.requestJSON(t, http.MethodPost, "/v1/run", startPayload)
	assertAPIError(t, resp, http.StatusConflict, apperr.CodeAgentNotConfigured)

	resp = server.requestJSON(t, http.MethodPost, "/v1/agent/install", nil)
	assertStatusCode(t, resp, http.StatusOK)
	installResp := decodeJSON[postAgentInstallResponse](t, resp.Body)
	if installResp.State != "configured" {
		t.Fatalf("install state = %q, want %q", installResp.State, "configured")
	}
	if installResp.Install == nil || installResp.Install.Result["installed"] != true {
		t.Fatalf("install result = %#v", installResp.Install)
	}

	resp = server.requestJSON(t, http.MethodGet, "/v1/state", nil)
	assertStatusCode(t, resp, http.StatusOK)
	configured := decodeJSON[stateResponse](t, resp.Body)
	if configured.Agent.State != "configured" {
		t.Fatalf("configured agent state = %q, want %q", configured.Agent.State, "configured")
	}
	if !configured.Capabilities.SupportsInstall || configured.Capabilities.SupportsSnapshot || configured.Capabilities.SupportsTrajectory {
		t.Fatalf("fixture capabilities = %+v", configured.Capabilities)
	}

	startRespHTTP := server.requestJSON(t, http.MethodPost, "/v1/run", startPayload)
	assertStatusCode(t, startRespHTTP, http.StatusCreated)
	start := decodeJSON[startRunResponse](t, startRespHTTP.Body)
	if start.RunID == "" || start.PID == nil || *start.PID <= 0 {
		t.Fatalf("start response = %+v", start)
	}

	exited := server.waitForRunState(t, 20*time.Second, "exited")
	if exited.RunID != start.RunID {
		t.Fatalf("run id = %q, want %q", exited.RunID, start.RunID)
	}
	if exited.ExitCode == nil || *exited.ExitCode != 0 {
		t.Fatalf("exit code = %#v, want %d", exited.ExitCode, 0)
	}

	resp = server.requestJSON(t, http.MethodPost, "/v1/run", startPayload)
	assertAPIError(t, resp, http.StatusConflict, apperr.CodeRunNotCleared)

	resp = server.requestJSON(t, http.MethodDelete, "/v1/run", nil)
	assertStatusCode(t, resp, http.StatusOK)
	deleted := decodeJSON[deleteRunResponse](t, resp.Body)
	if deleted.State != "idle" {
		t.Fatalf("delete state = %q, want %q", deleted.State, "idle")
	}
}

func TestServerDryRunSkipsPTYAndTrajectory(t *testing.T) {
	ensureDockerProviderHealthy(t)

	server := startServerContainer(t, nil)

	resp := server.requestJSON(t, http.MethodPut, "/v1/agent-definition", fixtureDefinitionRequest(t))
	assertStatusCode(t, resp, http.StatusCreated)

	resp = server.requestJSON(t, http.MethodPut, "/v1/agent-config", fixtureConfigRequest())
	assertStatusCode(t, resp, http.StatusCreated)

	resp = server.requestJSON(t, http.MethodPost, "/v1/agent/install", nil)
	assertStatusCode(t, resp, http.StatusOK)

	startPayload := map[string]any{
		"cwd":            "/marginlab/workspaces",
		"initial_prompt": "fixture dry run",
		"dry_run":        true,
		"env":            map[string]string{"TERM": "xterm-256color"},
		"pty":            map[string]int{"cols": 120, "rows": 40},
	}

	startRespHTTP := server.requestJSON(t, http.MethodPost, "/v1/run", startPayload)
	assertStatusCode(t, startRespHTTP, http.StatusCreated)
	start := decodeJSON[startRunResponse](t, startRespHTTP.Body)
	if start.RunID == "" {
		t.Fatalf("start response = %+v", start)
	}
	if start.State != "exited" {
		t.Fatalf("start state = %q, want %q", start.State, "exited")
	}
	if start.PID != nil {
		t.Fatalf("dry-run pid = %#v, want nil", start.PID)
	}

	exited := server.waitForRunState(t, 5*time.Second, "exited")
	if exited.RunID != start.RunID {
		t.Fatalf("run id = %q, want %q", exited.RunID, start.RunID)
	}
	if exited.ExitCode == nil || *exited.ExitCode != 0 {
		t.Fatalf("exit code = %#v, want %d", exited.ExitCode, 0)
	}
	if exited.TrajectoryStatus != "none" {
		t.Fatalf("trajectory_status = %q, want %q", exited.TrajectoryStatus, "none")
	}

	resp = server.requestJSON(t, http.MethodGet, "/v1/run/trajectory", nil)
	assertAPIError(t, resp, http.StatusNotFound, apperr.CodeTrajectoryUnavailable)

	resp = server.requestJSON(t, http.MethodDelete, "/v1/run", nil)
	assertStatusCode(t, resp, http.StatusOK)
}

func TestServerDryRunWithManagedNodeJSHooks(t *testing.T) {
	ensureDockerProviderHealthy(t)

	server := startServerContainer(t, nil)

	resp := server.requestJSON(t, http.MethodPut, "/v1/agent-definition", fixtureManagedNodeDefinitionRequest(t))
	assertStatusCode(t, resp, http.StatusCreated)

	resp = server.requestJSON(t, http.MethodPut, "/v1/agent-config", fixtureManagedNodeConfigRequest())
	assertStatusCode(t, resp, http.StatusCreated)

	resp = server.requestJSON(t, http.MethodPost, "/v1/agent/install", nil)
	assertStatusCode(t, resp, http.StatusOK)

	startPayload := map[string]any{
		"cwd":            "/marginlab/workspaces",
		"initial_prompt": "fixture managed-node dry run",
		"dry_run":        true,
		"env":            map[string]string{"TERM": "xterm-256color"},
		"pty":            map[string]int{"cols": 120, "rows": 40},
	}

	startRespHTTP := server.requestJSON(t, http.MethodPost, "/v1/run", startPayload)
	assertStatusCode(t, startRespHTTP, http.StatusCreated)
	start := decodeJSON[startRunResponse](t, startRespHTTP.Body)
	if start.RunID == "" {
		t.Fatalf("start response = %+v", start)
	}
	if start.State != "exited" {
		t.Fatalf("start state = %q, want %q", start.State, "exited")
	}
	if start.PID != nil {
		t.Fatalf("dry-run pid = %#v, want nil", start.PID)
	}

	exited := server.waitForRunState(t, 90*time.Second, "exited")
	if exited.RunID != start.RunID {
		t.Fatalf("run id = %q, want %q", exited.RunID, start.RunID)
	}
	if exited.ExitCode == nil || *exited.ExitCode != 0 {
		t.Fatalf("exit code = %#v, want %d", exited.ExitCode, 0)
	}
	if exited.TrajectoryStatus != "none" {
		t.Fatalf("trajectory_status = %q, want %q", exited.TrajectoryStatus, "none")
	}
}

func TestRepoOwnedDefinitionCapabilities(t *testing.T) {
	ensureDockerProviderHealthy(t)

	for _, agentName := range testfixture.RepoOwnedAgentNames() {
		agentName := agentName
		t.Run(agentName, func(t *testing.T) {
			server := startServerContainer(t, nil)

			resp := server.requestJSON(t, http.MethodPut, "/v1/agent-definition", repoOwnedDefinitionRequest(t, agentName))
			assertStatusCode(t, resp, http.StatusCreated)

			resp = server.requestJSON(t, http.MethodGet, "/v1/state", nil)
			assertStatusCode(t, resp, http.StatusOK)
			state := decodeJSON[stateResponse](t, resp.Body)
			if state.Agent.State != "definition_loaded" {
				t.Fatalf("agent state = %q, want %q", state.Agent.State, "definition_loaded")
			}
			if state.Agent.Definition == nil || state.Agent.Definition.Snapshot.Manifest.Name != agentName {
				t.Fatalf("definition = %#v", state.Agent.Definition)
			}
			requiredEnv := testfixture.RepoOwnedDefinitionRequiredEnv(agentName)
			if !slices.Equal(state.Capabilities.RequiredEnv, requiredEnv) {
				t.Fatalf("required_env = %v, want %v", state.Capabilities.RequiredEnv, requiredEnv)
			}
			if !state.Capabilities.SupportsInstall || !state.Capabilities.SupportsTrajectory {
				t.Fatalf("capabilities = %+v", state.Capabilities)
			}
			wantSnapshot := agentName == "codex" || agentName == "claude-code"
			if state.Capabilities.SupportsSnapshot != wantSnapshot {
				t.Fatalf("supports_snapshot = %t, want %t", state.Capabilities.SupportsSnapshot, wantSnapshot)
			}
		})
	}
}

func TestRepoOwnedConfigCapabilitiesResolveProviderAwareRequiredEnv(t *testing.T) {
	ensureDockerProviderHealthy(t)

	for _, agentName := range []string{"opencode", "pi"} {
		agentName := agentName
		t.Run(agentName, func(t *testing.T) {
			server := startServerContainer(t, nil)

			resp := server.requestJSON(t, http.MethodPut, "/v1/agent-definition", repoOwnedDefinitionRequest(t, agentName))
			assertStatusCode(t, resp, http.StatusCreated)

			configName := testfixture.RepoOwnedDefaultConfigName(agentName)
			resp = server.requestJSON(t, http.MethodPut, "/v1/agent-config", repoOwnedConfigRequest(t, agentName, configName, "latest"))
			assertStatusCode(t, resp, http.StatusCreated)

			resp = server.requestJSON(t, http.MethodGet, "/v1/state", nil)
			assertStatusCode(t, resp, http.StatusOK)
			state := decodeJSON[stateResponse](t, resp.Body)

			requiredEnv := testfixture.RepoOwnedRequiredEnvForConfig(agentName, configName)
			if !slices.Equal(state.Capabilities.RequiredEnv, requiredEnv) {
				t.Fatalf("required_env = %v, want %v", state.Capabilities.RequiredEnv, requiredEnv)
			}
		})
	}
}

func fixtureDefinitionRequest(t *testing.T) map[string]any {
	t.Helper()

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "hooks"), 0o755); err != nil {
		t.Fatalf("mkdir hooks: %v", err)
	}
	writeHook := func(name, body string) {
		t.Helper()
		path := filepath.Join(root, "hooks", name)
		if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	writeHook("install-check.sh", "#!/usr/bin/env bash\nset -euo pipefail\nprintf '{\"installed\":false}\\n'\n")
	writeHook("install-run.sh", "#!/usr/bin/env bash\nset -euo pipefail\nprintf '{\"installed\":true,\"version\":\"fixture\"}\\n'\n")
	writeHook("run-prepare.sh", "#!/usr/bin/env bash\nset -euo pipefail\ncat <<'JSON'\n{\"path\":\"bash\",\"args\":[\"-lc\",\"printf 'fixture-run\\n'\"],\"env\":{\"TERM\":\"xterm-256color\"},\"dir\":\"/marginlab/workspaces\"}\nJSON\n")

	pkg, err := testassets.PackDir(root)
	if err != nil {
		t.Fatalf("pack fixture definition: %v", err)
	}
	return map[string]any{
		"definition": toJSONMap(t, agentdef.DefinitionSnapshot{
			Manifest: agentdef.Manifest{
				Kind: "agent_definition",
				Name: "fixture-agent",
				Install: agentdef.InstallSpec{
					CheckHook: &agentdef.HookRef{Path: "hooks/install-check.sh"},
					RunHook:   &agentdef.HookRef{Path: "hooks/install-run.sh"},
				},
				Run: agentdef.RunSpec{
					PrepareHook: agentdef.HookRef{Path: "hooks/run-prepare.sh"},
				},
			},
			Package: pkg,
		}),
	}
}

func fixtureManagedNodeDefinitionRequest(t *testing.T) map[string]any {
	t.Helper()

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "hooks"), 0o755); err != nil {
		t.Fatalf("mkdir hooks: %v", err)
	}
	writeHook := func(name, body string) {
		t.Helper()
		path := filepath.Join(root, "hooks", name)
		if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	writeHook("install-check.sh", "#!/usr/bin/env bash\nset -euo pipefail\nprintf '{\"installed\":false}\\n'\n")
	writeHook("install-run.sh", "#!/usr/bin/env bash\nset -euo pipefail\nprintf '{\"installed\":true,\"version\":\"fixture-managed-node\"}\\n'\n")
	writeHook("validate.js", `#!/usr/bin/env node
"use strict";

const fs = require("fs");

const ctx = JSON.parse(fs.readFileSync(process.env.AGENT_CONTEXT_JSON, "utf8"));
process.stdout.write(JSON.stringify((ctx.config || {}).input || {}) + "\n");
`)
	writeHook("run-prepare.js", `#!/usr/bin/env node
"use strict";

const fs = require("fs");

const ctx = JSON.parse(fs.readFileSync(process.env.AGENT_CONTEXT_JSON, "utf8"));
const run = ctx.run || {};
process.stdout.write(JSON.stringify({
  path: "bash",
  args: ["-lc", "printf 'fixture-managed-node\\n'"],
  env: run.env || {},
  dir: run.cwd || "/marginlab/workspaces",
}) + "\n");
`)

	pkg, err := testassets.PackDir(root)
	if err != nil {
		t.Fatalf("pack fixture definition: %v", err)
	}
	return map[string]any{
		"definition": toJSONMap(t, agentdef.DefinitionSnapshot{
			Manifest: agentdef.Manifest{
				Kind: "agent_definition",
				Name: "fixture-managed-node-agent",
				Toolchains: agentdef.ToolchainSpec{
					Node: &agentdef.NodeToolchainSpec{
						Minimum:   "20",
						Preferred: "24",
					},
				},
				Config: agentdef.DefinitionConfigSpec{
					ValidateHook: &agentdef.HookRef{Path: "hooks/validate.js"},
				},
				Install: agentdef.InstallSpec{
					CheckHook: &agentdef.HookRef{Path: "hooks/install-check.sh"},
					RunHook:   &agentdef.HookRef{Path: "hooks/install-run.sh"},
				},
				Run: agentdef.RunSpec{
					PrepareHook: agentdef.HookRef{Path: "hooks/run-prepare.js"},
				},
			},
			Package: pkg,
		}),
	}
}

func fixtureConfigRequest() map[string]any {
	return map[string]any{
		"config": map[string]any{
			"name":  "fixture-default",
			"mode":  "direct",
			"input": map[string]any{"mode": "fixture"},
		},
	}
}

func fixtureManagedNodeConfigRequest() map[string]any {
	return map[string]any{
		"config": map[string]any{
			"name":  "fixture-managed-node-default",
			"mode":  "direct",
			"input": map[string]any{"mode": "fixture-managed-node"},
		},
	}
}

func repoOwnedDefinitionRequest(t *testing.T, agentName string) map[string]any {
	t.Helper()
	agent := testfixture.RepoOwnedAgent(agentName)
	return map[string]any{"definition": toJSONMap(t, agent.Definition)}
}

func repoOwnedConfigRequest(t *testing.T, agentName, configName, version string) map[string]any {
	t.Helper()
	agent := testfixture.RepoOwnedAgentWithConfigVersion(agentName, configName, version)
	return map[string]any{"config": toJSONMap(t, agent.Config)}
}

func repoOwnedInstructionConfigRequest(t *testing.T, agentName, configName, version string) map[string]any {
	t.Helper()
	agent, _, err := testfixture.RepoOwnedAgentWithInstructionFixtures(agentName, configName, version)
	if err != nil {
		t.Fatalf("build repo-owned config with instruction fixtures: %v", err)
	}
	return map[string]any{"config": toJSONMap(t, agent.Config)}
}

func toJSONMap(t *testing.T, value any) map[string]any {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal JSON map: %v", err)
	}
	out := map[string]any{}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("unmarshal JSON map: %v", err)
	}
	return out
}
