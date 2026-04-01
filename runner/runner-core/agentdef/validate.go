package agentdef

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v5"

	"github.com/marginlab/margin-eval/runner/runner-core/testassets"
)

var envNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

const (
	agentsMDFilenameAgents = "AGENTS.md"
	agentsMDFilenameClaude = "CLAUDE.md"
)

func ValidateManifest(manifest Manifest) error {
	if strings.TrimSpace(manifest.Kind) != "agent_definition" {
		return fmt.Errorf("kind must be %q", "agent_definition")
	}
	if strings.TrimSpace(manifest.Name) == "" {
		return fmt.Errorf("name is required")
	}
	if UsesProviderAuth(manifest.Auth) {
		if err := validateProviderAwareAuth(manifest.Auth); err != nil {
			return err
		}
	} else {
		if err := validateStaticAuth(manifest.Auth); err != nil {
			return err
		}
	}
	if err := validateHookRef("run.prepare_hook", manifest.Run.PrepareHook); err != nil {
		return err
	}
	if manifest.Config.ValidateHook != nil {
		if err := validateHookRef("config.validate_hook", *manifest.Config.ValidateHook); err != nil {
			return err
		}
	}
	if manifest.Install.CheckHook != nil {
		if err := validateHookRef("install.check_hook", *manifest.Install.CheckHook); err != nil {
			return err
		}
	}
	if manifest.Install.RunHook != nil {
		if err := validateHookRef("install.run_hook", *manifest.Install.RunHook); err != nil {
			return err
		}
	}
	if manifest.Snapshot != nil {
		if err := validateHookRef("snapshot.prepare_hook", manifest.Snapshot.PrepareHook); err != nil {
			return err
		}
	}
	if manifest.Trajectory != nil {
		if err := validateHookRef("trajectory.collect_hook", manifest.Trajectory.CollectHook); err != nil {
			return err
		}
	}
	if strings.TrimSpace(manifest.Config.SchemaRelPath) != "" {
		if _, err := sanitizeRelPath(manifest.Config.SchemaRelPath); err != nil {
			return fmt.Errorf("config.schema_rel_path: %w", err)
		}
	}
	if manifest.Skills != nil {
		if _, err := sanitizeRelPath(manifest.Skills.HomeRelDir); err != nil {
			return fmt.Errorf("skills.home_rel_dir: %w", err)
		}
	}
	if manifest.AgentsMD != nil {
		filename := strings.TrimSpace(manifest.AgentsMD.Filename)
		switch filename {
		case agentsMDFilenameAgents, agentsMDFilenameClaude:
		default:
			return fmt.Errorf("agents_md.filename must be %q or %q", agentsMDFilenameAgents, agentsMDFilenameClaude)
		}
	}
	if manifest.Config.Unified != nil {
		if err := validateHookRef("config.unified.translate_hook", manifest.Config.Unified.TranslateHook); err != nil {
			return err
		}
		if err := validateManifestAllowedValues("config.unified.allowed_models", manifest.Config.Unified.AllowedModels); err != nil {
			return err
		}
		if err := validateManifestAllowedValues("config.unified.allowed_reasoning_levels", manifest.Config.Unified.AllowedReasoningLevels); err != nil {
			return err
		}
	}
	if err := validateToolchains(manifest.Toolchains); err != nil {
		return err
	}
	return nil
}

