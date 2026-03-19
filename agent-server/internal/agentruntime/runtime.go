package agentruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/marginlab/margin-eval/agent-server/internal/config"
	"github.com/marginlab/margin-eval/agent-server/internal/fsutil"
	"github.com/marginlab/margin-eval/agent-server/internal/logutil"
	"github.com/marginlab/margin-eval/agent-server/internal/noderuntime"
	"github.com/marginlab/margin-eval/agent-server/internal/state"
	"github.com/marginlab/margin-eval/runner/runner-core/agentdef"
	"github.com/marginlab/margin-eval/runner/runner-core/testassets"
)

type Runtime struct {
	cfg         config.Config
	nodeRuntime managedNodeRuntime
}

type managedNodeRuntime interface {
	Ensure(ctx context.Context, spec noderuntime.Spec) (noderuntime.Info, error)
}

type RunContext struct {
	RunID             string
	SessionID         string
	SnapshotSessionID string
	CWD               string
	RunHome           string
	ArtifactsDir      string
	Env               map[string]string
	RunArgs           []string
	InitialPrompt     string
}

type ExecSpec struct {
	Path string
	Args []string
	Env  []string
	Dir  string
}

type hookContext struct {
	Definition agentdef.DefinitionSnapshot `json:"definition"`
	Config     *hookConfig                 `json:"config,omitempty"`
	Install    map[string]any              `json:"install,omitempty"`
	Paths      hookPaths                   `json:"paths"`
	Toolchains *hookToolchains             `json:"toolchains,omitempty"`
	Run        *hookRunContext             `json:"run,omitempty"`
	Snapshot   *hookSnapshotContext        `json:"snapshot,omitempty"`
}

type hookConfig struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	Mode        agentdef.ConfigMode    `json:"mode,omitempty"`
	Skills      []hookSkill            `json:"skills,omitempty"`
	AgentsMD    *agentdef.AgentsMDSpec `json:"agents_md,omitempty"`
	Input       map[string]any         `json:"input,omitempty"`
	Unified     *agentdef.UnifiedSpec  `json:"unified,omitempty"`
}

type hookSkill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type hookPaths struct {
	Root         string `json:"root"`
	BinDir       string `json:"bin_dir"`
	StateDir     string `json:"state_dir"`
	ConfigDir    string `json:"config_dir"`
	Workspaces   string `json:"workspaces_dir"`
	Definition   string `json:"definition_dir"`
	InstallDir   string `json:"install_dir"`
	RunHome      string `json:"run_home,omitempty"`
	ArtifactsDir string `json:"artifacts_dir,omitempty"`
}

type hookRunContext struct {
	SessionID     string            `json:"session_id,omitempty"`
	CWD           string            `json:"cwd"`
	InitialPrompt string            `json:"initial_prompt"`
	Env           map[string]string `json:"env"`
	Args          []string          `json:"args,omitempty"`
}

type hookSnapshotContext struct {
	SessionID string `json:"session_id,omitempty"`
}

type hookToolchains struct {
	Node    *hookNodeToolchain `json:"node,omitempty"`
	nodeEnv map[string]string  `json:"-"`
}

type hookNodeToolchain struct {
	BinDir        string `json:"bin_dir"`
	Node          string `json:"node"`
	NPM           string `json:"npm"`
	NPX           string `json:"npx"`
	Version       string `json:"version"`
	InstallMethod string `json:"install_method"`
}

type hookExecSpec struct {
	Path string         `json:"path"`
	Args []string       `json:"args"`
	Env  map[string]any `json:"env"`
	Dir  string         `json:"dir"`
}

func New(cfg config.Config) (*Runtime, error) {
	nodeRuntime, err := noderuntime.New(noderuntime.Config{
		BinDir:           cfg.BinDir,
		StateDir:         cfg.StateDir,
		ExtraCACertsFile: cfg.ExtraCACertsFile,
		NVMDir:           cfg.NVMDir,
		NVMVersion:       cfg.NVMVersion,
	})
	if err != nil {
		return nil, fmt.Errorf("create managed node runtime: %w", err)
	}
	return &Runtime{cfg: cfg, nodeRuntime: nodeRuntime}, nil
}

