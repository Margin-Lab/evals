package localexecutor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/marginlab/margin-eval/runner/runner-core/agentdef"
	"github.com/marginlab/margin-eval/runner/runner-core/agentexecutor"
	"github.com/marginlab/margin-eval/runner/runner-core/domain"
	"github.com/marginlab/margin-eval/runner/runner-core/imageresolver"
	"github.com/marginlab/margin-eval/runner/runner-core/runbundle"
	"github.com/marginlab/margin-eval/runner/runner-core/store"
	"github.com/marginlab/margin-eval/runner/runner-core/testassets"
	"github.com/marginlab/margin-eval/runner/runner-local/runfs"
)

const (
	defaultDockerBinary           = "docker"
	defaultContainerPort          = 8080
	defaultReadyPath              = "/readyz"
	defaultReadyTimeout           = 30 * time.Second
	defaultReadyPollInterval      = 500 * time.Millisecond
	defaultPTYCaptureInterval     = 2 * time.Second
	defaultPTYCaptureTimeout      = 5 * time.Second
	defaultRuntimeCaptureInterval = 2 * time.Second
	defaultHTTPTimeout            = 300 * time.Second
	defaultAgentServerRoot        = "/tmp/marginlab"
	defaultAgentAuthFilesDir      = defaultAgentServerRoot + "/config/auth-files"
	agentServerBinaryPath         = "/tmp/marginlab/bin/agent-server"
	agentServerRuntimeLogPath     = "/tmp/marginlab/state/agent-server.log"
	maxReadyResponseBodyRead      = 4096
	maxReadySummaryLength         = 512
	maxTestAssetsArchiveByte      = 64 << 20
	imageCleanupTimeout           = 15 * time.Second
	testExitCodePass              = 0
	testExitCodeFail              = 1
	testExitCodeInfra             = 2
)

type Config struct {
	AgentServerBinary         string
	AgentServerBinaryProvider AgentServerBinaryProvider
	DockerBinary              string
	ContainerPort             int
	ReadyPath                 string
	ReadyTimeout              time.Duration
	ReadyPollInterval         time.Duration

	AgentPollInterval time.Duration
	HTTPClient        *http.Client

	Env                  map[string]string
	Binds                map[string]string
	AuthFileOverridePath string
	ImageResolver        imageresolver.Resolver

	CleanupBuiltImages bool
}

type Executor struct {
	agentServerBinary         string
	agentServerBinaryProvider AgentServerBinaryProvider
	dockerBinary              string
	containerPort             int
	readyPath                 string
	readyTimeout              time.Duration
	readyPollInterval         time.Duration

	agentPollInterval time.Duration
	httpClient        *http.Client

	env                  map[string]string
	binds                map[string]string
	authFileOverridePath string
	imageResolver        imageresolver.Resolver
	imageCleaner         imageresolver.Cleaner

	cleanupBuiltImages bool

	imageBuildRefMu sync.Mutex
	imageBuildRefs  map[string]int
	runDirMu        sync.RWMutex
	runDirs         map[string]string
}

type buildLogResolver interface {
	ResolveWithBuildLog(ctx context.Context, in imageresolver.ResolveInput, buildLog io.Writer) (string, error)
}

type AgentServerBinaryProvider interface {
	ResolveAgentServerBinary(ctx context.Context, platform string) (string, error)
}

func New(cfg Config) (*Executor, error) {
	agentServerBinary := strings.TrimSpace(cfg.AgentServerBinary)
	var (
		absAgentServerBinary string
		err                  error
	)
	if agentServerBinary != "" {
		absAgentServerBinary, err = resolveAgentServerBinaryPath(agentServerBinary, "agent_server_binary")
		if err != nil {
			return nil, err
		}
	} else if cfg.AgentServerBinaryProvider == nil {
		return nil, fmt.Errorf("agent_server_binary or agent_server_binary_provider is required")
	}
	dockerBinary := strings.TrimSpace(cfg.DockerBinary)
	if dockerBinary == "" {
		dockerBinary = defaultDockerBinary
	}
	containerPort := cfg.ContainerPort
	if containerPort <= 0 {
		containerPort = defaultContainerPort
	}
	readyPath := strings.TrimSpace(cfg.ReadyPath)
	if readyPath == "" {
		readyPath = defaultReadyPath
	}
	if !strings.HasPrefix(readyPath, "/") {
		readyPath = "/" + readyPath
	}
	readyTimeout := cfg.ReadyTimeout
	if readyTimeout <= 0 {
		readyTimeout = defaultReadyTimeout
	}
	readyPollInterval := cfg.ReadyPollInterval
	if readyPollInterval <= 0 {
		readyPollInterval = defaultReadyPollInterval
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: defaultHTTPTimeout}
	}
	env := cloneStringMap(cfg.Env)
	authFileOverridePath := strings.TrimSpace(cfg.AuthFileOverridePath)
	if authFileOverridePath != "" {
		authFileOverridePath, err = filepath.Abs(authFileOverridePath)
		if err != nil {
			return nil, fmt.Errorf("resolve auth_file_override_path: %w", err)
		}
	}
	imageResolver := cfg.ImageResolver
	if imageResolver == nil {
		imageResolver, err = newLocalDockerImageResolver(dockerBinary)
		if err != nil {
			return nil, err
		}
	}
	imageCleaner, _ := imageResolver.(imageresolver.Cleaner)
	if cfg.CleanupBuiltImages && imageCleaner == nil {
		return nil, fmt.Errorf("cleanup_built_images requires an image resolver that supports cleanup")
	}

	return &Executor{
		agentServerBinary:         absAgentServerBinary,
		agentServerBinaryProvider: cfg.AgentServerBinaryProvider,
		dockerBinary:              dockerBinary,
		containerPort:             containerPort,
		readyPath:                 readyPath,
		readyTimeout:              readyTimeout,
		readyPollInterval:         readyPollInterval,
		agentPollInterval:         cfg.AgentPollInterval,
		httpClient:                client,
		env:                       env,
		binds:                     cloneStringMap(cfg.Binds),
		authFileOverridePath:      authFileOverridePath,
		imageResolver:             imageResolver,
		imageCleaner:              imageCleaner,
		cleanupBuiltImages:        cfg.CleanupBuiltImages,
		imageBuildRefs:            map[string]int{},
		runDirs:                   map[string]string{},
	}, nil
}

