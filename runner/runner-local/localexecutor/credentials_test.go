package localexecutor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marginlab/margin-eval/runner/runner-core/agentdef"
	"github.com/marginlab/margin-eval/runner/runner-core/agentexecutor"
)

func TestInjectRequiredAgentEnv(t *testing.T) {
	t.Setenv(envKeyOpenAIAPIKey, "sk-openai")
	t.Setenv(envKeyAnthropicAPIKey, "sk-anthropic")

	env := map[string]string{
		envKeyOpenAIAPIKey: "sk-explicit",
	}
	injectRequiredAgentEnv(env, []string{envKeyOpenAIAPIKey, envKeyAnthropicAPIKey, "MISSING_ENV"})

	if got := env[envKeyOpenAIAPIKey]; got != "sk-explicit" {
		t.Fatalf("%s = %q, want %q", envKeyOpenAIAPIKey, got, "sk-explicit")
	}
	if got := env[envKeyAnthropicAPIKey]; got != "sk-anthropic" {
		t.Fatalf("%s = %q, want %q", envKeyAnthropicAPIKey, got, "sk-anthropic")
	}
	if _, exists := env["MISSING_ENV"]; exists {
		t.Fatalf("unexpected missing env key injection")
	}
}

func TestBuildBootstrapCommand(t *testing.T) {
	t.Parallel()

	cmd := buildBootstrapCommand()
	expectContains(t, cmd, "mkdir -p '/tmp/marginlab/bin' '/tmp/marginlab/state' '/tmp/marginlab/config' '/tmp/marginlab/config/auth-files'")
}