func (r *Runtime) LoadDefinition(snapshot agentdef.DefinitionSnapshot) (state.DefinitionRecord, error) {
	if err := agentdef.ValidateDefinitionSnapshot(snapshot); err != nil {
		return state.DefinitionRecord{}, err
	}
	definitionDir := r.definitionDir(snapshot)
	if err := materializeDefinition(snapshot, definitionDir); err != nil {
		return state.DefinitionRecord{}, err
	}
	installDir := r.installDir(snapshot)
	if err := fsutil.EnsureDir(installDir, 0o755); err != nil {
		return state.DefinitionRecord{}, fmt.Errorf("create install dir: %w", err)
	}
	return state.DefinitionRecord{
		Snapshot:      snapshot,
		PackageHash:   snapshot.Package.ArchiveTGZSHA256,
		DefinitionDir: definitionDir,
		InstallDir:    installDir,
	}, nil
}

func (r *Runtime) ValidateConfig(definition state.DefinitionRecord, cfg agentdef.ConfigSpec) (agentdef.ConfigSnapshot, error) {
	if definition.Snapshot.Manifest.Name == "" {
		return agentdef.ConfigSnapshot{}, fmt.Errorf("agent definition is not loaded")
	}
	normalizedSpec, err := agentdef.ValidateAndNormalizeConfigSpec(definition.Snapshot, cfg)
	if err != nil {
		return agentdef.ConfigSnapshot{}, err
	}
	toolchains, err := r.resolveToolchains(context.Background(), definition.Snapshot.Manifest)
	if err != nil {
		return agentdef.ConfigSnapshot{}, err
	}

	snapshot := agentdef.ConfigSnapshot{
		Name:        normalizedSpec.Name,
		Description: normalizedSpec.Description,
		Mode:        normalizedSpec.Mode,
		Skills:      cloneSkillSpecs(normalizedSpec.Skills),
		AgentsMD:    cloneAgentsMDSpec(normalizedSpec.AgentsMD),
	}
	if normalizedSpec.Unified != nil {
		unified := cloneUnifiedSpec(normalizedSpec.Unified)
		snapshot.Unified = unified
	}
	switch normalizedSpec.Mode {
	case agentdef.ConfigModeDirect:
		snapshot.Input = cloneAnyMap(normalizedSpec.Input)
	case agentdef.ConfigModeUnified:
		translated, err := r.translateUnifiedConfig(definition, normalizedSpec, toolchains)
		if err != nil {
			return agentdef.ConfigSnapshot{}, err
		}
		snapshot.Input = translated
	default:
		return agentdef.ConfigSnapshot{}, fmt.Errorf("unsupported config mode %q", normalizedSpec.Mode)
	}

	snapshot, err = agentdef.ValidateAndNormalizeConfigSnapshot(definition.Snapshot, snapshot)
	if err != nil {
		return agentdef.ConfigSnapshot{}, err
	}

	validateHook := definition.Snapshot.Manifest.Config.ValidateHook
	if validateHook == nil {
		return snapshot, nil
	}
	ctx := hookContext{
		Definition: definition.Snapshot,
		Config:     hookConfigFromSnapshot(snapshot),
		Paths:      r.basePaths(definition),
		Toolchains: toolchains,
	}
	output, err := r.runHookJSON(context.Background(), definition.DefinitionDir, validateHook.Path, ctx)
	if err != nil {
		return agentdef.ConfigSnapshot{}, err
	}
	normalizedInput, err := decodeJSONObject(output)
	if err != nil {
		return agentdef.ConfigSnapshot{}, fmt.Errorf("decode config validate hook output: %w", err)
	}
	snapshot.Input = normalizedInput
	return agentdef.ValidateAndNormalizeConfigSnapshot(definition.Snapshot, snapshot)
}

func (r *Runtime) translateUnifiedConfig(definition state.DefinitionRecord, cfg agentdef.ConfigSpec, toolchains *hookToolchains) (map[string]any, error) {
	unified := definition.Snapshot.Manifest.Config.Unified
	if unified == nil {
		return nil, fmt.Errorf("selected agent definition does not support unified config")
	}
	ctx := hookContext{
		Definition: definition.Snapshot,
		Config:     hookConfigFromSpec(cfg),
		Paths:      r.basePaths(definition),
		Toolchains: toolchains,
	}
	output, err := r.runHookJSON(context.Background(), definition.DefinitionDir, unified.TranslateHook.Path, ctx)
	if err != nil {
		return nil, err
	}
	translated, err := decodeJSONObject(output)
	if err != nil {
		return nil, fmt.Errorf("decode unified translate hook output: %w", err)
	}
	return translated, nil
}