func (e *Executor) RegisterRunDir(runID, runDir string) error {
	trimmedRunID := strings.TrimSpace(runID)
	if trimmedRunID == "" {
		return fmt.Errorf("run id is required")
	}
	trimmedRunDir := strings.TrimSpace(runDir)
	if trimmedRunDir == "" {
		return fmt.Errorf("run dir is required")
	}
	absRunDir, err := filepath.Abs(trimmedRunDir)
	if err != nil {
		return fmt.Errorf("resolve run dir %q: %w", runDir, err)
	}
	if err := os.MkdirAll(absRunDir, 0o755); err != nil {
		return fmt.Errorf("create run dir %q: %w", absRunDir, err)
	}
	e.runDirMu.Lock()
	defer e.runDirMu.Unlock()
	if existing, ok := e.runDirs[trimmedRunID]; ok && existing != absRunDir {
		return fmt.Errorf("run dir already registered for run %s", trimmedRunID)
	}
	e.runDirs[trimmedRunID] = absRunDir
	return nil
}

func (e *Executor) runDir(runID string) (string, error) {
	e.runDirMu.RLock()
	defer e.runDirMu.RUnlock()
	runDir := strings.TrimSpace(e.runDirs[strings.TrimSpace(runID)])
	if runDir == "" {
		return "", fmt.Errorf("run dir not registered for run %s", strings.TrimSpace(runID))
	}
	return runDir, nil
}

func resolveAgentServerBinaryPath(value string, field string) (string, error) {
	absPath, err := filepath.Abs(strings.TrimSpace(value))
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", field, err)
	}
	info, err := os.Stat(absPath)
	if err != nil {
		return "", fmt.Errorf("stat %s: %w", field, err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("%s must be a file", field)
	}
	return absPath, nil
}