func validateStaticAuth(auth AuthSpec) error {
	requiredEnv := make(map[string]struct{}, len(auth.RequiredEnv))
	for i, name := range auth.RequiredEnv {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			return fmt.Errorf("auth.required_env[%d] is required", i)
		}
		if !envNamePattern.MatchString(trimmed) {
			return fmt.Errorf("auth.required_env[%d] must be a valid env name", i)
		}
		requiredEnv[trimmed] = struct{}{}
	}
	localCredentialsByEnv := make(map[string]struct{}, len(auth.LocalCredentials))
	localCredentialsByTarget := make(map[string]struct{}, len(auth.LocalCredentials))
	for i, credential := range auth.LocalCredentials {
		trimmedEnv := strings.TrimSpace(credential.RequiredEnv)
		if trimmedEnv == "" {
			return fmt.Errorf("auth.local_credentials[%d].required_env is required", i)
		}
		if !envNamePattern.MatchString(trimmedEnv) {
			return fmt.Errorf("auth.local_credentials[%d].required_env must be a valid env name", i)
		}
		if _, ok := requiredEnv[trimmedEnv]; !ok {
			return fmt.Errorf("auth.local_credentials[%d].required_env %q must reference auth.required_env", i, trimmedEnv)
		}
		if _, exists := localCredentialsByEnv[trimmedEnv]; exists {
			return fmt.Errorf("auth.local_credentials[%d].required_env %q must not be duplicated", i, trimmedEnv)
		}
		localCredentialsByEnv[trimmedEnv] = struct{}{}
		runHomeRelPath, err := sanitizeRelPath(credential.RunHomeRelPath)
		if err != nil {
			return fmt.Errorf("auth.local_credentials[%d].run_home_rel_path: %w", i, err)
		}
		if _, exists := localCredentialsByTarget[runHomeRelPath]; exists {
			return fmt.Errorf("auth.local_credentials[%d].run_home_rel_path %q must not be duplicated", i, runHomeRelPath)
		}
		localCredentialsByTarget[runHomeRelPath] = struct{}{}
		if err := validateJSONPath(credential.ValidateJSONPath); err != nil {
			return fmt.Errorf("auth.local_credentials[%d].validate_json_path: %w", i, err)
		}
		if len(credential.Sources) == 0 {
			return fmt.Errorf("auth.local_credentials[%d].sources must not be empty", i)
		}
		for sourceIdx, source := range credential.Sources {
			if err := validateAuthLocalSource(i, sourceIdx, source); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateProviderAwareAuth(auth AuthSpec) error {
	if len(auth.RequiredEnv) > 0 {
		return fmt.Errorf("auth.required_env must not be set when auth.providers is used")
	}
	if len(auth.LocalCredentials) > 0 {
		return fmt.Errorf("auth.local_credentials must not be set when auth.providers is used")
	}
	if auth.ProviderSelection == nil {
		return fmt.Errorf("auth.provider_selection is required when auth.providers is used")
	}
	if strings.TrimSpace(auth.ProviderSelection.DirectInputField) == "" {
		return fmt.Errorf("auth.provider_selection.direct_input_field is required when auth.providers is used")
	}
	if !auth.ProviderSelection.UnifiedModelProviderQualified {
		return fmt.Errorf("auth.provider_selection.unified_model_provider_qualified must be true when auth.providers is used")
	}
	if len(auth.Providers) == 0 {
		return fmt.Errorf("auth.providers must not be empty when auth.provider_selection is set")
	}
	seenProviders := make(map[string]struct{}, len(auth.Providers))
	for i, provider := range auth.Providers {
		name := strings.TrimSpace(provider.Name)
		if name == "" {
			return fmt.Errorf("auth.providers[%d].name is required", i)
		}
		if _, exists := seenProviders[name]; exists {
			return fmt.Errorf("auth.providers[%d].name %q must not be duplicated", i, name)
		}
		seenProviders[name] = struct{}{}
		mode := AuthProviderMode(strings.ToLower(strings.TrimSpace(string(provider.AuthMode))))
		if mode == "" {
			mode = AuthProviderModeEnv
		}
		switch mode {
		case AuthProviderModeEnv:
			if len(provider.RequiredEnv) == 0 {
				return fmt.Errorf("auth.providers[%d].required_env must not be empty when auth_mode=%q", i, AuthProviderModeEnv)
			}
			for envIdx, raw := range provider.RequiredEnv {
				trimmed := strings.TrimSpace(raw)
				if trimmed == "" {
					return fmt.Errorf("auth.providers[%d].required_env[%d] is required", i, envIdx)
				}
				if !envNamePattern.MatchString(trimmed) {
					return fmt.Errorf("auth.providers[%d].required_env[%d] must be a valid env name", i, envIdx)
				}
			}
		case AuthProviderModeNone:
			if len(provider.RequiredEnv) > 0 {
				return fmt.Errorf("auth.providers[%d].required_env must be empty when auth_mode=%q", i, AuthProviderModeNone)
			}
		default:
			return fmt.Errorf("auth.providers[%d].auth_mode %q is not supported", i, strings.TrimSpace(string(provider.AuthMode)))
		}
	}
	return nil
}

func validateAuthLocalSource(credentialIdx, sourceIdx int, source AuthLocalSource) error {
	switch AuthLocalSourceKind(strings.TrimSpace(string(source.Kind))) {
	case AuthLocalSourceKindHomeFile:
		if _, err := sanitizeRelPath(source.HomeRelPath); err != nil {
			return fmt.Errorf("auth.local_credentials[%d].sources[%d].home_rel_path: %w", credentialIdx, sourceIdx, err)
		}
		if strings.TrimSpace(source.Service) != "" {
			return fmt.Errorf("auth.local_credentials[%d].sources[%d].service is not allowed for kind %q", credentialIdx, sourceIdx, AuthLocalSourceKindHomeFile)
		}
	case AuthLocalSourceKindMacOSKeychain:
		if strings.TrimSpace(source.Service) == "" {
			return fmt.Errorf("auth.local_credentials[%d].sources[%d].service is required for kind %q", credentialIdx, sourceIdx, AuthLocalSourceKindMacOSKeychain)
		}
		if strings.TrimSpace(source.HomeRelPath) != "" {
			return fmt.Errorf("auth.local_credentials[%d].sources[%d].home_rel_path is not allowed for kind %q", credentialIdx, sourceIdx, AuthLocalSourceKindMacOSKeychain)
		}
	default:
		return fmt.Errorf("auth.local_credentials[%d].sources[%d].kind %q is not supported", credentialIdx, sourceIdx, strings.TrimSpace(string(source.Kind)))
	}
	for platformIdx, platform := range source.Platforms {
		if strings.TrimSpace(platform) == "" {
			return fmt.Errorf("auth.local_credentials[%d].sources[%d].platforms[%d] is required", credentialIdx, sourceIdx, platformIdx)
		}
	}
	return nil
}

func validateJSONPath(path string) error {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return nil
	}
	for idx, segment := range strings.Split(trimmed, ".") {
		if strings.TrimSpace(segment) == "" {
			return fmt.Errorf("segment %d is empty", idx)
		}
	}
	return nil
}

func ValidateDefinitionSnapshot(snapshot DefinitionSnapshot) error {
	if err := ValidateManifest(snapshot.Manifest); err != nil {
		return err
	}
	if err := testassets.ValidateDescriptor(snapshot.Package, testassets.DefaultMaxArchiveBytes); err != nil {
		return fmt.Errorf("package: %w", err)
	}
	paths, err := PackagePaths(snapshot.Package)
	if err != nil {
		return err
	}
	requiredPaths := make([]string, 0, 6)
	if strings.TrimSpace(snapshot.Manifest.Config.SchemaRelPath) != "" {
		requiredPaths = append(requiredPaths, mustSanitize(snapshot.Manifest.Config.SchemaRelPath))
	}
	requiredPaths = append(requiredPaths, mustSanitize(snapshot.Manifest.Run.PrepareHook.Path))
	if snapshot.Manifest.Config.ValidateHook != nil {
		requiredPaths = append(requiredPaths, mustSanitize(snapshot.Manifest.Config.ValidateHook.Path))
	}
	if snapshot.Manifest.Install.CheckHook != nil {
		requiredPaths = append(requiredPaths, mustSanitize(snapshot.Manifest.Install.CheckHook.Path))
	}
	if snapshot.Manifest.Install.RunHook != nil {
		requiredPaths = append(requiredPaths, mustSanitize(snapshot.Manifest.Install.RunHook.Path))
	}
	if snapshot.Manifest.Snapshot != nil {
		requiredPaths = append(requiredPaths, mustSanitize(snapshot.Manifest.Snapshot.PrepareHook.Path))
	}
	if snapshot.Manifest.Trajectory != nil {
		requiredPaths = append(requiredPaths, mustSanitize(snapshot.Manifest.Trajectory.CollectHook.Path))
	}
	if snapshot.Manifest.Config.Unified != nil {
		requiredPaths = append(requiredPaths, mustSanitize(snapshot.Manifest.Config.Unified.TranslateHook.Path))
	}
	for _, requiredPath := range requiredPaths {
		if !slices.Contains(paths, requiredPath) {
			return fmt.Errorf("definition package missing required path %q", requiredPath)
		}
	}
	return nil
}

func ValidateAndNormalizeConfigSpec(definition DefinitionSnapshot, config ConfigSpec) (ConfigSpec, error) {
	if err := ValidateDefinitionSnapshot(definition); err != nil {
		return ConfigSpec{}, err
	}
	normalized, err := normalizeConfigSpec(config)
	if err != nil {
		return ConfigSpec{}, err
	}
	if err := validateSkillsAgainstManifest(definition.Manifest, normalized.Skills); err != nil {
		return ConfigSpec{}, err
	}
	if err := validateAgentsMDAgainstManifest(definition.Manifest, normalized.AgentsMD); err != nil {
		return ConfigSpec{}, err
	}
	switch normalized.Mode {
	case ConfigModeDirect:
		if err := validateDirectInput(definition, normalized.Input); err != nil {
			return ConfigSpec{}, err
		}
	case ConfigModeUnified:
		if err := validateUnifiedAgainstManifest(definition.Manifest, *normalized.Unified); err != nil {
			return ConfigSpec{}, err
		}
	default:
		return ConfigSpec{}, fmt.Errorf("config.mode must be %q or %q", ConfigModeDirect, ConfigModeUnified)
	}
	if _, err := ResolveRequiredEnvForConfigSpec(definition, normalized); err != nil {
		return ConfigSpec{}, err
	}
	return normalized, nil
}

func ValidateAndNormalizeConfigSnapshot(definition DefinitionSnapshot, config ConfigSnapshot) (ConfigSnapshot, error) {
	if err := ValidateDefinitionSnapshot(definition); err != nil {
		return ConfigSnapshot{}, err
	}
	normalized, err := normalizeConfigSnapshot(config)
	if err != nil {
		return ConfigSnapshot{}, err
	}
	if err := validateSkillsAgainstManifest(definition.Manifest, normalized.Skills); err != nil {
		return ConfigSnapshot{}, err
	}
	if err := validateAgentsMDAgainstManifest(definition.Manifest, normalized.AgentsMD); err != nil {
		return ConfigSnapshot{}, err
	}
	if err := validateDirectInput(definition, normalized.Input); err != nil {
		return ConfigSnapshot{}, err
	}
	if normalized.Mode == ConfigModeUnified {
		if err := validateUnifiedAgainstManifest(definition.Manifest, *normalized.Unified); err != nil {
			return ConfigSnapshot{}, err
		}
	}
	if _, err := ResolveRequiredEnvForConfigSnapshot(definition, normalized); err != nil {
		return ConfigSnapshot{}, err
	}
	return normalized, nil
}

func validateToolchains(spec ToolchainSpec) error {
	if spec.Node == nil {
		return nil
	}
	node := spec.Node
	for fieldName, raw := range map[string]string{
		"minimum":   node.Minimum,
		"preferred": node.Preferred,
	} {
		value := strings.TrimSpace(raw)
		if value == "" {
			return fmt.Errorf("toolchains.node.%s is required", fieldName)
		}
		if strings.ContainsAny(value, " \t\r\n") {
			return fmt.Errorf("toolchains.node.%s must not contain whitespace", fieldName)
		}
		for _, ch := range value {
			if ch < '0' || ch > '9' {
				return fmt.Errorf("toolchains.node.%s must contain digits only", fieldName)
			}
		}
	}
	if compareNumericStrings(strings.TrimSpace(node.Preferred), strings.TrimSpace(node.Minimum)) < 0 {
		return fmt.Errorf("toolchains.node.preferred must be greater than or equal to toolchains.node.minimum")
	}
	return nil
}

func compareNumericStrings(left, right string) int {
	left = strings.TrimLeft(left, "0")
	right = strings.TrimLeft(right, "0")
	if left == "" {
		left = "0"
	}
	if right == "" {
		right = "0"
	}
	switch {
	case len(left) < len(right):
		return -1
	case len(left) > len(right):
		return 1
	case left < right:
		return -1
	case left > right:
		return 1
	default:
		return 0
	}
}

func ReadPackageFile(desc testassets.Descriptor, relPath string) ([]byte, error) {
	target, err := sanitizeRelPath(relPath)
	if err != nil {
		return nil, err
	}
	payload, err := testassets.DecodeAndValidate(desc, testassets.DefaultMaxArchiveBytes)
	if err != nil {
		return nil, err
	}
	gr, err := gzip.NewReader(bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("open package gzip: %w", err)
	}
	defer gr.Close()

	tr := tar.NewReader(gr)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read package archive: %w", err)
		}
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA {
			continue
		}
		name, err := sanitizeRelPath(header.Name)
		if err != nil {
			return nil, fmt.Errorf("invalid package path %q: %w", header.Name, err)
		}
		if name != target {
			continue
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			return nil, fmt.Errorf("read package file %s: %w", name, err)
		}
		return body, nil
	}
	return nil, fmt.Errorf("package file %q not found", target)
}