func (r *Runtime) Install(ctx context.Context, agent state.AgentRecord) (state.InstallInfo, error) {
	if agent.Definition == nil {
		return state.InstallInfo{}, fmt.Errorf("agent definition is not loaded")
	}
	definition := *agent.Definition
	toolchains, err := r.resolveToolchains(ctx, definition.Snapshot.Manifest)
	if err != nil {
		return state.InstallInfo{}, err
	}
	ctxPayload := hookContext{
		Definition: definition.Snapshot,
		Paths:      r.basePaths(definition),
		Toolchains: toolchains,
	}
	if agent.Config != nil {
		ctxPayload.Config = hookConfigFromSnapshot(agent.Config.Snapshot)
	}

	if checkHook := definition.Snapshot.Manifest.Install.CheckHook; checkHook != nil {
		output, err := r.runHookJSON(ctx, definition.DefinitionDir, checkHook.Path, ctxPayload)
		if err != nil {
			return state.InstallInfo{}, err
		}
		result, err := decodeJSONObject(output)
		if err != nil {
			return state.InstallInfo{}, fmt.Errorf("decode install check hook output: %w", err)
		}
		if isInstalled(result) {
			return state.InstallInfo{InstalledAt: time.Now().UTC(), Result: result}, nil
		}
	}

	if runHook := definition.Snapshot.Manifest.Install.RunHook; runHook != nil {
		output, err := r.runHookJSON(ctx, definition.DefinitionDir, runHook.Path, ctxPayload)
		if err != nil {
			return state.InstallInfo{}, err
		}
		result, err := decodeJSONObject(output)
		if err != nil {
			return state.InstallInfo{}, fmt.Errorf("decode install run hook output: %w", err)
		}
		return state.InstallInfo{InstalledAt: time.Now().UTC(), Result: result}, nil
	}

	return state.InstallInfo{
		InstalledAt: time.Now().UTC(),
		Result:      map[string]any{"installed": true},
	}, nil
}

func (r *Runtime) PrepareRun(ctx context.Context, agent state.AgentRecord, runCtx RunContext) (ExecSpec, error) {
	if agent.Definition == nil {
		return ExecSpec{}, fmt.Errorf("agent definition is not loaded")
	}
	runEnv, toolchains, err := r.resolveRuntimeEnv(ctx, agent.Definition.Snapshot.Manifest, runCtx.Env)
	if err != nil {
		return ExecSpec{}, err
	}
	if agent.Config != nil {
		if err := r.materializeSkills(agent.Definition.Snapshot.Manifest, agent.Config.Snapshot.Skills, runCtx.RunHome); err != nil {
			return ExecSpec{}, err
		}
	}
	ctxPayload := hookContext{
		Definition: agent.Definition.Snapshot,
		Paths:      r.pathsForRun(*agent.Definition, runCtx),
		Toolchains: toolchains,
		Run: &hookRunContext{
			SessionID:     runCtx.SessionID,
			CWD:           runCtx.CWD,
			InitialPrompt: runCtx.InitialPrompt,
			Env:           runEnv,
			Args:          append([]string(nil), runCtx.RunArgs...),
		},
	}
	if agent.Config != nil {
		ctxPayload.Config = hookConfigFromSnapshot(agent.Config.Snapshot)
	}
	if agent.Install != nil {
		ctxPayload.Install = cloneAnyMap(agent.Install.Result)
	}
	execSpec, err := r.prepareExecSpec(ctx, *agent.Definition, agent.Definition.Snapshot.Manifest.Run.PrepareHook.Path, ctxPayload, runEnv)
	if err != nil {
		return ExecSpec{}, err
	}
	if agent.Config != nil {
		if err := r.materializeAgentsMD(agent.Definition.Snapshot.Manifest, agent.Config.Snapshot.AgentsMD, runCtx, execSpec); err != nil {
			return ExecSpec{}, err
		}
	}
	return execSpec, nil
}

