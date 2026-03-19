package state

import (
	"time"

	"github.com/marginlab/margin-eval/runner/runner-core/agentdef"
)

type AgentState string

const (
	AgentStateEmpty            AgentState = "empty"
	AgentStateDefinitionLoaded AgentState = "definition_loaded"
	AgentStateInstalled        AgentState = "installed"
	AgentStateConfigured       AgentState = "configured"
)

type RunState string

const (
	RunStateIdle       RunState = "idle"
	RunStateStarting   RunState = "starting"
	RunStateRunning    RunState = "running"
	RunStateCollecting RunState = "collecting"
	RunStateExited     RunState = "exited"
)

func IsRunActive(runState RunState) bool {
	return runState == RunStateStarting || runState == RunStateRunning || runState == RunStateCollecting
}

type TrajectoryStatus string

const (
	TrajectoryStatusPending    TrajectoryStatus = "pending"
	TrajectoryStatusCollecting TrajectoryStatus = "collecting"
	TrajectoryStatusComplete   TrajectoryStatus = "complete"
	TrajectoryStatusFailed     TrajectoryStatus = "failed"
	TrajectoryStatusNone       TrajectoryStatus = "none"
)

type ServerState struct {
	Agent AgentRecord `json:"agent"`
	Run   RunRecord   `json:"run"`
}

type AgentRecord struct {
	State      AgentState        `json:"state"`
	Definition *DefinitionRecord `json:"definition,omitempty"`
	Config     *ConfigRecord     `json:"config,omitempty"`
	Install    *InstallInfo      `json:"install,omitempty"`
}

type DefinitionRecord struct {
	Snapshot      agentdef.DefinitionSnapshot `json:"snapshot"`
	PackageHash   string                      `json:"package_hash"`
	DefinitionDir string                      `json:"definition_dir"`
	InstallDir    string                      `json:"install_dir"`
}

type ConfigRecord struct {
	Snapshot agentdef.ConfigSnapshot `json:"snapshot"`
}

type InstallInfo struct {
	InstalledAt time.Time      `json:"installed_at"`
	Result      map[string]any `json:"result,omitempty"`
}

type RunAuthFileRecord struct {
	RequiredEnv    string `json:"required_env"`
	SourcePath     string `json:"source_path"`
	RunHomeRelPath string `json:"run_home_rel_path"`
}

type RunRecord struct {
	State            RunState            `json:"state"`
	RunID            string              `json:"run_id,omitempty"`
	PID              *int                `json:"pid,omitempty"`
	StartedAt        *time.Time          `json:"started_at,omitempty"`
	EndedAt          *time.Time          `json:"ended_at,omitempty"`
	CWD              string              `json:"cwd,omitempty"`
	Env              map[string]string   `json:"env,omitempty"`
	AuthFiles        []RunAuthFileRecord `json:"auth_files,omitempty"`
	ExitCode         *int                `json:"exit_code,omitempty"`
	Signal           *string             `json:"signal,omitempty"`
	TrajectoryStatus TrajectoryStatus    `json:"trajectory_status,omitempty"`
}

func DefaultServerState() ServerState {
	return ServerState{
		Agent: AgentRecord{State: AgentStateEmpty},
		Run:   RunRecord{State: RunStateIdle},
	}
}

func (r RunRecord) HasRun() bool {
	return r.RunID != ""
}
