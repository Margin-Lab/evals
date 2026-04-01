package agentdef

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/marginlab/margin-eval/runner/runner-core/testassets"
)

func TestValidateAndNormalizeUnifiedSpecNormalizesAndSortsServers(t *testing.T) {
	t.Parallel()

	timeout := 2500
	spec, err := ValidateAndNormalizeUnifiedSpec(UnifiedSpec{
		Model:          "gpt-5",
		ReasoningLevel: "MEDIUM",
		MCP: &MCPConfig{
			Servers: []MCPServer{
				{
					Name:      "zeta",
					Transport: MCPTransportSSE,
					URL:       "https://example.com/sse",
				},
				{
					Name:      "alpha",
					Transport: MCPTransportSTDIO,
					Command:   []string{"python", " server.py "},
					Env: map[string]string{
						" TOKEN ": "secret",
					},
					TimeoutMS: &timeout,
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("ValidateAndNormalizeUnifiedSpec() error = %v", err)
	}
	if spec.ReasoningLevel != "medium" {
		t.Fatalf("reasoning_level = %q, want %q", spec.ReasoningLevel, "medium")
	}
	if spec.MCP == nil || len(spec.MCP.Servers) != 2 {
		t.Fatalf("mcp.servers = %#v", spec.MCP)
	}
	if !slices.Equal([]string{spec.MCP.Servers[0].Name, spec.MCP.Servers[1].Name}, []string{"alpha", "zeta"}) {
		t.Fatalf("server order = %#v", spec.MCP.Servers)
	}
	if got := spec.MCP.Servers[0].Env["TOKEN"]; got != "secret" {
		t.Fatalf("normalized env = %#v", spec.MCP.Servers[0].Env)
	}
	if !slices.Equal(spec.MCP.Servers[0].Command, []string{"python", "server.py"}) {
		t.Fatalf("command = %#v", spec.MCP.Servers[0].Command)
	}
}

func TestValidateAndNormalizeConfigSpecDirectAppliesSchema(t *testing.T) {
	t.Parallel()

	definition := testDefinitionSnapshot(t, Manifest{
		Kind: "agent_definition",
		Name: "fixture-agent",
		Config: DefinitionConfigSpec{
			SchemaRelPath: "schema.json",
		},
		Run: RunSpec{PrepareHook: HookRef{Path: "hooks/run.sh"}},
	}, map[string]string{
		"hooks/run.sh": "#!/usr/bin/env bash\nprintf '{}\\n'\n",
		"schema.json": `{
  "type": "object",
  "required": ["command"],
  "additionalProperties": false,
  "properties": {
    "command": {"type": "string", "minLength": 1}
  }
}`,
	})

	normalized, err := ValidateAndNormalizeConfigSpec(definition, ConfigSpec{
		Name:  "fixture-direct",
		Mode:  ConfigModeDirect,
		Input: map[string]any{"command": "echo hello"},
	})
	if err != nil {
		t.Fatalf("ValidateAndNormalizeConfigSpec() error = %v", err)
	}
	if normalized.Mode != ConfigModeDirect {
		t.Fatalf("mode = %q, want %q", normalized.Mode, ConfigModeDirect)
	}
	if got := normalized.Input["command"]; got != "echo hello" {
		t.Fatalf("input.command = %#v", got)
	}

	_, err = ValidateAndNormalizeConfigSpec(definition, ConfigSpec{
		Name: "fixture-invalid",
		Mode: ConfigModeDirect,
	})
	if err == nil || !strings.Contains(err.Error(), `config.input is required when config.mode="direct"`) {
		t.Fatalf("expected direct input validation error, got %v", err)
	}
}

func TestValidateAndNormalizeConfigSpecUnifiedUsesManifestCapabilities(t *testing.T) {
	t.Parallel()

	definition := testDefinitionSnapshot(t, Manifest{
		Kind: "agent_definition",
		Name: "fixture-agent",
		Config: DefinitionConfigSpec{
			Unified: &UnifiedManifestSpec{
				TranslateHook:          HookRef{Path: "hooks/translate.sh"},
				AllowedModels:          []string{"gpt-5", "gpt-5-mini"},
				AllowedReasoningLevels: []string{"low", "medium", "high"},
			},
		},
		Run: RunSpec{PrepareHook: HookRef{Path: "hooks/run.sh"}},
	}, map[string]string{
		"hooks/run.sh":       "#!/usr/bin/env bash\nprintf '{}\\n'\n",
		"hooks/translate.sh": "#!/usr/bin/env bash\nprintf '{}\\n'\n",
	})

	normalized, err := ValidateAndNormalizeConfigSpec(definition, ConfigSpec{
		Name: "fixture-unified",
		Mode: ConfigModeUnified,
		Unified: &UnifiedSpec{
			Model:          "gpt-5",
			ReasoningLevel: "HIGH",
		},
	})
	if err != nil {
		t.Fatalf("ValidateAndNormalizeConfigSpec() error = %v", err)
	}
	if normalized.Unified == nil {
		t.Fatalf("unified payload missing")
	}
	if normalized.Unified.ReasoningLevel != "high" {
		t.Fatalf("reasoning_level = %q, want %q", normalized.Unified.ReasoningLevel, "high")
	}

	_, err = ValidateAndNormalizeConfigSpec(definition, ConfigSpec{
		Name: "fixture-unified",
		Mode: ConfigModeUnified,
		Unified: &UnifiedSpec{
			Model:          "not-allowed",
			ReasoningLevel: "medium",
		},
	})
	if err == nil || !strings.Contains(err.Error(), `config.unified.model "not-allowed" is not allowed`) {
		t.Fatalf("expected disallowed model error, got %v", err)
	}
}

func TestValidateAndNormalizeConfigResourceSpecRequiresMatchingModePayload(t *testing.T) {
	t.Parallel()

	spec, err := ValidateAndNormalizeConfigResourceSpec(ConfigResourceSpec{
		Name: "fixture-unified",
		DefinitionRef: DefinitionRef{
			ResourceID: "res_agent_definition",
			Version:    1,
		},
		Mode: ConfigModeUnified,
		Unified: &UnifiedSpec{
			Model:          "gpt-5",
			ReasoningLevel: "medium",
		},
	})
	if err != nil {
		t.Fatalf("ValidateAndNormalizeConfigResourceSpec() error = %v", err)
	}
	if spec.Mode != ConfigModeUnified || spec.Unified == nil {
		t.Fatalf("normalized spec = %#v", spec)
	}

	_, err = ValidateAndNormalizeConfigResourceSpec(ConfigResourceSpec{
		Name: "fixture-direct",
		DefinitionRef: DefinitionRef{
			ResourceID: "res_agent_definition",
			Version:    1,
		},
		Mode:  ConfigModeDirect,
		Input: map[string]any{"command": "echo hello"},
		Unified: &UnifiedSpec{
			Model:          "gpt-5",
			ReasoningLevel: "medium",
		},
	})
	if err == nil || !strings.Contains(err.Error(), `unified must not be set when mode="direct"`) {
		t.Fatalf("expected mixed-mode validation error, got %v", err)
	}
}

func TestValidateManifestRejectsUnsupportedAgentsMDFilename(t *testing.T) {
	t.Parallel()

	err := ValidateManifest(Manifest{
		Kind: "agent_definition",
		Name: "fixture-agent",
		AgentsMD: &AgentsMDManifestSpec{
			Filename: "README.md",
		},
		Run: RunSpec{PrepareHook: HookRef{Path: "hooks/run.sh"}},
	})
	if err == nil || !strings.Contains(err.Error(), `agents_md.filename must be "AGENTS.md" or "CLAUDE.md"`) {
		t.Fatalf("expected invalid agents_md filename error, got %v", err)
	}
}

func TestValidateManifestRequiresNodeMinimumWhenNodeToolchainDeclared(t *testing.T) {
	t.Parallel()

	err := ValidateManifest(Manifest{
		Kind: "agent_definition",
		Name: "fixture-agent",
		Toolchains: ToolchainSpec{
			Node: &NodeToolchainSpec{},
		},
		Run: RunSpec{PrepareHook: HookRef{Path: "hooks/run.sh"}},
	})
	if err == nil || !strings.Contains(err.Error(), "toolchains.node.minimum is required") {
		t.Fatalf("expected missing node minimum error, got %v", err)
	}
}

func TestValidateManifestRejectsPreferredBelowMinimum(t *testing.T) {
	t.Parallel()

	err := ValidateManifest(Manifest{
		Kind: "agent_definition",
		Name: "fixture-agent",
		Toolchains: ToolchainSpec{
			Node: &NodeToolchainSpec{Minimum: "20", Preferred: "18"},
		},
		Run: RunSpec{PrepareHook: HookRef{Path: "hooks/run.sh"}},
	})
	if err == nil || !strings.Contains(err.Error(), "toolchains.node.preferred must be greater than or equal to toolchains.node.minimum") {
		t.Fatalf("expected preferred below minimum error, got %v", err)
	}
}

func TestValidateAndNormalizeConfigSpecRejectsAgentsMDWhenUnsupported(t *testing.T) {
	t.Parallel()

	definition := testDefinitionSnapshot(t, Manifest{
		Kind: "agent_definition",
		Name: "fixture-agent",
		Run:  RunSpec{PrepareHook: HookRef{Path: "hooks/run.sh"}},
	}, map[string]string{
		"hooks/run.sh": "#!/usr/bin/env bash\nprintf '{}\\n'\n",
	})

	_, err := ValidateAndNormalizeConfigSpec(definition, ConfigSpec{
		Name: "fixture-direct",
		Mode: ConfigModeDirect,
		AgentsMD: &AgentsMDSpec{
			Content: "Always summarize your plan first.\n",
		},
		Input: map[string]any{"command": "echo hello"},
	})
	if err == nil || !strings.Contains(err.Error(), "selected agent definition does not support agents_md") {
		t.Fatalf("expected unsupported agents_md error, got %v", err)
	}
}

func TestValidateManifestAllowsAuthLocalCredentials(t *testing.T) {
	t.Parallel()

	err := ValidateManifest(Manifest{
		Kind: "agent_definition",
		Name: "fixture-agent",
		Auth: AuthSpec{
			RequiredEnv: []string{"OPENAI_API_KEY"},
			LocalCredentials: []AuthLocalCredential{{
				RequiredEnv:    "OPENAI_API_KEY",
				RunHomeRelPath: ".codex/auth.json",
				Sources: []AuthLocalSource{{
					Kind:        AuthLocalSourceKindHomeFile,
					HomeRelPath: ".codex/auth.json",
				}},
			}},
		},
		Run: RunSpec{PrepareHook: HookRef{Path: "hooks/run.sh"}},
	})
	if err != nil {
		t.Fatalf("ValidateManifest() error = %v", err)
	}
}

func TestValidateManifestRejectsInvalidAuthLocalCredentials(t *testing.T) {
	t.Parallel()

	err := ValidateManifest(Manifest{
		Kind: "agent_definition",
		Name: "fixture-agent",
		Auth: AuthSpec{
			RequiredEnv: []string{"OPENAI_API_KEY"},
			LocalCredentials: []AuthLocalCredential{{
				RequiredEnv:    "ANTHROPIC_API_KEY",
				RunHomeRelPath: ".claude/.credentials.json",
				Sources: []AuthLocalSource{{
					Kind:        AuthLocalSourceKindHomeFile,
					HomeRelPath: ".claude/.credentials.json",
				}},
			}},
		},
		Run: RunSpec{PrepareHook: HookRef{Path: "hooks/run.sh"}},
	})
	if err == nil || !strings.Contains(err.Error(), `must reference auth.required_env`) {
		t.Fatalf("expected auth.local_credentials required_env validation error, got %v", err)
	}

	err = ValidateManifest(Manifest{
		Kind: "agent_definition",
		Name: "fixture-agent",
		Auth: AuthSpec{
			RequiredEnv: []string{"OPENAI_API_KEY"},
			LocalCredentials: []AuthLocalCredential{
				{
					RequiredEnv:    "OPENAI_API_KEY",
					RunHomeRelPath: ".codex/auth.json",
					Sources: []AuthLocalSource{{
						Kind:        AuthLocalSourceKindHomeFile,
						HomeRelPath: ".codex/auth.json",
					}},
				},
				{
					RequiredEnv:    "OPENAI_API_KEY",
					RunHomeRelPath: ".codex/auth-backup.json",
					Sources: []AuthLocalSource{{
						Kind:        AuthLocalSourceKindHomeFile,
						HomeRelPath: ".codex/auth-backup.json",
					}},
				},
			},
		},
		Run: RunSpec{PrepareHook: HookRef{Path: "hooks/run.sh"}},
	})
	if err == nil || !strings.Contains(err.Error(), `must not be duplicated`) {
		t.Fatalf("expected duplicate auth.local_credentials error, got %v", err)
	}

	err = ValidateManifest(Manifest{
		Kind: "agent_definition",
		Name: "fixture-agent",
		Auth: AuthSpec{
			RequiredEnv: []string{"ANTHROPIC_API_KEY"},
			LocalCredentials: []AuthLocalCredential{{
				RequiredEnv:    "ANTHROPIC_API_KEY",
				RunHomeRelPath: ".claude/.credentials.json",
				Sources: []AuthLocalSource{{
					Kind:    AuthLocalSourceKindMacOSKeychain,
					Service: "Claude Code-credentials",
				}},
			}},
		},
		Run: RunSpec{PrepareHook: HookRef{Path: "hooks/run.sh"}},
	})
	if err != nil {
		t.Fatalf("expected macos_keychain source to validate, got %v", err)
	}
}

func TestValidateManifestAllowsProviderAwareAuth(t *testing.T) {
	t.Parallel()

	err := ValidateManifest(Manifest{
		Kind: "agent_definition",
		Name: "fixture-agent",
		Auth: AuthSpec{
			ProviderSelection: &AuthProviderSelection{
				DirectInputField:              "provider",
				UnifiedModelProviderQualified: true,
			},
			Providers: []AuthProvider{
				{Name: "openai", RequiredEnv: []string{"OPENAI_API_KEY"}},
				{Name: "anthropic", RequiredEnv: []string{"ANTHROPIC_API_KEY"}},
			},
		},
		Run: RunSpec{PrepareHook: HookRef{Path: "hooks/run.sh"}},
	})
	if err != nil {
		t.Fatalf("ValidateManifest() error = %v", err)
	}

	err = ValidateManifest(Manifest{
		Kind: "agent_definition",
		Name: "fixture-agent",
		Auth: AuthSpec{
			ProviderSelection: &AuthProviderSelection{
				DirectInputField:              "provider",
				UnifiedModelProviderQualified: true,
			},
			Providers: []AuthProvider{
				{Name: "openai", RequiredEnv: []string{"OPENAI_API_KEY"}},
				{Name: "*", AuthMode: AuthProviderModeNone},
			},
		},
		Run: RunSpec{PrepareHook: HookRef{Path: "hooks/run.sh"}},
	})
	if err != nil {
		t.Fatalf("ValidateManifest() with wildcard none error = %v", err)
	}
}

func TestValidateManifestRejectsMixedStaticAndProviderAwareAuth(t *testing.T) {
	t.Parallel()

	err := ValidateManifest(Manifest{
		Kind: "agent_definition",
		Name: "fixture-agent",
		Auth: AuthSpec{
			RequiredEnv: []string{"OPENAI_API_KEY"},
			ProviderSelection: &AuthProviderSelection{
				DirectInputField:              "provider",
				UnifiedModelProviderQualified: true,
			},
			Providers: []AuthProvider{
				{Name: "openai", RequiredEnv: []string{"OPENAI_API_KEY"}},
			},
		},
		Run: RunSpec{PrepareHook: HookRef{Path: "hooks/run.sh"}},
	})
	if err == nil || !strings.Contains(err.Error(), "auth.required_env must not be set when auth.providers is used") {
		t.Fatalf("expected mixed auth validation error, got %v", err)
	}

	err = ValidateManifest(Manifest{
		Kind: "agent_definition",
		Name: "fixture-agent",
		Auth: AuthSpec{
			LocalCredentials: []AuthLocalCredential{{
				RequiredEnv:    "OPENAI_API_KEY",
				RunHomeRelPath: ".codex/auth.json",
				Sources: []AuthLocalSource{{
					Kind:        AuthLocalSourceKindHomeFile,
					HomeRelPath: ".codex/auth.json",
				}},
			}},
			ProviderSelection: &AuthProviderSelection{
				DirectInputField:              "provider",
				UnifiedModelProviderQualified: true,
			},
			Providers: []AuthProvider{
				{Name: "openai", RequiredEnv: []string{"OPENAI_API_KEY"}},
			},
		},
		Run: RunSpec{PrepareHook: HookRef{Path: "hooks/run.sh"}},
	})
	if err == nil || !strings.Contains(err.Error(), "auth.local_credentials must not be set when auth.providers is used") {
		t.Fatalf("expected provider auth local credential validation error, got %v", err)
	}

	err = ValidateManifest(Manifest{
		Kind: "agent_definition",
		Name: "fixture-agent",
		Auth: AuthSpec{
			ProviderSelection: &AuthProviderSelection{
				DirectInputField:              "provider",
				UnifiedModelProviderQualified: true,
			},
			Providers: []AuthProvider{
				{Name: "*", AuthMode: AuthProviderModeNone, RequiredEnv: []string{"OPENAI_API_KEY"}},
			},
		},
		Run: RunSpec{PrepareHook: HookRef{Path: "hooks/run.sh"}},
	})
	if err == nil || !strings.Contains(err.Error(), `must be empty when auth_mode="none"`) {
		t.Fatalf("expected wildcard none required_env validation error, got %v", err)
	}
}

func TestResolveRequiredEnvForProviderAwareConfig(t *testing.T) {
	t.Parallel()

	definition := testDefinitionSnapshot(t, Manifest{
		Kind: "agent_definition",
		Name: "fixture-agent",
		Auth: AuthSpec{
			ProviderSelection: &AuthProviderSelection{
				DirectInputField:              "provider",
				UnifiedModelProviderQualified: true,
			},
			Providers: []AuthProvider{
				{Name: "openai", RequiredEnv: []string{"OPENAI_API_KEY"}},
				{Name: "anthropic", RequiredEnv: []string{"ANTHROPIC_API_KEY"}},
			},
		},
		Config: DefinitionConfigSpec{
			SchemaRelPath: "schema.json",
			Unified: &UnifiedManifestSpec{
				TranslateHook:          HookRef{Path: "hooks/translate.sh"},
				AllowedModels:          []string{"*"},
				AllowedReasoningLevels: []string{"medium"},
			},
		},
		Run: RunSpec{PrepareHook: HookRef{Path: "hooks/run.sh"}},
	}, map[string]string{
		"hooks/run.sh":       "#!/usr/bin/env bash\nprintf '{}\\n'\n",
		"hooks/translate.sh": "#!/usr/bin/env bash\nprintf '{}\\n'\n",
		"schema.json": `{
  "type": "object",
  "required": ["provider"],
  "additionalProperties": true,
  "properties": {
    "provider": {"type": "string", "minLength": 1}
  }
}`,
	})

	direct, err := ValidateAndNormalizeConfigSpec(definition, ConfigSpec{
		Name: "fixture-direct",
		Mode: ConfigModeDirect,
		Input: map[string]any{
			"provider": "anthropic",
		},
	})
	if err != nil {
		t.Fatalf("ValidateAndNormalizeConfigSpec(direct) error = %v", err)
	}
	required, err := ResolveRequiredEnvForConfigSpec(definition, direct)
	if err != nil {
		t.Fatalf("ResolveRequiredEnvForConfigSpec(direct) error = %v", err)
	}
	if !slices.Equal(required, []string{"ANTHROPIC_API_KEY"}) {
		t.Fatalf("required env = %v", required)
	}

	unified, err := ValidateAndNormalizeConfigSpec(definition, ConfigSpec{
		Name: "fixture-unified",
		Mode: ConfigModeUnified,
		Unified: &UnifiedSpec{
			Model:          "openai/gpt-5",
			ReasoningLevel: "medium",
		},
	})
	if err != nil {
		t.Fatalf("ValidateAndNormalizeConfigSpec(unified) error = %v", err)
	}
	required, err = ResolveRequiredEnvForConfigSpec(definition, unified)
	if err != nil {
		t.Fatalf("ResolveRequiredEnvForConfigSpec(unified) error = %v", err)
	}
	if !slices.Equal(required, []string{"OPENAI_API_KEY"}) {
		t.Fatalf("required env = %v", required)
	}
}

func TestValidateAndNormalizeConfigSpecRejectsUnknownProvider(t *testing.T) {
	t.Parallel()

	definition := testDefinitionSnapshot(t, Manifest{
		Kind: "agent_definition",
		Name: "fixture-agent",
		Auth: AuthSpec{
			ProviderSelection: &AuthProviderSelection{
				DirectInputField:              "provider",
				UnifiedModelProviderQualified: true,
			},
			Providers: []AuthProvider{
				{Name: "openai", RequiredEnv: []string{"OPENAI_API_KEY"}},
			},
		},
		Config: DefinitionConfigSpec{
			SchemaRelPath: "schema.json",
			Unified: &UnifiedManifestSpec{
				TranslateHook:          HookRef{Path: "hooks/translate.sh"},
				AllowedModels:          []string{"*"},
				AllowedReasoningLevels: []string{"medium"},
			},
		},
		Run: RunSpec{PrepareHook: HookRef{Path: "hooks/run.sh"}},
	}, map[string]string{
		"hooks/run.sh":       "#!/usr/bin/env bash\nprintf '{}\\n'\n",
		"hooks/translate.sh": "#!/usr/bin/env bash\nprintf '{}\\n'\n",
		"schema.json": `{
  "type": "object",
  "required": ["provider"],
  "additionalProperties": true,
  "properties": {
    "provider": {"type": "string", "minLength": 1}
  }
}`,
	})

	_, err := ValidateAndNormalizeConfigSpec(definition, ConfigSpec{
		Name: "fixture-direct",
		Mode: ConfigModeDirect,
		Input: map[string]any{
			"provider": "google",
		},
	})
	if err == nil || !strings.Contains(err.Error(), `config provider "google" is not declared`) {
		t.Fatalf("expected unknown provider error, got %v", err)
	}

	_, err = ValidateAndNormalizeConfigSpec(definition, ConfigSpec{
		Name: "fixture-unified",
		Mode: ConfigModeUnified,
		Unified: &UnifiedSpec{
			Model:          "google/gemini-2.5-pro",
			ReasoningLevel: "medium",
		},
	})
	if err == nil || !strings.Contains(err.Error(), `config provider "google" is not declared`) {
		t.Fatalf("expected unknown provider error, got %v", err)
	}
}

func TestResolveRequiredEnvForProviderAwareConfigUsesWildcardNoAuthFallback(t *testing.T) {
	t.Parallel()

	definition := testDefinitionSnapshot(t, Manifest{
		Kind: "agent_definition",
		Name: "fixture-agent",
		Auth: AuthSpec{
			ProviderSelection: &AuthProviderSelection{
				DirectInputField:              "provider",
				UnifiedModelProviderQualified: true,
			},
			Providers: []AuthProvider{
				{Name: "openai", RequiredEnv: []string{"OPENAI_API_KEY"}},
				{Name: "*", AuthMode: AuthProviderModeNone},
			},
		},
		Config: DefinitionConfigSpec{
			SchemaRelPath: "schema.json",
			Unified: &UnifiedManifestSpec{
				TranslateHook:          HookRef{Path: "hooks/translate.sh"},
				AllowedModels:          []string{"*"},
				AllowedReasoningLevels: []string{"medium"},
			},
		},
		Run: RunSpec{PrepareHook: HookRef{Path: "hooks/run.sh"}},
	}, map[string]string{
		"hooks/run.sh":       "#!/usr/bin/env bash\nprintf '{}\\n'\n",
		"hooks/translate.sh": "#!/usr/bin/env bash\nprintf '{}\\n'\n",
		"schema.json": `{
  "type": "object",
  "required": ["provider"],
  "additionalProperties": true,
  "properties": {
    "provider": {"type": "string", "minLength": 1}
  }
}`,
	})

	direct, err := ValidateAndNormalizeConfigSpec(definition, ConfigSpec{
		Name: "fixture-direct",
		Mode: ConfigModeDirect,
		Input: map[string]any{
			"provider": "local-ollama",
		},
	})
	if err != nil {
		t.Fatalf("ValidateAndNormalizeConfigSpec(direct) error = %v", err)
	}
	required, err := ResolveRequiredEnvForConfigSpec(definition, direct)
	if err != nil {
		t.Fatalf("ResolveRequiredEnvForConfigSpec(direct) error = %v", err)
	}
	if len(required) != 0 {
		t.Fatalf("required env = %v, want none", required)
	}

	unified, err := ValidateAndNormalizeConfigSpec(definition, ConfigSpec{
		Name: "fixture-unified",
		Mode: ConfigModeUnified,
		Unified: &UnifiedSpec{
			Model:          "my-local/qwen-coder",
			ReasoningLevel: "medium",
		},
	})
	if err != nil {
		t.Fatalf("ValidateAndNormalizeConfigSpec(unified) error = %v", err)
	}
	required, err = ResolveRequiredEnvForConfigSpec(definition, unified)
	if err != nil {
		t.Fatalf("ResolveRequiredEnvForConfigSpec(unified) error = %v", err)
	}
	if len(required) != 0 {
		t.Fatalf("required env = %v, want none", required)
	}
}

func TestResolveRequiredEnvForProviderAwareConfigPrefersExactProviderOverWildcard(t *testing.T) {
	t.Parallel()

	definition := testDefinitionSnapshot(t, Manifest{
		Kind: "agent_definition",
		Name: "fixture-agent",
		Auth: AuthSpec{
			ProviderSelection: &AuthProviderSelection{
				DirectInputField:              "provider",
				UnifiedModelProviderQualified: true,
			},
			Providers: []AuthProvider{
				{Name: "openai", RequiredEnv: []string{"OPENAI_API_KEY"}},
				{Name: "*", AuthMode: AuthProviderModeNone},
			},
		},
		Config: DefinitionConfigSpec{
			SchemaRelPath: "schema.json",
		},
		Run: RunSpec{PrepareHook: HookRef{Path: "hooks/run.sh"}},
	}, map[string]string{
		"hooks/run.sh": "#!/usr/bin/env bash\nprintf '{}\\n'\n",
		"schema.json": `{
  "type": "object",
  "required": ["provider"],
  "additionalProperties": true,
  "properties": {
    "provider": {"type": "string", "minLength": 1}
  }
}`,
	})

	direct, err := ValidateAndNormalizeConfigSpec(definition, ConfigSpec{
		Name: "fixture-direct",
		Mode: ConfigModeDirect,
		Input: map[string]any{
			"provider": "openai",
		},
	})
	if err != nil {
		t.Fatalf("ValidateAndNormalizeConfigSpec() error = %v", err)
	}
	required, err := ResolveRequiredEnvForConfigSpec(definition, direct)
	if err != nil {
		t.Fatalf("ResolveRequiredEnvForConfigSpec() error = %v", err)
	}
	if !slices.Equal(required, []string{"OPENAI_API_KEY"}) {
		t.Fatalf("required env = %v", required)
	}
}

func TestValidateAndNormalizeConfigResourceSpecPreservesAgentsMDContent(t *testing.T) {
	t.Parallel()

	spec, err := ValidateAndNormalizeConfigResourceSpec(ConfigResourceSpec{
		Name: "fixture-direct",
		DefinitionRef: DefinitionRef{
			ResourceID: "res_agent_definition",
			Version:    1,
		},
		Mode: ConfigModeDirect,
		AgentsMD: &AgentsMDSpec{
			Content: "",
		},
		Input: map[string]any{"command": "echo hello"},
	})
	if err != nil {
		t.Fatalf("ValidateAndNormalizeConfigResourceSpec() error = %v", err)
	}
	if spec.AgentsMD == nil {
		t.Fatalf("agents_md missing")
	}
	if spec.AgentsMD.Content != "" {
		t.Fatalf("agents_md.content = %q", spec.AgentsMD.Content)
	}
}

func testDefinitionSnapshot(t *testing.T, manifest Manifest, files map[string]string) DefinitionSnapshot {
	t.Helper()

	root := t.TempDir()
	for relPath, body := range files {
		fullPath := filepath.Join(root, filepath.FromSlash(relPath))
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", relPath, err)
		}
		if err := os.WriteFile(fullPath, []byte(body), 0o755); err != nil {
			t.Fatalf("write %s: %v", relPath, err)
		}
	}
	pkg, err := testassets.PackDir(root)
	if err != nil {
		t.Fatalf("pack dir: %v", err)
	}
	return DefinitionSnapshot{
		Manifest: manifest,
		Package:  pkg,
	}
}
