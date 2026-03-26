//go:build integration && integration_model

package integration

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/marginlab/margin-eval/runner/runner-core/runnerapi"
	"github.com/marginlab/margin-eval/runner/runner-local/localexecutor"
	"github.com/marginlab/margin-eval/runner/runner-local/localrunner"
)

func TestRunnerLocalWithRealAgentServerMatrix(t *testing.T) {
	cases := loadMatrixCases(t)
	requireMatrixAPIKeys(t, cases)

	ensureIntegrationEnv(t)
	ensureRealImageBuilt(t)
	agentServerBinary := ensureRealAgentServerBinaryBuilt(t)

	for _, c := range cases {
		c := c
		t.Run(c.DefinitionName+"_"+c.ConfigName, func(t *testing.T) {
			executor, err := localexecutor.New(localexecutor.Config{
				AgentServerBinary: agentServerBinary,
				Env: map[string]string{
					"AGENT_SERVER_LISTEN":                     ":8080",
					"AGENT_SERVER_STOP_GRACE_TIMEOUT":         "4s",
					"AGENT_SERVER_TRAJECTORY_COLLECT_TIMEOUT": "25s",
					"AGENT_SERVER_TRAJECTORY_POLL_INTERVAL":   "200ms",
				},
				ReadyPath:         "/readyz",
				OutputRoot:        t.TempDir(),
				AgentPollInterval: 400 * time.Millisecond,
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

			ctx, cancel := context.WithTimeout(context.Background(), 220*time.Second)
			defer cancel()
			svc.Start(ctx)

			run, err := svc.SubmitRun(context.Background(), runnerapi.SubmitInput{
				ProjectID:     "proj_local",
				CreatedByUser: "user_local",
				Name:          "local-real-matrix",
				Bundle:        bundleWithCaseImage(buildInstructionBundle(t, c.DefinitionName, c.ConfigName, "latest"), realImageTag),
			})
			if err != nil {
				t.Fatalf("submit run: %v", err)
			}
			finalRun, err := svc.WaitForTerminalRun(ctx, run.RunID, 250*time.Millisecond)
			if err != nil {
				t.Fatalf("wait for run: %v", err)
			}
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
	for _, c := range cases {
		for _, envName := range caseRequiredEnv(c) {
			switch envName {
			case "OPENAI_API_KEY":
				needsOpenAI = true
			case "ANTHROPIC_API_KEY":
				needsAnthropic = true
			}
		}
	}
	missing := []string{}
	if needsOpenAI && os.Getenv("OPENAI_API_KEY") == "" {
		missing = append(missing, "OPENAI_API_KEY")
	}
	if needsAnthropic && os.Getenv("ANTHROPIC_API_KEY") == "" {
		missing = append(missing, "ANTHROPIC_API_KEY")
	}
	if len(missing) > 0 {
		t.Fatalf("integration_model requires API keys for matrix cases; missing: %v", missing)
	}
}