func (e *Executor) ExecuteInstance(
	ctx context.Context,
	run store.Run,
	inst store.Instance,
	updateState func(domain.InstanceState) error,
	updateResolvedImage func(string) error,
) (result store.InstanceResult, artifacts []store.Artifact, err error) {
	if updateState == nil {
		return store.InstanceResult{}, nil, fmt.Errorf("update_state callback is required")
	}
	if updateResolvedImage == nil {
		return store.InstanceResult{}, nil, fmt.Errorf("update_resolved_image callback is required")
	}
	runDir, err := e.runDir(run.RunID)
	if err != nil {
		return store.InstanceResult{}, nil, err
	}
	logs, err := newExecutionLogs(runDir, inst.InstanceID)
	if err != nil {
		return store.InstanceResult{}, nil, err
	}
	defer logs.Close()
	defer func() {
		artifacts = append(artifacts, logs.Artifacts()...)
	}()
	fail := func(result store.InstanceResult, artifacts []store.Artifact, cause error) (store.InstanceResult, []store.Artifact, error) {
		return result, artifacts, cause
	}

	caseSpec, err := resolveCaseForExecution(run, inst)
	if err != nil {
		return fail(store.InstanceResult{}, nil, err)
	}
	requiredAgentEnv, err := agentdef.ResolveRequiredEnvForConfigSpec(run.Bundle.ResolvedSnapshot.Agent.Definition, run.Bundle.ResolvedSnapshot.Agent.Config)
	if err != nil {
		return fail(store.InstanceResult{}, nil, fmt.Errorf("resolve required agent auth: %w", err))
	}
	resolvedAuthFiles, err := resolveLocalAuthCredentials(
		e.env,
		requiredAgentEnv,
		run.Bundle.ResolvedSnapshot.Agent.Definition.Manifest.Auth.LocalCredentials,
		e.authFileOverridePath,
	)
	if err != nil {
		return fail(store.InstanceResult{}, nil, err)
	}
	imageResolveDetails := map[string]string{
		"case_id":             caseSpec.CaseID,
		"image_present":       fmt.Sprintf("%t", strings.TrimSpace(caseSpec.Image) != ""),
		"image_build_present": fmt.Sprintf("%t", caseSpec.ImageBuild != nil),
	}
	if requested := strings.TrimSpace(caseSpec.Image); requested != "" {
		imageResolveDetails["requested_image"] = requested
	}
	_ = logs.Step(
		store.ArtifactRoleDockerBuild,
		"image.resolve",
		"start",
		"Starting case image resolution using explicit image or image_build fallback.",
		cloneStringMap(imageResolveDetails),
	)
	if strings.TrimSpace(caseSpec.Image) == "" && caseSpec.ImageBuild != nil {
		if err := updateState(domain.InstanceStateImageBuilding); err != nil {
			return fail(store.InstanceResult{}, nil, err)
		}
	}
	buildWriter, err := logs.Writer(store.ArtifactRoleDockerBuild)
	if err != nil {
		return fail(store.InstanceResult{}, nil, err)
	}
	caseImage, err := e.resolveCaseImageWithBuildLog(ctx, caseSpec, buildWriter)
	if err != nil {
		failedDetails := cloneStringMap(imageResolveDetails)
		failedDetails["error"] = err.Error()
		_ = logs.Step(
			store.ArtifactRoleDockerBuild,
			"image.resolve",
			"failed",
			"Case image resolution failed; no runnable container image was produced.",
			failedDetails,
		)
		return fail(store.InstanceResult{}, nil, err)
	}
	completedImageDetails := cloneStringMap(imageResolveDetails)
	completedImageDetails["resolved_image"] = caseImage
	_ = logs.Step(
		store.ArtifactRoleDockerBuild,
		"image.resolve",
		"completed",
		"Case image resolution completed; resolved image is ready for container start.",
		completedImageDetails,
	)
	cleanupInput, cleanupBuildKey := e.acquireImageBuildRef(caseSpec)
	if cleanupBuildKey != "" {
		defer e.releaseImageBuildRef(context.Background(), cleanupBuildKey, cleanupInput, caseImage)
	}
	if err := updateResolvedImage(caseImage); err != nil {
		return fail(store.InstanceResult{}, nil, err)
	}
	caseSpec.Image = caseImage
	inst.Case = caseSpec

	bootWriter, err := logs.Writer(store.ArtifactRoleAgentBoot)
	if err != nil {
		return fail(store.InstanceResult{}, nil, err)
	}

	authStageDir, stagedAuthFiles, err := stageLocalAuthFiles(resolvedAuthFiles)
	if err != nil {
		return fail(store.InstanceResult{}, nil, err)
	}
	if strings.TrimSpace(authStageDir) != "" {
		defer os.RemoveAll(authStageDir)
	}

	containerID, baseURL, err := e.startContainer(ctx, caseSpec.Image, requiredAgentEnv, bootWriter, logs)
	if err != nil {
		return fail(store.InstanceResult{}, nil, err)
	}
	selectedAgentServerBinary, err := e.resolveAgentServerBinaryForContainer(ctx, containerID)
	if err != nil {
		e.removeContainer(context.Background(), containerID)
		return fail(store.InstanceResult{}, nil, err)
	}
	stopPTYCapture := func() {}
	defer func() {
		stopPTYCapture()
		if captureErr := e.captureAgentServerPTYLog(context.Background(), containerID, logs); captureErr != nil {
			_ = logs.Step(
				store.ArtifactRoleAgentControl,
				"agent_server.pty_log.capture.final",
				"warning",
				"Failed to capture final agent PTY transcript before container teardown.",
				map[string]string{
					"container_id": containerID,
					"error":        captureErr.Error(),
				},
			)
		}
		if captureErr := e.captureAgentServerRuntimeLog(context.Background(), containerID, logs); captureErr != nil {
			_ = logs.Step(
				store.ArtifactRoleAgentControl,
				"agent_server.runtime_log.capture.final",
				"warning",
				"Failed to capture final agent-server runtime log before container teardown.",
				map[string]string{
					"container_id":     containerID,
					"runtime_log_path": agentServerRuntimeLogPath,
					"error":            captureErr.Error(),
				},
			)
		}
		e.removeContainer(context.Background(), containerID)
	}()
	stopRuntimeCapture := func() {}
	defer func() {
		stopRuntimeCapture()
	}()
	if len(stagedAuthFiles) > 0 {
		copyDetails := map[string]string{
			"container_id": containerID,
			"file_count":   fmt.Sprintf("%d", len(stagedAuthFiles)),
		}
		_ = logs.Step(
			store.ArtifactRoleAgentControl,
			"agent_server.auth_files.copy",
			"start",
			"Copying staged auth files into the container filesystem before run launch.",
			cloneStringMap(copyDetails),
		)
		if err := e.copyAuthFilesToContainer(ctx, containerID, stagedAuthFiles); err != nil {
			failedDetails := cloneStringMap(copyDetails)
			failedDetails["error"] = err.Error()
			_ = logs.Step(
				store.ArtifactRoleAgentControl,
				"agent_server.auth_files.copy",
				"failed",
				"Failed to copy staged auth files into the container filesystem.",
				failedDetails,
			)
			return fail(store.InstanceResult{}, nil, err)
		}
		_ = logs.Step(
			store.ArtifactRoleAgentControl,
			"agent_server.auth_files.copy",
			"completed",
			"Staged auth files were copied into the container filesystem.",
			copyDetails,
		)
	}

	if err := updateState(domain.InstanceStateAgentServerInstalling); err != nil {
		return fail(store.InstanceResult{}, nil, err)
	}
	if err := e.installAndStartAgentServer(ctx, containerID, selectedAgentServerBinary, bootWriter, logs); err != nil {
		return fail(store.InstanceResult{}, nil, err)
	}
	runtimeCaptureCtx, cancelRuntimeCapture := context.WithCancel(context.Background())
	runtimeCaptureDone := make(chan struct{})
	go func() {
		defer close(runtimeCaptureDone)
		e.captureAgentServerRuntimeLogPeriodically(runtimeCaptureCtx, containerID, logs, defaultRuntimeCaptureInterval)
	}()
	stopRuntimeCapture = func() {
		cancelRuntimeCapture()
		<-runtimeCaptureDone
	}

	if err := updateState(domain.InstanceStateBooting); err != nil {
		return fail(store.InstanceResult{}, nil, err)
	}
	readyEndpoint, err := buildReadyEndpoint(baseURL + e.readyPath)
	if err != nil {
		return fail(store.InstanceResult{}, nil, fmt.Errorf("build readiness endpoint: %w", err))
	}
	if err := e.waitForReady(ctx, readyEndpoint); err != nil {
		return fail(store.InstanceResult{}, nil, err)
	}
	if captureErr := e.captureAgentServerRuntimeLog(ctx, containerID, logs); captureErr != nil {
		_ = logs.Step(
			store.ArtifactRoleAgentControl,
			"agent_server.runtime_log.capture.ready",
			"warning",
			"Failed to capture agent-server runtime log immediately after readiness succeeded.",
			map[string]string{
				"container_id":       containerID,
				"runtime_log_path":   agentServerRuntimeLogPath,
				"readiness_endpoint": readyEndpoint,
				"error":              captureErr.Error(),
			},
		)
	}
	ptyCaptureCtx, cancelPTYCapture := context.WithCancel(context.Background())
	ptyCaptureDone := make(chan struct{})
	go func() {
		defer close(ptyCaptureDone)
		e.captureAgentServerPTYLogPeriodically(ptyCaptureCtx, containerID, logs, defaultPTYCaptureInterval)
	}()
	stopPTYCapture = func() {
		cancelPTYCapture()
		<-ptyCaptureDone
	}
	provisionedAt := time.Now().UTC()
	if err := e.ensureCaseWorkingDir(ctx, containerID, caseSpec); err != nil {
		return fail(store.InstanceResult{}, nil, err)
	}

	agentExec, err := agentexecutor.New(agentexecutor.Config{
		BaseURL:      baseURL,
		HTTPClient:   e.httpClient,
		ArtifactRoot: runfs.InstanceDir(runDir, inst.InstanceID),
		AuthFiles:    toAgentExecutorAuthFiles(stagedAuthFiles),
		PollInterval: e.agentPollInterval,
		OnStep: func(ev agentexecutor.StepEvent) {
			_ = logs.Step(store.ArtifactRoleAgentControl, ev.Step, ev.Status, ev.Message, ev.Details)
		},
	})
	if err != nil {
		return fail(store.InstanceResult{}, nil, err)
	}

	updateStateWithRuntimeCapture := wrapUpdateStateWithRuntimeCapture(updateState, stopRuntimeCapture)
	result, artifacts, err = agentExec.ExecuteInstance(ctx, run, inst, updateStateWithRuntimeCapture)
	stopRuntimeCapture()
	stopRuntimeCapture = func() {}
	stopPTYCapture()
	stopPTYCapture = func() {}
	if result.ProvisionedAt == nil {
		result.ProvisionedAt = timePtr(provisionedAt)
	}
	e.rebaseStoredArtifacts(inst.InstanceID, artifacts)
	if strings.TrimSpace(result.Trajectory) != "" {
		if rel, _, ok := runfs.RelativePathForRole(inst.InstanceID, store.ArtifactRoleTrajectory); ok {
			result.Trajectory = rel
		}
	}

	if err != nil {
		return fail(result, artifacts, err)
	}

	if err := updateState(domain.InstanceStateTesting); err != nil {
		return fail(store.InstanceResult{}, artifacts, err)
	}
	testStartedAt := time.Now().UTC()
	if err := e.stageCaseTestAssets(ctx, containerID, caseSpec); err != nil {
		return fail(store.InstanceResult{}, artifacts, err)
	}
	testResult, err := e.executeCaseTest(ctx, runDir, inst.InstanceID, containerID, caseSpec)
	if err != nil {
		return fail(store.InstanceResult{}, append(artifacts, testResult.Artifacts...), err)
	}
	testEndedAt := time.Now().UTC()

	result.TestExitCode = intPtr(testResult.ExitCode)
	result.TestStartedAt = timePtr(testStartedAt)
	result.TestEndedAt = timePtr(testEndedAt)
	priorFinalState := result.FinalState
	switch {
	case priorFinalState.IsTerminal() && priorFinalState != domain.InstanceStateSucceeded:
		// Preserve an earlier terminal failure classification from agent execution.
		result.FinalState = priorFinalState
	default:
		result.FinalState = classifyTestFinalState(testResult.ExitCode)
		if result.FinalState == domain.InstanceStateInfraFailed {
			if result.ErrorCode == "" {
				if testResult.ExitCode == testExitCodeInfra {
					result.ErrorCode = "TEST_INFRA"
				} else {
					result.ErrorCode = "INVALID_TEST_EXIT_CODE"
				}
			}
			if result.ErrorMessage == "" {
				if testResult.ExitCode == testExitCodeInfra {
					result.ErrorMessage = "case test script reported infra failure"
				} else {
					result.ErrorMessage = fmt.Sprintf("case test script exited with unsupported status %d; expected 0, 1, or 2", testResult.ExitCode)
				}
			}
		}
	}

	if err := updateState(domain.InstanceStateCollecting); err != nil {
		return fail(store.InstanceResult{}, artifacts, err)
	}

	result.TestStdoutRef = testResult.StdoutRef
	result.TestStderrRef = testResult.StderrRef
	artifacts = append(artifacts, testResult.Artifacts...)
	return result, artifacts, nil
}

