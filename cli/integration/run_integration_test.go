//go:build integration

package integration

import (
	"strings"
	"testing"
	"time"

	"github.com/marginlab/margin-eval/runner/runner-core/store"
)

func TestCLIRunWithFakeAgentServerSuccess(t *testing.T) {
	ensureIntegrationEnv(t)
	ensureFakeImageBuilt(t)
	agentServerBinary := ensureFakeAgentServerBinaryBuilt(t)

	suitePath, configPath, evalPath := writeFakeRunFixture(
		t,
		"fake cli run [FAKE_RUN_MS=120]",
		"#!/usr/bin/env bash\n/marginlab/tests/run-eval\n",
		false,
	)
	rootDir := t.TempDir()

	result := runMargin(t, 3*time.Minute, nil,
		append([]string{
			"run",
			"--suite", suitePath,
			"--agent-config", configPath,
			"--eval", evalPath,
			"--root", rootDir,
			"--agent-server-binary", agentServerBinary,
			"--non-interactive",
			"--run-timeout", "2m",
		}, modelAgentEnvArgs()...)...,
	)
	if result.ExitCode != 0 {
		t.Fatalf("margin exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", result.ExitCode, result.Stdout, result.Stderr)
	}
	for _, needle := range []string{"[compile] start", "[run] started", "run_id:", "state: completed", "run_dir:"} {
		if !strings.Contains(result.Stdout, needle) {
			t.Fatalf("stdout missing %q:\n%s", needle, result.Stdout)
		}
	}

	summary := parseCLIRunSummary(t, result.Stdout)
	run := loadPersistedRun(t, rootDir, summary)
	if run.Results.Status.Succeeded.Count != 1 || run.Results.Status.Succeeded.Percentage != 100 {
		t.Fatalf("unexpected succeeded summary: %+v", run.Results.Status.Succeeded)
	}
	if run.Results.Usage.InputTokens != 12 || run.Results.Usage.OutputTokens != 5 || run.Results.Usage.ToolCalls != 1 {
		t.Fatalf("unexpected usage summary: %+v", run.Results.Usage)
	}
	requireArtifactRole(t, summary.RunDir, run.Artifacts, store.ArtifactRoleTrajectory)
}

func TestCLIRunWithFakeAgentServerDryRun(t *testing.T) {
	ensureIntegrationEnv(t)
	ensureFakeImageBuilt(t)
	agentServerBinary := ensureFakeAgentServerBinaryBuilt(t)

	suitePath, configPath, evalPath := writeFakeRunFixture(
		t,
		"fake cli dry run",
		"#!/usr/bin/env bash\n/marginlab/tests/run-eval\n",
		false,
	)
	rootDir := t.TempDir()

	result := runMargin(t, 3*time.Minute, nil,
		append([]string{
			"run",
			"--suite", suitePath,
			"--agent-config", configPath,
			"--eval", evalPath,
			"--root", rootDir,
			"--agent-server-binary", agentServerBinary,
			"--non-interactive",
			"--dry-run",
			"--run-timeout", "2m",
		}, modelAgentEnvArgs()...)...,
	)
	if result.ExitCode != 0 {
		t.Fatalf("margin exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", result.ExitCode, result.Stdout, result.Stderr)
	}
	if !strings.Contains(result.Stdout, "state: completed") {
		t.Fatalf("stdout missing completed state:\n%s", result.Stdout)
	}

	summary := parseCLIRunSummary(t, result.Stdout)
	run := loadPersistedRun(t, rootDir, summary)
	if run.Results.Usage.InputTokens != 0 || run.Results.Usage.OutputTokens != 0 || run.Results.Usage.ToolCalls != 0 {
		t.Fatalf("unexpected dry-run usage summary: %+v", run.Results.Usage)
	}
	if run.Results.Usage.InstancesWithUsage != 0 || run.Results.Usage.InstancesWithoutUsage != 1 {
		t.Fatalf("unexpected dry-run usage coverage: %+v", run.Results.Usage)
	}
	forbidArtifactRoles(t, run.Artifacts, store.ArtifactRoleTrajectory, store.ArtifactRoleTestStdout, store.ArtifactRoleTestStderr)
}

func TestCLIRunWithFakeAgentServerFailure(t *testing.T) {
	ensureIntegrationEnv(t)
	ensureFakeImageBuilt(t)
	agentServerBinary := ensureFakeAgentServerBinaryBuilt(t)

	suitePath, configPath, evalPath := writeFakeRunFixture(
		t,
		"fake cli failure [FAKE_TEST_FAIL]",
		"#!/usr/bin/env bash\n/marginlab/tests/run-eval\n",
		false,
	)
	rootDir := t.TempDir()

	result := runMargin(t, 3*time.Minute, nil,
		append([]string{
			"run",
			"--suite", suitePath,
			"--agent-config", configPath,
			"--eval", evalPath,
			"--root", rootDir,
			"--agent-server-binary", agentServerBinary,
			"--non-interactive",
			"--run-timeout", "2m",
		}, modelAgentEnvArgs()...)...,
	)
	if result.ExitCode == 0 {
		t.Fatalf("margin exit code = %d, want nonzero\nstdout:\n%s\nstderr:\n%s", result.ExitCode, result.Stdout, result.Stderr)
	}
	if !strings.Contains(result.Stderr, "error:") {
		t.Fatalf("stderr missing error prefix:\n%s", result.Stderr)
	}
	for _, needle := range []string{"run_id:", "state:", "run_dir:"} {
		if !strings.Contains(result.Stdout, needle) {
			t.Fatalf("stdout missing %q:\n%s", needle, result.Stdout)
		}
	}

	summary := parseCLIRunSummary(t, result.Stdout)
	if summary.State == "completed" {
		t.Fatalf("unexpected completed state in failure case")
	}
	run := loadPersistedRun(t, rootDir, summary)
	if run.Results.Status.TestFailed.Count != 1 {
		t.Fatalf("unexpected failure summary: %+v", run.Results.Status)
	}
}