func PackagePaths(desc testassets.Descriptor) ([]string, error) {
	payload, err := testassets.DecodeAndValidate(desc, testassets.DefaultMaxArchiveBytes)
	if err != nil {
		return nil, err
	}
	gr, err := gzip.NewReader(bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("open package gzip: %w", err)
	}
	defer gr.Close()

	paths := make([]string, 0, 16)
	tr := tar.NewReader(gr)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read package archive: %w", err)
		}
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA {
			continue
		}
		name, err := sanitizeRelPath(header.Name)
		if err != nil {
			return nil, fmt.Errorf("invalid package path %q: %w", header.Name, err)
		}
		paths = append(paths, name)
	}
	return paths, nil
}

func ValidateAndNormalizeConfigResourceSpec(spec ConfigResourceSpec) (ConfigResourceSpec, error) {
	normalized := ConfigResourceSpec{
		Name:        strings.TrimSpace(spec.Name),
		Description: strings.TrimSpace(spec.Description),
		DefinitionRef: DefinitionRef{
			ResourceID: strings.TrimSpace(spec.DefinitionRef.ResourceID),
			Version:    spec.DefinitionRef.Version,
		},
		Mode:     spec.Mode,
		Skills:   cloneSkillSpecs(spec.Skills),
		AgentsMD: cloneAgentsMDSpec(spec.AgentsMD),
		Input:    cloneAnyMap(spec.Input),
	}
	if spec.Unified != nil {
		normalized.Unified = cloneUnifiedSpec(spec.Unified)
	}
	if normalized.Name == "" {
		return ConfigResourceSpec{}, fmt.Errorf("name is required")
	}
	if normalized.DefinitionRef.ResourceID == "" {
		return ConfigResourceSpec{}, fmt.Errorf("definition_ref.resource_id is required")
	}
	if normalized.DefinitionRef.Version <= 0 {
		return ConfigResourceSpec{}, fmt.Errorf("definition_ref.version must be > 0")
	}
	mode, err := normalizeConfigMode(normalized.Mode)
	if err != nil {
		return ConfigResourceSpec{}, err
	}
	normalized.Mode = mode
	skills, err := ValidateAndNormalizeSkillSpecs(normalized.Skills)
	if err != nil {
		return ConfigResourceSpec{}, err
	}
	normalized.Skills = skills
	switch normalized.Mode {
	case ConfigModeDirect:
		if normalized.Input == nil {
			return ConfigResourceSpec{}, fmt.Errorf("input is required when mode=%q", ConfigModeDirect)
		}
		if normalized.Unified != nil {
			return ConfigResourceSpec{}, fmt.Errorf("unified must not be set when mode=%q", ConfigModeDirect)
		}
	case ConfigModeUnified:
		if normalized.Unified == nil {
			return ConfigResourceSpec{}, fmt.Errorf("unified is required when mode=%q", ConfigModeUnified)
		}
		if normalized.Input != nil {
			return ConfigResourceSpec{}, fmt.Errorf("input must not be set when mode=%q", ConfigModeUnified)
		}
		unified, err := ValidateAndNormalizeUnifiedSpec(*normalized.Unified)
		if err != nil {
			return ConfigResourceSpec{}, fmt.Errorf("unified: %w", err)
		}
		normalized.Unified = &unified
	default:
		return ConfigResourceSpec{}, fmt.Errorf("mode must be %q or %q", ConfigModeDirect, ConfigModeUnified)
	}
	return normalized, nil
}