func (r *Runtime) SupportsSnapshot(agent state.AgentRecord) bool {
	return agent.Definition != nil && agent.Definition.Snapshot.Manifest.Snapshot != nil
}

func (r *Runtime) PrepareSnapshot(ctx context.Context, agent state.AgentRecord, runCtx RunContext) (ExecSpec, error) {
	if agent.Definition == nil || agent.Definition.Snapshot.Manifest.Snapshot == nil {
		return ExecSpec{}, fmt.Errorf("snapshot is not supported for selected agent")
	}
	runEnv, toolchains, err := r.resolveRuntimeEnv(ctx, agent.Definition.Snapshot.Manifest, runCtx.Env)
	if err != nil {
		return ExecSpec{}, err
	}
	if agent.Config != nil {
		if err := r.materializeSkills(agent.Definition.Snapshot.Manifest, agent.Config.Snapshot.Skills, runCtx.RunHome); err != nil {
			return ExecSpec{}, err
		}
	}
	ctxPayload := hookContext{
		Definition: agent.Definition.Snapshot,
		Paths:      r.pathsForRun(*agent.Definition, runCtx),
		Toolchains: toolchains,
		Run: &hookRunContext{
			SessionID: runCtx.SessionID,
			CWD:       runCtx.CWD,
			Env:       runEnv,
		},
		Snapshot: &hookSnapshotContext{SessionID: runCtx.SnapshotSessionID},
	}
	if agent.Config != nil {
		ctxPayload.Config = hookConfigFromSnapshot(agent.Config.Snapshot)
	}
	if agent.Install != nil {
		ctxPayload.Install = cloneAnyMap(agent.Install.Result)
	}
	execSpec, err := r.prepareExecSpec(ctx, *agent.Definition, agent.Definition.Snapshot.Manifest.Snapshot.PrepareHook.Path, ctxPayload, runEnv)
	if err != nil {
		return ExecSpec{}, err
	}
	if agent.Config != nil {
		if err := r.materializeAgentsMD(agent.Definition.Snapshot.Manifest, agent.Config.Snapshot.AgentsMD, runCtx, execSpec); err != nil {
			return ExecSpec{}, err
		}
	}
	return execSpec, nil
}

func (r *Runtime) CollectTrajectory(ctx context.Context, agent state.AgentRecord, runCtx RunContext) (json.RawMessage, error) {
	if agent.Definition == nil || agent.Definition.Snapshot.Manifest.Trajectory == nil {
		return nil, nil
	}
	runEnv, toolchains, err := r.resolveRuntimeEnv(ctx, agent.Definition.Snapshot.Manifest, runCtx.Env)
	if err != nil {
		return nil, err
	}
	ctxPayload := hookContext{
		Definition: agent.Definition.Snapshot,
		Paths:      r.pathsForRun(*agent.Definition, runCtx),
		Toolchains: toolchains,
		Run: &hookRunContext{
			SessionID:     runCtx.SessionID,
			CWD:           runCtx.CWD,
			InitialPrompt: runCtx.InitialPrompt,
			Env:           runEnv,
			Args:          append([]string(nil), runCtx.RunArgs...),
		},
	}
	if agent.Config != nil {
		ctxPayload.Config = hookConfigFromSnapshot(agent.Config.Snapshot)
	}
	if agent.Install != nil {
		ctxPayload.Install = cloneAnyMap(agent.Install.Result)
	}
	output, err := r.runHookJSON(ctx, agent.Definition.DefinitionDir, agent.Definition.Snapshot.Manifest.Trajectory.CollectHook.Path, ctxPayload)
	if err != nil {
		return nil, err
	}
	trimmed := bytes.TrimSpace(output)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}
	return append(json.RawMessage(nil), trimmed...), nil
}

func (r *Runtime) RequiredEnv(agent state.AgentRecord) []string {
	if agent.Definition == nil {
		return nil
	}
	return append([]string(nil), agent.Definition.Snapshot.Manifest.Auth.RequiredEnv...)
}

func (r *Runtime) basePaths(definition state.DefinitionRecord) hookPaths {
	return hookPaths{
		Root:       r.cfg.RootDir,
		BinDir:     r.cfg.BinDir,
		StateDir:   r.cfg.StateDir,
		ConfigDir:  r.cfg.ConfigDir,
		Workspaces: r.cfg.WorkspacesDir,
		Definition: definition.DefinitionDir,
		InstallDir: definition.InstallDir,
	}
}

