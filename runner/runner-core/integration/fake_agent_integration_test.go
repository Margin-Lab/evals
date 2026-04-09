//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/marginlab/margin-eval/runner/runner-core/engine"
	"github.com/marginlab/margin-eval/runner/runner-core/store"
	"github.com/marginlab/margin-eval/runner/runner-core/testfixture"
	"github.com/marginlab/margin-eval/runner/runner-local/localexecutor"
)

func TestRunnerCoreWorkerWithFakeAgentServer(t *testing.T) {
	ensureIntegrationEnv(t)
	ensureFakeImageBuilt(t)
	agentServerBinary := ensureFakeAgentServerBinaryBuilt(t)

	runStore := store.NewMemoryStore()

	executor, err := localexecutor.New(localexecutor.Config{
		AgentServerBinary: agentServerBinary,
		AgentPollInterval: 300 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("new executor: %v", err)
	}

	pool := engine.NewPool(runStore, executor, engine.Config{
		WorkerID:          "it-worker",
		WorkerCount:       1,
		PollInterval:      100 * time.Millisecond,
		LeaseDuration:     15 * time.Second,
		HeartbeatInterval: 1 * time.Second,
		ReaperInterval:    1 * time.Second,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool.Start(ctx)

	successRunDir := t.TempDir()
	if err := executor.RegisterRunDir("run_fake_success", successRunDir); err != nil {
		t.Fatalf("register success run dir: %v", err)
	}
	success, err := runStore.CreateRun(context.Background(), store.CreateRunInput{
		RunID:         "run_fake_success",
		ProjectID:     "proj_it",
		CreatedByUser: "it_user",
		SourceKind:    "local_files",
		Bundle:        bundleWithCaseImage(bundleWithFakeEval(buildBundleWithAgent("respond with hello [FAKE_RUN_MS=150]", testfixture.MinimalAgent())), fakeImageTag),
		At:            time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("create success run: %v", err)
	}
	finalSuccess := waitForRunTerminal(t, runStore, success.RunID, 40*time.Second)
	if finalSuccess.State != "completed" {
		t.Fatalf("expected completed run, got %s", finalSuccess.State)
	}

	failedBundle := bundleWithCaseImage(bundleWithFakeEval(buildBundleWithAgent("fake run failure [FAKE_TEST_FAIL]", testfixture.MinimalAgent())), fakeImageTag)
	failedRunDir := t.TempDir()
	if err := executor.RegisterRunDir("run_fake_fail", failedRunDir); err != nil {
		t.Fatalf("register failed run dir: %v", err)
	}
	failed, err := runStore.CreateRun(context.Background(), store.CreateRunInput{
		RunID:         "run_fake_fail",
		ProjectID:     "proj_it",
		CreatedByUser: "it_user",
		SourceKind:    "local_files",
		Bundle:        failedBundle,
		At:            time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("create failed run: %v", err)
	}
	finalFailed := waitForRunTerminal(t, runStore, failed.RunID, 40*time.Second)
	if finalFailed.State != "failed" {
		t.Fatalf("expected failed run, got %s", finalFailed.State)
	}
}