func validateHookRef(name string, ref HookRef) error {
	if strings.TrimSpace(ref.Path) == "" {
		return fmt.Errorf("%s.path is required", name)
	}
	if _, err := sanitizeRelPath(ref.Path); err != nil {
		return fmt.Errorf("%s.path: %w", name, err)
	}
	return nil
}

func sanitizeRelPath(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", fmt.Errorf("relative path is required")
	}
	if strings.Contains(trimmed, "\\") {
		return "", fmt.Errorf("path must use slash-separated relative paths")
	}
	cleaned := path.Clean(trimmed)
	if strings.HasPrefix(cleaned, "/") {
		return "", fmt.Errorf("path must be relative")
	}
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("path must not escape the package root")
	}
	return cleaned, nil
}

func mustSanitize(value string) string {
	cleaned, err := sanitizeRelPath(value)
	if err != nil {
		panic(err)
	}
	return cleaned
}

func validateInputAgainstSchema(schemaBytes []byte, input map[string]any) error {
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("schema.json", bytes.NewReader(schemaBytes)); err != nil {
		return fmt.Errorf("compile schema resource: %w", err)
	}
	schema, err := compiler.Compile("schema.json")
	if err != nil {
		return fmt.Errorf("compile schema: %w", err)
	}
	payload, err := json.Marshal(input)
	if err != nil {
		return fmt.Errorf("marshal input: %w", err)
	}
	var normalized any
	if err := json.Unmarshal(payload, &normalized); err != nil {
		return fmt.Errorf("normalize input: %w", err)
	}
	if err := schema.Validate(normalized); err != nil {
		return err
	}
	return nil
}

