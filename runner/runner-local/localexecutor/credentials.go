package localexecutor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/marginlab/margin-eval/runner/runner-core/agentdef"
	"github.com/marginlab/margin-eval/runner/runner-core/agentexecutor"
)

const (
	envKeyOpenAIAPIKey    = "OPENAI_API_KEY"
	envKeyAnthropicAPIKey = "ANTHROPIC_API_KEY"
)

type AuthPreviewMode string

const (
	AuthPreviewModeAPIKey AuthPreviewMode = "api_key"
	AuthPreviewModeOAuth  AuthPreviewMode = "oauth"
)

type AuthPreview struct {
	RequiredEnv string
	Mode        AuthPreviewMode
	SourceKind  string
	SourceLabel string
}

type resolvedLocalAuthCredential struct {
	RequiredEnv    string
	ContainerPath  string
	RunHomeRelPath string
	Payload        []byte
	SourceKind     string
	SourceLabel    string
}

type stagedLocalAuthFile struct {
	RequiredEnv    string
	HostPath       string
	SourcePath     string
	RunHomeRelPath string
}

func injectRequiredAgentEnv(env map[string]string, required []string) {
	for _, name := range required {
		key := strings.TrimSpace(name)
		if key == "" {
			continue
		}
		if _, exists := env[key]; exists {
			continue
		}
		value, ok := os.LookupEnv(key)
		if !ok || strings.TrimSpace(value) == "" {
			continue
		}
		env[key] = value
	}
}

func PreviewAuth(explicitEnv map[string]string, required []string, localCredentials []agentdef.AuthLocalCredential, overridePath string) ([]AuthPreview, error) {
	if len(required) == 0 {
		return nil, nil
	}

	credentialsByEnv := indexLocalCredentials(localCredentials)
	homeDir, err := resolveAuthHomeDir(strings.TrimSpace(overridePath), len(localCredentials) > 0)
	if err != nil {
		return nil, err
	}

	out := make([]AuthPreview, 0, len(required))
	for _, name := range required {
		key := strings.TrimSpace(name)
		if key == "" {
			continue
		}
		if value, exists := explicitEnv[key]; exists && strings.TrimSpace(value) != "" {
			out = append(out, AuthPreview{
				RequiredEnv: key,
				Mode:        AuthPreviewModeAPIKey,
				SourceKind:  "explicit_env",
				SourceLabel: key,
			})
			continue
		}
		if value, ok := os.LookupEnv(key); ok && strings.TrimSpace(value) != "" {
			out = append(out, AuthPreview{
				RequiredEnv: key,
				Mode:        AuthPreviewModeAPIKey,
				SourceKind:  "host_env",
				SourceLabel: key,
			})
			continue
		}

		credential, ok := credentialsByEnv[key]
		if !ok {
			return nil, fmt.Errorf("required agent auth %q is not configured in agent env or host env", key)
		}
		resolved, err := resolveLocalCredential(key, credential, homeDir, strings.TrimSpace(overridePath))
		if err != nil {
			return nil, err
		}
		out = append(out, AuthPreview{
			RequiredEnv: key,
			Mode:        AuthPreviewModeOAuth,
			SourceKind:  resolved.SourceKind,
			SourceLabel: resolved.SourceLabel,
		})
	}

	return out, nil
}

func resolveLocalAuthCredentials(explicitEnv map[string]string, required []string, localCredentials []agentdef.AuthLocalCredential, overridePath string) ([]resolvedLocalAuthCredential, error) {
	if len(required) == 0 {
		return nil, nil
	}

	credentialsByEnv := indexLocalCredentials(localCredentials)
	homeDir, err := resolveAuthHomeDir(strings.TrimSpace(overridePath), len(localCredentials) > 0)
	if err != nil {
		return nil, err
	}

	out := make([]resolvedLocalAuthCredential, 0, len(localCredentials))
	for _, name := range required {
		key := strings.TrimSpace(name)
		if key == "" {
			continue
		}
		if _, ok := resolveRequiredAgentEnvValue(explicitEnv, key); ok {
			continue
		}

		credential, ok := credentialsByEnv[key]
		if !ok {
			return nil, fmt.Errorf("required agent auth %q is not configured in agent env or host env", key)
		}
		resolved, err := resolveLocalCredential(key, credential, homeDir, strings.TrimSpace(overridePath))
		if err != nil {
			return nil, err
		}
		out = append(out, resolved)
	}
	return out, nil
}

func indexLocalCredentials(localCredentials []agentdef.AuthLocalCredential) map[string]agentdef.AuthLocalCredential {
	credentialsByEnv := make(map[string]agentdef.AuthLocalCredential, len(localCredentials))
	for _, credential := range localCredentials {
		key := strings.TrimSpace(credential.RequiredEnv)
		if key == "" {
			continue
		}
		credentialsByEnv[key] = credential
	}
	return credentialsByEnv
}

