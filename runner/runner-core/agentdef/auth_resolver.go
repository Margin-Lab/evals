package agentdef

import (
	"fmt"
	"sort"
	"strings"
)

func UsesProviderAuth(auth AuthSpec) bool {
	return auth.ProviderSelection != nil || len(auth.Providers) > 0
}

func ResolveRequiredEnvForConfigSpec(definition DefinitionSnapshot, config ConfigSpec) ([]string, error) {
	return resolveRequiredEnvForConfig(definition.Manifest, config.Mode, config.Input, config.Unified)
}

func ResolveRequiredEnvForConfigSnapshot(definition DefinitionSnapshot, config ConfigSnapshot) ([]string, error) {
	return resolveRequiredEnvForConfig(definition.Manifest, config.Mode, config.Input, config.Unified)
}

func ResolveDefinitionRequiredEnv(definition DefinitionSnapshot) []string {
	if UsesProviderAuth(definition.Manifest.Auth) {
		return nil
	}
	return append([]string(nil), definition.Manifest.Auth.RequiredEnv...)
}

func resolveRequiredEnvForConfig(manifest Manifest, mode ConfigMode, input map[string]any, unified *UnifiedSpec) ([]string, error) {
	if !UsesProviderAuth(manifest.Auth) {
		return append([]string(nil), manifest.Auth.RequiredEnv...), nil
	}

	provider, err := resolveProviderName(manifest, mode, input, unified)
	if err != nil {
		return nil, err
	}
	candidate, ok := findAuthProvider(manifest.Auth.Providers, provider)
	if !ok {
		return nil, fmt.Errorf("config provider %q is not declared by the selected agent definition", provider)
	}
	modeName := AuthProviderMode(strings.ToLower(strings.TrimSpace(string(candidate.AuthMode))))
	if modeName == "" {
		modeName = AuthProviderModeEnv
	}
	switch modeName {
	case AuthProviderModeNone:
		return nil, nil
	case AuthProviderModeEnv:
		out := append([]string(nil), candidate.RequiredEnv...)
		sort.Strings(out)
		return out, nil
	default:
		return nil, fmt.Errorf("config provider %q uses unsupported auth_mode %q", provider, candidate.AuthMode)
	}
}

func ResolveProviderForConfigSpec(definition DefinitionSnapshot, config ConfigSpec) (string, error) {
	return resolveProviderName(definition.Manifest, config.Mode, config.Input, config.Unified)
}

func ResolveProviderForConfigSnapshot(definition DefinitionSnapshot, config ConfigSnapshot) (string, error) {
	return resolveProviderName(definition.Manifest, config.Mode, config.Input, config.Unified)
}

func resolveProviderName(manifest Manifest, mode ConfigMode, input map[string]any, unified *UnifiedSpec) (string, error) {
	if !UsesProviderAuth(manifest.Auth) {
		return "", nil
	}
	selection := manifest.Auth.ProviderSelection
	if selection == nil {
		return "", fmt.Errorf("selected agent definition is missing auth.provider_selection")
	}

	switch mode {
	case ConfigModeDirect:
		field := strings.TrimSpace(selection.DirectInputField)
		if field == "" {
			return "", fmt.Errorf("selected agent definition is missing auth.provider_selection.direct_input_field")
		}
		value, ok := input[field]
		if !ok {
			return "", fmt.Errorf("config.input.%s is required for provider-aware auth", field)
		}
		provider := strings.TrimSpace(fmt.Sprint(value))
		if provider == "" {
			return "", fmt.Errorf("config.input.%s is required for provider-aware auth", field)
		}
		return provider, nil
	case ConfigModeUnified:
		if !selection.UnifiedModelProviderQualified {
			return "", fmt.Errorf("selected agent definition does not support provider-qualified config.unified.model")
		}
		if unified == nil {
			return "", fmt.Errorf("config.unified is required for provider-aware auth")
		}
		provider, _, err := splitProviderQualifiedModel(unified.Model)
		if err != nil {
			return "", fmt.Errorf("config.unified.model: %w", err)
		}
		return provider, nil
	default:
		return "", fmt.Errorf("config.mode must be %q or %q", ConfigModeDirect, ConfigModeUnified)
	}
}

func splitProviderQualifiedModel(raw string) (string, string, error) {
	trimmed := strings.TrimSpace(raw)
	slash := strings.Index(trimmed, "/")
	if slash <= 0 || slash == len(trimmed)-1 {
		return "", "", fmt.Errorf("must be provider-qualified as provider/model")
	}
	provider := strings.TrimSpace(trimmed[:slash])
	model := strings.TrimSpace(trimmed[slash+1:])
	if provider == "" || model == "" {
		return "", "", fmt.Errorf("must be provider-qualified as provider/model")
	}
	return provider, model, nil
}

func findAuthProvider(providers []AuthProvider, provider string) (AuthProvider, bool) {
	var wildcard AuthProvider
	wildcardFound := false
	for _, candidate := range providers {
		name := strings.TrimSpace(candidate.Name)
		if name == "*" {
			wildcard = candidate
			wildcardFound = true
			continue
		}
		if strings.EqualFold(name, provider) {
			return candidate, true
		}
	}
	if wildcardFound {
		return wildcard, true
	}
	return AuthProvider{}, false
}
