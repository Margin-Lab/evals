package agentdef

import "github.com/marginlab/margin-eval/runner/runner-core/testassets"

type HookRef struct {
	Path string `json:"path" toml:"path"`
}

type AuthLocalFile struct {
	RequiredEnv    string `json:"required_env" toml:"required_env"`
	HomeRelPath    string `json:"home_rel_path" toml:"home_rel_path"`
	RunHomeRelPath string `json:"run_home_rel_path" toml:"run_home_rel_path"`
}

type AuthSpec struct {
	RequiredEnv []string        `json:"required_env,omitempty" toml:"required_env"`
	LocalFiles  []AuthLocalFile `json:"local_files,omitempty" toml:"local_files"`
}

type UnifiedManifestSpec struct {
	TranslateHook          HookRef  `json:"translate_hook" toml:"translate"`
	AllowedModels          []string `json:"allowed_models,omitempty" toml:"allowed_models"`
	AllowedReasoningLevels []string `json:"allowed_reasoning_levels,omitempty" toml:"allowed_reasoning_levels"`
}

type DefinitionConfigSpec struct {
	SchemaRelPath string               `json:"schema_rel_path,omitempty" toml:"schema"`
	ValidateHook  *HookRef             `json:"validate_hook,omitempty" toml:"-"`
	Unified       *UnifiedManifestSpec `json:"unified,omitempty" toml:"unified"`
}

type InstallSpec struct {
	CheckHook *HookRef `json:"check_hook,omitempty" toml:"-"`
	RunHook   *HookRef `json:"run_hook,omitempty" toml:"-"`
}

type RunSpec struct {
	PrepareHook HookRef `json:"prepare_hook" toml:"-"`
}

type SnapshotSpec struct {
	PrepareHook HookRef `json:"prepare_hook" toml:"-"`
}

type TrajectorySpec struct {
	CollectHook HookRef `json:"collect_hook" toml:"-"`
}

type NodeToolchainSpec struct {
	Minimum   string `json:"minimum,omitempty" toml:"minimum"`
	Preferred string `json:"preferred,omitempty" toml:"preferred"`
}

type ToolchainSpec struct {
	Node *NodeToolchainSpec `json:"node,omitempty" toml:"node"`
}

type SkillManifestSpec struct {
	HomeRelDir string `json:"home_rel_dir" toml:"home_rel_dir"`
}

type AgentsMDManifestSpec struct {
	Filename string `json:"filename" toml:"filename"`
}

type Manifest struct {
	Kind        string                `json:"kind" toml:"kind"`
	Name        string                `json:"name" toml:"name"`
	Description string                `json:"description,omitempty" toml:"description"`
	Auth        AuthSpec              `json:"auth,omitempty" toml:"auth"`
	Toolchains  ToolchainSpec         `json:"toolchains,omitempty" toml:"toolchains"`
	Config      DefinitionConfigSpec  `json:"config,omitempty" toml:"config"`
	Skills      *SkillManifestSpec    `json:"skills,omitempty" toml:"skills"`
	AgentsMD    *AgentsMDManifestSpec `json:"agents_md,omitempty" toml:"agents_md"`
	Install     InstallSpec           `json:"install,omitempty" toml:"install"`
	Run         RunSpec               `json:"run" toml:"run"`
	Snapshot    *SnapshotSpec         `json:"snapshot,omitempty" toml:"snapshot"`
	Trajectory  *TrajectorySpec       `json:"trajectory,omitempty" toml:"trajectory"`
}

type DefinitionSnapshot struct {
	Manifest Manifest              `json:"manifest"`
	Package  testassets.Descriptor `json:"package"`
}

type ConfigMode string

const (
	ConfigModeDirect  ConfigMode = "direct"
	ConfigModeUnified ConfigMode = "unified"
)

type UnifiedSpec struct {
	Model          string     `json:"model" toml:"model"`
	ReasoningLevel string     `json:"reasoning_level" toml:"reasoning_level"`
	MCP            *MCPConfig `json:"mcp,omitempty" toml:"mcp"`
}

type MCPConfig struct {
	Servers []MCPServer `json:"servers" toml:"servers"`
}

type MCPTransport string

const (
	MCPTransportSTDIO MCPTransport = "stdio"
	MCPTransportSSE   MCPTransport = "sse"
	MCPTransportHTTP  MCPTransport = "http"
)

type MCPServer struct {
	Name      string       `json:"name" toml:"name"`
	Transport MCPTransport `json:"transport" toml:"transport"`
	Enabled   *bool        `json:"enabled,omitempty" toml:"enabled"`
	TimeoutMS *int         `json:"timeout_ms,omitempty" toml:"timeout_ms"`

	Command []string          `json:"command,omitempty" toml:"command"`
	Env     map[string]string `json:"env,omitempty" toml:"env"`

	URL     string            `json:"url,omitempty" toml:"url"`
	Headers map[string]string `json:"headers,omitempty" toml:"headers"`
	OAuth   *MCPOAuth         `json:"oauth,omitempty" toml:"oauth"`
}

type MCPOAuth struct {
	Disabled     bool   `json:"disabled,omitempty" toml:"disabled"`
	ClientID     string `json:"client_id,omitempty" toml:"client_id"`
	ClientSecret string `json:"client_secret,omitempty" toml:"client_secret"`
	Scope        string `json:"scope,omitempty" toml:"scope"`
}

type SkillSpec struct {
	Name        string                `json:"name" toml:"name"`
	Description string                `json:"description" toml:"description"`
	Package     testassets.Descriptor `json:"package" toml:"package"`
}

type AgentsMDSpec struct {
	Content string `json:"content" toml:"content"`
}

type ConfigSpec struct {
	Name        string         `json:"name" toml:"name"`
	Description string         `json:"description,omitempty" toml:"description"`
	Mode        ConfigMode     `json:"mode" toml:"mode"`
	Skills      []SkillSpec    `json:"skills,omitempty" toml:"skills"`
	AgentsMD    *AgentsMDSpec  `json:"agents_md,omitempty" toml:"agents_md"`
	Input       map[string]any `json:"input,omitempty" toml:"input"`
	Unified     *UnifiedSpec   `json:"unified,omitempty" toml:"unified"`
}

type ConfigSnapshot struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Mode        ConfigMode     `json:"mode"`
	Skills      []SkillSpec    `json:"skills,omitempty"`
	AgentsMD    *AgentsMDSpec  `json:"agents_md,omitempty"`
	Input       map[string]any `json:"input"`
	Unified     *UnifiedSpec   `json:"unified,omitempty"`
}

type DefinitionRef struct {
	ResourceID string `json:"resource_id"`
	Version    int    `json:"version"`
}

type ConfigResourceSpec struct {
	Name          string         `json:"name"`
	Description   string         `json:"description,omitempty"`
	DefinitionRef DefinitionRef  `json:"definition_ref"`
	Mode          ConfigMode     `json:"mode"`
	Skills        []SkillSpec    `json:"skills,omitempty"`
	AgentsMD      *AgentsMDSpec  `json:"agents_md,omitempty"`
	Input         map[string]any `json:"input,omitempty"`
	Unified       *UnifiedSpec   `json:"unified,omitempty"`
}