func classifyTestFinalState(testExitCode int) domain.InstanceState {
	switch testExitCode {
	case testExitCodePass:
		return domain.InstanceStateSucceeded
	case testExitCodeFail:
		return domain.InstanceStateTestFailed
	case testExitCodeInfra:
		return domain.InstanceStateInfraFailed
	default:
		return domain.InstanceStateInfraFailed
	}
}

func resolveCaseForExecution(run store.Run, inst store.Instance) (runbundle.Case, error) {
	if inst.Ordinal < 0 || inst.Ordinal >= len(run.Bundle.ResolvedSnapshot.Cases) {
		return runbundle.Case{}, fmt.Errorf("instance %q has invalid case ordinal %d", inst.InstanceID, inst.Ordinal)
	}
	caseSpec := run.Bundle.ResolvedSnapshot.Cases[inst.Ordinal]
	if strings.TrimSpace(caseSpec.CaseID) != strings.TrimSpace(inst.Case.CaseID) {
		return runbundle.Case{}, fmt.Errorf(
			"instance %q case mismatch: bundle case_id=%q instance case_id=%q",
			inst.InstanceID,
			caseSpec.CaseID,
			inst.Case.CaseID,
		)
	}
	if resolved := strings.TrimSpace(inst.Case.Image); resolved != "" {
		caseSpec.Image = resolved
	}
	return caseSpec, nil
}

func (e *Executor) resolveCaseImage(ctx context.Context, c runbundle.Case) (string, error) {
	return e.resolveCaseImageWithBuildLog(ctx, c, nil)
}