func normalizeConfigSpec(config ConfigSpec) (ConfigSpec, error) {
	normalized := ConfigSpec{
		Name:        strings.TrimSpace(config.Name),
		Description: strings.TrimSpace(config.Description),
		Mode:        config.Mode,
		Skills:      cloneSkillSpecs(config.Skills),
		AgentsMD:    cloneAgentsMDSpec(config.AgentsMD),
		Input:       cloneAnyMap(config.Input),
	}
	if config.Unified != nil {
		normalized.Unified = cloneUnifiedSpec(config.Unified)
	}
	if normalized.Name == "" {
		return ConfigSpec{}, fmt.Errorf("config.name is required")
	}
	mode, err := normalizeConfigMode(normalized.Mode)
	if err != nil {
		return ConfigSpec{}, err
	}
	normalized.Mode = mode
	skills, err := ValidateAndNormalizeSkillSpecs(normalized.Skills)
	if err != nil {
		return ConfigSpec{}, err
	}
	normalized.Skills = skills
	switch normalized.Mode {
	case ConfigModeDirect:
		if normalized.Input == nil {
			return ConfigSpec{}, fmt.Errorf("config.input is required when config.mode=%q", ConfigModeDirect)
		}
		if normalized.Unified != nil {
			return ConfigSpec{}, fmt.Errorf("config.unified must not be set when config.mode=%q", ConfigModeDirect)
		}
	case ConfigModeUnified:
		if normalized.Unified == nil {
			return ConfigSpec{}, fmt.Errorf("config.unified is required when config.mode=%q", ConfigModeUnified)
		}
		if normalized.Input != nil {
			return ConfigSpec{}, fmt.Errorf("config.input must not be set when config.mode=%q", ConfigModeUnified)
		}
		unified, err := ValidateAndNormalizeUnifiedSpec(*normalized.Unified)
		if err != nil {
			return ConfigSpec{}, fmt.Errorf("config.unified: %w", err)
		}
		normalized.Unified = &unified
	default:
		return ConfigSpec{}, fmt.Errorf("config.mode must be %q or %q", ConfigModeDirect, ConfigModeUnified)
	}
	return normalized, nil
}

