//go:build integration

package integration

import (
	"os"
	"path/filepath"
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
	runDir := t.TempDir()

	result := runMargin(t, 3*time.Minute, nil,
		append([]string{
			"run",
			"--suite", suitePath,
			"--agent-config", configPath,
			"--eval", evalPath,
			"--output", runDir,
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
	run := loadPersistedRun(t, runDir, summary)
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
	runDir := t.TempDir()

	result := runMargin(t, 3*time.Minute, nil,
		append([]string{
			"run",
			"--suite", suitePath,
			"--agent-config", configPath,
			"--eval", evalPath,
			"--output", runDir,
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
	run := loadPersistedRun(t, runDir, summary)
	if run.Results.Usage.InputTokens != 0 || run.Results.Usage.OutputTokens != 0 || run.Results.Usage.ToolCalls != 0 {
		t.Fatalf("unexpected dry-run usage summary: %+v", run.Results.Usage)
	}
	if run.Results.Usage.InstancesWithUsage != 0 || run.Results.Usage.InstancesWithoutUsage != 1 {
		t.Fatalf("unexpected dry-run usage coverage: %+v", run.Results.Usage)
	}
	if len(run.Results.Instances) != 1 || run.Results.Instances[0].FinalState != "succeeded" {
		t.Fatalf("unexpected dry-run instance summary: %+v", run.Results.Instances)
	}
	forbidArtifactRoles(t, run.Artifacts, store.ArtifactRoleTrajectory)
	requireArtifactRole(t, summary.RunDir, run.Artifacts, store.ArtifactRoleTestStdout)
	requireArtifactRole(t, summary.RunDir, run.Artifacts, store.ArtifactRoleTestStderr)
}

func TestCLIRunDryRunSamplesPerCase(t *testing.T) {
	ensureIntegrationEnv(t)
	ensureFakeImageBuilt(t)
	agentServerBinary := ensureFakeAgentServerBinaryBuilt(t)

	suitePath, configPath, evalPath := writeFakeRunFixture(
		t,
		"fake cli sampled dry run",
		"#!/usr/bin/env bash\n/marginlab/tests/run-eval\n",
		false,
	)
	body, err := os.ReadFile(evalPath)
	if err != nil {
		t.Fatalf("read eval config: %v", err)
	}
	if err := os.WriteFile(evalPath, append(body, []byte("samples_per_case = 3\n")...), 0o644); err != nil {
		t.Fatalf("write eval config: %v", err)
	}
	runDir := filepath.Join(t.TempDir(), "sampled-run")

	result := runMargin(t, 3*time.Minute, nil,
		append([]string{
			"run",
			"--suite", suitePath,
			"--agent-config", configPath,
			"--eval", evalPath,
			"--output", runDir,
			"--agent-server-binary", agentServerBinary,
			"--non-interactive",
			"--dry-run",
			"--run-timeout", "2m",
		}, modelAgentEnvArgs()...)...,
	)
	if result.ExitCode != 0 {
		t.Fatalf("margin exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", result.ExitCode, result.Stdout, result.Stderr)
	}

	summary := parseCLIRunSummary(t, result.Stdout)
	run := loadPersistedRun(t, runDir, summary)
	if run.Results.TotalCases != 1 || run.Results.TotalInstances != 3 || run.Results.SamplesPerCase != 3 {
		t.Fatalf("unexpected sampled totals: cases=%d instances=%d samples_per_case=%d", run.Results.TotalCases, run.Results.TotalInstances, run.Results.SamplesPerCase)
	}
	if len(run.Results.Instances) != 3 {
		t.Fatalf("expected 3 instance summaries, got %d", len(run.Results.Instances))
	}
	wantKeys := []string{"case-1#1", "case-1#2", "case-1#3"}
	for i, inst := range run.Results.Instances {
		if inst.InstanceKey != wantKeys[i] || inst.SampleIndex != i+1 || inst.SampleCount != 3 {
			t.Fatalf("unexpected sample instance %d: %+v", i, inst)
		}
	}
	if len(run.Results.Cases) != 1 || run.Results.Cases[0].SampleCount != 3 || run.Results.Cases[0].PassCount != 3 {
		t.Fatalf("unexpected grouped case summary: %+v", run.Results.Cases)
	}
}

func TestCLIRunWithFakeAgentServerDryRunTestInfra(t *testing.T) {
	ensureIntegrationEnv(t)
	ensureFakeImageBuilt(t)
	agentServerBinary := ensureFakeAgentServerBinaryBuilt(t)

	suitePath, configPath, evalPath := writeFakeRunFixture(
		t,
		"fake cli dry run infra",
		"#!/usr/bin/env bash\nexit 2\n",
		false,
	)
	runDir := t.TempDir()

	result := runMargin(t, 3*time.Minute, nil,
		append([]string{
			"run",
			"--suite", suitePath,
			"--agent-config", configPath,
			"--eval", evalPath,
			"--output", runDir,
			"--agent-server-binary", agentServerBinary,
			"--non-interactive",
			"--dry-run",
			"--run-timeout", "2m",
		}, modelAgentEnvArgs()...)...,
	)
	if result.ExitCode != 1 {
		t.Fatalf("margin exit code = %d, want 1\nstdout:\n%s\nstderr:\n%s", result.ExitCode, result.Stdout, result.Stderr)
	}
	if !strings.Contains(result.Stdout, "state: failed") {
		t.Fatalf("stdout missing failed state:\n%s", result.Stdout)
	}
	if !strings.Contains(result.Stdout, "infra_fail 1") && !strings.Contains(result.Stdout, "infra_fail=1") {
		t.Fatalf("stdout missing infra summary:\n%s", result.Stdout)
	}

	summary := parseCLIRunSummary(t, result.Stdout)
	run := loadPersistedRun(t, runDir, summary)
	if run.Results.Status.InfraFailed.Count != 1 || run.Results.Status.TestFailed.Count != 0 {
		t.Fatalf("unexpected dry-run failure breakdown: %+v", run.Results.Status)
	}
	if len(run.Results.Instances) != 1 || run.Results.Instances[0].FinalState != "infra_failed" {
		t.Fatalf("unexpected dry-run infra instance summary: %+v", run.Results.Instances)
	}
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
	runDir := t.TempDir()

	result := runMargin(t, 3*time.Minute, nil,
		append([]string{
			"run",
			"--suite", suitePath,
			"--agent-config", configPath,
			"--eval", evalPath,
			"--output", runDir,
			"--agent-server-binary", agentServerBinary,
			"--non-interactive",
			"--run-timeout", "2m",
		}, modelAgentEnvArgs()...)...,
	)
	if result.ExitCode != 0 {
		t.Fatalf("margin exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", result.ExitCode, result.Stdout, result.Stderr)
	}
	if strings.Contains(result.Stderr, "error:") {
		t.Fatalf("stderr unexpectedly contained error prefix:\n%s", result.Stderr)
	}
	for _, needle := range []string{"run_id:", "state:", "run_dir:"} {
		if !strings.Contains(result.Stdout, needle) {
			t.Fatalf("stdout missing %q:\n%s", needle, result.Stdout)
		}
	}

	summary := parseCLIRunSummary(t, result.Stdout)
	if summary.State != "completed" {
		t.Fatalf("unexpected non-completed state in test failure case: %+v", summary)
	}
	run := loadPersistedRun(t, runDir, summary)
	if run.Results.Status.TestFailed.Count != 1 {
		t.Fatalf("unexpected failure summary: %+v", run.Results.Status)
	}
}