func (r *Runtime) pathsForRun(definition state.DefinitionRecord, runCtx RunContext) hookPaths {
	paths := r.basePaths(definition)
	paths.RunHome = runCtx.RunHome
	paths.ArtifactsDir = runCtx.ArtifactsDir
	return paths
}

func (r *Runtime) prepareExecSpec(ctx context.Context, definition state.DefinitionRecord, hookPath string, hookCtx hookContext, fallbackEnv map[string]string) (ExecSpec, error) {
	output, err := r.runHookJSON(ctx, definition.DefinitionDir, hookPath, hookCtx)
	if err != nil {
		return ExecSpec{}, err
	}
	var raw hookExecSpec
	if err := json.Unmarshal(output, &raw); err != nil {
		return ExecSpec{}, fmt.Errorf("decode exec spec output: %w", err)
	}
	if strings.TrimSpace(raw.Path) == "" {
		return ExecSpec{}, fmt.Errorf("exec spec path is required")
	}
	envMap, err := normalizeEnvMap(raw.Env)
	if err != nil {
		return ExecSpec{}, err
	}
	if len(envMap) == 0 {
		envMap = cloneStringMap(fallbackEnv)
	}
	return ExecSpec{
		Path: raw.Path,
		Args: append([]string(nil), raw.Args...),
		Env:  envMapToList(envMap),
		Dir:  strings.TrimSpace(raw.Dir),
	}, nil
}

func (r *Runtime) runHookJSON(ctx context.Context, definitionDir, hookRelPath string, hookCtx hookContext) ([]byte, error) {
	hookPath := filepath.Join(definitionDir, filepath.FromSlash(hookRelPath))
	contextFile, err := os.CreateTemp(r.cfg.StateDir, "agent-hook-context-*.json")
	if err != nil {
		return nil, fmt.Errorf("create hook context file: %w", err)
	}
	contextPath := contextFile.Name()
	defer os.Remove(contextPath)

	body, err := json.MarshalIndent(hookCtx, "", "  ")
	if err != nil {
		_ = contextFile.Close()
		return nil, fmt.Errorf("marshal hook context: %w", err)
	}
	if _, err := contextFile.Write(body); err != nil {
		_ = contextFile.Close()
		return nil, fmt.Errorf("write hook context: %w", err)
	}
	if err := contextFile.Close(); err != nil {
		return nil, fmt.Errorf("close hook context file: %w", err)
	}

	cmd := exec.CommandContext(ctx, hookPath)
	cmd.Dir = definitionDir
	cmd.Env = append(r.toolchainEnvironment(os.Environ(), hookCtx.Toolchains), "AGENT_CONTEXT_JSON="+contextPath)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return nil, fmt.Errorf("execute hook %s: %s", hookRelPath, message)
	}
	return bytes.TrimSpace(stdout.Bytes()), nil
}

func (r *Runtime) resolveRuntimeEnv(ctx context.Context, manifest agentdef.Manifest, baseEnv map[string]string) (map[string]string, *hookToolchains, error) {
	env := cloneStringMap(baseEnv)
	toolchains, err := r.resolveToolchains(ctx, manifest)
	if err != nil {
		return nil, nil, err
	}
	if toolchains != nil && toolchains.Node != nil {
		env = prependPathMap(env, toolchains.Node.BinDir)
		env = mergeStringMaps(env, toolchains.nodeEnv)
	}
	return env, toolchains, nil
}

func (r *Runtime) resolveToolchains(ctx context.Context, manifest agentdef.Manifest) (*hookToolchains, error) {
	if manifest.Toolchains.Node == nil {
		return nil, nil
	}
	if r.nodeRuntime == nil {
		return nil, fmt.Errorf("managed node runtime is not configured")
	}
	ensureCtx := ctx
	if r.cfg.NodeBootstrapTimeout > 0 {
		var cancel context.CancelFunc
		ensureCtx, cancel = context.WithTimeout(ctx, r.cfg.NodeBootstrapTimeout)
		defer cancel()
	}
	info, err := r.nodeRuntime.Ensure(ensureCtx, noderuntime.Spec{
		Minimum:   manifest.Toolchains.Node.Minimum,
		Preferred: manifest.Toolchains.Node.Preferred,
	})
	if err != nil {
		return nil, fmt.Errorf("ensure managed node runtime: %w", err)
	}
	return plannedToolchains(info), nil
}