func normalizeConfigSnapshot(config ConfigSnapshot) (ConfigSnapshot, error) {
	normalized := ConfigSnapshot{
		Name:        strings.TrimSpace(config.Name),
		Description: strings.TrimSpace(config.Description),
		Mode:        config.Mode,
		Skills:      cloneSkillSpecs(config.Skills),
		AgentsMD:    cloneAgentsMDSpec(config.AgentsMD),
		Input:       cloneAnyMap(config.Input),
	}
	if config.Unified != nil {
		normalized.Unified = cloneUnifiedSpec(config.Unified)
	}
	if normalized.Name == "" {
		return ConfigSnapshot{}, fmt.Errorf("config.name is required")
	}
	mode, err := normalizeConfigMode(normalized.Mode)
	if err != nil {
		return ConfigSnapshot{}, err
	}
	normalized.Mode = mode
	skills, err := ValidateAndNormalizeSkillSpecs(normalized.Skills)
	if err != nil {
		return ConfigSnapshot{}, err
	}
	normalized.Skills = skills
	switch normalized.Mode {
	case ConfigModeDirect:
		if normalized.Input == nil {
			return ConfigSnapshot{}, fmt.Errorf("config.input is required when config.mode=%q", ConfigModeDirect)
		}
		if normalized.Unified != nil {
			return ConfigSnapshot{}, fmt.Errorf("config.unified must not be set when config.mode=%q", ConfigModeDirect)
		}
	case ConfigModeUnified:
		if normalized.Input == nil {
			return ConfigSnapshot{}, fmt.Errorf("config.input is required when config.mode=%q", ConfigModeUnified)
		}
		if normalized.Unified == nil {
			return ConfigSnapshot{}, fmt.Errorf("config.unified is required when config.mode=%q", ConfigModeUnified)
		}
		unified, err := ValidateAndNormalizeUnifiedSpec(*normalized.Unified)
		if err != nil {
			return ConfigSnapshot{}, fmt.Errorf("config.unified: %w", err)
		}
		normalized.Unified = &unified
	default:
		return ConfigSnapshot{}, fmt.Errorf("config.mode must be %q or %q", ConfigModeDirect, ConfigModeUnified)
	}
	return normalized, nil
}