func (e *Executor) resolveCaseImageWithBuildLog(ctx context.Context, c runbundle.Case, buildLog io.Writer) (string, error) {
	input := resolveInputFromCase(c)
	var (
		resolved string
		err      error
	)
	if resolver, ok := e.imageResolver.(buildLogResolver); ok {
		resolved, err = resolver.ResolveWithBuildLog(ctx, input, buildLog)
	} else {
		resolved, err = e.imageResolver.Resolve(ctx, input)
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(resolved), nil
}

func resolveInputFromCase(c runbundle.Case) imageresolver.ResolveInput {
	return imageresolver.ResolveInput{
		CaseID:     strings.TrimSpace(c.CaseID),
		Image:      strings.TrimSpace(c.Image),
		ImageBuild: c.ImageBuild,
	}
}

func (e *Executor) acquireImageBuildRef(c runbundle.Case) (imageresolver.ResolveInput, string) {
	input := resolveInputFromCase(c)
	if !e.cleanupBuiltImages || input.ImageBuild == nil || e.imageCleaner == nil {
		return input, ""
	}
	buildKey := strings.TrimSpace(imageresolver.BuildKey(input))
	if buildKey == "" {
		return input, ""
	}
	e.imageBuildRefMu.Lock()
	e.imageBuildRefs[buildKey] = e.imageBuildRefs[buildKey] + 1
	e.imageBuildRefMu.Unlock()
	return input, buildKey
}

func (e *Executor) releaseImageBuildRef(ctx context.Context, buildKey string, input imageresolver.ResolveInput, resolvedImage string) {
	key := strings.TrimSpace(buildKey)
	if key == "" || !e.cleanupBuiltImages || e.imageCleaner == nil {
		return
	}
	shouldCleanup := false
	e.imageBuildRefMu.Lock()
	current := e.imageBuildRefs[key]
	if current <= 1 {
		delete(e.imageBuildRefs, key)
		shouldCleanup = true
	} else {
		e.imageBuildRefs[key] = current - 1
	}
	e.imageBuildRefMu.Unlock()
	if !shouldCleanup {
		return
	}
	cleanupCtx, cancel := context.WithTimeout(ctx, imageCleanupTimeout)
	defer cancel()
	_ = e.imageCleaner.Cleanup(cleanupCtx, input, resolvedImage)
}

func (e *Executor) startContainer(ctx context.Context, caseImage string, requiredAgentEnv []string, bootLog io.Writer, logs *executionLogs) (string, string, error) {
	cidDir, err := os.MkdirTemp("", "marginlab-container-id-*")
	if err != nil {
		return "", "", fmt.Errorf("create docker cidfile dir: %w", err)
	}
	defer os.RemoveAll(cidDir)
	cidFilePath := filepath.Join(cidDir, "cid")
	startDetails := map[string]string{
		"image":          strings.TrimSpace(caseImage),
		"container_port": fmt.Sprintf("%d", e.containerPort),
		"bind_count":     fmt.Sprintf("%d", len(e.binds)),
	}
	if logs != nil {
		_ = logs.Step(
			store.ArtifactRoleAgentBoot,
			"container.start",
			"start",
			"Starting container from resolved case image.",
			cloneStringMap(startDetails),
		)
	}

	args := []string{
		"run",
		"-d",
		"--cidfile", cidFilePath,
		"-p",
		fmt.Sprintf("127.0.0.1::%d", e.containerPort),
	}

	env := e.containerEnv(requiredAgentEnv)
	envKeys := sortedKeys(env)
	for _, key := range envKeys {
		args = append(args, "-e", key+"="+env[key])
	}
	bindKeys := sortedKeys(e.binds)
	for _, hostPath := range bindKeys {
		containerPath := strings.TrimSpace(e.binds[hostPath])
		if containerPath == "" {
			return "", "", fmt.Errorf("bind for host path %q has empty container path", hostPath)
		}
		args = append(args, "-v", hostPath+":"+containerPath+":ro")
	}
	args = append(args,
		"--entrypoint", "/bin/sh",
		caseImage,
		"-lc", "while true; do sleep 3600; done",
	)

	if _, err := e.runDockerWithWriter(ctx, bootLog, args...); err != nil {
		if logs != nil {
			failedDetails := cloneStringMap(startDetails)
			failedDetails["error"] = err.Error()
			_ = logs.Step(
				store.ArtifactRoleAgentBoot,
				"container.start",
				"failed",
				"Failed to start container from resolved case image.",
				failedDetails,
			)
		}
		return "", "", err
	}
	containerID, err := readContainerID(cidFilePath)
	if err != nil {
		if logs != nil {
			failedDetails := cloneStringMap(startDetails)
			failedDetails["error"] = err.Error()
			_ = logs.Step(
				store.ArtifactRoleAgentBoot,
				"container.start",
				"failed",
				"Container start completed but the container ID file could not be read.",
				failedDetails,
			)
		}
		return "", "", err
	}
	baseURL, err := e.resolveBaseURL(ctx, containerID)
	if err != nil {
		e.removeContainer(context.Background(), containerID)
		if logs != nil {
			failedDetails := cloneStringMap(startDetails)
			failedDetails["container_id"] = containerID
			failedDetails["error"] = err.Error()
			_ = logs.Step(
				store.ArtifactRoleAgentBoot,
				"container.start",
				"failed",
				"Container started but the published port mapping could not be resolved.",
				failedDetails,
			)
		}
		return "", "", err
	}
	if logs != nil {
		completedDetails := cloneStringMap(startDetails)
		completedDetails["container_id"] = containerID
		completedDetails["base_url"] = baseURL
		_ = logs.Step(
			store.ArtifactRoleAgentBoot,
			"container.start",
			"completed",
			"Container started from resolved case image and port mapping was resolved.",
			completedDetails,
		)
	}
	return containerID, baseURL, nil
}

func (e *Executor) installAndStartAgentServer(
	ctx context.Context,
	containerID string,
	agentServerBinary string,
	bootstrapLog io.Writer,
	logs *executionLogs,
) error {
	runStep := func(step, startMessage, completedMessage, failedMessage string, details map[string]string, args ...string) error {
		_ = logs.Step(store.ArtifactRoleAgentBoot, step, "start", startMessage, cloneStringMap(details))
		if _, err := e.runDockerWithWriter(ctx, bootstrapLog, args...); err != nil {
			failedDetails := cloneStringMap(details)
			failedDetails["error"] = err.Error()
			_ = logs.Step(store.ArtifactRoleAgentBoot, step, "failed", failedMessage, failedDetails)
			return err
		}
		_ = logs.Step(store.ArtifactRoleAgentBoot, step, "completed", completedMessage, cloneStringMap(details))
		return nil
	}

	if err := runStep(
		"agent_server.bootstrap.prepare",
		"Starting bootstrap prepare: creating runtime/state directories inside container.",
		"Bootstrap prepare completed: runtime/state directories are ready inside container.",
		"Bootstrap prepare failed while creating runtime/state directories inside container.",
		map[string]string{
			"container_id": containerID,
			"runtime_root": defaultAgentServerRoot,
		},
		"exec", containerID, "sh", "-lc", buildBootstrapCommand(),
	); err != nil {
		return err
	}
	if err := runStep(
		"agent_server.bootstrap.copy_binary",
		"Starting bootstrap copy: transferring local agent-server binary into container filesystem.",
		"Bootstrap copy completed: agent-server binary is present in container filesystem.",
		"Bootstrap copy failed while transferring agent-server binary into the container.",
		map[string]string{
			"container_id": containerID,
			"source_path":  agentServerBinary,
			"target_path":  agentServerBinaryPath,
		},
		"cp", agentServerBinary, containerID+":"+agentServerBinaryPath,
	); err != nil {
		return err
	}
	if err := runStep(
		"agent_server.bootstrap.chmod_binary",
		"Starting bootstrap chmod: applying execute permission to agent-server binary in container.",
		"Bootstrap chmod completed: agent-server binary is executable in container.",
		"Bootstrap chmod failed while applying execute permission to agent-server binary.",
		map[string]string{
			"container_id": containerID,
			"target_path":  agentServerBinaryPath,
		},
		"exec", containerID, "chmod", "+x", agentServerBinaryPath,
	); err != nil {
		return err
	}
	if err := runStep(
		"agent_server.bootstrap.start_process",
		"Starting bootstrap launch: running agent-server in background with runtime log redirection.",
		"Bootstrap launch completed: background agent-server start command exited successfully.",
		"Bootstrap launch failed while starting agent-server in background.",
		map[string]string{
			"container_id":     containerID,
			"binary_path":      agentServerBinaryPath,
			"runtime_log_path": agentServerRuntimeLogPath,
		},
		"exec",
		"-d",
		containerID,
		"sh",
		"-lc",
		agentServerBinaryPath+" >"+agentServerRuntimeLogPath+" 2>&1",
	); err != nil {
		return err
	}
	return nil
}

func (e *Executor) copyAuthFilesToContainer(ctx context.Context, containerID string, files []stagedLocalAuthFile) error {
	if len(files) == 0 {
		return nil
	}
	if _, err := e.runDocker(ctx, "exec", containerID, "mkdir", "-p", defaultAgentAuthFilesDir); err != nil {
		return fmt.Errorf("prepare auth file directory %q: %w", defaultAgentAuthFilesDir, err)
	}
	for _, file := range files {
		hostPath := strings.TrimSpace(file.HostPath)
		if hostPath == "" {
			return fmt.Errorf("staged auth file for %q is missing host path", file.RequiredEnv)
		}
		sourcePath := strings.TrimSpace(file.SourcePath)
		if sourcePath == "" {
			return fmt.Errorf("staged auth file for %q is missing container source path", file.RequiredEnv)
		}
		if _, err := e.runDocker(ctx, "cp", hostPath, containerID+":"+sourcePath); err != nil {
			return fmt.Errorf("copy auth file for %q to %q: %w", file.RequiredEnv, sourcePath, err)
		}
	}
	return nil
}

func (e *Executor) resolveBaseURL(ctx context.Context, containerID string) (string, error) {
	portRaw, err := e.runDocker(ctx, "port", containerID, fmt.Sprintf("%d/tcp", e.containerPort))
	if err != nil {
		return "", err
	}
	lines := strings.Split(strings.TrimSpace(portRaw), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) == "" {
		return "", fmt.Errorf("docker port returned no mapping for %d/tcp", e.containerPort)
	}
	mapped := strings.TrimSpace(lines[0])
	_, port, err := splitHostPort(mapped)
	if err != nil {
		return "", err
	}
	return "http://127.0.0.1:" + port, nil
}

func (e *Executor) resolveAgentServerBinaryForContainer(ctx context.Context, containerID string) (string, error) {
	if strings.TrimSpace(e.agentServerBinary) != "" {
		return e.agentServerBinary, nil
	}
	platform, err := e.inspectContainerPlatform(ctx, containerID)
	if err != nil {
		return "", err
	}
	if e.agentServerBinaryProvider == nil {
		return "", fmt.Errorf("no agent-server binary provider configured for platform-aware selection")
	}
	return e.agentServerBinaryProvider.ResolveAgentServerBinary(ctx, platform)
}

func (e *Executor) inspectContainerPlatform(ctx context.Context, containerID string) (string, error) {
	imageID, err := e.runDocker(ctx, "inspect", containerID, "--format", "{{.Image}}")
	if err != nil {
		return "", fmt.Errorf("inspect container image id for %q: %w", containerID, err)
	}
	imageID = strings.TrimSpace(imageID)
	if imageID == "" {
		return "", fmt.Errorf("inspect container image id for %q returned empty image id", containerID)
	}
	platform, err := e.runDocker(ctx, "image", "inspect", imageID, "--format", "{{.Os}}/{{.Architecture}}")
	if err != nil {
		return "", fmt.Errorf("inspect container platform for %q: %w", containerID, err)
	}
	platform = strings.ToLower(strings.TrimSpace(platform))
	if platform == "" || platform == "/" || strings.HasPrefix(platform, "/") || strings.HasSuffix(platform, "/") {
		return "", fmt.Errorf("inspect container platform for %q returned invalid platform %q", containerID, platform)
	}
	return platform, nil
}

func (e *Executor) waitForReady(ctx context.Context, endpoint string) error {
	deadline := time.NewTimer(e.readyTimeout)
	defer deadline.Stop()
	ticker := time.NewTicker(e.readyPollInterval)
	defer ticker.Stop()
	var lastNotReadyDetail string
	var lastTransportErr error

	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return fmt.Errorf("build readiness request: %w", err)
		}
		resp, err := e.httpClient.Do(req)
		if err == nil {
			body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxReadyResponseBodyRead))
			_ = resp.Body.Close()
			if readErr != nil {
				body = []byte(fmt.Sprintf("failed to read readiness response body: %v", readErr))
			}
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				return nil
			}
			lastNotReadyDetail = summarizeNotReadyResponse(resp.StatusCode, body)
			lastTransportErr = nil
		} else {
			lastTransportErr = err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			if lastNotReadyDetail != "" {
				return fmt.Errorf(
					"agent-server readiness check timed out after %s at %s; last not-ready response: %s",
					e.readyTimeout,
					endpoint,
					lastNotReadyDetail,
				)
			}
			if lastTransportErr != nil {
				return fmt.Errorf(
					"Never received a /readyz HTTP response from `agent-server`, please check that `agent-server` binary runs on the host environment (timed out after %s at %s; last transport error: %v)",
					e.readyTimeout,
					endpoint,
					lastTransportErr,
				)
			}
			return fmt.Errorf("timed out waiting for agent-server readiness after %s at %s", e.readyTimeout, endpoint)
		case <-ticker.C:
		}
	}
}