func TestResolveLocalAuthFilesUsesDiscoveredHomeFileWhenEnvMissing(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	hostPath := filepath.Join(homeDir, ".codex", "auth.json")
	if err := os.MkdirAll(filepath.Dir(hostPath), 0o755); err != nil {
		t.Fatalf("mkdir auth dir: %v", err)
	}
	if err := os.WriteFile(hostPath, []byte(`{"token":"oauth"}`), 0o600); err != nil {
		t.Fatalf("write auth file: %v", err)
	}

	files, err := resolveLocalAuthFiles(
		nil,
		[]string{envKeyOpenAIAPIKey},
		[]agentdef.AuthLocalFile{{
			RequiredEnv:    envKeyOpenAIAPIKey,
			HomeRelPath:    ".codex/auth.json",
			RunHomeRelPath: ".codex/auth.json",
		}},
		"",
	)
	if err != nil {
		t.Fatalf("resolveLocalAuthFiles() error = %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("resolved files = %#v", files)
	}
	if files[0].HostPath != hostPath {
		t.Fatalf("host path = %q, want %q", files[0].HostPath, hostPath)
	}
	if files[0].ContainerPath != filepath.Join(defaultAgentAuthFilesDir, envKeyOpenAIAPIKey) {
		t.Fatalf("container path = %q", files[0].ContainerPath)
	}
}

func TestResolveLocalAuthFilesUsesOverridePathAndExplicitBlankEnvDisablesHostLookup(t *testing.T) {
	t.Setenv(envKeyOpenAIAPIKey, "sk-host-openai")
	overridePath := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(overridePath, []byte(`{"token":"oauth"}`), 0o600); err != nil {
		t.Fatalf("write auth file: %v", err)
	}

	files, err := resolveLocalAuthFiles(
		map[string]string{envKeyOpenAIAPIKey: ""},
		[]string{envKeyOpenAIAPIKey},
		[]agentdef.AuthLocalFile{{
			RequiredEnv:    envKeyOpenAIAPIKey,
			HomeRelPath:    ".codex/auth.json",
			RunHomeRelPath: ".codex/auth.json",
		}},
		overridePath,
	)
	if err != nil {
		t.Fatalf("resolveLocalAuthFiles() error = %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("resolved files = %#v", files)
	}
	if files[0].HostPath != overridePath {
		t.Fatalf("host path = %q, want %q", files[0].HostPath, overridePath)
	}
}

func TestResolveLocalAuthFilesPrefersAPIKey(t *testing.T) {
	t.Setenv(envKeyOpenAIAPIKey, "sk-openai")

	files, err := resolveLocalAuthFiles(
		nil,
		[]string{envKeyOpenAIAPIKey},
		[]agentdef.AuthLocalFile{{
			RequiredEnv:    envKeyOpenAIAPIKey,
			HomeRelPath:    ".codex/auth.json",
			RunHomeRelPath: ".codex/auth.json",
		}},
		filepath.Join(t.TempDir(), "unused-auth.json"),
	)
	if err != nil {
		t.Fatalf("resolveLocalAuthFiles() error = %v", err)
	}
	if len(files) != 0 {
		t.Fatalf("expected API key to win over auth file, got %#v", files)
	}
}

func TestPreviewAuthReportsAPIKeyMode(t *testing.T) {
	t.Setenv(envKeyOpenAIAPIKey, "sk-openai")

	preview, err := PreviewAuth(
		nil,
		[]string{envKeyOpenAIAPIKey},
		[]agentdef.AuthLocalFile{{
			RequiredEnv:    envKeyOpenAIAPIKey,
			HomeRelPath:    ".codex/auth.json",
			RunHomeRelPath: ".codex/auth.json",
		}},
		"",
	)
	if err != nil {
		t.Fatalf("PreviewAuth() error = %v", err)
	}
	if len(preview) != 1 {
		t.Fatalf("preview = %#v", preview)
	}
	if preview[0].Mode != AuthPreviewModeAPIKey {
		t.Fatalf("mode = %q", preview[0].Mode)
	}
	if preview[0].RequiredEnv != envKeyOpenAIAPIKey {
		t.Fatalf("required env = %q", preview[0].RequiredEnv)
	}
}

func TestPreviewAuthReportsOAuthMode(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	hostPath := filepath.Join(homeDir, ".codex", "auth.json")
	if err := os.MkdirAll(filepath.Dir(hostPath), 0o755); err != nil {
		t.Fatalf("mkdir auth dir: %v", err)
	}
	if err := os.WriteFile(hostPath, []byte(`{"token":"oauth"}`), 0o600); err != nil {
		t.Fatalf("write auth file: %v", err)
	}

	preview, err := PreviewAuth(
		nil,
		[]string{envKeyOpenAIAPIKey},
		[]agentdef.AuthLocalFile{{
			RequiredEnv:    envKeyOpenAIAPIKey,
			HomeRelPath:    ".codex/auth.json",
			RunHomeRelPath: ".codex/auth.json",
		}},
		"",
	)
	if err != nil {
		t.Fatalf("PreviewAuth() error = %v", err)
	}
	if len(preview) != 1 {
		t.Fatalf("preview = %#v", preview)
	}
	if preview[0].Mode != AuthPreviewModeOAuth {
		t.Fatalf("mode = %q", preview[0].Mode)
	}
	if preview[0].Path != hostPath {
		t.Fatalf("path = %q, want %q", preview[0].Path, hostPath)
	}
}

func TestResolveLocalAuthFilesFailsWhenRequiredAuthMissing(t *testing.T) {
	_, err := resolveLocalAuthFiles(nil, []string{envKeyOpenAIAPIKey}, nil, "")
	if err == nil || !strings.Contains(err.Error(), envKeyOpenAIAPIKey) {
		t.Fatalf("expected missing auth error, got %v", err)
	}
}

func TestStageLocalAuthFilesCopiesPayloadAndBuildsRunRequestEntries(t *testing.T) {
	hostPath := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(hostPath, []byte(`{"token":"oauth"}`), 0o600); err != nil {
		t.Fatalf("write auth file: %v", err)
	}

	stageDir, staged, err := stageLocalAuthFiles([]resolvedLocalAuthFile{{
		RequiredEnv:    envKeyOpenAIAPIKey,
		HostPath:       hostPath,
		ContainerPath:  filepath.Join(defaultAgentAuthFilesDir, envKeyOpenAIAPIKey),
		RunHomeRelPath: ".codex/auth.json",
	}})
	if err != nil {
		t.Fatalf("stageLocalAuthFiles() error = %v", err)
	}
	defer os.RemoveAll(stageDir)

	body, err := os.ReadFile(filepath.Join(stageDir, envKeyOpenAIAPIKey))
	if err != nil {
		t.Fatalf("read staged auth file: %v", err)
	}
	if string(body) != `{"token":"oauth"}` {
		t.Fatalf("staged body = %q", string(body))
	}
	if staged[0].HostPath != filepath.Join(stageDir, envKeyOpenAIAPIKey) {
		t.Fatalf("staged host path = %q", staged[0].HostPath)
	}

	got := toAgentExecutorAuthFiles(staged)
	want := []agentexecutor.StartRunAuthFile{{
		RequiredEnv:    envKeyOpenAIAPIKey,
		SourcePath:     filepath.Join(defaultAgentAuthFilesDir, envKeyOpenAIAPIKey),
		RunHomeRelPath: ".codex/auth.json",
	}}
	if len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("run auth files = %#v, want %#v", got, want)
	}
}

func expectContains(t *testing.T, body, needle string) {
	t.Helper()
	if !strings.Contains(body, needle) {
		t.Fatalf("expected %q to contain %q", body, needle)
	}
}