func plannedToolchains(info noderuntime.Info) *hookToolchains {
	return &hookToolchains{
		Node: &hookNodeToolchain{
			BinDir:        info.BinDir,
			Node:          info.NodePath,
			NPM:           info.NPMPath,
			NPX:           info.NPXPath,
			Version:       info.Version,
			InstallMethod: info.InstallMethod,
		},
		nodeEnv: cloneStringMap(info.Environment),
	}
}

func (r *Runtime) toolchainEnvironment(base []string, toolchains *hookToolchains) []string {
	env := append([]string(nil), base...)
	if toolchains == nil || toolchains.Node == nil {
		return env
	}
	env = prependPathList(env, toolchains.Node.BinDir)
	for key, value := range toolchains.nodeEnv {
		env = setEnvListValue(env, key, value)
	}
	return env
}

func (r *Runtime) definitionDir(snapshot agentdef.DefinitionSnapshot) string {
	return filepath.Join(r.cfg.StateDir, "agent-definitions", snapshot.Package.ArchiveTGZSHA256, "src")
}

func (r *Runtime) installDir(snapshot agentdef.DefinitionSnapshot) string {
	return filepath.Join(r.cfg.BinDir, "agent-definitions", sanitize(snapshot.Manifest.Name), snapshot.Package.ArchiveTGZSHA256)
}

func materializeDefinition(snapshot agentdef.DefinitionSnapshot, definitionDir string) error {
	if info, err := os.Stat(definitionDir); err == nil && info.IsDir() {
		return nil
	}
	if err := fsutil.EnsureDir(filepath.Dir(definitionDir), 0o755); err != nil {
		return fmt.Errorf("create definition root: %w", err)
	}
	tempRoot, err := os.MkdirTemp(filepath.Dir(definitionDir), "materialize-*")
	if err != nil {
		return fmt.Errorf("create definition temp dir: %w", err)
	}
	defer os.RemoveAll(tempRoot)

	stageDir := filepath.Join(tempRoot, "src")
	if err := fsutil.EnsureDir(filepath.Dir(stageDir), 0o755); err != nil {
		return fmt.Errorf("create definition stage root: %w", err)
	}
	if err := testassets.Materialize(snapshot.Package, stageDir, testassets.DefaultMaxArchiveBytes); err != nil {
		return fmt.Errorf("materialize definition package: %w", err)
	}
	if err := os.Rename(stageDir, definitionDir); err != nil {
		if info, statErr := os.Stat(definitionDir); statErr == nil && info.IsDir() {
			return nil
		}
		return fmt.Errorf("publish definition package: %w", err)
	}
	return nil
}

func (r *Runtime) materializeSkills(manifest agentdef.Manifest, skills []agentdef.SkillSpec, runHome string) error {
	if len(skills) == 0 {
		return nil
	}
	if manifest.Skills == nil {
		return fmt.Errorf("selected agent definition does not support skills")
	}
	skillsRoot := filepath.Join(runHome, filepath.FromSlash(manifest.Skills.HomeRelDir))
	if err := fsutil.EnsureDir(skillsRoot, 0o755); err != nil {
		return fmt.Errorf("create skills root: %w", err)
	}
	for _, skill := range skills {
		skillDir := filepath.Join(skillsRoot, skill.Name)
		if err := materializeSkill(skill, skillDir); err != nil {
			return fmt.Errorf("materialize skill %q: %w", skill.Name, err)
		}
	}
	return nil
}