func summarizeNotReadyResponse(statusCode int, body []byte) string {
	statusSummary := fmt.Sprintf("status=%d", statusCode)
	if text := strings.TrimSpace(http.StatusText(statusCode)); text != "" {
		statusSummary = fmt.Sprintf("%s (%s)", statusSummary, text)
	}

	trimmedBody := strings.TrimSpace(string(body))
	if trimmedBody == "" {
		return statusSummary
	}

	type readyEnvelope struct {
		Status     string `json:"status"`
		Summary    string `json:"summary"`
		ReasonCode string `json:"reason_code"`
		Checks     map[string]struct {
			Status     string         `json:"status"`
			ReasonCode string         `json:"reason_code"`
			Message    string         `json:"message"`
			Details    map[string]any `json:"details"`
		} `json:"checks"`
	}
	var parsed readyEnvelope
	if err := json.Unmarshal([]byte(trimmedBody), &parsed); err == nil && strings.TrimSpace(parsed.Status) != "" {
		parts := make([]string, 0, 4)
		if status := strings.TrimSpace(parsed.Status); status != "" {
			parts = append(parts, "status="+status)
		}
		if reasonCode := strings.TrimSpace(parsed.ReasonCode); reasonCode != "" {
			parts = append(parts, "reason_code="+reasonCode)
		}
		if summary := strings.TrimSpace(parsed.Summary); summary != "" {
			parts = append(parts, "summary="+summary)
		}
		for name, check := range parsed.Checks {
			if !strings.EqualFold(strings.TrimSpace(check.Status), "not_ready") {
				continue
			}
			checkParts := []string{name}
			if code := strings.TrimSpace(check.ReasonCode); code != "" {
				checkParts = append(checkParts, "reason_code="+code)
			}
			if msg := strings.TrimSpace(check.Message); msg != "" {
				checkParts = append(checkParts, "message="+msg)
			}
			if len(check.Details) > 0 {
				if detailsJSON, marshalErr := json.Marshal(check.Details); marshalErr == nil {
					checkParts = append(checkParts, "details="+string(detailsJSON))
				}
			}
			parts = append(parts, "failing_check="+strings.Join(checkParts, " "))
			break
		}
		if len(parts) > 0 {
			return truncateReadySummary(statusSummary + ": " + strings.Join(parts, "; "))
		}
	}

	cleanBody := strings.Join(strings.Fields(strings.ReplaceAll(strings.ReplaceAll(trimmedBody, "\r", " "), "\n", " ")), " ")
	return truncateReadySummary(statusSummary + ": body=" + cleanBody)
}