func resolveAuthHomeDir(overridePath string, needsHomeDir bool) (string, error) {
	if overridePath != "" || !needsHomeDir {
		return "", nil
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve user home directory: %w", err)
	}
	return homeDir, nil
}

func resolveLocalCredential(requiredEnv string, credential agentdef.AuthLocalCredential, homeDir string, overridePath string) (resolvedLocalAuthCredential, error) {
	containerPath := filepath.Join(defaultAgentAuthFilesDir, requiredEnv)
	runHomeRelPath := strings.TrimSpace(credential.RunHomeRelPath)
	if overridePath != "" {
		resolved, err := resolveOverrideAuthFile(overridePath)
		if err != nil {
			return resolvedLocalAuthCredential{}, fmt.Errorf("resolve override auth file for %q: %w", requiredEnv, err)
		}
		if err := validateCredentialPayload(credential, resolved.Payload); err != nil {
			return resolvedLocalAuthCredential{}, fmt.Errorf("validate override auth file for %q: %w", requiredEnv, err)
		}
		return resolvedLocalAuthCredential{
			RequiredEnv:    requiredEnv,
			ContainerPath:  containerPath,
			RunHomeRelPath: runHomeRelPath,
			Payload:        resolved.Payload,
			SourceKind:     resolved.SourceKind,
			SourceLabel:    resolved.SourceLabel,
		}, nil
	}

	failures := make([]string, 0, len(credential.Sources))
	for _, source := range credential.Sources {
		resolved, err := resolveLocalCredentialSource(source, homeDir)
		if err != nil {
			failures = append(failures, err.Error())
			continue
		}
		if err := validateCredentialPayload(credential, resolved.Payload); err != nil {
			failures = append(failures, fmt.Sprintf("%s %q failed validation: %v", resolved.SourceKind, resolved.SourceLabel, err))
			continue
		}
		return resolvedLocalAuthCredential{
			RequiredEnv:    requiredEnv,
			ContainerPath:  containerPath,
			RunHomeRelPath: runHomeRelPath,
			Payload:        resolved.Payload,
			SourceKind:     resolved.SourceKind,
			SourceLabel:    resolved.SourceLabel,
		}, nil
	}

	return resolvedLocalAuthCredential{}, fmt.Errorf(
		"required agent auth %q is not configured in agent env or host env and no local credential source produced %q: %s",
		requiredEnv,
		runHomeRelPath,
		strings.Join(failures, "; "),
	)
}

func resolveRequiredAgentEnvValue(explicitEnv map[string]string, key string) (string, bool) {
	if value, exists := explicitEnv[key]; exists {
		return value, strings.TrimSpace(value) != ""
	}
	value, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(value) == "" {
		return "", false
	}
	return value, true
}

func stageLocalAuthFiles(files []resolvedLocalAuthCredential) (string, []stagedLocalAuthFile, error) {
	if len(files) == 0 {
		return "", nil, nil
	}
	stageDir, err := os.MkdirTemp("", "marginlab-auth-files-*")
	if err != nil {
		return "", nil, fmt.Errorf("create auth file staging dir: %w", err)
	}

	staged := make([]stagedLocalAuthFile, 0, len(files))
	for _, file := range files {
		targetPath := filepath.Join(stageDir, filepath.Base(file.ContainerPath))
		if err := os.WriteFile(targetPath, file.Payload, 0o600); err != nil {
			_ = os.RemoveAll(stageDir)
			return "", nil, fmt.Errorf("stage auth file for %q: %w", file.RequiredEnv, err)
		}
		staged = append(staged, stagedLocalAuthFile{
			RequiredEnv:    file.RequiredEnv,
			HostPath:       targetPath,
			SourcePath:     file.ContainerPath,
			RunHomeRelPath: file.RunHomeRelPath,
		})
	}
	return stageDir, staged, nil
}

func toAgentExecutorAuthFiles(files []stagedLocalAuthFile) []agentexecutor.StartRunAuthFile {
	if len(files) == 0 {
		return nil
	}
	out := make([]agentexecutor.StartRunAuthFile, 0, len(files))
	for _, file := range files {
		out = append(out, agentexecutor.StartRunAuthFile{
			RequiredEnv:    file.RequiredEnv,
			SourcePath:     file.SourcePath,
			RunHomeRelPath: file.RunHomeRelPath,
		})
	}
	return out
}

func buildBootstrapCommand() string {
	binDir := shellQuote(defaultAgentServerRoot + "/bin")
	stateDir := shellQuote(defaultAgentServerRoot + "/state")
	configDir := shellQuote(defaultAgentServerRoot + "/config")
	authFilesDir := shellQuote(defaultAgentAuthFilesDir)

	return fmt.Sprintf("mkdir -p %s %s %s %s", binDir, stateDir, configDir, authFilesDir)
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}
