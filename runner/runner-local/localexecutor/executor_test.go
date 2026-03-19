package localexecutor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marginlab/margin-eval/runner/runner-core/imageresolver"
	"github.com/marginlab/margin-eval/runner/runner-core/runbundle"
	"github.com/marginlab/margin-eval/runner/runner-core/store"
	"github.com/marginlab/margin-eval/runner/runner-core/testfixture"
)

type stubAgentServerBinaryProvider struct {
	path         string
	err          error
	lastPlatform string
	resolveCalls int
}

func (s *stubAgentServerBinaryProvider) ResolveAgentServerBinary(_ context.Context, platform string) (string, error) {
	s.resolveCalls++
	s.lastPlatform = platform
	if s.err != nil {
		return "", s.err
	}
	return s.path, nil
}

func TestContainerEnvInheritsRequiredAgentEnvFromHost(t *testing.T) {
	t.Setenv(envKeyOpenAIAPIKey, "sk-openai")
	t.Setenv(envKeyAnthropicAPIKey, "sk-anthropic")

	exec, err := New(Config{
		AgentServerBinary: writeTempBinary(t),
		ArtifactRoot:      t.TempDir(),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	containerEnv := exec.containerEnv([]string{envKeyOpenAIAPIKey, envKeyAnthropicAPIKey})
	if got := containerEnv[envKeyOpenAIAPIKey]; got != "sk-openai" {
		t.Fatalf("%s = %q, want %q", envKeyOpenAIAPIKey, got, "sk-openai")
	}
	if got := containerEnv[envKeyAnthropicAPIKey]; got != "sk-anthropic" {
		t.Fatalf("%s = %q, want %q", envKeyAnthropicAPIKey, got, "sk-anthropic")
	}
}

func TestContainerEnvExplicitRequiredAgentEnvOverridesHost(t *testing.T) {
	t.Setenv(envKeyOpenAIAPIKey, "sk-host-openai")
	t.Setenv(envKeyAnthropicAPIKey, "sk-host-anthropic")

	exec, err := New(Config{
		AgentServerBinary: writeTempBinary(t),
		ArtifactRoot:      t.TempDir(),
		Env: map[string]string{
			envKeyOpenAIAPIKey:    "sk-config-openai",
			envKeyAnthropicAPIKey: "",
		},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	containerEnv := exec.containerEnv([]string{envKeyOpenAIAPIKey, envKeyAnthropicAPIKey})
	if got := containerEnv[envKeyOpenAIAPIKey]; got != "sk-config-openai" {
		t.Fatalf("%s = %q, want %q", envKeyOpenAIAPIKey, got, "sk-config-openai")
	}
	if got := containerEnv[envKeyAnthropicAPIKey]; got != "" {
		t.Fatalf("%s = %q, want empty", envKeyAnthropicAPIKey, got)
	}
}

func TestCopyAuthFilesToContainerUsesDockerCP(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "docker.log")
	dockerBin := writeFakeDockerBinary(t, fmt.Sprintf(`#!/bin/sh
set -eu
printf '%%s\n' "$*" >> %q
case "$1" in
  exec)
    if [ "$2" = "container-123" ] && [ "$3" = "mkdir" ] && [ "$4" = "-p" ] && [ "$5" = %q ]; then
      exit 0
    fi
    ;;
  cp)
    exit 0
    ;;
esac
echo "unexpected docker invocation: $*" >&2
exit 1
`, logPath, defaultAgentAuthFilesDir))
	hostPath := filepath.Join(t.TempDir(), "OPENAI_API_KEY")
	if err := os.WriteFile(hostPath, []byte(`{"token":"oauth"}`), 0o600); err != nil {
		t.Fatalf("write staged auth file: %v", err)
	}
	exec := &Executor{dockerBinary: dockerBin}

	err := exec.copyAuthFilesToContainer(context.Background(), "container-123", []stagedLocalAuthFile{{
		RequiredEnv:    envKeyOpenAIAPIKey,
		HostPath:       hostPath,
		SourcePath:     filepath.Join(defaultAgentAuthFilesDir, envKeyOpenAIAPIKey),
		RunHomeRelPath: ".codex/auth.json",
	}})
	if err != nil {
		t.Fatalf("copyAuthFilesToContainer() error = %v", err)
	}

	logBody, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read fake docker log: %v", err)
	}
	logText := string(logBody)
	if !strings.Contains(logText, "exec container-123 mkdir -p "+defaultAgentAuthFilesDir) {
		t.Fatalf("expected mkdir auth-files command in log, got:\n%s", logText)
	}
	if !strings.Contains(logText, "cp "+hostPath+" container-123:"+filepath.Join(defaultAgentAuthFilesDir, envKeyOpenAIAPIKey)) {
		t.Fatalf("expected docker cp command in log, got:\n%s", logText)
	}
}

func TestNewInitializesDefaultImageResolver(t *testing.T) {
	exec, err := New(Config{
		AgentServerBinary: writeTempBinary(t),
		ArtifactRoot:      t.TempDir(),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if exec.imageResolver == nil {
		t.Fatalf("expected default image resolver")
	}
}

func TestNewAcceptsAgentServerBinaryProvider(t *testing.T) {
	provider := &stubAgentServerBinaryProvider{path: "/tmp/agent-server"}
	exec, err := New(Config{
		AgentServerBinaryProvider: provider,
		ArtifactRoot:              t.TempDir(),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if exec.agentServerBinary != "" {
		t.Fatalf("agentServerBinary = %q, want empty exact override", exec.agentServerBinary)
	}
	if exec.agentServerBinaryProvider != provider {
		t.Fatalf("agentServerBinaryProvider = %#v", exec.agentServerBinaryProvider)
	}
}

func TestResolveCaseForExecutionUsesPersistedImage(t *testing.T) {
	bundleCase := runbundle.Case{
		CaseID:            "case_1",
		Image:             "",
		ImageBuild:        &runbundle.CaseImageBuild{Context: testfixture.MinimalTestAssets(), DockerfileRelPath: "Dockerfile"},
		InitialPrompt:     "fix",
		TestCommand:       []string{"bash", "-lc", "true"},
		TestCwd:           "/work",
		TestTimeoutSecond: 30,
		TestAssets:        testfixture.MinimalTestAssets(),
	}
	run := store.Run{
		Bundle: runbundle.Bundle{
			ResolvedSnapshot: runbundle.ResolvedSnapshot{
				Cases: []runbundle.Case{bundleCase},
			},
		},
	}
	inst := store.Instance{
		InstanceID: "inst_1",
		Ordinal:    0,
		Case: runbundle.Case{
			CaseID: "case_1",
			Image:  "ghcr.io/acme/repo@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		},
	}

	got, err := resolveCaseForExecution(run, inst)
	if err != nil {
		t.Fatalf("resolveCaseForExecution() error = %v", err)
	}
	if got.Image != inst.Case.Image {
		t.Fatalf("resolved image = %q, want persisted image %q", got.Image, inst.Case.Image)
	}
}

func TestResolveCaseImageUsesConfiguredResolver(t *testing.T) {
	const expected = "marginlab-local/case_1@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	stub := &stubImageResolver{out: expected}
	exec := &Executor{imageResolver: stub}

	caseSpec := runbundle.Case{
		CaseID:        "case_1",
		Image:         "",
		ImageBuild:    &runbundle.CaseImageBuild{Context: testfixture.MinimalTestAssets(), DockerfileRelPath: "Dockerfile"},
		InitialPrompt: "fix",
	}
	got, err := exec.resolveCaseImage(context.Background(), caseSpec)
	if err != nil {
		t.Fatalf("resolveCaseImage() error = %v", err)
	}
	if got != expected {
		t.Fatalf("resolved image = %q, want %q", got, expected)
	}
	if stub.lastInput.CaseID != "case_1" {
		t.Fatalf("resolver case_id = %q, want case_1", stub.lastInput.CaseID)
	}
	if stub.lastInput.ImageBuild == nil {
		t.Fatalf("expected resolver input image_build")
	}
}

func TestNewRejectsCleanupWhenResolverDoesNotSupportCleanup(t *testing.T) {
	_, err := New(Config{
		AgentServerBinary:  writeTempBinary(t),
		ArtifactRoot:       t.TempDir(),
		ImageResolver:      &stubImageResolver{out: "ghcr.io/acme/repo@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"},
		CleanupBuiltImages: true,
	})
	if err == nil || !strings.Contains(err.Error(), "supports cleanup") {
		t.Fatalf("expected cleanup capability error, got %v", err)
	}
}

func TestAcquireAndReleaseImageBuildRefCleansUpOnLastUse(t *testing.T) {
	cleaner := &stubCleanableResolver{
		out: "marginlab-local/buildctx@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	}
	exec := &Executor{
		cleanupBuiltImages: true,
		imageCleaner:       cleaner,
		imageBuildRefs:     map[string]int{},
	}
	caseSpec := runbundle.Case{
		CaseID: "case_1",
		ImageBuild: &runbundle.CaseImageBuild{
			Context:           testfixture.MinimalTestAssets(),
			DockerfileRelPath: "Dockerfile",
		},
	}
	input, key := exec.acquireImageBuildRef(caseSpec)
	if key == "" {
		t.Fatalf("expected build key for lazy build case")
	}
	if _, key2 := exec.acquireImageBuildRef(caseSpec); key2 != key {
		t.Fatalf("build keys differ: %q vs %q", key, key2)
	}

	exec.releaseImageBuildRef(context.Background(), key, input, cleaner.out)
	if cleaner.cleanupCalls != 0 {
		t.Fatalf("cleanup calls = %d, want 0 before last release", cleaner.cleanupCalls)
	}
	exec.releaseImageBuildRef(context.Background(), key, input, cleaner.out)
	if cleaner.cleanupCalls != 1 {
		t.Fatalf("cleanup calls = %d, want 1 after last release", cleaner.cleanupCalls)
	}
}

func TestReleaseImageBuildRefIgnoresCleanupErrors(t *testing.T) {
	cleaner := &stubCleanableResolver{
		out: "marginlab-local/buildctx@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		err: context.DeadlineExceeded,
	}
	exec := &Executor{
		cleanupBuiltImages: true,
		imageCleaner:       cleaner,
		imageBuildRefs:     map[string]int{},
	}
	caseSpec := runbundle.Case{
		CaseID: "case_1",
		ImageBuild: &runbundle.CaseImageBuild{
			Context:           testfixture.MinimalTestAssets(),
			DockerfileRelPath: "Dockerfile",
		},
	}
	input, key := exec.acquireImageBuildRef(caseSpec)
	exec.releaseImageBuildRef(context.Background(), key, input, cleaner.out)
	if cleaner.cleanupCalls != 1 {
		t.Fatalf("cleanup calls = %d, want 1", cleaner.cleanupCalls)
	}
}

func TestResolveAgentServerBinaryForContainerUsesExactOverride(t *testing.T) {
	exec := &Executor{agentServerBinary: "/tmp/agent-server"}
	got, err := exec.resolveAgentServerBinaryForContainer(context.Background(), "container-123")
	if err != nil {
		t.Fatalf("resolveAgentServerBinaryForContainer() error = %v", err)
	}
	if got != "/tmp/agent-server" {
		t.Fatalf("resolved binary = %q", got)
	}
}

func TestResolveAgentServerBinaryForContainerUsesProvider(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "docker.log")
	dockerBin := writeFakeDockerBinary(t, fmt.Sprintf(`#!/bin/sh
set -eu
printf '%%s\n' "$*" >> %q
case "$1 $2 $3 $4" in
  "inspect container-123 --format {{.Image}}")
    printf 'sha256:image123\n'
    exit 0
    ;;
  "image inspect sha256:image123 --format")
    printf 'linux/arm64\n'
    exit 0
    ;;
esac
echo "unexpected docker invocation: $*" >&2
exit 1
`, logPath))
	provider := &stubAgentServerBinaryProvider{path: "/tmp/agent-server-linux-arm64"}
	exec := &Executor{
		dockerBinary:              dockerBin,
		agentServerBinaryProvider: provider,
	}

	got, err := exec.resolveAgentServerBinaryForContainer(context.Background(), "container-123")
	if err != nil {
		t.Fatalf("resolveAgentServerBinaryForContainer() error = %v", err)
	}
	if got != "/tmp/agent-server-linux-arm64" {
		t.Fatalf("resolved binary = %q", got)
	}
	if provider.resolveCalls != 1 {
		t.Fatalf("provider resolve calls = %d, want 1", provider.resolveCalls)
	}
	if provider.lastPlatform != "linux/arm64" {
		t.Fatalf("provider platform = %q", provider.lastPlatform)
	}
}

func TestResolveAgentServerBinaryForContainerReturnsProviderError(t *testing.T) {
	dockerBin := writeFakeDockerBinary(t, `#!/bin/sh
set -eu
case "$1 $2 $3 $4" in
  "inspect container-123 --format {{.Image}}")
    printf 'sha256:image123\n'
    exit 0
    ;;
  "image inspect sha256:image123 --format")
    printf 'linux/s390x\n'
    exit 0
    ;;
esac
echo "unexpected docker invocation: $*" >&2
exit 1
`)
	provider := &stubAgentServerBinaryProvider{err: fmt.Errorf("unsupported platform")}
	exec := &Executor{
		dockerBinary:              dockerBin,
		agentServerBinaryProvider: provider,
	}

	_, err := exec.resolveAgentServerBinaryForContainer(context.Background(), "container-123")
	if err == nil || !strings.Contains(err.Error(), "unsupported platform") {
		t.Fatalf("expected provider error, got %v", err)
	}
}

type stubImageResolver struct {
	out       string
	lastInput imageresolver.ResolveInput
}

func (s *stubImageResolver) Resolve(_ context.Context, in imageresolver.ResolveInput) (string, error) {
	s.lastInput = in
	return s.out, nil
}

type stubCleanableResolver struct {
	out          string
	err          error
	cleanupCalls int
}

func (s *stubCleanableResolver) Resolve(_ context.Context, in imageresolver.ResolveInput) (string, error) {
	_ = in
	return s.out, nil
}

func (s *stubCleanableResolver) Cleanup(_ context.Context, _ imageresolver.ResolveInput, _ string) error {
	s.cleanupCalls++
	return s.err
}

func writeTempBinary(t *testing.T) string {
	t.Helper()
	return writeTempBinaryNamed(t, "agent-server")
}

func writeTempBinaryNamed(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write temp binary: %v", err)
	}
	return path
}