func truncateReadySummary(text string) string {
	if len(text) <= maxReadySummaryLength {
		return text
	}
	return text[:maxReadySummaryLength] + "..."
}

func buildReadyEndpoint(endpoint string) (string, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return "", err
	}
	return parsed.String(), nil
}

func (e *Executor) captureAgentServerRuntimeLog(ctx context.Context, containerID string, logs *executionLogs) error {
	if logs == nil || strings.TrimSpace(containerID) == "" {
		return nil
	}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		timeoutCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		ctx = timeoutCtx
	}
	output, err := e.runDocker(ctx, "exec", containerID, "sh", "-lc", "cat "+agentServerRuntimeLogPath)
	if err != nil {
		return err
	}
	return logs.Replace(store.ArtifactRoleAgentRuntime, []byte(output))
}

func (e *Executor) captureAgentServerRuntimeLogPeriodically(ctx context.Context, containerID string, logs *executionLogs, interval time.Duration) {
	if interval <= 0 {
		interval = defaultRuntimeCaptureInterval
	}
	capture := func(step, phase string) bool {
		if err := e.captureAgentServerRuntimeLog(ctx, containerID, logs); err != nil {
			if ctx.Err() != nil {
				return false
			}
			if isSkippableRuntimeLogCaptureError(err) {
				return true
			}
			_ = logs.Step(
				store.ArtifactRoleAgentControl,
				step,
				"warning",
				fmt.Sprintf("Failed to capture %s agent-server runtime log snapshot during live polling.", phase),
				map[string]string{
					"container_id":     containerID,
					"runtime_log_path": agentServerRuntimeLogPath,
					"error":            err.Error(),
				},
			)
			return true
		}
		return true
	}
	if !capture("agent_server.runtime_log.capture.initial", "initial") {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !capture("agent_server.runtime_log.capture.poll", "periodic") {
				return
			}
		}
	}
}

func (e *Executor) captureAgentServerPTYLog(ctx context.Context, containerID string, logs *executionLogs) error {
	if logs == nil || strings.TrimSpace(containerID) == "" {
		return nil
	}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		timeoutCtx, cancel := context.WithTimeout(ctx, defaultPTYCaptureTimeout)
		defer cancel()
		ctx = timeoutCtx
	}
	output, err := e.runDocker(
		ctx,
		"exec",
		containerID,
		"sh",
		"-lc",
		`path=$(ls -1t /tmp/marginlab/state/runs/*/artifacts/pty.log 2>/dev/null | head -n 1); [ -n "$path" ] || exit 0; cat "$path"`,
	)
	if err != nil {
		return err
	}
	if output == "" {
		return nil
	}
	return logs.Replace(store.ArtifactRoleAgentPTY, []byte(output))
}

func (e *Executor) captureAgentServerPTYLogPeriodically(ctx context.Context, containerID string, logs *executionLogs, interval time.Duration) {
	if interval <= 0 {
		interval = defaultPTYCaptureInterval
	}
	capture := func(step, phase string) {
		if err := e.captureAgentServerPTYLog(ctx, containerID, logs); err != nil {
			_ = logs.Step(
				store.ArtifactRoleAgentControl,
				step,
				"warning",
				fmt.Sprintf("Failed to capture %s agent PTY log snapshot during live polling.", phase),
				map[string]string{
					"container_id": containerID,
					"phase":        phase,
					"error":        err.Error(),
				},
			)
		}
	}
	capture("agent_server.pty_log.capture.start", "initial")
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			capture("agent_server.pty_log.capture.poll", "periodic")
		}
	}
}

func wrapUpdateStateWithRuntimeCapture(updateState func(domain.InstanceState) error, stopRuntimeCapture func()) func(domain.InstanceState) error {
	return func(state domain.InstanceState) error {
		if stopRuntimeCapture != nil && !isProvisioningState(state) {
			stopRuntimeCapture()
			stopRuntimeCapture = nil
		}
		return updateState(state)
	}
}

func isProvisioningState(state domain.InstanceState) bool {
	switch state {
	case domain.InstanceStateProvisioning,
		domain.InstanceStateImageBuilding,
		domain.InstanceStateAgentServerInstalling,
		domain.InstanceStateBooting,
		domain.InstanceStateAgentInstalling,
		domain.InstanceStateAgentConfiguring:
		return true
	default:
		return false
	}
}

func isSkippableRuntimeLogCaptureError(err error) bool {
	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(msg, "no such file") ||
		strings.Contains(msg, "cannot open") ||
		strings.Contains(msg, "not found")
}

func (e *Executor) removeContainer(ctx context.Context, containerID string) {
	if strings.TrimSpace(containerID) == "" {
		return
	}
	_, _ = e.runDocker(ctx, "rm", "-f", containerID)
}

func (e *Executor) runDocker(ctx context.Context, args ...string) (string, error) {
	return e.runDockerWithWriter(ctx, nil, args...)
}

