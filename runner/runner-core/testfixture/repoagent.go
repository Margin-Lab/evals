package testfixture

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"

	toml "github.com/pelletier/go-toml/v2"

	"github.com/marginlab/margin-eval/runner/runner-core/agentdef"
	"github.com/marginlab/margin-eval/runner/runner-core/runbundle"
	"github.com/marginlab/margin-eval/runner/runner-core/testassets"
)

type repoOwnedAgentProfile struct {
	DefinitionName string
	DefaultConfig  string
	UnifiedConfig  string
	VersionInput   string
}

type definitionFile struct {
	Kind        string               `toml:"kind"`
	Name        string               `toml:"name"`
	Description string               `toml:"description"`
	Auth        definitionAuthFile   `toml:"auth"`
	Toolchains  definitionToolchains `toml:"toolchains"`
	Config      definitionConfigFile `toml:"config"`
	Skills      *definitionSkills    `toml:"skills"`
	AgentsMD    *definitionAgentsMD  `toml:"agents_md"`
	Install     definitionInstall    `toml:"install"`
	Run         definitionRunFile    `toml:"run"`
	Snapshot    *definitionRunFile   `toml:"snapshot"`
	Trajectory  *definitionMetaFile  `toml:"trajectory"`
}

type definitionAuthFile struct {
	RequiredEnv       []string                         `toml:"required_env"`
	LocalCredentials  []definitionAuthLocalCredential  `toml:"local_credentials"`
	ProviderSelection *definitionAuthProviderSelection `toml:"provider_selection"`
	Providers         []definitionAuthProvider         `toml:"providers"`
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

type definitionAuthProviderSelection struct {
	DirectInputField              string `toml:"direct_input_field"`
	UnifiedModelProviderQualified bool   `toml:"unified_model_provider_qualified"`
}

type definitionAuthProvider struct {
	Name        string   `toml:"name"`
	AuthMode    string   `toml:"auth_mode"`
	RequiredEnv []string `toml:"required_env"`
}

type definitionNodeToolchains struct {
	Minimum   string `toml:"minimum"`
	Preferred string `toml:"preferred"`
}

type definitionToolchains struct {
	Node *definitionNodeToolchains `toml:"node"`
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

type definitionSkills struct {
	HomeRelDir string `toml:"home_rel_dir"`
}

type definitionAgentsMD struct {
	Filename string `toml:"filename"`
}

type definitionInstall struct {
	Check string `toml:"check"`
	Run   string `toml:"run"`
}

type definitionRunFile struct {
	Prepare string `toml:"prepare"`
}

type definitionMetaFile struct {
	Collect string `toml:"collect"`
}

type configFile struct {
	Kind        string                `toml:"kind"`
	Name        string                `toml:"name"`
	Description string                `toml:"description"`
	Definition  string                `toml:"definition"`
	Mode        string                `toml:"mode"`
	Skills      []configSkillFile     `toml:"skills"`
	AgentsMD    *configAgentsMDFile   `toml:"agents_md"`
	Input       map[string]any        `toml:"input"`
	Unified     *agentdef.UnifiedSpec `toml:"unified"`
}

type configSkillFile struct {
	Path string `toml:"path"`
}

type configAgentsMDFile struct {
	Path string `toml:"path"`
}

var (
	repoOwnedProfiles = map[string]repoOwnedAgentProfile{
		"codex": {
			DefinitionName: "codex",
			DefaultConfig:  "codex-default",
			UnifiedConfig:  "codex-unified",
			VersionInput:   "codex_version",
		},
		"claude-code": {
			DefinitionName: "claude-code",
			DefaultConfig:  "claude-code-default",
			UnifiedConfig:  "claude-code-unified",
			VersionInput:   "claude_version",
		},
		"gemini-cli": {
			DefinitionName: "gemini-cli",
			DefaultConfig:  "gemini-cli-default",
			UnifiedConfig:  "gemini-cli-unified",
			VersionInput:   "gemini_version",
		},
		"opencode": {
			DefinitionName: "opencode",
			DefaultConfig:  "opencode-default",
			UnifiedConfig:  "opencode-unified",
			VersionInput:   "opencode_version",
		},
		"pi": {
			DefinitionName: "pi",
			DefaultConfig:  "pi-default",
			UnifiedConfig:  "pi-unified",
			VersionInput:   "pi_version",
		},
	}

	repoOwnedLoadMu sync.Mutex
	repoOwnedCache  = map[string]runbundle.Agent{}
)

func RepoOwnedAgentNames() []string {
	names := make([]string, 0, len(repoOwnedProfiles))
	for name := range repoOwnedProfiles {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

func RepoOwnedDefaultConfigName(name string) string {
	profile, ok := repoOwnedProfiles[strings.TrimSpace(name)]
	if !ok {
		panic(fmt.Errorf("unknown repo-owned agent %q", name))
	}
	return profile.DefaultConfig
}

func RepoOwnedUnifiedConfigName(name string) string {
	profile, ok := repoOwnedProfiles[strings.TrimSpace(name)]
	if !ok {
		panic(fmt.Errorf("unknown repo-owned agent %q", name))
	}
	return profile.UnifiedConfig
}

func RepoOwnedDefinitionNameForConfig(configName string) string {
	trimmed := strings.TrimSpace(configName)
	for _, profile := range repoOwnedProfiles {
		if profile.DefaultConfig == trimmed || profile.UnifiedConfig == trimmed {
			return profile.DefinitionName
		}
	}
	panic(fmt.Errorf("unknown repo-owned config %q", configName))
}

func RepoOwnedAgent(name string) runbundle.Agent {
	agent, err := loadRepoOwnedAgent(name, RepoOwnedDefaultConfigName(name))
	if err != nil {
		panic(err)
	}
	return agent
}

func RepoOwnedUnifiedAgent(name string) runbundle.Agent {
	agent, err := loadRepoOwnedAgent(name, RepoOwnedUnifiedConfigName(name))
	if err != nil {
		panic(err)
	}
	return agent
}

func RepoOwnedAgentWithVersion(name, version string) runbundle.Agent {
	return RepoOwnedAgentWithConfigVersion(name, RepoOwnedDefaultConfigName(name), version)
}

func RepoOwnedAgentWithConfigVersion(name, configName, version string) runbundle.Agent {
	agent, err := loadRepoOwnedAgent(name, configName)
	if err != nil {
		panic(err)
	}
	profile, ok := repoOwnedProfiles[strings.TrimSpace(name)]
	if !ok {
		panic(fmt.Errorf("unknown repo-owned agent %q", name))
	}
	trimmedVersion := strings.TrimSpace(version)
	if trimmedVersion == "" || agent.Config.Mode != agentdef.ConfigModeDirect || agent.Config.Input == nil {
		return agent
	}
	agent.Config.Input[profile.VersionInput] = trimmedVersion
	return agent
}

func RepoOwnedRequiredEnv(name string) []string {
	return RepoOwnedRequiredEnvForConfig(name, RepoOwnedDefaultConfigName(name))
}

func RepoOwnedRequiredEnvForConfig(name, configName string) []string {
	agent, err := loadRepoOwnedAgent(name, configName)
	if err != nil {
		panic(err)
	}
	required, err := agentdef.ResolveRequiredEnvForConfigSpec(agent.Definition, agent.Config)
	if err != nil {
		panic(err)
	}
	return required
}

func RepoOwnedDefinitionRequiredEnv(name string) []string {
	agent, err := loadRepoOwnedAgent(name, RepoOwnedDefaultConfigName(name))
	if err != nil {
		panic(err)
	}
	return agentdef.ResolveDefinitionRequiredEnv(agent.Definition)
}

func loadRepoOwnedAgent(name, configName string) (runbundle.Agent, error) {
	trimmedName := strings.TrimSpace(name)
	profile, ok := repoOwnedProfiles[trimmedName]
	if !ok {
		return runbundle.Agent{}, fmt.Errorf("unknown repo-owned agent %q", name)
	}
	resolvedConfigName := strings.TrimSpace(configName)
	if resolvedConfigName == "" {
		resolvedConfigName = profile.DefaultConfig
	}

	repoOwnedLoadMu.Lock()
	defer repoOwnedLoadMu.Unlock()

	cacheKey := trimmedName + "::" + resolvedConfigName
	if cached, ok := repoOwnedCache[cacheKey]; ok {
		return cloneAgent(cached), nil
	}

	root := repoRoot()
	definitionDir := filepath.Join(root, "configs", "agent-definitions", profile.DefinitionName)
	configDir := filepath.Join(root, "configs", "agent-configs", resolvedConfigName)
	if _, err := os.Stat(configDir); err != nil {
		if !os.IsNotExist(err) {
			return runbundle.Agent{}, fmt.Errorf("stat repo-owned config dir: %w", err)
		}
		configDir = filepath.Join(root, "configs", "example-agent-configs", resolvedConfigName)
	}

	definition, err := loadDefinitionSnapshot(definitionDir)
	if err != nil {
		return runbundle.Agent{}, err
	}
	config, err := loadConfigSnapshot(configDir)
	if err != nil {
		return runbundle.Agent{}, err
	}
	normalizedConfig, err := agentdef.ValidateAndNormalizeConfigSpec(definition, config)
	if err != nil {
		return runbundle.Agent{}, fmt.Errorf("validate repo-owned agent %q: %w", trimmedName, err)
	}

	agent := runbundle.Agent{
		Definition: definition,
		Config:     normalizedConfig,
	}
	repoOwnedCache[cacheKey] = agent
	return cloneAgent(agent), nil
}

func loadDefinitionSnapshot(dir string) (agentdef.DefinitionSnapshot, error) {
	body, err := os.ReadFile(filepath.Join(dir, "definition.toml"))
	if err != nil {
		return agentdef.DefinitionSnapshot{}, fmt.Errorf("read definition manifest: %w", err)
	}
	var file definitionFile
	if err := toml.Unmarshal(body, &file); err != nil {
		return agentdef.DefinitionSnapshot{}, fmt.Errorf("decode definition manifest: %w", err)
	}
	pkg, err := testassets.PackDir(dir)
	if err != nil {
		return agentdef.DefinitionSnapshot{}, fmt.Errorf("pack definition dir: %w", err)
	}
	snapshot := agentdef.DefinitionSnapshot{
		Manifest: agentdef.Manifest{
			Kind:        strings.TrimSpace(file.Kind),
			Name:        strings.TrimSpace(file.Name),
			Description: strings.TrimSpace(file.Description),
			Auth: agentdef.AuthSpec{
				RequiredEnv:      append([]string(nil), file.Auth.RequiredEnv...),
				LocalCredentials: cloneAuthLocalCredentials(file.Auth.LocalCredentials),
				Providers:        cloneAuthProviders(file.Auth.Providers),
			},
			Toolchains: loadDefinitionToolchains(file.Toolchains),
			Config: agentdef.DefinitionConfigSpec{
				SchemaRelPath: strings.TrimSpace(file.Config.Schema),
			},
			Install: agentdef.InstallSpec{},
			Run: agentdef.RunSpec{
				PrepareHook: agentdef.HookRef{Path: strings.TrimSpace(file.Run.Prepare)},
			},
		},
		Package: pkg,
	}
	if file.Auth.ProviderSelection != nil {
		snapshot.Manifest.Auth.ProviderSelection = &agentdef.AuthProviderSelection{
			DirectInputField:              strings.TrimSpace(file.Auth.ProviderSelection.DirectInputField),
			UnifiedModelProviderQualified: file.Auth.ProviderSelection.UnifiedModelProviderQualified,
		}
	}
	if path := strings.TrimSpace(file.Config.Validate); path != "" {
		snapshot.Manifest.Config.ValidateHook = &agentdef.HookRef{Path: path}
	}
	if file.Skills != nil {
		snapshot.Manifest.Skills = &agentdef.SkillManifestSpec{HomeRelDir: strings.TrimSpace(file.Skills.HomeRelDir)}
	}
	if file.AgentsMD != nil {
		snapshot.Manifest.AgentsMD = &agentdef.AgentsMDManifestSpec{Filename: strings.TrimSpace(file.AgentsMD.Filename)}
	}
	if file.Config.Unified != nil {
		snapshot.Manifest.Config.Unified = &agentdef.UnifiedManifestSpec{
			TranslateHook:          agentdef.HookRef{Path: strings.TrimSpace(file.Config.Unified.Translate)},
			AllowedModels:          append([]string(nil), file.Config.Unified.AllowedModels...),
			AllowedReasoningLevels: append([]string(nil), file.Config.Unified.AllowedReasoningLevels...),
		}
	}
	if path := strings.TrimSpace(file.Install.Check); path != "" {
		snapshot.Manifest.Install.CheckHook = &agentdef.HookRef{Path: path}
	}
	if path := strings.TrimSpace(file.Install.Run); path != "" {
		snapshot.Manifest.Install.RunHook = &agentdef.HookRef{Path: path}
	}
	if file.Snapshot != nil && strings.TrimSpace(file.Snapshot.Prepare) != "" {
		snapshot.Manifest.Snapshot = &agentdef.SnapshotSpec{
			PrepareHook: agentdef.HookRef{Path: strings.TrimSpace(file.Snapshot.Prepare)},
		}
	}
	if file.Trajectory != nil && strings.TrimSpace(file.Trajectory.Collect) != "" {
		snapshot.Manifest.Trajectory = &agentdef.TrajectorySpec{
			CollectHook: agentdef.HookRef{Path: strings.TrimSpace(file.Trajectory.Collect)},
		}
	}
	return snapshot, nil
}

func loadDefinitionToolchains(file definitionToolchains) agentdef.ToolchainSpec {
	var out agentdef.ToolchainSpec
	if file.Node != nil {
		out.Node = &agentdef.NodeToolchainSpec{
			Minimum:   strings.TrimSpace(file.Node.Minimum),
			Preferred: strings.TrimSpace(file.Node.Preferred),
		}
	}
	return out
}

func loadConfigSnapshot(dir string) (agentdef.ConfigSpec, error) {
	body, err := os.ReadFile(filepath.Join(dir, "config.toml"))
	if err != nil {
		return agentdef.ConfigSpec{}, fmt.Errorf("read config manifest: %w", err)
	}
	var file configFile
	if err := toml.Unmarshal(body, &file); err != nil {
		return agentdef.ConfigSpec{}, fmt.Errorf("decode config manifest: %w", err)
	}
	spec := agentdef.ConfigSpec{
		Name:        strings.TrimSpace(file.Name),
		Description: strings.TrimSpace(file.Description),
		Mode:        agentdef.ConfigMode(strings.TrimSpace(file.Mode)),
		Input:       cloneAnyMap(file.Input),
	}
	spec.Skills, err = loadLocalSkills(dir, file.Skills)
	if err != nil {
		return agentdef.ConfigSpec{}, fmt.Errorf("load config skills: %w", err)
	}
	spec.AgentsMD, err = loadLocalAgentsMD(dir, file.AgentsMD)
	if err != nil {
		return agentdef.ConfigSpec{}, fmt.Errorf("load config agents_md: %w", err)
	}
	if file.Unified != nil {
		spec.Unified = cloneUnified(file.Unified)
	}
	return spec, nil
}

func cloneAuthLocalCredentials(files []definitionAuthLocalCredential) []agentdef.AuthLocalCredential {
	if len(files) == 0 {
		return nil
	}
	out := make([]agentdef.AuthLocalCredential, 0, len(files))
	for _, file := range files {
		sources := make([]agentdef.AuthLocalSource, 0, len(file.Sources))
		for _, source := range file.Sources {
			sources = append(sources, agentdef.AuthLocalSource{
				Kind:        agentdef.AuthLocalSourceKind(strings.TrimSpace(source.Kind)),
				HomeRelPath: strings.TrimSpace(source.HomeRelPath),
				Service:     strings.TrimSpace(source.Service),
				Platforms:   append([]string(nil), source.Platforms...),
			})
		}
		out = append(out, agentdef.AuthLocalCredential{
			RequiredEnv:      strings.TrimSpace(file.RequiredEnv),
			RunHomeRelPath:   strings.TrimSpace(file.RunHomeRelPath),
			ValidateJSONPath: strings.TrimSpace(file.ValidateJSONPath),
			Sources:          sources,
		})
	}
	return out
}

func cloneAuthProviders(files []definitionAuthProvider) []agentdef.AuthProvider {
	if len(files) == 0 {
		return nil
	}
	out := make([]agentdef.AuthProvider, 0, len(files))
	for _, file := range files {
		out = append(out, agentdef.AuthProvider{
			Name:        strings.TrimSpace(file.Name),
			AuthMode:    agentdef.AuthProviderMode(strings.TrimSpace(file.AuthMode)),
			RequiredEnv: append([]string(nil), file.RequiredEnv...),
		})
	}
	return out
}

func cloneAgent(agent runbundle.Agent) runbundle.Agent {
	clone := agent
	clone.Config = agentdef.ConfigSpec{
		Name:        agent.Config.Name,
		Description: agent.Config.Description,
		Mode:        agent.Config.Mode,
		Skills:      cloneSkills(agent.Config.Skills),
		AgentsMD:    cloneAgentsMD(agent.Config.AgentsMD),
		Input:       cloneAnyMap(agent.Config.Input),
	}
	if agent.Config.Unified != nil {
		clone.Config.Unified = cloneUnified(agent.Config.Unified)
	}
	return clone
}

func loadLocalSkills(configDir string, files []configSkillFile) ([]agentdef.SkillSpec, error) {
	if len(files) == 0 {
		return nil, nil
	}
	skills := make([]agentdef.SkillSpec, 0, len(files))
	for idx, file := range files {
		rawPath := strings.TrimSpace(file.Path)
		if rawPath == "" {
			return nil, fmt.Errorf("skills[%d].path is required", idx)
		}
		skillPath := rawPath
		if !filepath.IsAbs(skillPath) {
			skillPath = filepath.Join(configDir, filepath.FromSlash(rawPath))
		}
		info, err := os.Stat(skillPath)
		if err != nil {
			return nil, fmt.Errorf("skills[%d]: stat %s: %w", idx, skillPath, err)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("skills[%d]: %s is not a directory", idx, skillPath)
		}
		skill, err := agentdef.LoadSkillSpecFromDir(skillPath)
		if err != nil {
			return nil, fmt.Errorf("skills[%d]: %w", idx, err)
		}
		skills = append(skills, skill)
	}
	return skills, nil
}

func loadLocalAgentsMD(configDir string, file *configAgentsMDFile) (*agentdef.AgentsMDSpec, error) {
	if file == nil {
		return nil, nil
	}
	rawPath := strings.TrimSpace(file.Path)
	if rawPath == "" {
		return nil, fmt.Errorf("agents_md.path is required")
	}
	agentsMDPath := rawPath
	if !filepath.IsAbs(agentsMDPath) {
		agentsMDPath = filepath.Join(configDir, filepath.FromSlash(rawPath))
	}
	body, err := os.ReadFile(agentsMDPath)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", agentsMDPath, err)
	}
	return &agentdef.AgentsMDSpec{Content: string(body)}, nil
}

func cloneAnyMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	body, err := json.Marshal(in)
	if err != nil {
		out := make(map[string]any, len(in))
		for key, value := range in {
			out[key] = value
		}
		return out
	}
	out := map[string]any{}
	if err := json.Unmarshal(body, &out); err != nil {
		out = make(map[string]any, len(in))
		for key, value := range in {
			out[key] = value
		}
	}
	return out
}

func cloneUnified(in *agentdef.UnifiedSpec) *agentdef.UnifiedSpec {
	if in == nil {
		return nil
	}
	body, err := json.Marshal(in)
	if err != nil {
		copy := *in
		return &copy
	}
	var out agentdef.UnifiedSpec
	if err := json.Unmarshal(body, &out); err != nil {
		copy := *in
		return &copy
	}
	return &out
}

func cloneSkills(in []agentdef.SkillSpec) []agentdef.SkillSpec {
	if len(in) == 0 {
		return nil
	}
	out := make([]agentdef.SkillSpec, len(in))
	copy(out, in)
	return out
}

func cloneAgentsMD(in *agentdef.AgentsMDSpec) *agentdef.AgentsMDSpec {
	if in == nil {
		return nil
	}
	copy := *in
	return &copy
}

func repoRoot() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		panic("resolve testfixture caller")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
}