func materializeSkill(skill agentdef.SkillSpec, skillDir string) error {
	if info, err := os.Stat(skillDir); err == nil {
		if !info.IsDir() {
			return fmt.Errorf("%s exists and is not a directory", skillDir)
		}
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat skill dir: %w", err)
	}
	if err := fsutil.EnsureDir(filepath.Dir(skillDir), 0o755); err != nil {
		return fmt.Errorf("create skill root: %w", err)
	}
	tempRoot, err := os.MkdirTemp(filepath.Dir(skillDir), "materialize-skill-*")
	if err != nil {
		return fmt.Errorf("create skill temp dir: %w", err)
	}
	defer os.RemoveAll(tempRoot)

	stageDir := filepath.Join(tempRoot, "skill")
	if err := fsutil.EnsureDir(filepath.Dir(stageDir), 0o755); err != nil {
		return fmt.Errorf("create skill stage root: %w", err)
	}
	if err := testassets.Materialize(skill.Package, stageDir, testassets.DefaultMaxArchiveBytes); err != nil {
		return fmt.Errorf("materialize skill package: %w", err)
	}
	if err := os.Rename(stageDir, skillDir); err != nil {
		if info, statErr := os.Stat(skillDir); statErr == nil && info.IsDir() {
			return nil
		}
		return fmt.Errorf("publish skill package: %w", err)
	}
	return nil
}

func (r *Runtime) materializeAgentsMD(manifest agentdef.Manifest, spec *agentdef.AgentsMDSpec, runCtx RunContext, execSpec ExecSpec) error {
	if spec == nil {
		return nil
	}
	if manifest.AgentsMD == nil {
		return fmt.Errorf("selected agent definition does not support agents_md")
	}
	targetDir, err := r.resolveAgentStartDir(runCtx.CWD, execSpec.Dir)
	if err != nil {
		return err
	}
	targetPath := filepath.Join(targetDir, manifest.AgentsMD.Filename)
	content := []byte(spec.Content)

	existing, err := os.ReadFile(targetPath)
	switch {
	case err == nil:
		if bytes.Equal(existing, content) {
			return nil
		}
		logutil.Warn("agents_md_overwrite", map[string]any{
			"run_id":     runCtx.RunID,
			"session_id": runCtx.SessionID,
			"path":       targetPath,
			"filename":   manifest.AgentsMD.Filename,
		})
	case os.IsNotExist(err):
	default:
		return fmt.Errorf("read existing %s: %w", targetPath, err)
	}

	if err := fsutil.WriteFileAtomic(targetPath, content, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", targetPath, err)
	}
	return nil
}

func (r *Runtime) resolveAgentStartDir(runCWD, execDir string) (string, error) {
	targetDir := strings.TrimSpace(execDir)
	if targetDir == "" {
		targetDir = strings.TrimSpace(runCWD)
	}
	if targetDir == "" {
		return "", fmt.Errorf("agent start directory is required")
	}
	if !filepath.IsAbs(targetDir) {
		if strings.TrimSpace(runCWD) == "" {
			return "", fmt.Errorf("relative exec dir %q requires run cwd", execDir)
		}
		targetDir = filepath.Join(runCWD, targetDir)
	}
	validatedDir, err := fsutil.ValidateExistingDirUnderRoot(targetDir, r.cfg.WorkspacesDir)
	if err != nil {
		return "", fmt.Errorf("validate agent start dir %q under workspaces: %w", targetDir, err)
	}
	return validatedDir, nil
}

func decodeJSONObject(raw []byte) (map[string]any, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return map[string]any{}, nil
	}
	var out map[string]any
	if err := json.Unmarshal(trimmed, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func isInstalled(result map[string]any) bool {
	value, ok := result["installed"]
	if !ok {
		return false
	}
	installed, _ := value.(bool)
	return installed
}

func normalizeEnvMap(raw map[string]any) (map[string]string, error) {
	if raw == nil {
		return nil, nil
	}
	out := make(map[string]string, len(raw))
	for key, value := range raw {
		trimmedKey := strings.TrimSpace(key)
		if trimmedKey == "" {
			return nil, fmt.Errorf("exec spec env keys must not be empty")
		}
		out[trimmedKey] = fmt.Sprint(value)
	}
	return out, nil
}

func envMapToList(env map[string]string) []string {
	if env == nil {
		return nil
	}
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, key+"="+env[key])
	}
	return out
}

func cloneStringMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func mergeStringMaps(base map[string]string, overlay map[string]string) map[string]string {
	out := cloneStringMap(base)
	if out == nil {
		out = map[string]string{}
	}
	for key, value := range overlay {
		out[key] = value
	}
	return out
}

func cloneAnyMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	body, err := json.Marshal(in)
	if err != nil {
		return in
	}
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		return in
	}
	return out
}

