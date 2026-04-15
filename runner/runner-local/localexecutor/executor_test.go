package localexecutor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/marginlab/margin-eval/runner/runner-core/domain"
	"github.com/marginlab/margin-eval/runner/runner-core/imageresolver"
	"github.com/marginlab/margin-eval/runner/runner-core/runbundle"
	"github.com/marginlab/margin-eval/runner/runner-core/store"
	"github.com/marginlab/margin-eval/runner/runner-core/testfixture"
	"github.com/marginlab/margin-eval/runner/runner-local/runfs"
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
		AgentCwd:          "/workspace",
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

func TestClassifyTestFinalState(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		exitCode int
		want     store.InstanceResult
	}{
		{
			name:     "pass",
			exitCode: 0,
			want:     store.InstanceResult{FinalState: domain.InstanceStateSucceeded},
		},
		{
			name:     "fail",
			exitCode: 1,
			want:     store.InstanceResult{FinalState: domain.InstanceStateTestFailed},
		},
		{
			name:     "infra",
			exitCode: 2,
			want: store.InstanceResult{
				FinalState:   domain.InstanceStateInfraFailed,
				ErrorCode:    "TEST_INFRA",
				ErrorMessage: "case test script reported infra failure",
			},
		},
		{
			name:     "unexpected",
			exitCode: 3,
			want: store.InstanceResult{
				FinalState:   domain.InstanceStateInfraFailed,
				ErrorCode:    "INVALID_TEST_EXIT_CODE",
				ErrorMessage: "case test script exited with unsupported status 3; expected 0, 1, or 2",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := store.InstanceResult{FinalState: classifyTestFinalState(tt.exitCode)}
			if got.FinalState == domain.InstanceStateInfraFailed {
				if tt.exitCode == testExitCodeInfra {
					got.ErrorCode = "TEST_INFRA"
					got.ErrorMessage = "case test script reported infra failure"
				} else if tt.exitCode != testExitCodePass && tt.exitCode != testExitCodeFail {
					got.ErrorCode = "INVALID_TEST_EXIT_CODE"
					got.ErrorMessage = fmt.Sprintf("case test script exited with unsupported status %d; expected 0, 1, or 2", tt.exitCode)
				}
			}
			if got.FinalState != tt.want.FinalState || got.ErrorCode != tt.want.ErrorCode || got.ErrorMessage != tt.want.ErrorMessage {
				t.Fatalf("result = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestExecuteCaseTestStreamsOutputToArtifacts(t *testing.T) {
	runDir := t.TempDir()
	dockerBin := writeFakeDockerBinary(t, `#!/bin/sh
set -eu
if [ "$1" != "exec" ]; then
  echo "unexpected docker invocation: $*" >&2
  exit 1
fi
printf 'test stdout line\n'
printf 'test stderr line\n' >&2
exit 1
`)
	exec := &Executor{
		dockerBinary: dockerBin,
		runDirs: map[string]string{
			"run_1": runDir,
		},
	}

	result, err := exec.executeCaseTest(context.Background(), runDir, "inst_1", "container-123", runbundle.Case{
		TestCwd:           "/workspace",
		TestCommand:       []string{"sh", "-lc", "echo ignored"},
		TestTimeoutSecond: 30,
	})
	if err != nil {
		t.Fatalf("executeCaseTest() error = %v", err)
	}
	if result.ExitCode != 1 {
		t.Fatalf("exit code = %d, want 1", result.ExitCode)
	}
	if result.StdoutRef != "instances/inst_1/test/test_stdout.txt" {
		t.Fatalf("stdout ref = %q", result.StdoutRef)
	}
	if result.StderrRef != "instances/inst_1/test/test_stderr.txt" {
		t.Fatalf("stderr ref = %q", result.StderrRef)
	}
	if len(result.Artifacts) != 2 {
		t.Fatalf("artifact count = %d, want 2", len(result.Artifacts))
	}

	stdoutPath := filepath.Join(runDir, filepath.FromSlash(result.StdoutRef))
	stderrPath := filepath.Join(runDir, filepath.FromSlash(result.StderrRef))
	stdoutBody, err := os.ReadFile(stdoutPath)
	if err != nil {
		t.Fatalf("read stdout artifact: %v", err)
	}
	stderrBody, err := os.ReadFile(stderrPath)
	if err != nil {
		t.Fatalf("read stderr artifact: %v", err)
	}
	if string(stdoutBody) != "test stdout line\n" {
		t.Fatalf("stdout artifact = %q", string(stdoutBody))
	}
	if string(stderrBody) != "test stderr line\n" {
		t.Fatalf("stderr artifact = %q", string(stderrBody))
	}
}

func TestExecuteCaseOracleStreamsOutputToArtifacts(t *testing.T) {
	runDir := t.TempDir()
	dockerBin := writeFakeDockerBinary(t, `#!/bin/sh
set -eu
if [ "$1" != "exec" ]; then
  echo "unexpected docker invocation: $*" >&2
  exit 1
fi
printf 'oracle stdout line\n'
printf 'oracle stderr line\n' >&2
exit 0
`)
	exec := &Executor{
		dockerBinary: dockerBin,
		runDirs: map[string]string{
			"run_1": runDir,
		},
	}

	result, err := exec.executeCaseOracle(context.Background(), runDir, "inst_1", "container-123", runbundle.Case{
		AgentCwd:          "/workspace/repo",
		TestCwd:           "/workspace",
		TestTimeoutSecond: 30,
	})
	if err != nil {
		t.Fatalf("executeCaseOracle() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("exit code = %d, want 0", result.ExitCode)
	}
	if result.StdoutRef != "instances/inst_1/oracle/oracle_stdout.txt" {
		t.Fatalf("stdout ref = %q", result.StdoutRef)
	}
	if result.StderrRef != "instances/inst_1/oracle/oracle_stderr.txt" {
		t.Fatalf("stderr ref = %q", result.StderrRef)
	}
	if len(result.Artifacts) != 2 {
		t.Fatalf("artifact count = %d, want 2", len(result.Artifacts))
	}

	stdoutPath := filepath.Join(runDir, filepath.FromSlash(result.StdoutRef))
	stderrPath := filepath.Join(runDir, filepath.FromSlash(result.StderrRef))
	stdoutBody, err := os.ReadFile(stdoutPath)
	if err != nil {
		t.Fatalf("read stdout artifact: %v", err)
	}
	stderrBody, err := os.ReadFile(stderrPath)
	if err != nil {
		t.Fatalf("read stderr artifact: %v", err)
	}
	if string(stdoutBody) != "oracle stdout line\n" {
		t.Fatalf("stdout artifact = %q", string(stdoutBody))
	}
	if string(stderrBody) != "oracle stderr line\n" {
		t.Fatalf("stderr artifact = %q", string(stderrBody))
	}
}

func TestStartContainerLogsProgressToAgentBoot(t *testing.T) {
	root := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "docker.log")
	dockerBin := writeFakeDockerBinary(t, fmt.Sprintf(`#!/bin/sh
set -eu
printf '%%s\n' "$*" >> %q
case "$1" in
  run)
    cidfile=""
    prev=""
    for arg in "$@"; do
      if [ "$prev" = "--cidfile" ]; then
        cidfile="$arg"
        break
      fi
      prev="$arg"
    done
    [ -n "$cidfile" ]
    printf 'container-123\n' > "$cidfile"
    printf 'pulling cached layers\n'
    printf 'container-123\n'
    exit 0
    ;;
  port)
    if [ "$2" = "container-123" ] && [ "$3" = "8080/tcp" ]; then
      printf '127.0.0.1:32771\n'
      exit 0
    fi
    ;;
esac
echo "unexpected docker invocation: $*" >&2
exit 1
`, logPath))
	exec := &Executor{
		dockerBinary:  dockerBin,
		containerPort: 8080,
	}
	logs, err := newExecutionLogs(root, "inst_1")
	if err != nil {
		t.Fatalf("newExecutionLogs() error = %v", err)
	}
	defer logs.Close()
	bootWriter, err := logs.Writer(store.ArtifactRoleAgentBoot)
	if err != nil {
		t.Fatalf("logs.Writer() error = %v", err)
	}

	containerID, baseURL, err := exec.startContainer(context.Background(), "ghcr.io/acme/case@sha256:1234", nil, bootWriter, logs, true)
	if err != nil {
		t.Fatalf("startContainer() error = %v", err)
	}
	if containerID != "container-123" {
		t.Fatalf("containerID = %q, want %q", containerID, "container-123")
	}
	if baseURL != "http://127.0.0.1:32771" {
		t.Fatalf("baseURL = %q, want %q", baseURL, "http://127.0.0.1:32771")
	}

	bootPath, _, _, ok := runfs.AbsoluteArtifactPath(root, "inst_1", store.ArtifactRoleAgentBoot)
	if !ok {
		t.Fatalf("expected boot artifact path")
	}
	records := mustReadStructuredRecords(t, bootPath)
	assertHasStructuredStep(t, records, "container.start", "start", "Starting container from resolved case image.", "image", "ghcr.io/acme/case@sha256:1234")
	assertHasStructuredOutputMessage(t, records, "pulling cached layers")
	assertHasStructuredStep(t, records, "container.start", "completed", "Container started from resolved case image and port mapping was resolved.", "base_url", "http://127.0.0.1:32771")
}

func TestStartContainerSkipsPortPublishWhenDisabled(t *testing.T) {
	root := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "docker.log")
	dockerBin := writeFakeDockerBinary(t, fmt.Sprintf(`#!/bin/sh
set -eu
printf '%%s\n' "$*" >> %q
case "$1" in
  run)
    cidfile=""
    prev=""
    for arg in "$@"; do
      if [ "$prev" = "--cidfile" ]; then
        cidfile="$arg"
        break
      fi
      prev="$arg"
    done
    [ -n "$cidfile" ]
    printf 'container-456\n' > "$cidfile"
    printf 'container-456\n'
    exit 0
    ;;
  port)
    echo "unexpected port lookup" >&2
    exit 1
    ;;
esac
echo "unexpected docker invocation: $*" >&2
exit 1
`, logPath))
	exec := &Executor{
		dockerBinary:  dockerBin,
		containerPort: 8080,
	}
	logs, err := newExecutionLogs(root, "inst_1")
	if err != nil {
		t.Fatalf("newExecutionLogs() error = %v", err)
	}
	defer logs.Close()
	bootWriter, err := logs.Writer(store.ArtifactRoleAgentBoot)
	if err != nil {
		t.Fatalf("logs.Writer() error = %v", err)
	}

	containerID, baseURL, err := exec.startContainer(context.Background(), "ghcr.io/acme/case@sha256:1234", nil, bootWriter, logs, false)
	if err != nil {
		t.Fatalf("startContainer() error = %v", err)
	}
	if containerID != "container-456" {
		t.Fatalf("containerID = %q, want %q", containerID, "container-456")
	}
	if baseURL != "" {
		t.Fatalf("baseURL = %q, want empty", baseURL)
	}

	logBytes, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read docker log: %v", err)
	}
	if strings.Contains(string(logBytes), "\nport ") {
		t.Fatalf("expected no docker port invocation, got log:\n%s", string(logBytes))
	}

	bootPath, _, _, ok := runfs.AbsoluteArtifactPath(root, "inst_1", store.ArtifactRoleAgentBoot)
	if !ok {
		t.Fatalf("expected boot artifact path")
	}
	records := mustReadStructuredRecords(t, bootPath)
	assertHasStructuredStep(t, records, "container.start", "completed", "Container started from resolved case image.", "container_id", "container-456")
}

func TestCaptureAgentServerRuntimeLogPeriodicallyUpdatesArtifact(t *testing.T) {
	root := t.TempDir()
	runtimeSourcePath := filepath.Join(t.TempDir(), "agent-server.log")
	initial := `{"v":1,"kind":"step","message":"server.listening"}` + "\n"
	if err := os.WriteFile(runtimeSourcePath, []byte(initial), 0o644); err != nil {
		t.Fatalf("write initial runtime log: %v", err)
	}
	dockerBin := writeFakeDockerBinary(t, fmt.Sprintf(`#!/bin/sh
set -eu
if [ "$1" = "exec" ]; then
  cat %q
  exit 0
fi
echo "unexpected docker invocation: $*" >&2
exit 1
`, runtimeSourcePath))
	exec := &Executor{dockerBinary: dockerBin}
	logs, err := newExecutionLogs(root, "inst_1")
	if err != nil {
		t.Fatalf("newExecutionLogs() error = %v", err)
	}
	defer logs.Close()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		exec.captureAgentServerRuntimeLogPeriodically(ctx, "container-123", logs, 10*time.Millisecond)
	}()

	deadline := time.Now().Add(time.Second)
	runtimePath, _, _, ok := runfs.AbsoluteArtifactPath(root, "inst_1", store.ArtifactRoleAgentRuntime)
	if !ok {
		t.Fatalf("expected runtime artifact path")
	}
	for {
		body, err := os.ReadFile(runtimePath)
		if err == nil && strings.Contains(string(body), "server.listening") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("runtime artifact did not contain initial content before deadline")
		}
		time.Sleep(10 * time.Millisecond)
	}

	updated := initial + `{"v":1,"kind":"step","message":"run.dry_run_completed"}` + "\n"
	if err := os.WriteFile(runtimeSourcePath, []byte(updated), 0o644); err != nil {
		t.Fatalf("write updated runtime log: %v", err)
	}
	for {
		body, err := os.ReadFile(runtimePath)
		if err == nil && strings.Contains(string(body), "run.dry_run_completed") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("runtime artifact did not contain updated content before deadline")
		}
		time.Sleep(10 * time.Millisecond)
	}

	cancel()
	<-done
}

func TestNewRejectsCleanupWhenResolverDoesNotSupportCleanup(t *testing.T) {
	_, err := New(Config{
		AgentServerBinary:  writeTempBinary(t),
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
