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
	Source      string
	Path        string
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

func PreviewAuth(explicitEnv map[string]string, required []string, localFiles []agentdef.AuthLocalFile, overridePath string) ([]AuthPreview, error) {
	if len(required) == 0 {
		return nil, nil
	}

	localFilesByEnv := make(map[string]agentdef.AuthLocalFile, len(localFiles))
	for _, file := range localFiles {
		key := strings.TrimSpace(file.RequiredEnv)
		if key == "" {
			continue
		}
		localFilesByEnv[key] = file
	}

	trimmedOverride := strings.TrimSpace(overridePath)
	homeDir := ""
	if trimmedOverride == "" && len(localFilesByEnv) > 0 {
		var err error
		homeDir, err = os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("resolve user home directory: %w", err)
		}
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
				Source:      "explicit_env",
			})
			continue
		}
		if value, ok := os.LookupEnv(key); ok && strings.TrimSpace(value) != "" {
			out = append(out, AuthPreview{
				RequiredEnv: key,
				Mode:        AuthPreviewModeAPIKey,
				Source:      "host_env",
			})
			continue
		}

		file, ok := localFilesByEnv[key]
		if !ok {
			return nil, fmt.Errorf("required agent auth %q is not configured in agent env or host env", key)
		}

		sourcePath := trimmedOverride
		sourceKind := "override_file"
		if sourcePath == "" {
			sourcePath = filepath.Join(homeDir, filepath.FromSlash(file.HomeRelPath))
			sourceKind = "home_file"
		}
		absSourcePath, err := filepath.Abs(sourcePath)
		if err != nil {
			return nil, fmt.Errorf("resolve auth file for %q: %w", key, err)
		}
		info, err := os.Stat(absSourcePath)
		if err != nil {
			return nil, fmt.Errorf("required agent auth %q is not configured in agent env or host env and auth file %q is unavailable: %w", key, absSourcePath, err)
		}
		if info.IsDir() {
			return nil, fmt.Errorf("auth file for %q must be a file, got directory %q", key, absSourcePath)
		}

		out = append(out, AuthPreview{
			RequiredEnv: key,
			Mode:        AuthPreviewModeOAuth,
			Source:      sourceKind,
			Path:        absSourcePath,
		})
	}

	return out, nil
}

type resolvedLocalAuthFile struct {
	RequiredEnv    string
	HostPath       string
	ContainerPath  string
	RunHomeRelPath string
}

type stagedLocalAuthFile struct {
	RequiredEnv    string
	HostPath       string
	SourcePath     string
	RunHomeRelPath string
}

func resolveLocalAuthFiles(explicitEnv map[string]string, required []string, localFiles []agentdef.AuthLocalFile, overridePath string) ([]resolvedLocalAuthFile, error) {
	if len(required) == 0 {
		return nil, nil
	}

	localFilesByEnv := make(map[string]agentdef.AuthLocalFile, len(localFiles))
	for _, file := range localFiles {
		key := strings.TrimSpace(file.RequiredEnv)
		if key == "" {
			continue
		}
		localFilesByEnv[key] = file
	}

	trimmedOverride := strings.TrimSpace(overridePath)
	homeDir := ""
	if trimmedOverride == "" && len(localFilesByEnv) > 0 {
		var err error
		homeDir, err = os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("resolve user home directory: %w", err)
		}
	}

	out := make([]resolvedLocalAuthFile, 0, len(localFilesByEnv))
	for _, name := range required {
		key := strings.TrimSpace(name)
		if key == "" {
			continue
		}
		if _, ok := resolveRequiredAgentEnvValue(explicitEnv, key); ok {
			continue
		}

		file, ok := localFilesByEnv[key]
		if !ok {
			return nil, fmt.Errorf("required agent auth %q is not configured in agent env or host env", key)
		}

		sourcePath := trimmedOverride
		if sourcePath == "" {
			sourcePath = filepath.Join(homeDir, filepath.FromSlash(file.HomeRelPath))
		}
		absSourcePath, err := filepath.Abs(sourcePath)
		if err != nil {
			return nil, fmt.Errorf("resolve auth file for %q: %w", key, err)
		}
		info, err := os.Stat(absSourcePath)
		if err != nil {
			return nil, fmt.Errorf("required agent auth %q is not configured in agent env or host env and auth file %q is unavailable: %w", key, absSourcePath, err)
		}
		if info.IsDir() {
			return nil, fmt.Errorf("auth file for %q must be a file, got directory %q", key, absSourcePath)
		}

		out = append(out, resolvedLocalAuthFile{
			RequiredEnv:    key,
			HostPath:       absSourcePath,
			ContainerPath:  filepath.Join(defaultAgentAuthFilesDir, key),
			RunHomeRelPath: strings.TrimSpace(file.RunHomeRelPath),
		})
	}
	return out, nil
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

func stageLocalAuthFiles(files []resolvedLocalAuthFile) (string, []stagedLocalAuthFile, error) {
	if len(files) == 0 {
		return "", nil, nil
	}
	stageDir, err := os.MkdirTemp("", "marginlab-auth-files-*")
	if err != nil {
		return "", nil, fmt.Errorf("create auth file staging dir: %w", err)
	}

	staged := make([]stagedLocalAuthFile, 0, len(files))
	for _, file := range files {
		payload, err := os.ReadFile(file.HostPath)
		if err != nil {
			_ = os.RemoveAll(stageDir)
			return "", nil, fmt.Errorf("read auth file for %q: %w", file.RequiredEnv, err)
		}
		targetPath := filepath.Join(stageDir, filepath.Base(file.ContainerPath))
		if err := os.WriteFile(targetPath, payload, 0o600); err != nil {
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
