package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/marginlab/margin-eval/cli/internal/compiler"
	"github.com/marginlab/margin-eval/cli/internal/missioncontrol"
	"github.com/marginlab/margin-eval/cli/internal/plaincontrol"
	"github.com/marginlab/margin-eval/cli/internal/remotesuite"

	"github.com/marginlab/margin-eval/runner/runner-core/agentdef"
	"github.com/marginlab/margin-eval/runner/runner-core/domain"
	"github.com/marginlab/margin-eval/runner/runner-core/engine"
	"github.com/marginlab/margin-eval/runner/runner-core/runbundle"
	"github.com/marginlab/margin-eval/runner/runner-core/runnerapi"
	"github.com/marginlab/margin-eval/runner/runner-core/store"
	"github.com/marginlab/margin-eval/runner/runner-core/testassets"
	"github.com/marginlab/margin-eval/runner/runner-local/localexecutor"
	"github.com/marginlab/margin-eval/runner/runner-local/localrunner"
	"github.com/marginlab/margin-eval/runner/runner-local/runfs"
)

type fakeRunnerService struct{}

func (f fakeRunnerService) Start(_ context.Context) {}

func (f fakeRunnerService) SubmitRun(_ context.Context, _ runnerapi.SubmitInput) (store.Run, error) {
	return store.Run{RunID: "run_test_1"}, nil
}

func (f fakeRunnerService) WaitForTerminalRun(_ context.Context, runID string, _ time.Duration) (store.Run, error) {
	return store.Run{RunID: runID, State: domain.RunStateCompleted}, nil
}

func (f fakeRunnerService) GetRunSnapshot(_ context.Context, _ string, _ runnerapi.SnapshotOptions) (runnerapi.RunSnapshot, error) {
	return runnerapi.RunSnapshot{}, nil
}

func (f fakeRunnerService) GetInstanceSnapshot(_ context.Context, _ string, _ runnerapi.SnapshotOptions) (runnerapi.InstanceSnapshot, error) {
	return runnerapi.InstanceSnapshot{}, nil
}

type fakeExecutor struct{}

func (f fakeExecutor) ExecuteInstance(_ context.Context, _ store.Run, _ store.Instance, _ func(domain.InstanceState) error, _ func(string) error) (store.InstanceResult, []store.Artifact, error) {
	return store.InstanceResult{}, nil, nil
}

type capturingRunnerService struct {
	submitInput runnerapi.SubmitInput
}

func (c *capturingRunnerService) Start(_ context.Context) {}

func (c *capturingRunnerService) SubmitRun(_ context.Context, in runnerapi.SubmitInput) (store.Run, error) {
	c.submitInput = in
	return store.Run{RunID: "run_test_1"}, nil
}

func (c *capturingRunnerService) WaitForTerminalRun(_ context.Context, runID string, _ time.Duration) (store.Run, error) {
	return store.Run{RunID: runID, State: domain.RunStateCompleted}, nil
}

func (c *capturingRunnerService) GetRunSnapshot(_ context.Context, _ string, _ runnerapi.SnapshotOptions) (runnerapi.RunSnapshot, error) {
	return runnerapi.RunSnapshot{}, nil
}

func (c *capturingRunnerService) GetInstanceSnapshot(_ context.Context, _ string, _ runnerapi.SnapshotOptions) (runnerapi.InstanceSnapshot, error) {
	return runnerapi.InstanceSnapshot{}, nil
}

type orderingRunnerService struct {
	started bool
}

func (o *orderingRunnerService) Start(_ context.Context) {
	o.started = true
}

func (o *orderingRunnerService) SubmitRun(_ context.Context, _ runnerapi.SubmitInput) (store.Run, error) {
	if o.started {
		return store.Run{}, io.ErrUnexpectedEOF
	}
	return store.Run{RunID: "run_test_1"}, nil
}

func (o *orderingRunnerService) WaitForTerminalRun(_ context.Context, runID string, _ time.Duration) (store.Run, error) {
	return store.Run{RunID: runID, State: domain.RunStateCompleted}, nil
}

func (o *orderingRunnerService) GetRunSnapshot(_ context.Context, _ string, _ runnerapi.SnapshotOptions) (runnerapi.RunSnapshot, error) {
	return runnerapi.RunSnapshot{}, nil
}

func (o *orderingRunnerService) GetInstanceSnapshot(_ context.Context, _ string, _ runnerapi.SnapshotOptions) (runnerapi.InstanceSnapshot, error) {
	return runnerapi.InstanceSnapshot{}, nil
}

type stubAgentServerBinaryProvider struct{}

func (s *stubAgentServerBinaryProvider) ResolveAgentServerBinary(_ context.Context, _ string) (string, error) {
	return "/tmp/embedded-agent-server", nil
}

func writeSavedBundle(t *testing.T, rootDir, runID string, bundle runbundle.Bundle) {
	t.Helper()

	body, err := json.Marshal(bundle)
	if err != nil {
		t.Fatalf("marshal source bundle: %v", err)
	}
	path := runfs.BundlePath(rootDir, runID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir source bundle path: %v", err)
	}
	if err := os.WriteFile(path, append(body, '\n'), 0o644); err != nil {
		t.Fatalf("write source bundle: %v", err)
	}
}

func writeExecutableFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir executable parent: %v", err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write executable: %v", err)
	}
}

func writeTestFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir file parent: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
}