func normalizeConfigMode(mode ConfigMode) (ConfigMode, error) {
	normalized := ConfigMode(strings.ToLower(strings.TrimSpace(string(mode))))
	switch normalized {
	case ConfigModeDirect, ConfigModeUnified:
		return normalized, nil
	default:
		return "", fmt.Errorf("config.mode must be %q or %q", ConfigModeDirect, ConfigModeUnified)
	}
}

func validateDirectInput(definition DefinitionSnapshot, input map[string]any) error {
	if input == nil {
		return fmt.Errorf("config.input is required")
	}
	if strings.TrimSpace(definition.Manifest.Config.SchemaRelPath) == "" {
		return nil
	}
	schemaBytes, err := ReadPackageFile(definition.Package, definition.Manifest.Config.SchemaRelPath)
	if err != nil {
		return fmt.Errorf("load config schema: %w", err)
	}
	if err := validateInputAgainstSchema(schemaBytes, input); err != nil {
		return fmt.Errorf("config.input: %w", err)
	}
	return nil
}

func validateUnifiedAgainstManifest(manifest Manifest, spec UnifiedSpec) error {
	if manifest.Config.Unified == nil {
		return fmt.Errorf("selected agent definition does not support unified config")
	}
	if !allowedByManifest(manifest.Config.Unified.AllowedModels, spec.Model) {
		return fmt.Errorf("config.unified.model %q is not allowed by the selected agent definition", spec.Model)
	}
	if !allowedByManifest(manifest.Config.Unified.AllowedReasoningLevels, spec.ReasoningLevel) {
		return fmt.Errorf("config.unified.reasoning_level %q is not allowed by the selected agent definition", spec.ReasoningLevel)
	}
	return nil
}

