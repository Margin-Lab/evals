//go:build integration && integration_model

package integration

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/marginlab/margin-eval/runner/runner-core/engine"
	"github.com/marginlab/margin-eval/runner/runner-core/store"
	"github.com/marginlab/margin-eval/runner/runner-local/localexecutor"
)

func TestRunnerCoreWorkerWithRealAgentServerMatrix(t *testing.T) {
	cases := loadMatrixCases(t)
	requireMatrixAPIKeys(t, cases)

	ensureIntegrationEnv(t)
	ensureRealImageBuilt(t)
	agentServerBinary := ensureRealAgentServerBinaryBuilt(t)

	for _, c := range cases {
		c := c
		t.Run(c.DefinitionName+"_"+c.ConfigName, func(t *testing.T) {
			runStore := store.NewMemoryStore()
			executor, err := localexecutor.New(localexecutor.Config{
				AgentServerBinary: agentServerBinary,
				Env: map[string]string{
					"AGENT_SERVER_LISTEN":                     ":8080",
					"AGENT_SERVER_STOP_GRACE_TIMEOUT":         "4s",
					"AGENT_SERVER_TRAJECTORY_COLLECT_TIMEOUT": "25s",
					"AGENT_SERVER_TRAJECTORY_POLL_INTERVAL":   "200ms",
				},
				ReadyPath:         "/readyz",
				ArtifactRoot:      t.TempDir(),
				AgentPollInterval: 500 * time.Millisecond,
			})
			if err != nil {
				t.Fatalf("new executor: %v", err)
			}

			pool := engine.NewPool(runStore, executor, engine.Config{
				WorkerID:          "it-model-worker",
				WorkerCount:       1,
				PollInterval:      100 * time.Millisecond,
				LeaseDuration:     20 * time.Second,
				HeartbeatInterval: 1 * time.Second,
				ReaperInterval:    2 * time.Second,
			})

			ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
			defer cancel()
			pool.Start(ctx)

			r, err := runStore.CreateRun(context.Background(), store.CreateRunInput{
				RunID:         "run_real_" + c.DefinitionName + "_" + c.ConfigName,
				ProjectID:     "proj_it",
				CreatedByUser: "it_user",
				SourceKind:    "catalog_refs",
				Bundle:        bundleWithCaseImage(buildInstructionBundle(t, c.DefinitionName, c.ConfigName, "latest"), realImageTag),
				At:            time.Now().UTC(),
			})
			if err != nil {
				t.Fatalf("create run: %v", err)
			}
			finalRun := waitForRunTerminal(t, runStore, r.RunID, 160*time.Second)
			if finalRun.State != "completed" {
				t.Fatalf("expected completed run for %s/%s, got %s", c.DefinitionName, c.ConfigName, finalRun.State)
			}
		})
	}
}

func requireMatrixAPIKeys(t *testing.T, cases []agentMatrixCase) {
	t.Helper()
	needsOpenAI := false
	needsAnthropic := false
	needsGemini := false
	for _, c := range cases {
		switch caseRequiresKey(c) {
		case "OPENAI_API_KEY":
			needsOpenAI = true
		case "ANTHROPIC_API_KEY":
			needsAnthropic = true
		case "GEMINI_API_KEY":
			needsGemini = true
		}
	}
	missing := []string{}
	if needsOpenAI && os.Getenv("OPENAI_API_KEY") == "" {
		missing = append(missing, "OPENAI_API_KEY")
	}
	if needsAnthropic && os.Getenv("ANTHROPIC_API_KEY") == "" {
		missing = append(missing, "ANTHROPIC_API_KEY")
	}
	if needsGemini && os.Getenv("GEMINI_API_KEY") == "" {
		missing = append(missing, "GEMINI_API_KEY")
	}
	if len(missing) > 0 {
		t.Fatalf("integration_model requires API keys for matrix cases; missing: %v", missing)
	}
}