func TestRunPassesDirectPathsToCompile(t *testing.T) {
	origCompile := compileBundle
	origNewExecutor := newLocalExecutor
	origNewService := newLocalRunnerService
	origLaunchMissionControl := launchMissionControl
	defer func() {
		compileBundle = origCompile
		newLocalExecutor = origNewExecutor
		newLocalRunnerService = origNewService
		launchMissionControl = origLaunchMissionControl
	}()

	compileBundle = func(in compiler.CompileInput) (runbundle.Bundle, error) {
		if in.SuitePath != "./suites/smoke" {
			t.Fatalf("SuitePath = %q, want unchanged direct path", in.SuitePath)
		}
		if in.AgentConfigPath != "./configs/example-agent-configs/codex-unified" {
			t.Fatalf("AgentConfigPath = %q, want unchanged direct path", in.AgentConfigPath)
		}
		if in.EvalPath != "./configs/example-eval-configs/default.toml" {
			t.Fatalf("EvalPath = %q, want unchanged direct path", in.EvalPath)
		}
		return runbundle.Bundle{
			ResolvedSnapshot: runbundle.ResolvedSnapshot{
				Name:      "smoke",
				Execution: runbundle.Execution{Mode: runbundle.ExecutionModeFull, MaxConcurrency: 1},
			},
		}, nil
	}
	newLocalExecutor = func(_ localexecutor.Config) (engine.Executor, error) {
		return fakeExecutor{}, nil
	}
	newLocalRunnerService = func(_ localrunner.Config) (runnerapi.Service, error) {
		return fakeRunnerService{}, nil
	}
	launchMissionControl = func(_ context.Context, cfg missioncontrol.Config) (missioncontrol.Outcome, error) {
		return missioncontrol.Outcome{
			FinalRun: store.Run{RunID: cfg.RunID, State: domain.RunStateCompleted},
		}, nil
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	a := New(&stdout, &stderr)
	err := a.runRun(context.Background(), []string{
		"--suite", "./suites/smoke",
		"--agent-config", "./configs/example-agent-configs/codex-unified",
		"--eval", "./configs/example-eval-configs/default.toml",
		"--agent-server-binary", "agent-server",
	})
	if err != nil {
		t.Fatalf("runRun returned error: %v", err)
	}
}

func TestRunResolvesRemoteSuiteBeforeCompile(t *testing.T) {
	origCompile := compileBundle
	origResolveRemoteSuite := resolveRemoteSuite
	origNewExecutor := newLocalExecutor
	origNewService := newLocalRunnerService
	origLaunchMissionControl := launchMissionControl
	defer func() {
		compileBundle = origCompile
		resolveRemoteSuite = origResolveRemoteSuite
		newLocalExecutor = origNewExecutor
		newLocalRunnerService = origNewService
		launchMissionControl = origLaunchMissionControl
	}()

	resolveRemoteSuite = func(_ context.Context, in remotesuite.ResolveInput) (remotesuite.Result, error) {
		if in.Suite != "git::https://github.com/example/suites.git//suites/remote" {
			t.Fatalf("Suite = %q", in.Suite)
		}
		if in.Refresh {
			t.Fatalf("expected run path to avoid forced refresh")
		}
		return remotesuite.Result{
			LocalPath: "/tmp/remote-suite-cache/suite",
			SuiteGit: &runbundle.SuiteGitRef{
				RepoURL:        "https://github.com/example/suites",
				ResolvedCommit: "0123456789abcdef0123456789abcdef01234567",
				Subdir:         "suites/remote",
			},
		}, nil
	}

	var gotCompileInput compiler.CompileInput
	compileBundle = func(in compiler.CompileInput) (runbundle.Bundle, error) {
		gotCompileInput = in
		return validRemoteSuiteRunBundle(t), nil
	}
	newLocalExecutor = func(_ localexecutor.Config) (engine.Executor, error) {
		return fakeExecutor{}, nil
	}
	svc := &capturingRunnerService{}
	newLocalRunnerService = func(_ localrunner.Config) (runnerapi.Service, error) {
		return svc, nil
	}
	launchMissionControl = func(_ context.Context, cfg missioncontrol.Config) (missioncontrol.Outcome, error) {
		return missioncontrol.Outcome{
			FinalRun: store.Run{RunID: cfg.RunID, State: domain.RunStateCompleted},
		}, nil
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	a := New(&stdout, &stderr)
	err := a.runRun(context.Background(), []string{
		"--suite", "git::https://github.com/example/suites.git//suites/remote",
		"--agent-config", "agent-config",
		"--eval", "eval",
		"--agent-server-binary", "agent-server",
	})
	if err != nil {
		t.Fatalf("runRun returned error: %v", err)
	}
	if gotCompileInput.SuitePath != "/tmp/remote-suite-cache/suite" {
		t.Fatalf("SuitePath = %q", gotCompileInput.SuitePath)
	}
	if svc.submitInput.Bundle.Source.SuiteGit == nil {
		t.Fatal("expected remote suite metadata in submitted bundle")
	}
	if svc.submitInput.Bundle.Source.SuiteGit.ResolvedCommit != "0123456789abcdef0123456789abcdef01234567" {
		t.Fatalf("ResolvedCommit = %q", svc.submitInput.Bundle.Source.SuiteGit.ResolvedCommit)
	}
}

func TestRunUsesBundleMaxConcurrencyForWorkerCount(t *testing.T) {
	origCompile := compileBundle
	origNewExecutor := newLocalExecutor
	origNewService := newLocalRunnerService
	origLaunchMissionControl := launchMissionControl
	defer func() {
		compileBundle = origCompile
		newLocalExecutor = origNewExecutor
		newLocalRunnerService = origNewService
		launchMissionControl = origLaunchMissionControl
	}()

	compileBundle = func(in compiler.CompileInput) (runbundle.Bundle, error) {
		if in.AgentConfigPath != "agent-config" {
			t.Fatalf("AgentConfigPath = %q, want %q", in.AgentConfigPath, "agent-config")
		}
		return runbundle.Bundle{
			ResolvedSnapshot: runbundle.ResolvedSnapshot{
				Name:      "smoke",
				Execution: runbundle.Execution{Mode: runbundle.ExecutionModeFull, MaxConcurrency: 7},
			},
		}, nil
	}

	newLocalExecutor = func(_ localexecutor.Config) (engine.Executor, error) {
		return fakeExecutor{}, nil
	}

	var gotCfg localrunner.Config
	newLocalRunnerService = func(cfg localrunner.Config) (runnerapi.Service, error) {
		gotCfg = cfg
		return fakeRunnerService{}, nil
	}
	launchMissionControl = func(_ context.Context, cfg missioncontrol.Config) (missioncontrol.Outcome, error) {
		return missioncontrol.Outcome{
			FinalRun: store.Run{
				RunID: cfg.RunID,
				State: domain.RunStateCompleted,
			},
		}, nil
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	a := New(&stdout, &stderr)
	err := a.runRun(context.Background(), []string{
		"--suite", "suite",
		"--agent-config", "agent-config",
		"--eval", "eval",
		"--agent-server-binary", "agent-server",
	})
	if err != nil {
		t.Fatalf("runRun returned error: %v", err)
	}
	if gotCfg.EngineConfig.WorkerCount != 7 {
		t.Fatalf("expected worker_count=7, got %d", gotCfg.EngineConfig.WorkerCount)
	}
}

func TestRunPassesPruneBuiltImageFlagToLocalExecutorAndRunner(t *testing.T) {
	origCompile := compileBundle
	origNewExecutor := newLocalExecutor
	origNewService := newLocalRunnerService
	origLaunchMissionControl := launchMissionControl
	defer func() {
		compileBundle = origCompile
		newLocalExecutor = origNewExecutor
		newLocalRunnerService = origNewService
		launchMissionControl = origLaunchMissionControl
	}()

	compileBundle = func(_ compiler.CompileInput) (runbundle.Bundle, error) {
		return runbundle.Bundle{
			ResolvedSnapshot: runbundle.ResolvedSnapshot{
				Name:      "smoke",
				Execution: runbundle.Execution{Mode: runbundle.ExecutionModeFull, MaxConcurrency: 1},
			},
		}, nil
	}

	var gotCfg localexecutor.Config
	var gotRunnerCfg localrunner.Config
	newLocalExecutor = func(cfg localexecutor.Config) (engine.Executor, error) {
		gotCfg = cfg
		return fakeExecutor{}, nil
	}
	newLocalRunnerService = func(cfg localrunner.Config) (runnerapi.Service, error) {
		gotRunnerCfg = cfg
		return fakeRunnerService{}, nil
	}
	launchMissionControl = func(_ context.Context, cfg missioncontrol.Config) (missioncontrol.Outcome, error) {
		return missioncontrol.Outcome{
			FinalRun: store.Run{
				RunID: cfg.RunID,
				State: domain.RunStateCompleted,
			},
		}, nil
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	a := New(&stdout, &stderr)
	err := a.runRun(context.Background(), []string{
		"--suite", "suite",
		"--agent-config", "agent-config",
		"--eval", "eval",
		"--agent-server-binary", "agent-server",
		"--prune-built-image", "32",
	})
	if err != nil {
		t.Fatalf("runRun returned error: %v", err)
	}
	if !gotCfg.CleanupBuiltImages {
		t.Fatalf("expected CleanupBuiltImages to be true")
	}
	if gotRunnerCfg.GlobalImagePruneEvery != 32 {
		t.Fatalf("GlobalImagePruneEvery = %d, want 32", gotRunnerCfg.GlobalImagePruneEvery)
	}
	if !strings.Contains(stderr.String(), "docker image prune -a -f") {
		t.Fatalf("expected prune warning in stderr, got %q", stderr.String())
	}
}

func TestRunRejectsNegativePruneBuiltImageInterval(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	a := New(&stdout, &stderr)

	err := a.runRun(context.Background(), []string{
		"--suite", "suite",
		"--agent-config", "agent-config",
		"--eval", "eval",
		"--prune-built-image", "-1",
	})
	if err == nil || !strings.Contains(err.Error(), "--prune-built-image must be >= 0") {
		t.Fatalf("expected prune interval validation error, got %v", err)
	}
}

func TestRunUsesInternalDefaultProjectAndUserMetadata(t *testing.T) {
	origCompile := compileBundle
	origNewExecutor := newLocalExecutor
	origNewService := newLocalRunnerService
	origLaunchMissionControl := launchMissionControl
	origNewAgentServerBinaryProvider := newAgentServerBinaryProvider
	defer func() {
		compileBundle = origCompile
		newLocalExecutor = origNewExecutor
		newLocalRunnerService = origNewService
		launchMissionControl = origLaunchMissionControl
		newAgentServerBinaryProvider = origNewAgentServerBinaryProvider
	}()

	var gotCompileInput compiler.CompileInput
	compileBundle = func(in compiler.CompileInput) (runbundle.Bundle, error) {
		gotCompileInput = in
		return runbundle.Bundle{
			ResolvedSnapshot: runbundle.ResolvedSnapshot{
				Name:      "smoke",
				Execution: runbundle.Execution{Mode: runbundle.ExecutionModeFull, MaxConcurrency: 1},
			},
		}, nil
	}
	newLocalExecutor = func(_ localexecutor.Config) (engine.Executor, error) {
		return fakeExecutor{}, nil
	}
	newAgentServerBinaryProvider = func() (localexecutor.AgentServerBinaryProvider, error) {
		return &stubAgentServerBinaryProvider{}, nil
	}
	svc := &capturingRunnerService{}
	newLocalRunnerService = func(_ localrunner.Config) (runnerapi.Service, error) {
		return svc, nil
	}
	launchMissionControl = func(_ context.Context, cfg missioncontrol.Config) (missioncontrol.Outcome, error) {
		return missioncontrol.Outcome{
			FinalRun: store.Run{RunID: cfg.RunID, State: domain.RunStateCompleted},
		}, nil
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	a := New(&stdout, &stderr)
	err := a.runRun(context.Background(), []string{
		"--suite", "suite",
		"--agent-config", "agent-config",
		"--eval", "eval",
	})
	if err != nil {
		t.Fatalf("runRun returned error: %v", err)
	}
	if gotCompileInput.SubmitProjectID != defaultLocalProjectID {
		t.Fatalf("CompileInput.SubmitProjectID = %q, want %q", gotCompileInput.SubmitProjectID, defaultLocalProjectID)
	}
	if svc.submitInput.ProjectID != defaultLocalProjectID {
		t.Fatalf("SubmitInput.ProjectID = %q, want %q", svc.submitInput.ProjectID, defaultLocalProjectID)
	}
	if svc.submitInput.CreatedByUser != defaultLocalUserID {
		t.Fatalf("SubmitInput.CreatedByUser = %q, want %q", svc.submitInput.CreatedByUser, defaultLocalUserID)
	}
}

func TestRunUsesEmbeddedAgentServerProviderByDefault(t *testing.T) {
	origCompile := compileBundle
	origNewExecutor := newLocalExecutor
	origNewService := newLocalRunnerService
	origLaunchMissionControl := launchMissionControl
	origNewAgentServerBinaryProvider := newAgentServerBinaryProvider
	defer func() {
		compileBundle = origCompile
		newLocalExecutor = origNewExecutor
		newLocalRunnerService = origNewService
		launchMissionControl = origLaunchMissionControl
		newAgentServerBinaryProvider = origNewAgentServerBinaryProvider
	}()

	compileBundle = func(_ compiler.CompileInput) (runbundle.Bundle, error) {
		return runbundle.Bundle{
			ResolvedSnapshot: runbundle.ResolvedSnapshot{
				Name:      "smoke",
				Execution: runbundle.Execution{Mode: runbundle.ExecutionModeFull, MaxConcurrency: 1},
			},
		}, nil
	}

	var gotCfg localexecutor.Config
	newLocalExecutor = func(cfg localexecutor.Config) (engine.Executor, error) {
		gotCfg = cfg
		return fakeExecutor{}, nil
	}
	provider := &stubAgentServerBinaryProvider{}
	newAgentServerBinaryProvider = func() (localexecutor.AgentServerBinaryProvider, error) {
		return provider, nil
	}
	newLocalRunnerService = func(_ localrunner.Config) (runnerapi.Service, error) {
		return fakeRunnerService{}, nil
	}
	launchMissionControl = func(_ context.Context, cfg missioncontrol.Config) (missioncontrol.Outcome, error) {
		return missioncontrol.Outcome{
			FinalRun: store.Run{RunID: cfg.RunID, State: domain.RunStateCompleted},
		}, nil
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	a := New(&stdout, &stderr)
	err := a.runRun(context.Background(), []string{
		"--suite", "suite",
		"--agent-config", "agent-config",
		"--eval", "eval",
	})
	if err != nil {
		t.Fatalf("runRun returned error: %v", err)
	}
	if gotCfg.AgentServerBinary != "" {
		t.Fatalf("AgentServerBinary = %q, want empty exact override", gotCfg.AgentServerBinary)
	}
	if gotCfg.AgentServerBinaryProvider != provider {
		t.Fatalf("AgentServerBinaryProvider = %#v", gotCfg.AgentServerBinaryProvider)
	}
}

func TestResolveAgentServerBinaryUsesEmbeddedProviderByDefault(t *testing.T) {
	origNewAgentServerBinaryProvider := newAgentServerBinaryProvider
	defer func() {
		newAgentServerBinaryProvider = origNewAgentServerBinaryProvider
	}()

	provider := &stubAgentServerBinaryProvider{}
	newAgentServerBinaryProvider = func() (localexecutor.AgentServerBinaryProvider, error) {
		return provider, nil
	}

	exact, resolvedProvider, err := resolveAgentServerBinary("")
	if err != nil {
		t.Fatalf("resolveAgentServerBinary() error = %v", err)
	}
	if exact != "" {
		t.Fatalf("exact = %q", exact)
	}
	if resolvedProvider != provider {
		t.Fatalf("provider = %#v", resolvedProvider)
	}
}

func TestRunPassesResumeModeToRunner(t *testing.T) {
	origCompile := compileBundle
	origNewExecutor := newLocalExecutor
	origNewService := newLocalRunnerService
	origLaunchMissionControl := launchMissionControl
	defer func() {
		compileBundle = origCompile
		newLocalExecutor = origNewExecutor
		newLocalRunnerService = origNewService
		launchMissionControl = origLaunchMissionControl
	}()

	compileBundle = func(_ compiler.CompileInput) (runbundle.Bundle, error) {
		t.Fatalf("did not expect compile during resume")
		return runbundle.Bundle{}, nil
	}
	resumeRoot := t.TempDir()
	writeSavedBundle(t, resumeRoot, "run_123", runbundle.Bundle{
		SchemaVersion: runbundle.SchemaVersionV1,
		BundleID:      "bun_source",
		CreatedAt:     time.Date(2026, 3, 1, 2, 3, 4, 0, time.UTC),
		Source: runbundle.Source{
			Kind:            runbundle.SourceKindRunSnapshot,
			SubmitProjectID: "proj_source",
			OriginRunID:     "run_origin",
		},
		ResolvedSnapshot: runbundle.ResolvedSnapshot{
			Name:      "smoke",
			Execution: runbundle.Execution{Mode: runbundle.ExecutionModeFull, MaxConcurrency: 1},
		},
	})
	newLocalExecutor = func(_ localexecutor.Config) (engine.Executor, error) {
		return fakeExecutor{}, nil
	}
	svc := &capturingRunnerService{}
	newLocalRunnerService = func(_ localrunner.Config) (runnerapi.Service, error) {
		return svc, nil
	}
	launchMissionControl = func(_ context.Context, cfg missioncontrol.Config) (missioncontrol.Outcome, error) {
		return missioncontrol.Outcome{
			FinalRun: store.Run{RunID: cfg.RunID, State: domain.RunStateCompleted},
		}, nil
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	a := New(&stdout, &stderr)
	err := a.runRun(context.Background(), []string{
		"--agent-server-binary", "agent-server",
		"--root", resumeRoot,
		"--resume-from", "run_123",
		"--resume-mode", "retry-failed",
	})
	if err != nil {
		t.Fatalf("runRun returned error: %v", err)
	}
	if svc.submitInput.ResumeMode != runnerapi.ResumeModeRetryFailed {
		t.Fatalf("ResumeMode = %q", svc.submitInput.ResumeMode)
	}
}

func TestRunSubmitsResumeBeforeStartingRunnerService(t *testing.T) {
	origCompile := compileBundle
	origNewExecutor := newLocalExecutor
	origNewService := newLocalRunnerService
	origLaunchMissionControl := launchMissionControl
	defer func() {
		compileBundle = origCompile
		newLocalExecutor = origNewExecutor
		newLocalRunnerService = origNewService
		launchMissionControl = origLaunchMissionControl
	}()

	compileBundle = func(_ compiler.CompileInput) (runbundle.Bundle, error) {
		t.Fatalf("did not expect compile during resume")
		return runbundle.Bundle{}, nil
	}
	resumeRoot := t.TempDir()
	writeSavedBundle(t, resumeRoot, "run_123", runbundle.Bundle{
		SchemaVersion: runbundle.SchemaVersionV1,
		BundleID:      "bun_source",
		CreatedAt:     time.Date(2026, 3, 1, 2, 3, 4, 0, time.UTC),
		Source: runbundle.Source{
			Kind:            runbundle.SourceKindRunSnapshot,
			SubmitProjectID: "proj_source",
			OriginRunID:     "run_origin",
		},
		ResolvedSnapshot: runbundle.ResolvedSnapshot{
			Name:      "smoke",
			Execution: runbundle.Execution{Mode: runbundle.ExecutionModeFull, MaxConcurrency: 1},
		},
	})
	newLocalExecutor = func(_ localexecutor.Config) (engine.Executor, error) {
		return fakeExecutor{}, nil
	}
	svc := &orderingRunnerService{}
	newLocalRunnerService = func(_ localrunner.Config) (runnerapi.Service, error) {
		return svc, nil
	}
	launchMissionControl = func(_ context.Context, cfg missioncontrol.Config) (missioncontrol.Outcome, error) {
		return missioncontrol.Outcome{
			FinalRun: store.Run{RunID: cfg.RunID, State: domain.RunStateCompleted},
		}, nil
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	a := New(&stdout, &stderr)
	err := a.runRun(context.Background(), []string{
		"--agent-server-binary", "agent-server",
		"--root", resumeRoot,
		"--resume-from", "run_123",
	})
	if err != nil {
		t.Fatalf("runRun returned error: %v", err)
	}
	if !svc.started {
		t.Fatalf("expected runner service to be started after submit")
	}
}

func TestRunSetsExecutionModeDryRunWhenFlagPresent(t *testing.T) {
	origCompile := compileBundle
	origNewExecutor := newLocalExecutor
	origNewService := newLocalRunnerService
	origLaunchMissionControl := launchMissionControl
	defer func() {
		compileBundle = origCompile
		newLocalExecutor = origNewExecutor
		newLocalRunnerService = origNewService
		launchMissionControl = origLaunchMissionControl
	}()

	compileBundle = func(_ compiler.CompileInput) (runbundle.Bundle, error) {
		return runbundle.Bundle{
			ResolvedSnapshot: runbundle.ResolvedSnapshot{
				Name:      "smoke",
				Execution: runbundle.Execution{Mode: runbundle.ExecutionModeFull, MaxConcurrency: 1},
			},
		}, nil
	}
	newLocalExecutor = func(_ localexecutor.Config) (engine.Executor, error) {
		return fakeExecutor{}, nil
	}
	svc := &capturingRunnerService{}
	newLocalRunnerService = func(_ localrunner.Config) (runnerapi.Service, error) {
		return svc, nil
	}
	launchMissionControl = func(_ context.Context, cfg missioncontrol.Config) (missioncontrol.Outcome, error) {
		return missioncontrol.Outcome{
			FinalRun: store.Run{RunID: cfg.RunID, State: domain.RunStateCompleted},
		}, nil
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	a := New(&stdout, &stderr)
	err := a.runRun(context.Background(), []string{
		"--suite", "suite",
		"--agent-config", "agent-config",
		"--eval", "eval",
		"--agent-server-binary", "agent-server",
		"--dry-run",
	})
	if err != nil {
		t.Fatalf("runRun returned error: %v", err)
	}
	if svc.submitInput.Bundle.ResolvedSnapshot.Execution.Mode != runbundle.ExecutionModeDryRun {
		t.Fatalf("Execution.Mode = %q, want %q", svc.submitInput.Bundle.ResolvedSnapshot.Execution.Mode, runbundle.ExecutionModeDryRun)
	}
}

func TestRunSetsExecutionModeFullByDefault(t *testing.T) {
	origCompile := compileBundle
	origNewExecutor := newLocalExecutor
	origNewService := newLocalRunnerService
	origLaunchMissionControl := launchMissionControl
	defer func() {
		compileBundle = origCompile
		newLocalExecutor = origNewExecutor
		newLocalRunnerService = origNewService
		launchMissionControl = origLaunchMissionControl
	}()

	compileBundle = func(_ compiler.CompileInput) (runbundle.Bundle, error) {
		return runbundle.Bundle{
			ResolvedSnapshot: runbundle.ResolvedSnapshot{
				Name:      "smoke",
				Execution: runbundle.Execution{Mode: runbundle.ExecutionModeDryRun, MaxConcurrency: 1},
			},
		}, nil
	}
	newLocalExecutor = func(_ localexecutor.Config) (engine.Executor, error) {
		return fakeExecutor{}, nil
	}
	svc := &capturingRunnerService{}
	newLocalRunnerService = func(_ localrunner.Config) (runnerapi.Service, error) {
		return svc, nil
	}
	launchMissionControl = func(_ context.Context, cfg missioncontrol.Config) (missioncontrol.Outcome, error) {
		return missioncontrol.Outcome{
			FinalRun: store.Run{RunID: cfg.RunID, State: domain.RunStateCompleted},
		}, nil
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	a := New(&stdout, &stderr)
	err := a.runRun(context.Background(), []string{
		"--suite", "suite",
		"--agent-config", "agent-config",
		"--eval", "eval",
		"--agent-server-binary", "agent-server",
	})
	if err != nil {
		t.Fatalf("runRun returned error: %v", err)
	}
	if svc.submitInput.Bundle.ResolvedSnapshot.Execution.Mode != runbundle.ExecutionModeFull {
		t.Fatalf("Execution.Mode = %q, want %q", svc.submitInput.Bundle.ResolvedSnapshot.Execution.Mode, runbundle.ExecutionModeFull)
	}
}

func TestRunRejectsResumeModeWithoutResumeFrom(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	a := New(&stdout, &stderr)
	err := a.runRun(context.Background(), []string{
		"--suite", "suite",
		"--agent-config", "agent-config",
		"--eval", "eval",
		"--agent-server-binary", "agent-server",
		"--resume-mode", "retry-failed",
	})
	if err == nil || !strings.Contains(err.Error(), "--resume-mode requires --resume-from") {
		t.Fatalf("expected resume-mode validation error, got %v", err)
	}
}

func TestRunUsesSavedBundleMetadataForLocalResume(t *testing.T) {
	origCompile := compileBundle
	origNewExecutor := newLocalExecutor
	origNewService := newLocalRunnerService
	origLaunchMissionControl := launchMissionControl
	defer func() {
		compileBundle = origCompile
		newLocalExecutor = origNewExecutor
		newLocalRunnerService = origNewService
		launchMissionControl = origLaunchMissionControl
	}()

	resumeRoot := t.TempDir()
	sourceCreatedAt := time.Date(2026, 3, 1, 2, 3, 4, 0, time.UTC)
	sourceBundle := runbundle.Bundle{
		SchemaVersion: runbundle.SchemaVersionV1,
		BundleID:      "bun_source",
		CreatedAt:     sourceCreatedAt,
		Source: runbundle.Source{
			Kind:            runbundle.SourceKindRunSnapshot,
			SubmitProjectID: "proj_source",
			OriginRunID:     "run_origin",
		},
		ResolvedSnapshot: runbundle.ResolvedSnapshot{
			Name:      "smoke",
			Execution: runbundle.Execution{Mode: runbundle.ExecutionModeFull, MaxConcurrency: 1},
		},
	}
	writeSavedBundle(t, resumeRoot, "run_source", sourceBundle)

	compileBundle = func(_ compiler.CompileInput) (runbundle.Bundle, error) {
		t.Fatalf("did not expect compile during resume")
		return runbundle.Bundle{}, nil
	}
	newLocalExecutor = func(_ localexecutor.Config) (engine.Executor, error) {
		return fakeExecutor{}, nil
	}
	svc := &capturingRunnerService{}
	newLocalRunnerService = func(_ localrunner.Config) (runnerapi.Service, error) {
		return svc, nil
	}
	launchMissionControl = func(_ context.Context, cfg missioncontrol.Config) (missioncontrol.Outcome, error) {
		return missioncontrol.Outcome{
			FinalRun: store.Run{RunID: cfg.RunID, State: domain.RunStateCompleted},
		}, nil
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	a := New(&stdout, &stderr)
	err := a.runRun(context.Background(), []string{
		"--agent-server-binary", "agent-server",
		"--root", resumeRoot,
		"--resume-from", "run_source",
	})
	if err != nil {
		t.Fatalf("runRun returned error: %v", err)
	}
	if svc.submitInput.Bundle.BundleID != "bun_source" {
		t.Fatalf("BundleID = %q", svc.submitInput.Bundle.BundleID)
	}
	if !svc.submitInput.Bundle.CreatedAt.Equal(sourceCreatedAt) {
		t.Fatalf("CreatedAt = %s", svc.submitInput.Bundle.CreatedAt)
	}
	if svc.submitInput.Bundle.Source.Kind != runbundle.SourceKindRunSnapshot {
		t.Fatalf("Source.Kind = %q", svc.submitInput.Bundle.Source.Kind)
	}
	if svc.submitInput.Bundle.Source.OriginRunID != "run_origin" {
		t.Fatalf("OriginRunID = %q", svc.submitInput.Bundle.Source.OriginRunID)
	}
}

func TestRunRejectsSourceFlagsWhenResumeFromIsSet(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	a := New(&stdout, &stderr)

	err := a.runRun(context.Background(), []string{
		"--suite", "suite",
		"--agent-config", "agent-config",
		"--eval", "eval",
		"--resume-from", "run_123",
	})
	if err == nil || !strings.Contains(err.Error(), "--resume-from infers the saved bundle") {
		t.Fatalf("expected resume/source flag validation error, got %v", err)
	}
}

func TestRunRejectsMalformedGitSuiteSpecifier(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	a := New(&stdout, &stderr)

	err := a.runRun(context.Background(), []string{
		"--suite", "git::https://github.com/example/suites.git",
		"--agent-config", "agent-config",
		"--eval", "eval",
	})
	if err == nil || !strings.Contains(err.Error(), "git remote suites must use") {
		t.Fatalf("expected malformed git suite validation error, got %v", err)
	}
}

func TestRunResumeWithoutSavedBundleReturnsLoadError(t *testing.T) {
	origCompile := compileBundle
	defer func() {
		compileBundle = origCompile
	}()

	compileBundle = func(_ compiler.CompileInput) (runbundle.Bundle, error) {
		t.Fatalf("did not expect compile during resume")
		return runbundle.Bundle{}, nil
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	a := New(&stdout, &stderr)

	err := a.runRun(context.Background(), []string{
		"--agent-server-binary", "agent-server",
		"--root", t.TempDir(),
		"--resume-from", "run_missing",
	})
	if err == nil || !strings.Contains(err.Error(), "read source bundle for resume") {
		t.Fatalf("expected saved bundle read error, got %v", err)
	}
}

func TestRunPassesAuthFileOverridePathToLocalExecutor(t *testing.T) {
	origCompile := compileBundle
	origNewExecutor := newLocalExecutor
	origNewService := newLocalRunnerService
	origLaunchMissionControl := launchMissionControl
	defer func() {
		compileBundle = origCompile
		newLocalExecutor = origNewExecutor
		newLocalRunnerService = origNewService
		launchMissionControl = origLaunchMissionControl
	}()

	compileBundle = func(_ compiler.CompileInput) (runbundle.Bundle, error) {
		return runbundle.Bundle{
			ResolvedSnapshot: runbundle.ResolvedSnapshot{
				Name:      "smoke",
				Execution: runbundle.Execution{Mode: runbundle.ExecutionModeFull, MaxConcurrency: 1},
				Agent: runbundle.Agent{
					Definition: agentdef.DefinitionSnapshot{
						Manifest: agentdef.Manifest{
							Auth: agentdef.AuthSpec{
								LocalCredentials: []agentdef.AuthLocalCredential{{
									RequiredEnv:    "OPENAI_API_KEY",
									RunHomeRelPath: ".codex/auth.json",
									Sources: []agentdef.AuthLocalSource{{
										Kind:        agentdef.AuthLocalSourceKindHomeFile,
										HomeRelPath: ".codex/auth.json",
									}},
								}},
							},
						},
					},
				},
			},
		}, nil
	}

	var gotCfg localexecutor.Config
	newLocalExecutor = func(cfg localexecutor.Config) (engine.Executor, error) {
		gotCfg = cfg
		return fakeExecutor{}, nil
	}
	newLocalRunnerService = func(_ localrunner.Config) (runnerapi.Service, error) {
		return fakeRunnerService{}, nil
	}
	launchMissionControl = func(_ context.Context, cfg missioncontrol.Config) (missioncontrol.Outcome, error) {
		return missioncontrol.Outcome{
			FinalRun: store.Run{RunID: cfg.RunID, State: domain.RunStateCompleted},
		}, nil
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	a := New(&stdout, &stderr)
	err := a.runRun(context.Background(), []string{
		"--suite", "suite",
		"--agent-config", "agent-config",
		"--eval", "eval",
		"--agent-server-binary", "agent-server",
		"--auth-file-path", "auth.json",
	})
	if err != nil {
		t.Fatalf("runRun returned error: %v", err)
	}
	if gotCfg.AuthFileOverridePath != "auth.json" {
		t.Fatalf("AuthFileOverridePath = %q", gotCfg.AuthFileOverridePath)
	}
}

func TestRunRejectsAuthFileOverrideWithoutSingleManifestLocalFile(t *testing.T) {
	origCompile := compileBundle
	defer func() {
		compileBundle = origCompile
	}()

	compileBundle = func(_ compiler.CompileInput) (runbundle.Bundle, error) {
		return runbundle.Bundle{
			ResolvedSnapshot: runbundle.ResolvedSnapshot{
				Execution: runbundle.Execution{Mode: runbundle.ExecutionModeFull, MaxConcurrency: 1},
				Agent: runbundle.Agent{
					Definition: agentdef.DefinitionSnapshot{
						Manifest: agentdef.Manifest{
							Auth: agentdef.AuthSpec{
								LocalCredentials: []agentdef.AuthLocalCredential{
									{
										RequiredEnv:    "OPENAI_API_KEY",
										RunHomeRelPath: ".codex/auth.json",
										Sources: []agentdef.AuthLocalSource{{
											Kind:        agentdef.AuthLocalSourceKindHomeFile,
											HomeRelPath: ".codex/auth.json",
										}},
									},
									{
										RequiredEnv:    "ANTHROPIC_API_KEY",
										RunHomeRelPath: ".claude/.credentials.json",
										Sources: []agentdef.AuthLocalSource{{
											Kind:        agentdef.AuthLocalSourceKindHomeFile,
											HomeRelPath: ".claude/.credentials.json",
										}},
									},
								},
							},
						},
					},
				},
			},
		}, nil
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	a := New(&stdout, &stderr)
	err := a.runRun(context.Background(), []string{
		"--suite", "suite",
		"--agent-config", "agent-config",
		"--eval", "eval",
		"--agent-server-binary", "agent-server",
		"--auth-file-path", "auth.json",
	})
	if err == nil || !strings.Contains(err.Error(), "exactly one auth.local_credentials entry") {
		t.Fatalf("expected auth-file-path validation error, got %v", err)
	}
}

func TestRunShowsPreRunConfirmationWithResolvedAuthAndPruneWarning(t *testing.T) {
	origCompile := compileBundle
	origNewExecutor := newLocalExecutor
	origNewService := newLocalRunnerService
	origLaunchMissionControl := launchMissionControl
	origShouldConfirmRun := shouldConfirmRun
	origLaunchRunConfirmation := launchRunConfirmation
	defer func() {
		compileBundle = origCompile
		newLocalExecutor = origNewExecutor
		newLocalRunnerService = origNewService
		launchMissionControl = origLaunchMissionControl
		shouldConfirmRun = origShouldConfirmRun
		launchRunConfirmation = origLaunchRunConfirmation
	}()

	t.Setenv("OPENAI_API_KEY", "sk-openai")

	compileBundle = func(_ compiler.CompileInput) (runbundle.Bundle, error) {
		return runbundle.Bundle{
			ResolvedSnapshot: runbundle.ResolvedSnapshot{
				Name:      "smoke",
				Execution: runbundle.Execution{Mode: runbundle.ExecutionModeFull, MaxConcurrency: 1},
				Agent: runbundle.Agent{
					Definition: agentdef.DefinitionSnapshot{
						Manifest: agentdef.Manifest{
							Name: "codex",
							Auth: agentdef.AuthSpec{
								RequiredEnv: []string{"OPENAI_API_KEY"},
								LocalCredentials: []agentdef.AuthLocalCredential{{
									RequiredEnv:    "OPENAI_API_KEY",
									RunHomeRelPath: ".codex/auth.json",
									Sources: []agentdef.AuthLocalSource{{
										Kind:        agentdef.AuthLocalSourceKindHomeFile,
										HomeRelPath: ".codex/auth.json",
									}},
								}},
							},
						},
					},
				},
			},
		}, nil
	}
	newLocalExecutor = func(_ localexecutor.Config) (engine.Executor, error) {
		return fakeExecutor{}, nil
	}
	newLocalRunnerService = func(_ localrunner.Config) (runnerapi.Service, error) {
		return fakeRunnerService{}, nil
	}
	launchMissionControl = func(_ context.Context, cfg missioncontrol.Config) (missioncontrol.Outcome, error) {
		return missioncontrol.Outcome{
			FinalRun: store.Run{RunID: cfg.RunID, State: domain.RunStateCompleted},
		}, nil
	}
	shouldConfirmRun = func(io.Writer) bool { return true }
	var gotSpec runConfirmationSpec
	launchRunConfirmation = func(_ io.Writer, spec runConfirmationSpec) (bool, error) {
		gotSpec = spec
		return true, nil
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	a := New(&stdout, &stderr)
	err := a.runRun(context.Background(), []string{
		"--suite", "suite",
		"--agent-config", "agent-config",
		"--eval", "eval",
		"--agent-server-binary", "agent-server",
		"--prune-built-image", "7",
	})
	if err != nil {
		t.Fatalf("runRun returned error: %v", err)
	}
	if gotSpec.AgentName != "codex" {
		t.Fatalf("agent name = %q", gotSpec.AgentName)
	}
	if gotSpec.PruneBuiltImage != 7 {
		t.Fatalf("prune interval = %d", gotSpec.PruneBuiltImage)
	}
	if len(gotSpec.Auth) != 1 {
		t.Fatalf("auth spec = %#v", gotSpec.Auth)
	}
	if gotSpec.Auth[0].Method != "API key" {
		t.Fatalf("method = %q", gotSpec.Auth[0].Method)
	}
	if gotSpec.Auth[0].Source != "OPENAI_API_KEY environment variable" {
		t.Fatalf("source = %q", gotSpec.Auth[0].Source)
	}
	if gotSpec.Auth[0].Requirement != "OPENAI_API_KEY" {
		t.Fatalf("requirement = %q", gotSpec.Auth[0].Requirement)
	}
}

func TestRunSkipsPreRunConfirmationInNonInteractiveMode(t *testing.T) {
	origCompile := compileBundle
	origNewExecutor := newLocalExecutor
	origNewService := newLocalRunnerService
	origLaunchPlainControl := launchPlainControl
	origShouldConfirmRun := shouldConfirmRun
	origLaunchRunConfirmation := launchRunConfirmation
	defer func() {
		compileBundle = origCompile
		newLocalExecutor = origNewExecutor
		newLocalRunnerService = origNewService
		launchPlainControl = origLaunchPlainControl
		shouldConfirmRun = origShouldConfirmRun
		launchRunConfirmation = origLaunchRunConfirmation
	}()

	t.Setenv("OPENAI_API_KEY", "sk-openai")

	compileBundle = func(_ compiler.CompileInput) (runbundle.Bundle, error) {
		return runbundle.Bundle{
			ResolvedSnapshot: runbundle.ResolvedSnapshot{
				Name:      "smoke",
				Execution: runbundle.Execution{Mode: runbundle.ExecutionModeFull, MaxConcurrency: 1},
				Agent: runbundle.Agent{
					Definition: agentdef.DefinitionSnapshot{
						Manifest: agentdef.Manifest{
							Name: "codex",
							Auth: agentdef.AuthSpec{
								RequiredEnv: []string{"OPENAI_API_KEY"},
							},
						},
					},
				},
			},
		}, nil
	}
	newLocalExecutor = func(_ localexecutor.Config) (engine.Executor, error) {
		return fakeExecutor{}, nil
	}
	newLocalRunnerService = func(_ localrunner.Config) (runnerapi.Service, error) {
		return fakeRunnerService{}, nil
	}
	launchPlainControl = func(_ context.Context, cfg plaincontrol.Config) (missioncontrol.Outcome, error) {
		return missioncontrol.Outcome{
			FinalRun: store.Run{RunID: cfg.RunID, State: domain.RunStateCompleted},
		}, nil
	}
	shouldConfirmRun = func(io.Writer) bool { return true }
	launchRunConfirmation = func(io.Writer, runConfirmationSpec) (bool, error) {
		t.Fatalf("did not expect confirmation screen in non-interactive mode")
		return false, nil
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	a := New(&stdout, &stderr)
	err := a.runRun(context.Background(), []string{
		"--suite", "suite",
		"--agent-config", "agent-config",
		"--eval", "eval",
		"--agent-server-binary", "agent-server",
		"--non-interactive",
	})
	if err != nil {
		t.Fatalf("runRun returned error: %v", err)
	}
}

func TestBuildRunConfirmationSpecUsesOAuthCredentialWording(t *testing.T) {
	spec := buildRunConfirmationSpec("codex", []localexecutor.AuthPreview{{
		RequiredEnv: "OPENAI_API_KEY",
		Mode:        localexecutor.AuthPreviewModeOAuth,
		SourceKind:  "home_file",
		SourceLabel: "/Users/josebouza/.codex/auth.json",
	}}, 0, false)

	if len(spec.Auth) != 1 {
		t.Fatalf("auth spec = %#v", spec.Auth)
	}
	if spec.Auth[0].Method != "OAuth credential file" {
		t.Fatalf("method = %q", spec.Auth[0].Method)
	}
	if spec.Auth[0].FilePath != "/Users/josebouza/.codex/auth.json" {
		t.Fatalf("file path = %q", spec.Auth[0].FilePath)
	}
	if spec.Auth[0].Source != "fallback for OPENAI_API_KEY" {
		t.Fatalf("source = %q", spec.Auth[0].Source)
	}
}

func TestBuildRunConfirmationSpecUsesKeychainOAuthWording(t *testing.T) {
	spec := buildRunConfirmationSpec("claude-code", []localexecutor.AuthPreview{{
		RequiredEnv: "ANTHROPIC_API_KEY",
		Mode:        localexecutor.AuthPreviewModeOAuth,
		SourceKind:  "macos_keychain",
		SourceLabel: "Claude Code-credentials",
	}}, 0, false)

	if len(spec.Auth) != 1 {
		t.Fatalf("auth spec = %#v", spec.Auth)
	}
	if spec.Auth[0].Method != "OAuth credential" {
		t.Fatalf("method = %q", spec.Auth[0].Method)
	}
	if spec.Auth[0].Source != `macOS Keychain item "Claude Code-credentials"` {
		t.Fatalf("source = %q", spec.Auth[0].Source)
	}
}

func TestBuildRunConfirmationSpecMarksDryRun(t *testing.T) {
	spec := buildRunConfirmationSpec("codex", []localexecutor.AuthPreview{{
		RequiredEnv: "OPENAI_API_KEY",
		Mode:        localexecutor.AuthPreviewModeAPIKey,
	}}, 0, true)

	if !spec.DryRun {
		t.Fatalf("expected dry run spec")
	}
}

func TestRunFailsFastWhenCompiledBundleHasInvalidMaxConcurrency(t *testing.T) {
	origCompile := compileBundle
	origNewExecutor := newLocalExecutor
	origNewService := newLocalRunnerService
	origLaunchMissionControl := launchMissionControl
	defer func() {
		compileBundle = origCompile
		newLocalExecutor = origNewExecutor
		newLocalRunnerService = origNewService
		launchMissionControl = origLaunchMissionControl
	}()

	compileBundle = func(_ compiler.CompileInput) (runbundle.Bundle, error) {
		return runbundle.Bundle{
			ResolvedSnapshot: runbundle.ResolvedSnapshot{
				Execution: runbundle.Execution{Mode: runbundle.ExecutionModeFull, MaxConcurrency: 0},
			},
		}, nil
	}

	executorCalled := false
	newLocalExecutor = func(_ localexecutor.Config) (engine.Executor, error) {
		executorCalled = true
		return fakeExecutor{}, nil
	}

	serviceCalled := false
	newLocalRunnerService = func(_ localrunner.Config) (runnerapi.Service, error) {
		serviceCalled = true
		return fakeRunnerService{}, nil
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	a := New(&stdout, &stderr)
	err := a.runRun(context.Background(), []string{
		"--suite", "suite",
		"--agent-config", "agent-config",
		"--eval", "eval",
		"--agent-server-binary", "agent-server",
	})
	if err == nil {
		t.Fatalf("expected error for invalid max_concurrency")
	}
	if !strings.Contains(err.Error(), "max_concurrency must be > 0") {
		t.Fatalf("expected max_concurrency validation error, got: %v", err)
	}
	if executorCalled {
		t.Fatalf("expected executor constructor not to be called")
	}
	if serviceCalled {
		t.Fatalf("expected local runner service constructor not to be called")
	}
}

func TestLocalWorkerCount(t *testing.T) {
	got, err := localWorkerCount(4)
	if err != nil {
		t.Fatalf("localWorkerCount returned error: %v", err)
	}
	if got != 4 {
		t.Fatalf("expected 4, got %d", got)
	}

	_, err = localWorkerCount(0)
	if err == nil {
		t.Fatalf("expected error for zero max_concurrency")
	}
}

func TestRunReturnsErrorWhenMissionControlAborts(t *testing.T) {
	origCompile := compileBundle
	origNewExecutor := newLocalExecutor
	origNewService := newLocalRunnerService
	origLaunchMissionControl := launchMissionControl
	defer func() {
		compileBundle = origCompile
		newLocalExecutor = origNewExecutor
		newLocalRunnerService = origNewService
		launchMissionControl = origLaunchMissionControl
	}()

	compileBundle = func(_ compiler.CompileInput) (runbundle.Bundle, error) {
		return runbundle.Bundle{
			ResolvedSnapshot: runbundle.ResolvedSnapshot{
				Execution: runbundle.Execution{Mode: runbundle.ExecutionModeFull, MaxConcurrency: 1},
			},
		}, nil
	}
	newLocalExecutor = func(_ localexecutor.Config) (engine.Executor, error) {
		return fakeExecutor{}, nil
	}
	newLocalRunnerService = func(_ localrunner.Config) (runnerapi.Service, error) {
		return fakeRunnerService{}, nil
	}
	launchMissionControl = func(_ context.Context, cfg missioncontrol.Config) (missioncontrol.Outcome, error) {
		return missioncontrol.Outcome{
			FinalRun: store.Run{RunID: cfg.RunID, State: domain.RunStateRunning},
			Aborted:  true,
		}, nil
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	a := New(&stdout, &stderr)
	err := a.runRun(context.Background(), []string{
		"--suite", "suite",
		"--agent-config", "agent-config",
		"--eval", "eval",
		"--agent-server-binary", "agent-server",
	})
	if err == nil {
		t.Fatalf("expected abort error")
	}
	if !strings.Contains(err.Error(), "aborted before terminal state") {
		t.Fatalf("expected abort error, got: %v", err)
	}
}

func TestRunUsesNonInteractiveMonitorWhenFlagPresent(t *testing.T) {
	origCompile := compileBundle
	origNewExecutor := newLocalExecutor
	origNewService := newLocalRunnerService
	origLaunchMissionControl := launchMissionControl
	origLaunchPlainControl := launchPlainControl
	defer func() {
		compileBundle = origCompile
		newLocalExecutor = origNewExecutor
		newLocalRunnerService = origNewService
		launchMissionControl = origLaunchMissionControl
		launchPlainControl = origLaunchPlainControl
	}()

	compileBundle = func(in compiler.CompileInput) (runbundle.Bundle, error) {
		if in.Progress != nil {
			in.Progress(compiler.CompileProgress{Stage: compiler.CompileStageCasesDiscovered, TotalCases: 12})
			in.Progress(compiler.CompileProgress{Stage: compiler.CompileStageCaseDone, CompletedCases: 1, TotalCases: 12})
			in.Progress(compiler.CompileProgress{Stage: compiler.CompileStageComplete, CompletedCases: 12, TotalCases: 12})
		}
		return runbundle.Bundle{
			ResolvedSnapshot: runbundle.ResolvedSnapshot{
				Name:      "smoke",
				Execution: runbundle.Execution{Mode: runbundle.ExecutionModeFull, MaxConcurrency: 1},
			},
		}, nil
	}
	newLocalExecutor = func(_ localexecutor.Config) (engine.Executor, error) {
		return fakeExecutor{}, nil
	}
	newLocalRunnerService = func(_ localrunner.Config) (runnerapi.Service, error) {
		return fakeRunnerService{}, nil
	}
	launchMissionControl = func(_ context.Context, _ missioncontrol.Config) (missioncontrol.Outcome, error) {
		t.Fatalf("did not expect Mission Control to launch in non-interactive mode")
		return missioncontrol.Outcome{}, nil
	}
	launchPlainControl = func(_ context.Context, cfg plaincontrol.Config) (missioncontrol.Outcome, error) {
		if cfg.Out == nil {
			t.Fatalf("expected non-interactive output writer")
		}
		if _, err := io.WriteString(cfg.Out, "[run] started run_id=run_test_1 total=12 run_dir="+cfg.RunDir+"\n"); err != nil {
			t.Fatalf("write plain output: %v", err)
		}
		return missioncontrol.Outcome{
			FinalRun: store.Run{RunID: cfg.RunID, State: domain.RunStateCompleted},
		}, nil
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	a := New(&stdout, &stderr)
	err := a.runRun(context.Background(), []string{
		"--suite", "suite",
		"--agent-config", "agent-config",
		"--eval", "eval",
		"--agent-server-binary", "agent-server",
		"--non-interactive",
	})
	if err != nil {
		t.Fatalf("runRun returned error: %v", err)
	}

	text := stdout.String()
	for _, want := range []string{
		"[compile] start",
		"[compile] 0/12 cases",
		"[compile] 1/12 cases",
		"[compile] complete 12/12 cases",
		"[run] started run_id=run_test_1 total=12 run_dir=",
		"run_id: run_test_1",
		"state: completed",
		"run_dir: ",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected stdout to contain %q, got:\n%s", want, text)
		}
	}
}

func validRemoteSuiteRunBundle(t *testing.T) runbundle.Bundle {
	t.Helper()
	return runbundle.Bundle{
		SchemaVersion: runbundle.SchemaVersionV1,
		BundleID:      "bun_test",
		CreatedAt:     time.Date(2026, 3, 24, 12, 0, 0, 0, time.UTC),
		Source: runbundle.Source{
			Kind:            runbundle.SourceKindLocalFiles,
			SubmitProjectID: defaultLocalProjectID,
		},
		ResolvedSnapshot: runbundle.ResolvedSnapshot{
			Name: "smoke",
			Execution: runbundle.Execution{
				Mode:                  runbundle.ExecutionModeFull,
				MaxConcurrency:        1,
				InstanceTimeoutSecond: 1,
			},
			Agent: runbundle.Agent{
				Definition: agentdef.DefinitionSnapshot{
					Manifest: agentdef.Manifest{
						Kind: "agent_definition",
						Name: "fixture-agent",
						Run: agentdef.RunSpec{
							PrepareHook: agentdef.HookRef{Path: "hooks/run-prepare.sh"},
						},
					},
					Package: remoteSuiteTestAgentPackage(t),
				},
				Config: agentdef.ConfigSpec{
					Name:  "fixture-agent-default",
					Mode:  agentdef.ConfigModeDirect,
					Input: map[string]any{"command": []any{"bash", "-lc", "echo hi"}},
				},
			},
			RunDefaults: runbundle.RunDefault{
				Cwd: "/work",
			},
			Cases: []runbundle.Case{{
				CaseID:            "case-1",
				Image:             "ghcr.io/acme/repo@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
				InitialPrompt:     "prompt",
				TestCommand:       []string{"bash", "-lc", "tests/test.sh"},
				TestCwd:           "/work",
				TestTimeoutSecond: 1,
				TestAssets: runbundle.TestAssets{
					ArchiveTGZBase64: "H4sIAAAAAAAC/+3NQQrCMBSE4axziohrTVRiz5PCkwaklrzE8xu6KbhXBP9vM8NsporWo07mk0I3xLhm954hxMvW1304X0/GBfMFTWsq/dL8p/3ONy1+zLOX+enGpJNVqe4g7eGWvMgt5butpYk1AAAAAAAAAAAAAAAAAIDf8QLX6+4FACgAAA==",
					ArchiveTGZSHA256: "32e2807f93a86a87c24cd79cfde22b62adb1861c1778c1d6218a13baa38c285c",
					ArchiveTGZBytes:  136,
				},
			}},
		},
	}
}

func remoteSuiteTestAgentPackage(t *testing.T) testassets.Descriptor {
	t.Helper()
	root := t.TempDir()
	hookPath := filepath.Join(root, "hooks", "run-prepare.sh")
	if err := os.MkdirAll(filepath.Dir(hookPath), 0o755); err != nil {
		t.Fatalf("mkdir hook dir: %v", err)
	}
	if err := os.WriteFile(hookPath, []byte("#!/usr/bin/env bash\nset -euo pipefail\n"), 0o755); err != nil {
		t.Fatalf("write hook: %v", err)
	}
	desc, err := testassets.PackDir(root)
	if err != nil {
		t.Fatalf("pack agent definition: %v", err)
	}
	return desc
}
