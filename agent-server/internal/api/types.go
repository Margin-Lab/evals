package api

import (
	"github.com/marginlab/margin-eval/agent-server/internal/run"
	"github.com/marginlab/margin-eval/agent-server/internal/state"
	"github.com/marginlab/margin-eval/runner/runner-core/agentdef"
)

type healthResponse struct {
	Status string `json:"status"`
}

type readyResponse struct {
	Status     string                `json:"status"`
	Summary    string                `json:"summary,omitempty"`
	ReasonCode string                `json:"reason_code,omitempty"`
	Checks     map[string]readyCheck `json:"checks"`
}

type readyCheck struct {
	Status     string         `json:"status"`
	ReasonCode string         `json:"reason_code,omitempty"`
	Message    string         `json:"message,omitempty"`
	Details    map[string]any `json:"details,omitempty"`
}

type stateRunSummary struct {
	State state.RunState `json:"state"`
	RunID *string        `json:"run_id"`
}

type statePaths struct {
	Root       string `json:"root"`
	Bin        string `json:"bin"`
	State      string `json:"state"`
	Workspaces string `json:"workspaces"`
}

type stateCapabilities struct {
	SupportsInstall        bool     `json:"supports_install"`
	SupportsSnapshot       bool     `json:"supports_snapshot"`
	SupportsTrajectory     bool     `json:"supports_trajectory"`
	SupportsSkills         bool     `json:"supports_skills"`
	SupportsAgentsMD       bool     `json:"supports_agents_md"`
	SupportsUnifiedConfig  bool     `json:"supports_unified_config"`
	RequiredEnv            []string `json:"required_env"`
	AgentsMDFilename       string   `json:"agents_md_filename,omitempty"`
	AllowedModels          []string `json:"allowed_models,omitempty"`
	AllowedReasoningLevels []string `json:"allowed_reasoning_levels,omitempty"`
}

type getStateResponse struct {
	Agent        state.AgentRecord `json:"agent"`
	Run          stateRunSummary   `json:"run"`
	Paths        statePaths        `json:"paths"`
	Capabilities stateCapabilities `json:"capabilities"`
	ShuttingDown bool              `json:"shutting_down"`
}

type putAgentDefinitionRequest struct {
	Definition agentdef.DefinitionSnapshot `json:"definition"`
}

type putAgentDefinitionResponse struct {
	State      state.AgentState        `json:"state"`
	Definition *state.DefinitionRecord `json:"definition,omitempty"`
}

type putAgentConfigRequest struct {
	Config agentdef.ConfigSpec `json:"config"`
}

type putAgentConfigResponse struct {
	State  state.AgentState        `json:"state"`
	Config agentdef.ConfigSnapshot `json:"config"`
}

type postAgentInstallResponse struct {
	State   state.AgentState   `json:"state"`
	Install *state.InstallInfo `json:"install,omitempty"`
}

type deleteRunResponse struct {
	State state.RunState `json:"state"`
}

type postRunSnapshotRequest struct {
	RunID string      `json:"run_id"`
	PTY   run.PTYSize `json:"pty"`
}

type postRunSnapshotResponse struct {
	RunID           string         `json:"run_id"`
	Agent           string         `json:"agent"`
	RunState        state.RunState `json:"run_state"`
	CapturedAt      string         `json:"captured_at"`
	ContentType     string         `json:"content_type"`
	ContentEncoding string         `json:"content_encoding"`
	Content         string         `json:"content"`
	Truncated       bool           `json:"truncated"`
}