func (e *Executor) runDockerWithWriter(ctx context.Context, outputLog io.Writer, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, e.dockerBinary, args...)
	var out bytes.Buffer
	if outputLog != nil {
		mw := newSynchronizedWriter(io.MultiWriter(&out, outputLog))
		cmd.Stdout = mw
		cmd.Stderr = mw
	} else {
		writer := newSynchronizedWriter(&out)
		cmd.Stdout = writer
		cmd.Stderr = writer
	}
	err := cmd.Run()
	if err != nil {
		return "", fmt.Errorf("docker %s failed: %w\noutput:\n%s", strings.Join(args, " "), err, out.String())
	}
	return out.String(), nil
}

func readContainerID(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read docker cidfile: %w", err)
	}
	containerID := strings.TrimSpace(string(raw))
	if containerID == "" {
		return "", fmt.Errorf("docker run produced empty cidfile: %s", path)
	}
	return containerID, nil
}

func (e *Executor) executeCaseTest(ctx context.Context, runDir, instanceID, containerID string, tc runbundle.Case) (testExecutionResult, error) {
	timeout := time.Duration(tc.TestTimeoutSecond) * time.Second
	if timeout <= 0 {
		return testExecutionResult{}, fmt.Errorf("case test_timeout_seconds must be > 0")
	}
	testCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	capture, err := newTestOutputCapture(runDir, instanceID)
	if err != nil {
		return testExecutionResult{}, err
	}
	defer capture.close()

	args := []string{"exec", "-w", tc.TestCwd, containerID}
	args = append(args, tc.TestCommand...)

	cmd := exec.CommandContext(testCtx, e.dockerBinary, args...)
	cmd.Stdout = capture.stdoutFile
	cmd.Stderr = capture.stderrFile
	err = cmd.Run()

	result := testExecutionResult{}
	switch {
	case err == nil:
		result.ExitCode = 0
	case errors.Is(testCtx.Err(), context.DeadlineExceeded):
		result.ExitCode = 124
	default:
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			result.ExitCode = exitErr.ExitCode()
		}
	}

	artifacts, stdoutRef, stderrRef, finalizeErr := capture.finalize()
	result.Artifacts = artifacts
	result.StdoutRef = stdoutRef
	result.StderrRef = stderrRef
	if finalizeErr != nil {
		if err != nil {
			return result, fmt.Errorf("docker %s failed: %w; finalize streamed test output artifacts: %v", strings.Join(args, " "), err, finalizeErr)
		}
		return result, fmt.Errorf("finalize streamed test output artifacts: %w", finalizeErr)
	}
	if err == nil {
		return result, nil
	}
	if errors.Is(testCtx.Err(), context.DeadlineExceeded) {
		return result, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return result, nil
	}
	return result, fmt.Errorf("docker %s failed: %w", strings.Join(args, " "), err)
}

func (e *Executor) stageCaseTestAssets(ctx context.Context, containerID string, tc runbundle.Case) error {
	testCwd := strings.TrimSpace(tc.TestCwd)
	if testCwd == "" {
		return fmt.Errorf("case test_cwd is required")
	}

	tempDir, err := os.MkdirTemp("", "marginlab-test-assets-*")
	if err != nil {
		return fmt.Errorf("create temp test assets directory: %w", err)
	}
	defer os.RemoveAll(tempDir)

	if err := testassets.Materialize(tc.TestAssets, tempDir, maxTestAssetsArchiveByte); err != nil {
		return fmt.Errorf("materialize case test assets: %w", err)
	}

	testsDir := path.Join(testCwd, "tests")
	if err := e.ensureCaseWorkingDir(ctx, containerID, tc); err != nil {
		return err
	}
	if _, err := e.runDocker(ctx, "exec", containerID, "rm", "-rf", testsDir); err != nil {
		return err
	}
	if _, err := e.runDocker(ctx, "exec", containerID, "mkdir", "-p", testsDir); err != nil {
		return err
	}
	if _, err := e.runDocker(ctx, "cp", tempDir+"/.", containerID+":"+testsDir); err != nil {
		return err
	}
	return nil
}

func (e *Executor) ensureCaseWorkingDir(ctx context.Context, containerID string, tc runbundle.Case) error {
	testCwd := strings.TrimSpace(tc.TestCwd)
	if testCwd == "" {
		return fmt.Errorf("case test_cwd is required")
	}
	if _, err := e.runDocker(ctx, "exec", containerID, "mkdir", "-p", testCwd); err != nil {
		return fmt.Errorf("prepare case working directory %q: %w", testCwd, err)
	}
	return nil
}

func (e *Executor) rebaseStoredArtifacts(instanceID string, artifacts []store.Artifact) {
	for i := range artifacts {
		if rel, _, ok := runfs.RelativePathForRole(instanceID, artifacts[i].Role); ok {
			artifacts[i].StoreKey = rel
		}
	}
}

func splitHostPort(raw string) (string, string, error) {
	raw = strings.TrimSpace(raw)
	host, port, err := net.SplitHostPort(raw)
	if err == nil {
		return host, port, nil
	}
	if strings.Contains(raw, ":") {
		parts := strings.Split(raw, ":")
		if len(parts) >= 2 {
			port = strings.TrimSpace(parts[len(parts)-1])
			host = strings.TrimSpace(strings.Join(parts[:len(parts)-1], ":"))
			if port != "" {
				return host, port, nil
			}
		}
	}
	return "", "", fmt.Errorf("parse docker port mapping %q: %w", raw, err)
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func cloneStringMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func (e *Executor) containerEnv(requiredAgentEnv []string) map[string]string {
	out := map[string]string{
		"AGENT_SERVER_LISTEN":         fmt.Sprintf(":%d", e.containerPort),
		"AGENT_SERVER_ROOT":           defaultAgentServerRoot,
		"AGENT_SERVER_BIN_DIR":        defaultAgentServerRoot + "/bin",
		"AGENT_SERVER_STATE_DIR":      defaultAgentServerRoot + "/state",
		"AGENT_SERVER_CONFIG_DIR":     defaultAgentServerRoot + "/config",
		"AGENT_SERVER_WORKSPACES_DIR": "/",
	}
	for k, v := range e.env {
		out[k] = v
	}
	injectRequiredAgentEnv(out, requiredAgentEnv)
	return out
}

func timePtr(v time.Time) *time.Time {
	return &v
}

func intPtr(v int) *int {
	return &v
}

func sanitizeID(value string) string {
	replacer := strings.NewReplacer("/", "-", "\\", "-", " ", "-", "\t", "-", "\n", "-", ":", "-", ".", "-")
	return replacer.Replace(strings.TrimSpace(value))
}
