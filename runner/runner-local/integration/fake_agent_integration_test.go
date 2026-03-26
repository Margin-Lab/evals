//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/marginlab/margin-eval/runner/runner-core/runbundle"
	"github.com/marginlab/margin-eval/runner/runner-core/runnerapi"
	"github.com/marginlab/margin-eval/runner/runner-core/runresults"
	"github.com/marginlab/margin-eval/runner/runner-core/store"
	"github.com/marginlab/margin-eval/runner/runner-core/testfixture"
	"github.com/marginlab/margin-eval/runner/runner-local/localexecutor"
	"github.com/marginlab/margin-eval/runner/runner-local/localrunner"
	"github.com/marginlab/margin-eval/runner/runner-local/runfs"
)

func TestRunnerLocalWithFakeAgentServer(t *testing.T) {
	ensureIntegrationEnv(t)
	ensureFakeImageBuilt(t)
	agentServerBinary := ensureFakeAgentServerBinaryBuilt(t)

	rootDir := t.TempDir()
	executor, err := localexecutor.New(localexecutor.Config{
		AgentServerBinary: agentServerBinary,
		OutputRoot:        t.TempDir(),
		AgentPollInterval: 250 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("new executor: %v", err)
	}
	svc, err := localrunner.NewService(localrunner.Config{
		RootDir:  rootDir,
		Executor: executor,
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	svc.Start(ctx)

	run, err := svc.SubmitRun(context.Background(), runnerapi.SubmitInput{
		ProjectID:     "proj_local",
		CreatedByUser: "user_local",
		Name:          "local-fake-success",
		Bundle:        bundleWithCaseImage(bundleWithFakeEval(buildBundleWithAgent("fake local run [FAKE_RUN_MS=120]", testfixture.MinimalAgent())), fakeImageTag),
	})
	if err != nil {
		t.Fatalf("submit run: %v", err)
	}
	finalRun, err := svc.WaitForTerminalRun(ctx, run.RunID, 150*time.Millisecond)
	if err != nil {
		t.Fatalf("wait for run: %v", err)
	}
	if finalRun.State != "completed" {
		t.Fatalf("expected completed run, got %s", finalRun.State)
	}

	for _, full := range []string{
		runfs.BundlePath(rootDir, run.RunID),
		runfs.ManifestPath(rootDir, run.RunID),
		runfs.ResultsPath(rootDir, run.RunID),
		runfs.EventsPath(rootDir, run.RunID),
		runfs.ArtifactsIndexPath(rootDir, run.RunID),
	} {
		if _, err := os.Stat(full); err != nil {
			t.Fatalf("expected %s: %v", full, err)
		}
	}
	resultsPath := runfs.ResultsPath(rootDir, run.RunID)
	raw, err := os.ReadFile(resultsPath)
	if err != nil {
		t.Fatalf("read results.json: %v", err)
	}
	var summary runresults.Summary
	if err := json.Unmarshal(raw, &summary); err != nil {
		t.Fatalf("unmarshal results.json: %v", err)
	}
	if summary.Status.Succeeded.Count != 1 || summary.Status.Succeeded.Percentage != 100 {
		t.Fatalf("unexpected succeeded summary: %+v", summary.Status.Succeeded)
	}
	if summary.Usage.InputTokens != 12 || summary.Usage.OutputTokens != 5 || summary.Usage.ToolCalls != 1 {
		t.Fatalf("unexpected usage summary: %+v", summary.Usage)
	}
	artifactPayload := filepath.Join(rootDir, "runs", run.RunID, "instances", run.RunID+"-inst-0001", "trajectory.json")
	if _, err := os.Stat(artifactPayload); err != nil {
		t.Fatalf("expected trajectory payload copy at %s: %v", artifactPayload, err)
	}
}

func TestRunnerLocalWithFakeAgentServerFailure(t *testing.T) {
	ensureIntegrationEnv(t)
	ensureFakeImageBuilt(t)
	agentServerBinary := ensureFakeAgentServerBinaryBuilt(t)

	executor, err := localexecutor.New(localexecutor.Config{
		AgentServerBinary: agentServerBinary,
		OutputRoot:        t.TempDir(),
		AgentPollInterval: 250 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("new executor: %v", err)
	}
	svc, err := localrunner.NewService(localrunner.Config{
		RootDir:  t.TempDir(),
		Executor: executor,
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	svc.Start(ctx)

	run, err := svc.SubmitRun(context.Background(), runnerapi.SubmitInput{
		ProjectID:     "proj_local",
		CreatedByUser: "user_local",
		Name:          "local-fake-failure",
		Bundle:        bundleWithCaseImage(bundleWithFakeEval(buildBundleWithAgent("fake local run failure [FAKE_TEST_FAIL]", testfixture.MinimalAgent())), fakeImageTag),
	})
	if err != nil {
		t.Fatalf("submit run: %v", err)
	}
	finalRun, err := svc.WaitForTerminalRun(ctx, run.RunID, 150*time.Millisecond)
	if err != nil {
		t.Fatalf("wait for run: %v", err)
	}
	if finalRun.State != "failed" {
		t.Fatalf("expected failed run, got %s", finalRun.State)
	}
}

func TestRunnerLocalWithFakeAgentServerDryRun(t *testing.T) {
	ensureIntegrationEnv(t)
	ensureFakeImageBuilt(t)
	agentServerBinary := ensureFakeAgentServerBinaryBuilt(t)

	rootDir := t.TempDir()
	executor, err := localexecutor.New(localexecutor.Config{
		AgentServerBinary: agentServerBinary,
		OutputRoot:        t.TempDir(),
		AgentPollInterval: 250 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("new executor: %v", err)
	}
	svc, err := localrunner.NewService(localrunner.Config{
		RootDir:  rootDir,
		Executor: executor,
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	svc.Start(ctx)

	bundle := bundleWithCaseImage(buildBundleWithAgent("fake local dry run", testfixture.MinimalAgent()), fakeImageTag)
	bundle.ResolvedSnapshot.Execution.Mode = runbundle.ExecutionModeDryRun
	run, err := svc.SubmitRun(context.Background(), runnerapi.SubmitInput{
		ProjectID:     "proj_local",
		CreatedByUser: "user_local",
		Name:          "local-fake-dry-run",
		Bundle:        bundle,
	})
	if err != nil {
		t.Fatalf("submit run: %v", err)
	}
	finalRun, err := svc.WaitForTerminalRun(ctx, run.RunID, 150*time.Millisecond)
	if err != nil {
		t.Fatalf("wait for run: %v", err)
	}
	if finalRun.State != "completed" {
		t.Fatalf("expected completed run, got %s", finalRun.State)
	}

	manifestPath := runfs.ManifestPath(rootDir, run.RunID)
	manifestRaw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read manifest.json: %v", err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(manifestRaw, &manifest); err != nil {
		t.Fatalf("unmarshal manifest.json: %v", err)
	}
	if manifest["execution_mode"] != string(runbundle.ExecutionModeDryRun) {
		t.Fatalf("manifest execution_mode = %#v, want %q", manifest["execution_mode"], runbundle.ExecutionModeDryRun)
	}

	resultsPath := runfs.ResultsPath(rootDir, run.RunID)
	raw, err := os.ReadFile(resultsPath)
	if err != nil {
		t.Fatalf("read results.json: %v", err)
	}
	var summary runresults.Summary
	if err := json.Unmarshal(raw, &summary); err != nil {
		t.Fatalf("unmarshal results.json: %v", err)
	}
	if summary.Status.Succeeded.Count != 1 || summary.Status.Succeeded.Percentage != 100 {
		t.Fatalf("unexpected succeeded summary: %+v", summary.Status.Succeeded)
	}
	if summary.Usage.InputTokens != 0 || summary.Usage.OutputTokens != 0 || summary.Usage.ToolCalls != 0 {
		t.Fatalf("unexpected usage summary: %+v", summary.Usage)
	}
	if summary.Usage.InstancesWithUsage != 0 || summary.Usage.InstancesWithoutUsage != 1 {
		t.Fatalf("unexpected usage coverage: %+v", summary.Usage)
	}
	if len(summary.Instances) != 1 || summary.Instances[0].FinalState != "succeeded" {
		t.Fatalf("unexpected instance summaries: %+v", summary.Instances)
	}

	metadataPath := runfs.ArtifactsIndexPath(rootDir, run.RunID)
	metadataRaw, err := os.ReadFile(metadataPath)
	if err != nil {
		t.Fatalf("read artifacts metadata: %v", err)
	}
	var artifacts []store.Artifact
	if err := json.Unmarshal(metadataRaw, &artifacts); err != nil {
		t.Fatalf("unmarshal artifacts metadata: %v", err)
	}
	for _, artifact := range artifacts {
		switch artifact.Role {
		case store.ArtifactRoleTrajectory, store.ArtifactRoleTestStdout, store.ArtifactRoleTestStderr:
			t.Fatalf("unexpected dry-run artifact role %q in metadata", artifact.Role)
		}
	}

	trajectoryPayload := filepath.Join(rootDir, "runs", run.RunID, "instances", run.RunID+"-inst-0001", "trajectory.json")
	if _, err := os.Stat(trajectoryPayload); !os.IsNotExist(err) {
		t.Fatalf("expected no trajectory payload copy, stat err=%v", err)
	}
}
