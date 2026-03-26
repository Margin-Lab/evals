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

func TestResolveLocalAuthCredentialsUsesDiscoveredHomeFileWhenEnvMissing(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	hostPath := filepath.Join(homeDir, ".codex", "auth.json")
	if err := os.MkdirAll(filepath.Dir(hostPath), 0o755); err != nil {
		t.Fatalf("mkdir auth dir: %v", err)
	}
	if err := os.WriteFile(hostPath, []byte(`{"token":"oauth"}`), 0o600); err != nil {
		t.Fatalf("write auth file: %v", err)
	}

	files, err := resolveLocalAuthCredentials(
		nil,
		[]string{envKeyOpenAIAPIKey},
		[]agentdef.AuthLocalCredential{codexLocalCredential()},
		"",
	)
	if err != nil {
		t.Fatalf("resolveLocalAuthCredentials() error = %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("resolved files = %#v", files)
	}
	if string(files[0].Payload) != `{"token":"oauth"}` {
		t.Fatalf("payload = %q", string(files[0].Payload))
	}
	if files[0].SourceKind != "home_file" {
		t.Fatalf("source kind = %q", files[0].SourceKind)
	}
	if files[0].SourceLabel != hostPath {
		t.Fatalf("source label = %q, want %q", files[0].SourceLabel, hostPath)
	}
	if files[0].ContainerPath != filepath.Join(defaultAgentAuthFilesDir, envKeyOpenAIAPIKey) {
		t.Fatalf("container path = %q", files[0].ContainerPath)
	}
}

func TestResolveLocalAuthCredentialsUsesOverridePathAndExplicitBlankEnvDisablesHostLookup(t *testing.T) {
	t.Setenv(envKeyOpenAIAPIKey, "sk-host-openai")
	overridePath := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(overridePath, []byte(`{"token":"oauth"}`), 0o600); err != nil {
		t.Fatalf("write auth file: %v", err)
	}

	files, err := resolveLocalAuthCredentials(
		map[string]string{envKeyOpenAIAPIKey: ""},
		[]string{envKeyOpenAIAPIKey},
		[]agentdef.AuthLocalCredential{codexLocalCredential()},
		overridePath,
	)
	if err != nil {
		t.Fatalf("resolveLocalAuthCredentials() error = %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("resolved files = %#v", files)
	}
	if files[0].SourceKind != "override_file" {
		t.Fatalf("source kind = %q", files[0].SourceKind)
	}
	if files[0].SourceLabel != overridePath {
		t.Fatalf("source label = %q, want %q", files[0].SourceLabel, overridePath)
	}
}

func TestResolveLocalAuthCredentialsPrefersAPIKey(t *testing.T) {
	t.Setenv(envKeyOpenAIAPIKey, "sk-openai")

	files, err := resolveLocalAuthCredentials(
		nil,
		[]string{envKeyOpenAIAPIKey},
		[]agentdef.AuthLocalCredential{codexLocalCredential()},
		filepath.Join(t.TempDir(), "unused-auth.json"),
	)
	if err != nil {
		t.Fatalf("resolveLocalAuthCredentials() error = %v", err)
	}
	if len(files) != 0 {
		t.Fatalf("expected API key to win over local credentials, got %#v", files)
	}
}

func TestPreviewAuthReportsAPIKeyMode(t *testing.T) {
	t.Setenv(envKeyOpenAIAPIKey, "sk-openai")

	preview, err := PreviewAuth(
		nil,
		[]string{envKeyOpenAIAPIKey},
		[]agentdef.AuthLocalCredential{codexLocalCredential()},
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

func TestPreviewAuthReportsOAuthHomeFileMode(t *testing.T) {
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
		[]agentdef.AuthLocalCredential{codexLocalCredential()},
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
	if preview[0].SourceKind != "home_file" {
		t.Fatalf("source kind = %q", preview[0].SourceKind)
	}
	if preview[0].SourceLabel != hostPath {
		t.Fatalf("source label = %q, want %q", preview[0].SourceLabel, hostPath)
	}
}

func TestResolveLocalAuthCredentialsUsesMacOSKeychainSource(t *testing.T) {
	origGOOS := authSourceGOOS
	origRun := runAuthSourceCommand
	defer func() {
		authSourceGOOS = origGOOS
		runAuthSourceCommand = origRun
	}()

	authSourceGOOS = func() string { return "darwin" }
	runAuthSourceCommand = func(name string, args ...string) ([]byte, error) {
		if name != "security" {
			t.Fatalf("command name = %q", name)
		}
		if strings.Join(args, " ") != "find-generic-password -s Claude Code-credentials -w" {
			t.Fatalf("command args = %#v", args)
		}
		return []byte(`{"claudeAiOauth":{"accessToken":"token"}}`), nil
	}

	files, err := resolveLocalAuthCredentials(
		nil,
		[]string{envKeyAnthropicAPIKey},
		[]agentdef.AuthLocalCredential{claudeLocalCredential()},
		"",
	)
	if err != nil {
		t.Fatalf("resolveLocalAuthCredentials() error = %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("resolved files = %#v", files)
	}
	if files[0].SourceKind != "macos_keychain" {
		t.Fatalf("source kind = %q", files[0].SourceKind)
	}
	if files[0].SourceLabel != "Claude Code-credentials" {
		t.Fatalf("source label = %q", files[0].SourceLabel)
	}
	if string(files[0].Payload) != `{"claudeAiOauth":{"accessToken":"token"}}` {
		t.Fatalf("payload = %q", string(files[0].Payload))
	}
}

func TestResolveLocalAuthCredentialsFallsBackAfterInvalidPayload(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	hostPath := filepath.Join(homeDir, ".claude", ".credentials.json")
	if err := os.MkdirAll(filepath.Dir(hostPath), 0o755); err != nil {
		t.Fatalf("mkdir auth dir: %v", err)
	}
	if err := os.WriteFile(hostPath, []byte(`{"claudeAiOauth":{"accessToken":"from-file"}}`), 0o600); err != nil {
		t.Fatalf("write auth file: %v", err)
	}

	origGOOS := authSourceGOOS
	origRun := runAuthSourceCommand
	defer func() {
		authSourceGOOS = origGOOS
		runAuthSourceCommand = origRun
	}()

	authSourceGOOS = func() string { return "darwin" }
	runAuthSourceCommand = func(string, ...string) ([]byte, error) {
		return []byte(`{"claudeAiOauth":{"accessToken":"   "}}`), nil
	}

	files, err := resolveLocalAuthCredentials(
		nil,
		[]string{envKeyAnthropicAPIKey},
		[]agentdef.AuthLocalCredential{claudeLocalCredential()},
		"",
	)
	if err != nil {
		t.Fatalf("resolveLocalAuthCredentials() error = %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("resolved files = %#v", files)
	}
	if files[0].SourceKind != "home_file" {
		t.Fatalf("source kind = %q", files[0].SourceKind)
	}
}

func TestResolveLocalAuthCredentialsFailsWhenRequiredAuthMissing(t *testing.T) {
	_, err := resolveLocalAuthCredentials(nil, []string{envKeyOpenAIAPIKey}, nil, "")
	if err == nil || !strings.Contains(err.Error(), envKeyOpenAIAPIKey) {
		t.Fatalf("expected missing auth error, got %v", err)
	}
}

func TestStageLocalAuthFilesCopiesPayloadAndBuildsRunRequestEntries(t *testing.T) {
	stageDir, staged, err := stageLocalAuthFiles([]resolvedLocalAuthCredential{{
		RequiredEnv:    envKeyOpenAIAPIKey,
		ContainerPath:  filepath.Join(defaultAgentAuthFilesDir, envKeyOpenAIAPIKey),
		RunHomeRelPath: ".codex/auth.json",
		Payload:        []byte(`{"token":"oauth"}`),
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

func codexLocalCredential() agentdef.AuthLocalCredential {
	return agentdef.AuthLocalCredential{
		RequiredEnv:    envKeyOpenAIAPIKey,
		RunHomeRelPath: ".codex/auth.json",
		Sources: []agentdef.AuthLocalSource{{
			Kind:        agentdef.AuthLocalSourceKindHomeFile,
			HomeRelPath: ".codex/auth.json",
		}},
	}
}

func claudeLocalCredential() agentdef.AuthLocalCredential {
	return agentdef.AuthLocalCredential{
		RequiredEnv:      envKeyAnthropicAPIKey,
		RunHomeRelPath:   ".claude/.credentials.json",
		ValidateJSONPath: "claudeAiOauth.accessToken",
		Sources: []agentdef.AuthLocalSource{
			{
				Kind:      agentdef.AuthLocalSourceKindMacOSKeychain,
				Service:   "Claude Code-credentials",
				Platforms: []string{"darwin"},
			},
			{
				Kind:        agentdef.AuthLocalSourceKindHomeFile,
				HomeRelPath: ".claude/.credentials.json",
			},
		},
	}
}

func expectContains(t *testing.T, body, needle string) {
	t.Helper()
	if !strings.Contains(body, needle) {
		t.Fatalf("expected %q to contain %q", body, needle)
	}
}