func prependPathMap(env map[string]string, pathEntry string) map[string]string {
	out := cloneStringMap(env)
	if out == nil {
		out = map[string]string{}
	}
	trimmedEntry := strings.TrimSpace(pathEntry)
	if trimmedEntry == "" {
		return out
	}
	currentPath := strings.TrimSpace(out["PATH"])
	if currentPath == "" {
		out["PATH"] = trimmedEntry
		return out
	}
	parts := []string{trimmedEntry}
	for _, part := range strings.Split(currentPath, string(os.PathListSeparator)) {
		if part == "" || part == trimmedEntry {
			continue
		}
		parts = append(parts, part)
	}
	out["PATH"] = strings.Join(parts, string(os.PathListSeparator))
	return out
}

func prependPathList(env []string, pathEntry string) []string {
	trimmedEntry := strings.TrimSpace(pathEntry)
	if trimmedEntry == "" {
		return env
	}

	var currentPath string
	var hasPath bool
	for _, entry := range env {
		if !strings.HasPrefix(entry, "PATH=") {
			continue
		}
		currentPath = strings.TrimPrefix(entry, "PATH=")
		hasPath = true
		break
	}

	parts := []string{trimmedEntry}
	if hasPath {
		for _, part := range strings.Split(currentPath, string(os.PathListSeparator)) {
			if part == "" || part == trimmedEntry {
				continue
			}
			parts = append(parts, part)
		}
	}

	return setEnvListValue(env, "PATH", strings.Join(parts, string(os.PathListSeparator)))
}

func setEnvListValue(env []string, key, value string) []string {
	prefix := key + "="
	for idx, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			env[idx] = prefix + value
			return env
		}
	}
	return append(env, prefix+value)
}

func sanitize(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "anonymous"
	}
	replacer := strings.NewReplacer("/", "-", "\\", "-", " ", "-", ":", "-", ".", "-")
	return replacer.Replace(trimmed)
}

func hookConfigFromSpec(cfg agentdef.ConfigSpec) *hookConfig {
	out := &hookConfig{
		Name:        cfg.Name,
		Description: cfg.Description,
		Mode:        cfg.Mode,
		Skills:      hookSkillsFromSkillSpecs(cfg.Skills),
		AgentsMD:    cloneAgentsMDSpec(cfg.AgentsMD),
		Input:       cloneAnyMap(cfg.Input),
		Unified:     cloneUnifiedSpec(cfg.Unified),
	}
	return out
}

func hookConfigFromSnapshot(cfg agentdef.ConfigSnapshot) *hookConfig {
	out := &hookConfig{
		Name:        cfg.Name,
		Description: cfg.Description,
		Mode:        cfg.Mode,
		Skills:      hookSkillsFromSkillSpecs(cfg.Skills),
		AgentsMD:    cloneAgentsMDSpec(cfg.AgentsMD),
		Input:       cloneAnyMap(cfg.Input),
		Unified:     cloneUnifiedSpec(cfg.Unified),
	}
	return out
}

func hookSkillsFromSkillSpecs(skills []agentdef.SkillSpec) []hookSkill {
	if len(skills) == 0 {
		return nil
	}
	out := make([]hookSkill, 0, len(skills))
	for _, skill := range skills {
		out = append(out, hookSkill{Name: skill.Name, Description: skill.Description})
	}
	return out
}

func cloneUnifiedSpec(spec *agentdef.UnifiedSpec) *agentdef.UnifiedSpec {
	if spec == nil {
		return nil
	}
	body, err := json.Marshal(spec)
	if err != nil {
		copy := *spec
		return &copy
	}
	var out agentdef.UnifiedSpec
	if err := json.Unmarshal(body, &out); err != nil {
		copy := *spec
		return &copy
	}
	return &out
}

func cloneSkillSpecs(specs []agentdef.SkillSpec) []agentdef.SkillSpec {
	if len(specs) == 0 {
		return nil
	}
	out := make([]agentdef.SkillSpec, len(specs))
	copy(out, specs)
	return out
}

func cloneAgentsMDSpec(spec *agentdef.AgentsMDSpec) *agentdef.AgentsMDSpec {
	if spec == nil {
		return nil
	}
	copy := *spec
	return &copy
}
