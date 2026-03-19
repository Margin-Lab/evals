package run

import (
	"strings"

	"github.com/marginlab/margin-eval/agent-server/internal/state"
)

const (
	runHomeEnvKey    = "HOME"
	runSandboxEnvKey = "IS_SANDBOX"
)

func mergeEnvironment(baseEnv []string, overrides map[string]string) map[string]string {
	env := make(map[string]string, len(baseEnv)+len(overrides))
	for _, pair := range baseEnv {
		parts := strings.SplitN(pair, "=", 2)
		if len(parts) != 2 {
			continue
		}
		env[parts[0]] = parts[1]
	}
	for k, v := range overrides {
		env[k] = v
	}
	return env
}

func mergeEnvironmentMap(base map[string]string, overrides map[string]string) map[string]string {
	out := make(map[string]string, len(base)+len(overrides))
	for key, value := range base {
		out[key] = value
	}
	for key, value := range overrides {
		out[key] = value
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

func cloneAuthFiles(in []AuthFile) []AuthFile {
	if len(in) == 0 {
		return nil
	}
	out := make([]AuthFile, 0, len(in))
	for _, item := range in {
		out = append(out, AuthFile{
			RequiredEnv:    strings.TrimSpace(item.RequiredEnv),
			SourcePath:     strings.TrimSpace(item.SourcePath),
			RunHomeRelPath: strings.TrimSpace(item.RunHomeRelPath),
		})
	}
	return out
}

func cloneRunAuthFileRecords(in []AuthFile) []state.RunAuthFileRecord {
	if len(in) == 0 {
		return nil
	}
	out := make([]state.RunAuthFileRecord, 0, len(in))
	for _, item := range in {
		out = append(out, state.RunAuthFileRecord{
			RequiredEnv:    strings.TrimSpace(item.RequiredEnv),
			SourcePath:     strings.TrimSpace(item.SourcePath),
			RunHomeRelPath: strings.TrimSpace(item.RunHomeRelPath),
		})
	}
	return out
}

func authFilesFromRunRecord(in []state.RunAuthFileRecord) []AuthFile {
	if len(in) == 0 {
		return nil
	}
	out := make([]AuthFile, 0, len(in))
	for _, item := range in {
		out = append(out, AuthFile{
			RequiredEnv:    strings.TrimSpace(item.RequiredEnv),
			SourcePath:     strings.TrimSpace(item.SourcePath),
			RunHomeRelPath: strings.TrimSpace(item.RunHomeRelPath),
		})
	}
	return out
}

func applyRunEnvironmentDefaults(env map[string]string, runHome string) {
	env[runHomeEnvKey] = runHome
	env[runSandboxEnvKey] = "1"
}

func agentName(agent state.AgentRecord) string {
	if agent.Definition == nil {
		return ""
	}
	return strings.TrimSpace(agent.Definition.Snapshot.Manifest.Name)
}