func validateSkillsAgainstManifest(manifest Manifest, skills []SkillSpec) error {
	if len(skills) == 0 {
		return nil
	}
	if manifest.Skills == nil {
		return fmt.Errorf("selected agent definition does not support skills")
	}
	if _, err := sanitizeRelPath(manifest.Skills.HomeRelDir); err != nil {
		return fmt.Errorf("selected agent definition has invalid skills.home_rel_dir: %w", err)
	}
	return nil
}

func validateAgentsMDAgainstManifest(manifest Manifest, spec *AgentsMDSpec) error {
	if spec == nil {
		return nil
	}
	if manifest.AgentsMD == nil {
		return fmt.Errorf("selected agent definition does not support agents_md")
	}
	filename := strings.TrimSpace(manifest.AgentsMD.Filename)
	switch filename {
	case agentsMDFilenameAgents, agentsMDFilenameClaude:
		return nil
	default:
		return fmt.Errorf("selected agent definition has invalid agents_md.filename %q", filename)
	}
}

func validateManifestAllowedValues(name string, values []string) error {
	if len(values) == 0 {
		return fmt.Errorf("%s must contain at least one value", name)
	}
	seenWildcard := false
	for idx, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			return fmt.Errorf("%s[%d] is required", name, idx)
		}
		if trimmed == "*" {
			seenWildcard = true
		}
	}
	if seenWildcard && len(values) > 1 {
		return fmt.Errorf("%s must not combine \"*\" with specific values", name)
	}
	return nil
}

func allowedByManifest(values []string, candidate string) bool {
	if len(values) == 1 && strings.TrimSpace(values[0]) == "*" {
		return true
	}
	for _, value := range values {
		if strings.TrimSpace(value) == candidate {
			return true
		}
	}
	return false
}

func cloneAnyMap(input map[string]any) map[string]any {
	if input == nil {
		return nil
	}
	body, err := json.Marshal(input)
	if err != nil {
		out := make(map[string]any, len(input))
		for key, value := range input {
			out[key] = value
		}
		return out
	}
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		out = make(map[string]any, len(input))
		for key, value := range input {
			out[key] = value
		}
	}
	return out
}

func cloneSkillSpecs(input []SkillSpec) []SkillSpec {
	if len(input) == 0 {
		return nil
	}
	out := make([]SkillSpec, len(input))
	copy(out, input)
	return out
}

func cloneAgentsMDSpec(spec *AgentsMDSpec) *AgentsMDSpec {
	if spec == nil {
		return nil
	}
	copy := *spec
	return &copy
}

func cloneUnifiedSpec(spec *UnifiedSpec) *UnifiedSpec {
	if spec == nil {
		return nil
	}
	body, err := json.Marshal(spec)
	if err != nil {
		copy := *spec
		return &copy
	}
	var out UnifiedSpec
	if err := json.Unmarshal(body, &out); err != nil {
		copy := *spec
		return &copy
	}
	return &out
}
