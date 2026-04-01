//go:build integration && integration_model

package integration

import (
	"strings"
	"testing"
	"time"

	"github.com/marginlab/margin-eval/runner/runner-core/store"
	"github.com/marginlab/margin-eval/runner/runner-core/testfixture"
)

func TestCLIRunWithRealAgentServerMatrix(t *testing.T) {
	cases := loadMatrixCases(t)
	requireMatrixAPIKeys(t, cases)

	ensureIntegrationEnv(t)
	ensureRealImageBuilt(t)
	agentServerBinary := ensureRealAgentServerBinaryBuilt(t)

	for _, tc := range expandVersionedMatrixCases(t) {
		tc := tc
		t.Run(caseName(tc.DefinitionName, tc.ConfigName, tc.AgentVersion), func(t *testing.T) {
			suitePath, configPath, evalPath := writeModelRunFixture(t, tc.DefinitionName, tc.ConfigName, tc.AgentVersion)
			rootDir := t.TempDir()

			result := runMargin(t, 7*time.Minute, nil,
				append([]string{
					"run",
					"--suite", suitePath,
					"--agent-config", configPath,
					"--eval", evalPath,
					"--root", rootDir,
					"--agent-server-binary", agentServerBinary,
					"--non-interactive",
					"--run-timeout", "5m",
				}, modelAgentEnvArgs()...)...,
			)
			if result.ExitCode != 0 {
				t.Fatalf("margin exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", result.ExitCode, result.Stdout, result.Stderr)
			}
			for _, needle := range []string{"run_id:", "state: completed", "run_dir:"} {
				if !strings.Contains(result.Stdout, needle) {
					t.Fatalf("stdout missing %q:\n%s", needle, result.Stdout)
				}
			}

			summary := parseCLIRunSummary(t, result.Stdout)
			run := loadPersistedRun(t, rootDir, summary)
			if run.Results.Status.Succeeded.Count != 1 || run.Results.Status.Succeeded.Percentage != 100 {
				t.Fatalf("unexpected succeeded summary: %+v", run.Results.Status.Succeeded)
			}

			trajectory := requireArtifactRole(t, summary.RunDir, run.Artifacts, store.ArtifactRoleTrajectory)
			body := readArtifactBody(t, summary.RunDir, trajectory)
			expected := testfixture.IntegrationInstructionKeywordsForAgent(tc.DefinitionName).ExpectedResponse()
			if !strings.Contains(body, expected) {
				t.Fatalf("trajectory missing expected response %q", expected)
			}
		})
	}
}
