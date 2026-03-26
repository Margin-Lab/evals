package compiler

import (
	"time"

	"github.com/marginlab/margin-eval/runner/runner-core/agentdef"
)

type CompileInput struct {
	SuitePath       string
	AgentConfigPath string
	EvalPath        string

	SubmitProjectID string
	BundleID        string
	CreatedAt       time.Time

	RunCwd  string
	RunEnv  map[string]string
	PTYCols int
	PTYRows int

	Progress CompileProgressFunc
}

type CompileProgressStage string

const (
	CompileStageCasesDiscovered CompileProgressStage = "cases_discovered"
	CompileStageCaseStart       CompileProgressStage = "case_start"
	CompileStageCaseDone        CompileProgressStage = "case_done"
	CompileStageComplete        CompileProgressStage = "complete"
)

type CompileProgress struct {
	Stage          CompileProgressStage
	CaseID         string
	CurrentCase    int
	CompletedCases int
	TotalCases     int
	Message        string
}

type CompileProgressFunc func(CompileProgress)

type suiteFile struct {
	Kind        string   `toml:"kind"`
	Name        string   `toml:"name"`
	Description string   `toml:"description"`
	Cases       []string `toml:"cases"`
}

type caseFile struct {
	Kind               string `toml:"kind"`
	Name               string `toml:"name"`
	Description        string `toml:"description"`
	Image              string `toml:"image"`
	TestCwd            string `toml:"test_cwd"`
	TestTimeoutSeconds int    `toml:"test_timeout_seconds"`
}

type agentDefinitionFile struct {
	Kind        string                    `toml:"kind"`
	Name        string                    `toml:"name"`
	Description string                    `toml:"description"`
	Auth        definitionAuthFile        `toml:"auth"`
	Toolchains  definitionToolchainFile   `toml:"toolchains"`
	Config      definitionConfigFile      `toml:"config"`
	Skills      *definitionSkillsFile     `toml:"skills"`
	AgentsMD    *definitionAgentsMDFile   `toml:"agents_md"`
	Install     definitionInstallFile     `toml:"install"`
	Run         definitionRunFile         `toml:"run"`
	Snapshot    *definitionSnapshotFile   `toml:"snapshot"`
	Trajectory  *definitionTrajectoryFile `toml:"trajectory"`
}

type definitionAuthFile struct {
	RequiredEnv      []string                        `toml:"required_env"`
	LocalCredentials []definitionAuthLocalCredential `toml:"local_credentials"`
}

type definitionAuthLocalCredential struct {
	RequiredEnv      string                      `toml:"required_env"`
	RunHomeRelPath   string                      `toml:"run_home_rel_path"`
	ValidateJSONPath string                      `toml:"validate_json_path"`
	Sources          []definitionAuthLocalSource `toml:"sources"`
}

type definitionAuthLocalSource struct {
	Kind        string   `toml:"kind"`
	HomeRelPath string   `toml:"home_rel_path"`
	Service     string   `toml:"service"`
	Platforms   []string `toml:"platforms"`
}

type definitionNodeToolchainFile struct {
	Minimum   string `toml:"minimum"`
	Preferred string `toml:"preferred"`
}

type definitionToolchainFile struct {
	Node *definitionNodeToolchainFile `toml:"node"`
}

type definitionConfigFile struct {
	Schema   string                 `toml:"schema"`
	Validate string                 `toml:"validate"`
	Unified  *definitionUnifiedFile `toml:"unified"`
}

type definitionUnifiedFile struct {
	Translate              string   `toml:"translate"`
	AllowedModels          []string `toml:"allowed_models"`
	AllowedReasoningLevels []string `toml:"allowed_reasoning_levels"`
}

type definitionSkillsFile struct {
	HomeRelDir string `toml:"home_rel_dir"`
}

type definitionAgentsMDFile struct {
	Filename string `toml:"filename"`
}

type definitionInstallFile struct {
	Check string `toml:"check"`
	Run   string `toml:"run"`
}

type definitionRunFile struct {
	Prepare string `toml:"prepare"`
}

type definitionSnapshotFile struct {
	Prepare string `toml:"prepare"`
}

type definitionTrajectoryFile struct {
	Collect string `toml:"collect"`
}

type agentConfigFile struct {
	Kind        string                   `toml:"kind"`
	Name        string                   `toml:"name"`
	Description string                   `toml:"description"`
	Definition  string                   `toml:"definition"`
	Mode        string                   `toml:"mode"`
	Skills      []agentConfigSkillFile   `toml:"skills"`
	AgentsMD    *agentConfigAgentsMDFile `toml:"agents_md"`
	Input       map[string]any           `toml:"input"`
	Unified     *agentdef.UnifiedSpec    `toml:"unified"`
}

type agentConfigSkillFile struct {
	Path string `toml:"path"`
}

type agentConfigAgentsMDFile struct {
	Path string `toml:"path"`
}

type evalFile struct {
	Kind                  string `toml:"kind"`
	Name                  string `toml:"name"`
	Description           string `toml:"description"`
	MaxConcurrency        int    `toml:"max_concurrency"`
	FailFast              bool   `toml:"fail_fast"`
	RetryCount            *int   `toml:"retry_count"`
	InstanceTimeoutSecond int    `toml:"instance_timeout_seconds"`
}
