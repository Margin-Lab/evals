package missioncontrol

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	xansi "github.com/charmbracelet/x/ansi"

	"github.com/marginlab/margin-eval/runner/runner-core/domain"
	"github.com/marginlab/margin-eval/runner/runner-core/runbundle"
	"github.com/marginlab/margin-eval/runner/runner-core/runnerapi"
	"github.com/marginlab/margin-eval/runner/runner-core/store"
)

// plainText strips ANSI escape sequences for assertion safety.
func plainText(s string) string {
	return stripANSISequences(s)
}

func assertLinesWithinWidth(t *testing.T, rendered string, width int) {
	t.Helper()
	for i, line := range strings.Split(plainText(rendered), "\n") {
		if got := xansi.StringWidth(line); got > width {
			t.Fatalf("line %d exceeds width %d: %d cells (%q)", i+1, width, got, line)
		}
	}
}

func assertHeightWithinLimit(t *testing.T, rendered string, height int) {
	t.Helper()
	if got := lipgloss.Height(rendered); got > height {
		t.Fatalf("rendered height exceeds limit %d: %d lines\n%s", height, got, plainText(rendered))
	}
}

func mustLogStreamByID(t *testing.T, id logStream) logStreamSpec {
	t.Helper()
	for _, spec := range logStreams {
		if spec.ID == id {
			return spec
		}
	}
	t.Fatalf("log stream %q not found", id)
	return logStreamSpec{}
}

type fakeMissionSource struct {
	snapshot runnerapi.RunSnapshot
	text     ArtifactText
	texts    map[string]ArtifactText
	errs     map[string]error
}

func (f *fakeMissionSource) GetRunSnapshot(context.Context, string) (runnerapi.RunSnapshot, error) {
	return f.snapshot, nil
}

func (f *fakeMissionSource) ReadArtifactText(_ context.Context, artifact store.Artifact, _ int64) (ArtifactText, error) {
	if f.errs != nil {
		for _, key := range []string{artifact.ArtifactID, artifact.StoreKey, artifact.Role} {
			if err, ok := f.errs[key]; ok {
				return ArtifactText{}, err
			}
		}
	}
	if f.texts != nil {
		for _, key := range []string{artifact.ArtifactID, artifact.StoreKey, artifact.Role} {
			if text, ok := f.texts[key]; ok {
				return text, nil
			}
		}
	}
	return f.text, nil
}

func TestResolveCombinedTestOutputByRole(t *testing.T) {
	inst := &runnerapi.InstanceSnapshot{
		Instance: store.Instance{InstanceID: "inst_1", State: domain.InstanceStateSucceeded},
		Artifacts: []store.Artifact{
			{
				ArtifactID: "art_stdout",
				Role:       store.ArtifactRoleTestStdout,
				StoreKey:   "instances/inst_1/test/test_stdout.txt",
			},
			{
				ArtifactID: "art_stderr",
				Role:       store.ArtifactRoleTestStderr,
				StoreKey:   "instances/inst_1/test/test_stderr.txt",
			},
		},
	}
	targets, _, _ := resolveLogTargets(inst, mustLogStreamByID(t, logStreamTestOutput))
	if len(targets) != 2 {
		t.Fatalf("expected 2 combined targets, got %d", len(targets))
	}
	if targets[0].Artifact.ArtifactID != "art_stdout" {
		t.Fatalf("unexpected stdout artifact id: %s", targets[0].Artifact.ArtifactID)
	}
	if targets[1].Artifact.ArtifactID != "art_stderr" {
		t.Fatalf("unexpected stderr artifact id: %s", targets[1].Artifact.ArtifactID)
	}
}

func TestResolveCombinedTestOutputFallsBackToResultRefs(t *testing.T) {
	inst := &runnerapi.InstanceSnapshot{
		Instance: store.Instance{InstanceID: "inst_1", State: domain.InstanceStateSucceeded},
		Result: &store.StoredInstanceResult{
			TestStdoutRef: "instances/inst_1/test/test_stdout.txt",
			TestStderrRef: "instances/inst_1/test/test_stderr.txt",
		},
		Artifacts: []store.Artifact{
			{
				ArtifactID: "art_stdout",
				Role:       "other",
				StoreKey:   "instances/inst_1/test/test_stdout.txt",
			},
			{
				ArtifactID: "art_stderr",
				Role:       "other",
				StoreKey:   "instances/inst_1/test/test_stderr.txt",
			},
		},
	}
	targets, _, _ := resolveLogTargets(inst, mustLogStreamByID(t, logStreamTestOutput))
	if len(targets) != 2 {
		t.Fatalf("expected 2 combined targets via refs, got %d", len(targets))
	}
	if targets[0].Artifact.ArtifactID != "art_stdout" {
		t.Fatalf("unexpected stdout artifact id: %s", targets[0].Artifact.ArtifactID)
	}
	if targets[1].Artifact.ArtifactID != "art_stderr" {
		t.Fatalf("unexpected stderr artifact id: %s", targets[1].Artifact.ArtifactID)
	}
}

func TestResolveCombinedTestOutputAllowsSingleSide(t *testing.T) {
	inst := &runnerapi.InstanceSnapshot{
		Instance: store.Instance{InstanceID: "inst_1", State: domain.InstanceStateSucceeded},
		Artifacts: []store.Artifact{{
			ArtifactID: "art_stdout",
			Role:       store.ArtifactRoleTestStdout,
			StoreKey:   "instances/inst_1/test/test_stdout.txt",
		}},
	}
	targets, _, _ := resolveLogTargets(inst, mustLogStreamByID(t, logStreamTestOutput))
	if len(targets) != 1 {
		t.Fatalf("expected 1 combined target, got %d", len(targets))
	}
	if targets[0].Section != "stdout" {
		t.Fatalf("expected stdout section, got %q", targets[0].Section)
	}
}

func TestResolveLogTargetsFindsExecutorRole(t *testing.T) {
	inst := &runnerapi.InstanceSnapshot{
		Instance: store.Instance{InstanceID: "inst_1", State: domain.InstanceStateAgentRunning},
		Artifacts: []store.Artifact{{
			ArtifactID: "art_control",
			Role:       store.ArtifactRoleAgentControl,
			StoreKey:   "instances/inst_1/run/agent_server_control.log",
		}},
	}
	targets, _, _ := resolveLogTargets(inst, mustLogStreamByID(t, logStreamAgentControl))
	if len(targets) != 1 {
		t.Fatalf("expected 1 agent-control target, got %d", len(targets))
	}
	if targets[0].Artifact.ArtifactID != "art_control" {
		t.Fatalf("unexpected artifact id: %s", targets[0].Artifact.ArtifactID)
	}
}

func TestRenderInstancesDoesNotShowImage(t *testing.T) {
	m := &model{
		snapshotLoaded: true,
		snapshot: runnerapi.RunSnapshot{
			Instances: []runnerapi.InstanceSnapshot{
				{
					Instance: store.Instance{
						Ordinal: 0,
						State:   domain.InstanceStatePending,
						Case: runbundle.Case{
							CaseID: "case_1",
							Image:  "ghcr.io/acme/repo@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
						},
					},
				},
			},
		},
	}

	out := m.renderInstances(120, 10)
	if strings.Contains(plainText(out), "image:") {
		t.Fatalf("did not expect image in instances view, got:\n%s", out)
	}
}

func TestRenderRightPaneShowsRetainedIdentityOnly(t *testing.T) {
	agentExitCode := 1
	testExitCode := 2
	m := &model{
		snapshotLoaded: true,
		selectedState:  simplifiedStateRunningAgent,
		snapshot: runnerapi.RunSnapshot{
			Instances: []runnerapi.InstanceSnapshot{
				{
					Instance: store.Instance{
						InstanceID: "run_1-inst-0001",
						Ordinal:    0,
						State:      domain.InstanceStateAgentRunning,
						Case: runbundle.Case{
							CaseID: "case_1",
							Image:  "ghcr.io/acme/repo@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
						},
					},
					Result: &store.StoredInstanceResult{
						FinalState:    domain.InstanceStateInfraFailed,
						AgentRunID:    "agent_run_1",
						AgentExitCode: &agentExitCode,
						TestExitCode:  &testExitCode,
						TestStdoutRef: "instances/inst_1/test/test_stdout.txt",
						TestStderrRef: "instances/inst_1/test/test_stderr.txt",
					},
				},
			},
		},
	}

	out := m.renderRightPane(160, 20)
	plain := plainText(out)
	if !strings.Contains(plain, "inst: run_1-inst-0001") {
		t.Fatalf("expected instance id in right pane, got:\n%s", plain)
	}
	if !strings.Contains(plain, "case: case_1") {
		t.Fatalf("expected case id in right pane, got:\n%s", plain)
	}
	for _, unexpected := range []string{
		"image:",
		"agent_run_id:",
		"agent_exit_code:",
		"test_exit_code:",
		"test_stdout_ref:",
		"test_stderr_ref:",
	} {
		if strings.Contains(plain, unexpected) {
			t.Fatalf("did not expect removed field %q in right pane, got:\n%s", unexpected, plain)
		}
	}
}

func TestRenderHeaderIncludesPerformanceStats(t *testing.T) {
	start := time.Date(2026, 3, 4, 10, 0, 0, 0, time.UTC)
	end := start.Add(10 * time.Minute)
	m := &model{
		snapshotLoaded: true,
		snapshot: runnerapi.RunSnapshot{
			Run: store.Run{
				RunID:     "run_1",
				State:     domain.RunStateCompleted,
				CreatedAt: start,
				StartedAt: &start,
				EndedAt:   &end,
				Counts: domain.RunCounts{
					Succeeded:   8,
					TestFailed:  1,
					InfraFailed: 1,
					Canceled:    1,
				},
			},
		},
	}

	out := plainText(m.renderHeader(160))
	if !strings.Contains(out, "completed+test_fail") {
		t.Fatalf("expected warning badge for completed run with test failures, got:\n%s", out)
	}
	if !strings.Contains(out, "pass:8") {
		t.Fatalf("expected pass count in header, got:\n%s", out)
	}
	if !strings.Contains(out, "test_fail:1") {
		t.Fatalf("expected test failure count in header, got:\n%s", out)
	}
	if !strings.Contains(out, "infra_fail:1") {
		t.Fatalf("expected infra failure count in header, got:\n%s", out)
	}
	if !strings.Contains(out, "cancel:1") {
		t.Fatalf("expected cancel count in header, got:\n%s", out)
	}
	if !strings.Contains(out, "elapsed:10m") {
		t.Fatalf("expected elapsed stat in header, got:\n%s", out)
	}
	if !strings.Contains(out, "rate:1.10 inst/min") {
		t.Fatalf("expected completion rate stat in header, got:\n%s", out)
	}
	if !strings.Contains(out, "pass/fail:80.0%/20.0%") {
		t.Fatalf("expected pass/fail stat in header, got:\n%s", out)
	}
}

func TestRenderInstancesSummarySeparatesFailureTypes(t *testing.T) {
	m := &model{
		snapshotLoaded: true,
		snapshot: runnerapi.RunSnapshot{
			Run: store.Run{
				Counts: domain.RunCounts{
					Succeeded:   8,
					TestFailed:  1,
					InfraFailed: 2,
				},
			},
			Instances: []runnerapi.InstanceSnapshot{
				{
					Instance: store.Instance{
						Ordinal: 0,
						State:   domain.InstanceStateSucceeded,
						Case: runbundle.Case{
							CaseID: "case_1",
						},
					},
				},
			},
		},
	}

	out := plainText(m.renderInstances(100, 8))
	if !strings.Contains(out, "8 pass") {
		t.Fatalf("expected pass count in instance summary, got:\n%s", out)
	}
	if !strings.Contains(out, "1 test_fail") {
		t.Fatalf("expected separate test failure count in instance summary, got:\n%s", out)
	}
	if !strings.Contains(out, "2 infra_fail") {
		t.Fatalf("expected separate infra failure count in instance summary, got:\n%s", out)
	}
	if strings.Contains(out, "3 fail") {
		t.Fatalf("did not expect aggregated fail count in instance summary, got:\n%s", out)
	}
}

func TestRenderRightPaneShowsSimplifiedStates(t *testing.T) {
	m := &model{
		snapshotLoaded:  true,
		selectedState:   simplifiedStateProvisioningAgent,
		autoStateSelect: false,
		logFollowTail:   false,
		snapshot: runnerapi.RunSnapshot{
			Instances: []runnerapi.InstanceSnapshot{{
				Instance: store.Instance{
					InstanceID: "run_1-inst-0001",
					Ordinal:    0,
					State:      domain.InstanceStateAgentConfiguring,
					Case: runbundle.Case{
						CaseID: "case_1",
					},
				},
			}},
		},
	}
	m.setLogContent("line one\nline two\n")

	out := plainText(m.renderRightPane(120, 24))
	// Breadcrumb uses short labels
	for _, label := range []string{
		"Pending",
		"Building",
		"Provisioning",
		"Running Agent",
		"Testing",
	} {
		if !strings.Contains(out, label) {
			t.Fatalf("expected state tab breadcrumb to include %q, got:\n%s", label, out)
		}
	}
	for _, label := range []string{"Succeeded", "Test failed", "Infra failed", "Canceled"} {
		if strings.Contains(out, label) {
			t.Fatalf("did not expect unreached terminal state %q in right pane, got:\n%s", label, out)
		}
	}
	if strings.Contains(out, "agent_configuring") {
		t.Fatalf("did not expect exact internal state in right pane, got:\n%s", out)
	}
	if !strings.Contains(out, "case: case_1") {
		t.Fatalf("expected retained case field in right pane, got:\n%s", out)
	}
	if strings.Contains(out, "Provisioning agent") {
		t.Fatalf("did not expect selected-state detail line in right pane, got:\n%s", out)
	}
}

func TestRenderRightPaneShowsOnlyReachedTerminalState(t *testing.T) {
	m := &model{
		snapshotLoaded:  true,
		selectedState:   simplifiedStateInfraFailed,
		autoStateSelect: true,
		snapshot: runnerapi.RunSnapshot{
			Instances: []runnerapi.InstanceSnapshot{{
				Instance: store.Instance{
					InstanceID: "run_1-inst-0001",
					Ordinal:    0,
					State:      domain.InstanceStateInfraFailed,
				},
			}},
		},
	}

	out := plainText(m.renderRightPane(120, 24))
	// "Infra" short label appears in breadcrumb, "Infra failed" full label in log header
	if !strings.Contains(out, "Infra") {
		t.Fatalf("expected current terminal state in right pane, got:\n%s", out)
	}
	for _, label := range []string{"Succeeded", "Test failed", "Canceled"} {
		if strings.Contains(out, label) {
			t.Fatalf("did not expect other terminal state %q in right pane, got:\n%s", label, out)
		}
	}
}

func TestRenderInstancesScrollsWithSelection(t *testing.T) {
	instances := make([]runnerapi.InstanceSnapshot, 0, 30)
	for i := 0; i < 30; i++ {
		instances = append(instances, runnerapi.InstanceSnapshot{
			Instance: store.Instance{
				Ordinal: i,
				State:   domain.InstanceStatePending,
				Case: runbundle.Case{
					CaseID: fmt.Sprintf("case_%02d", i),
				},
			},
		})
	}

	m := &model{
		snapshotLoaded: true,
		selectedIdx:    20,
		snapshot: runnerapi.RunSnapshot{
			Instances: instances,
		},
	}

	out := m.renderInstances(100, 6)
	plain := plainText(out)
	if !strings.Contains(plain, "case_20") {
		t.Fatalf("expected selected case to be visible, got:\n%s", plain)
	}
	if strings.Contains(plain, "case_00") {
		t.Fatalf("did not expect first case once list is scrolled, got:\n%s", plain)
	}
}

func TestRenderInstancesUsesAvailablePanelWidthForCaseID(t *testing.T) {
	caseID := "case_id_abcdefghijklmnopqrstuvwxyz_0123456789"
	m := &model{
		snapshotLoaded: true,
		snapshot: runnerapi.RunSnapshot{
			Instances: []runnerapi.InstanceSnapshot{
				{
					Instance: store.Instance{
						Ordinal: 0,
						State:   domain.InstanceStatePending,
						Case: runbundle.Case{
							CaseID: caseID,
						},
					},
				},
			},
		},
	}

	out := m.renderInstances(60, 8)
	plain := plainText(out)
	if !strings.Contains(plain, caseID[:24]) {
		t.Fatalf("expected case id to use most available row width, got:\n%s", plain)
	}
	lines := strings.Split(plain, "\n")
	if len(lines) < 3 {
		t.Fatalf("expected at least one instance row, got:\n%s", plain)
	}
	if got := len([]rune(lines[2])); got != 60 {
		t.Fatalf("expected instance row width 60, got %d (%q)", got, lines[2])
	}
}

func TestRenderInstancesIncludesStateColumn(t *testing.T) {
	m := &model{
		snapshotLoaded: true,
		snapshot: runnerapi.RunSnapshot{
			Instances: []runnerapi.InstanceSnapshot{
				{
					Instance: store.Instance{
						Ordinal: 7,
						State:   domain.InstanceStateAgentRunning,
						Case: runbundle.Case{
							CaseID: "case_with_state",
						},
					},
				},
			},
		},
	}

	out := m.renderInstances(70, 8)
	plain := plainText(out)
	if !strings.Contains(plain, "Running Agent") {
		t.Fatalf("expected instance state column in row, got:\n%s", plain)
	}
	if strings.Contains(plain, "agent_running") {
		t.Fatalf("did not expect exact internal state in row, got:\n%s", plain)
	}
	if !strings.Contains(plain, "case_with_state") {
		t.Fatalf("expected case id in row, got:\n%s", plain)
	}
}

func TestRenderInstancesIncludesRetryCounter(t *testing.T) {
	m := &model{
		snapshotLoaded: true,
		snapshot: runnerapi.RunSnapshot{
			Run: store.Run{
				Bundle: runbundle.Bundle{
					ResolvedSnapshot: runbundle.ResolvedSnapshot{
						Execution: runbundle.Execution{RetryCount: 2},
					},
				},
			},
			Instances: []runnerapi.InstanceSnapshot{
				{
					Instance: store.Instance{
						Ordinal: 7,
						State:   domain.InstanceStateAgentRunning,
						Case: runbundle.Case{
							CaseID: "case_with_retry",
						},
					},
					Events: []store.InstanceEvent{
						{Source: "reaper"},
						{Source: "retry"},
					},
				},
			},
		},
	}

	out := m.renderInstances(70, 8)
	plain := plainText(out)
	assertLinesWithinWidth(t, out, 70)
	if !strings.Contains(plain, "r1/2") {
		t.Fatalf("expected retry counter in instance row, got:\n%s", plain)
	}
	if !strings.Contains(plain, "case_with_retry") {
		t.Fatalf("expected case id in row with retry counter, got:\n%s", plain)
	}
}

func TestRenderInstancesHidesRetryCounterBeforeFirstRetry(t *testing.T) {
	m := &model{
		snapshotLoaded: true,
		snapshot: runnerapi.RunSnapshot{
			Run: store.Run{
				Bundle: runbundle.Bundle{
					ResolvedSnapshot: runbundle.ResolvedSnapshot{
						Execution: runbundle.Execution{RetryCount: 2},
					},
				},
			},
			Instances: []runnerapi.InstanceSnapshot{
				{
					Instance: store.Instance{
						Ordinal: 7,
						State:   domain.InstanceStateAgentRunning,
						Case: runbundle.Case{
							CaseID: "case_without_retry",
						},
					},
				},
			},
		},
	}

	out := m.renderInstances(70, 8)
	plain := plainText(out)
	if strings.Contains(plain, "r0/2") {
		t.Fatalf("did not expect retry counter before first retry, got:\n%s", plain)
	}
}

func TestRenderRightPaneUsesSimplifiedStateLabels(t *testing.T) {
	m := &model{
		snapshotLoaded: true,
		selectedState:  simplifiedStateInfraFailed,
		snapshot: runnerapi.RunSnapshot{
			Instances: []runnerapi.InstanceSnapshot{{
				Instance: store.Instance{
					InstanceID: "inst_1",
					State:      domain.InstanceStateInfraFailed,
					Case: runbundle.Case{
						CaseID: "case_1",
						Image:  "ghcr.io/acme/repo:latest",
					},
				},
				Result: &store.StoredInstanceResult{
					FinalState: domain.InstanceStateInfraFailed,
				},
			}},
		},
	}

	out := plainText(m.renderRightPane(120, 20))
	if !strings.Contains(out, "Infra") {
		t.Fatalf("expected terminal breadcrumb label, got:\n%s", out)
	}
	if strings.Contains(out, "Infra failed") {
		t.Fatalf("did not expect removed selected-state detail line, got:\n%s", out)
	}
	if strings.Contains(out, "infra_failed") {
		t.Fatalf("did not expect exact internal state in right pane, got:\n%s", out)
	}
}

func TestRenderRightPaneIncludesRetryCounter(t *testing.T) {
	m := &model{
		snapshotLoaded: true,
		selectedState:  simplifiedStateRunningAgent,
		snapshot: runnerapi.RunSnapshot{
			Run: store.Run{
				Bundle: runbundle.Bundle{
					ResolvedSnapshot: runbundle.ResolvedSnapshot{
						Execution: runbundle.Execution{RetryCount: 2},
					},
				},
			},
			Instances: []runnerapi.InstanceSnapshot{{
				Instance: store.Instance{
					InstanceID: "inst_1",
					State:      domain.InstanceStateAgentRunning,
					Case: runbundle.Case{
						CaseID: "case_1",
					},
				},
				Events: []store.InstanceEvent{
					{Source: "retry"},
				},
			}},
		},
	}

	out := plainText(m.renderRightPane(120, 20))
	assertLinesWithinWidth(t, out, 120)
	if !strings.Contains(out, "retry: 1/2") {
		t.Fatalf("expected retry counter in right pane, got:\n%s", out)
	}
}

func TestRenderRightPaneHidesRetryCounterBeforeFirstRetry(t *testing.T) {
	m := &model{
		snapshotLoaded: true,
		selectedState:  simplifiedStateRunningAgent,
		snapshot: runnerapi.RunSnapshot{
			Run: store.Run{
				Bundle: runbundle.Bundle{
					ResolvedSnapshot: runbundle.ResolvedSnapshot{
						Execution: runbundle.Execution{RetryCount: 2},
					},
				},
			},
			Instances: []runnerapi.InstanceSnapshot{{
				Instance: store.Instance{
					InstanceID: "inst_1",
					State:      domain.InstanceStateAgentRunning,
					Case: runbundle.Case{
						CaseID: "case_1",
					},
				},
			}},
		},
	}

	out := plainText(m.renderRightPane(120, 20))
	if strings.Contains(out, "retry: 0/2") {
		t.Fatalf("did not expect retry counter before first retry, got:\n%s", out)
	}
}

func TestRenderRightPaneWrapsHeaderToPaneWidth(t *testing.T) {
	m := &model{
		snapshotLoaded:  true,
		selectedState:   simplifiedStatePending,
		autoStateSelect: false,
		snapshot: runnerapi.RunSnapshot{
			Instances: []runnerapi.InstanceSnapshot{{
				Instance: store.Instance{
					InstanceID: "instance_with_a_long_identifier",
					Ordinal:    0,
					State:      domain.InstanceStatePending,
					Case: runbundle.Case{
						CaseID: "case_with_a_long_identifier",
					},
				},
			}},
		},
	}

	out := m.renderRightPane(20, 12)
	assertLinesWithinWidth(t, out, 20)
}

func TestRenderRightPaneRespectsPaneHeight(t *testing.T) {
	m := &model{
		snapshotLoaded:  true,
		selectedState:   simplifiedStatePending,
		autoStateSelect: false,
		snapshot: runnerapi.RunSnapshot{
			Instances: []runnerapi.InstanceSnapshot{{
				Instance: store.Instance{
					InstanceID: "instance_with_a_long_identifier",
					Ordinal:    0,
					State:      domain.InstanceStatePending,
					Case: runbundle.Case{
						CaseID: "case_with_a_long_identifier",
					},
				},
			}},
		},
	}

	out := m.renderRightPane(20, 6)
	assertHeightWithinLimit(t, out, 6)
}

func TestRenderBodyUsesPaneGapBetweenBorders(t *testing.T) {
	m := &model{
		snapshotLoaded: true,
		selectedState:  simplifiedStatePending,
		snapshot: runnerapi.RunSnapshot{
			Instances: []runnerapi.InstanceSnapshot{{
				Instance: store.Instance{
					InstanceID: "inst_1",
					Ordinal:    0,
					State:      domain.InstanceStatePending,
					Case: runbundle.Case{
						CaseID: "case_1",
					},
				},
			}},
		},
	}

	out := plainText(m.renderBody(m.computeScreenLayout()))
	lines := strings.Split(out, "\n")
	if len(lines) == 0 {
		t.Fatal("expected rendered body output")
	}
	if !strings.Contains(lines[0], "╮  ╭") {
		t.Fatalf("expected top borders to be separated by a two-space pane gap, got:\n%s", lines[0])
	}
}

func TestScreenLayoutBodyStartsImmediatelyAfterHeader(t *testing.T) {
	m := &model{
		snapshotLoaded: true,
		snapshot: runnerapi.RunSnapshot{
			Run: store.Run{RunID: "run_1"},
			Instances: []runnerapi.InstanceSnapshot{{
				Instance: store.Instance{
					InstanceID: "inst_1",
					Ordinal:    0,
					State:      domain.InstanceStatePending,
					Case:       runbundle.Case{CaseID: "case_1"},
				},
			}},
		},
	}

	layout := m.computeScreenLayout()
	headerHeight := lipgloss.Height(m.renderHeader(layout.Width))
	if layout.BodyRect.Y != headerHeight {
		t.Fatalf("expected body to start immediately after header at row %d, got %d", headerHeight, layout.BodyRect.Y)
	}
}

func TestRightPaneUpDownScrollLogs(t *testing.T) {
	m := &model{
		focusedPane:   paneRight,
		selectedState: simplifiedStateTestingAgent,
		logFollowTail: false,
		snapshot: runnerapi.RunSnapshot{
			Instances: []runnerapi.InstanceSnapshot{{
				Instance: store.Instance{
					InstanceID: "inst_1",
					State:      domain.InstanceStateTesting,
				},
			}},
		},
	}

	m.setLogContent(strings.Repeat("line\n", 50))
	_ = m.renderSelectedStateLogs(120, 5)
	if m.logViewport.YOffset != 0 {
		t.Fatalf("expected initial log viewport offset 0, got %d", m.logViewport.YOffset)
	}

	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if m.logViewport.YOffset == 0 {
		t.Fatalf("expected log viewport to scroll after down arrow")
	}

	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if m.logViewport.YOffset != 0 {
		t.Fatalf("expected log viewport to scroll back to top after up arrow, got %d", m.logViewport.YOffset)
	}
}

func TestTabKeySwitchesPaneFocus(t *testing.T) {
	m := &model{focusedPane: paneLeft}

	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if m.focusedPane != paneRight {
		t.Fatalf("expected right pane after tab, got %d", m.focusedPane)
	}

	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if m.focusedPane != paneLeft {
		t.Fatalf("expected left pane after second tab, got %d", m.focusedPane)
	}
}

func TestMouseClickLeftPaneFocusesLeft(t *testing.T) {
	m := &model{
		focusedPane:    paneRight,
		snapshotLoaded: true,
		snapshot: runnerapi.RunSnapshot{
			Instances: []runnerapi.InstanceSnapshot{{Instance: store.Instance{InstanceID: "inst_1"}}},
		},
	}

	layout := m.computeScreenLayout()
	x, y := layout.LeftPane.Outer.center()
	_, _ = m.Update(tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	if m.focusedPane != paneLeft {
		t.Fatalf("expected left pane to take focus, got %d", m.focusedPane)
	}
}

func TestMouseClickRightPaneFocusesRight(t *testing.T) {
	m := &model{
		focusedPane:    paneLeft,
		snapshotLoaded: true,
		snapshot: runnerapi.RunSnapshot{
			Instances: []runnerapi.InstanceSnapshot{{Instance: store.Instance{InstanceID: "inst_1"}}},
		},
	}

	layout := m.computeScreenLayout()
	x, y := layout.RightPane.Pane.Outer.center()
	_, _ = m.Update(tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	if m.focusedPane != paneRight {
		t.Fatalf("expected right pane to take focus, got %d", m.focusedPane)
	}
}

func TestUpDownNavigateInstancesInLeftPane(t *testing.T) {
	m := &model{
		focusedPane: paneLeft,
		snapshot: runnerapi.RunSnapshot{
			Instances: []runnerapi.InstanceSnapshot{
				{Instance: store.Instance{InstanceID: "inst_1"}},
				{Instance: store.Instance{InstanceID: "inst_2"}},
			},
		},
	}

	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if m.selectedIdx != 1 {
		t.Fatalf("expected instance 1 after down in left pane, got %d", m.selectedIdx)
	}
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if m.selectedIdx != 0 {
		t.Fatalf("expected instance 0 after up in left pane, got %d", m.selectedIdx)
	}
}

func TestMouseClickInstanceRowSelectsInstance(t *testing.T) {
	m := &model{
		snapshotLoaded: true,
		snapshot: runnerapi.RunSnapshot{
			Instances: []runnerapi.InstanceSnapshot{
				{Instance: store.Instance{InstanceID: "inst_1"}},
				{Instance: store.Instance{InstanceID: "inst_2"}},
			},
		},
	}

	layout := m.computeScreenLayout()
	if len(layout.InstanceRows) < 2 {
		t.Fatalf("expected at least 2 visible instance rows, got %d", len(layout.InstanceRows))
	}
	x, y := layout.InstanceRows[1].Rect.center()
	_, _ = m.Update(tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	if m.focusedPane != paneLeft {
		t.Fatalf("expected left pane focus after instance click, got %d", m.focusedPane)
	}
	if m.selectedIdx != 1 {
		t.Fatalf("expected second instance after click, got %d", m.selectedIdx)
	}
	if !m.autoStateSelect {
		t.Fatalf("expected auto state selection to be enabled after instance click")
	}
}

func TestLeftRightNavigateStatesInRightPane(t *testing.T) {
	m := &model{
		focusedPane:   paneRight,
		selectedState: simplifiedStatePending,
		snapshot: runnerapi.RunSnapshot{
			Instances: []runnerapi.InstanceSnapshot{
				{Instance: store.Instance{InstanceID: "inst_1"}},
				{Instance: store.Instance{InstanceID: "inst_2"}},
			},
		},
	}

	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyRight})
	if m.selectedState != simplifiedStateBuildingImage {
		t.Fatalf("expected building image after right arrow in right pane, got %s", m.selectedState)
	}
	// Instance should not have moved
	if m.selectedIdx != 0 {
		t.Fatalf("expected instance idx to stay 0, got %d", m.selectedIdx)
	}
}

func TestMouseClickStateBoxSelectsState(t *testing.T) {
	m := &model{
		focusedPane:    paneLeft,
		snapshotLoaded: true,
		selectedState:  simplifiedStatePending,
		snapshot: runnerapi.RunSnapshot{
			Instances: []runnerapi.InstanceSnapshot{{
				Instance: store.Instance{
					InstanceID: "inst_1",
					State:      domain.InstanceStateTesting,
				},
			}},
		},
	}

	layout := m.computeScreenLayout()
	var target *stateBoxLayout
	for i := range layout.RightPane.StateBoxes {
		if layout.RightPane.StateBoxes[i].State == simplifiedStateTestingAgent {
			target = &layout.RightPane.StateBoxes[i]
			break
		}
	}
	if target == nil {
		t.Fatalf("expected testing state box to be visible")
	}
	x, y := target.Rect.center()
	_, _ = m.Update(tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	if m.focusedPane != paneRight {
		t.Fatalf("expected right pane focus after state click, got %d", m.focusedPane)
	}
	if m.selectedState != simplifiedStateTestingAgent {
		t.Fatalf("expected testing state selected after click, got %s", m.selectedState)
	}
	if m.autoStateSelect {
		t.Fatalf("expected manual state selection to disable auto state selection")
	}
}

func TestRenderStateBreadcrumb(t *testing.T) {
	m := &model{
		selectedState: simplifiedStateRunningAgent,
		now:           func() time.Time { return time.Date(2026, 4, 7, 12, 1, 50, 0, time.UTC) },
		snapshot: runnerapi.RunSnapshot{
			Instances: []runnerapi.InstanceSnapshot{{
				Instance: store.Instance{
					InstanceID: "inst_1",
					State:      domain.InstanceStateAgentRunning,
				},
				Events: []store.InstanceEvent{
					{ToState: domain.InstanceStateProvisioning, CreatedAt: time.Date(2026, 4, 7, 12, 1, 5, 0, time.UTC)},
					{ToState: domain.InstanceStateAgentRunning, CreatedAt: time.Date(2026, 4, 7, 12, 1, 20, 0, time.UTC)},
				},
			}},
		},
	}

	out := plainText(m.renderStateBreadcrumb(120, simplifiedStateRunningAgent))
	// Completed states should have checkmarks
	if !strings.Contains(out, "Pending") {
		t.Fatalf("expected Pending in breadcrumb, got:\n%s", out)
	}
	if !strings.Contains(out, "Building") {
		t.Fatalf("expected Building in breadcrumb, got:\n%s", out)
	}
	if !strings.Contains(out, "Running Agent") {
		t.Fatalf("expected Running Agent in breadcrumb, got:\n%s", out)
	}
	if !strings.Contains(out, "15s") {
		t.Fatalf("expected provisioning duration in breadcrumb, got:\n%s", out)
	}
	if !strings.Contains(out, "30s") {
		t.Fatalf("expected running duration in breadcrumb, got:\n%s", out)
	}
	// Arrow separators
	if !strings.Contains(out, "→") {
		t.Fatalf("expected arrow separators in breadcrumb, got:\n%s", out)
	}
}

func TestStateCycleKeys(t *testing.T) {
	m := &model{
		focusedPane:   paneRight,
		selectedState: simplifiedStatePending,
	}
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyRight})
	if m.selectedState != simplifiedStateBuildingImage {
		t.Fatalf("expected building image after right arrow in right pane, got %s", m.selectedState)
	}
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	if m.selectedState != simplifiedStatePending {
		t.Fatalf("expected pending after left arrow in right pane, got %s", m.selectedState)
	}
}

func TestStateCycleSkipsUnreachedTerminalStates(t *testing.T) {
	m := &model{
		focusedPane:   paneRight,
		selectedState: simplifiedStateTestingAgent,
		snapshot: runnerapi.RunSnapshot{
			Instances: []runnerapi.InstanceSnapshot{{
				Instance: store.Instance{
					InstanceID: "inst_1",
					State:      domain.InstanceStateTesting,
				},
			}},
		},
	}

	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyRight})
	if m.selectedState != simplifiedStatePending {
		t.Fatalf("expected cycle to wrap to pending, got %s", m.selectedState)
	}
}

func TestStateSelectionDefaultsToCurrentSimplifiedState(t *testing.T) {
	m := &model{
		autoStateSelect: true,
		snapshot: runnerapi.RunSnapshot{
			Instances: []runnerapi.InstanceSnapshot{{
				Instance: store.Instance{
					InstanceID: "inst_1",
					State:      domain.InstanceStateImageBuilding,
				},
			}},
		},
	}
	_ = m.maybeLoadSelectedLog()
	if got := m.selectedState; got != simplifiedStateBuildingImage {
		t.Fatalf("expected building image state, got %s", got)
	}
}

func TestStateSelectionMapsProvisioningToProvisioningState(t *testing.T) {
	m := &model{
		autoStateSelect: true,
		snapshot: runnerapi.RunSnapshot{
			Instances: []runnerapi.InstanceSnapshot{{
				Instance: store.Instance{
					InstanceID: "inst_1",
					State:      domain.InstanceStateProvisioning,
				},
			}},
		},
	}
	_ = m.maybeLoadSelectedLog()
	if got := m.selectedState; got != simplifiedStateProvisioningAgent {
		t.Fatalf("expected provisioning state, got %s", got)
	}
}

func TestStateSelectionResetsOnInstanceChange(t *testing.T) {
	m := &model{
		focusedPane:     paneLeft,
		selectedState:   simplifiedStatePending,
		autoStateSelect: false,
		snapshot: runnerapi.RunSnapshot{
			Instances: []runnerapi.InstanceSnapshot{
				{Instance: store.Instance{InstanceID: "inst_1", State: domain.InstanceStatePending}},
				{Instance: store.Instance{InstanceID: "inst_2", State: domain.InstanceStateAgentRunning}},
			},
		},
	}

	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if m.selectedState != simplifiedStateRunningAgent {
		t.Fatalf("expected running agent after instance change, got %s", m.selectedState)
	}
}

func TestStateTabUsesPlaceholderForTerminalStates(t *testing.T) {
	m := &model{
		autoStateSelect: true,
		snapshot: runnerapi.RunSnapshot{
			Instances: []runnerapi.InstanceSnapshot{{
				Instance: store.Instance{
					InstanceID: "inst_1",
					State:      domain.InstanceStateSucceeded,
				},
			}},
		},
	}

	cmd := m.maybeLoadSelectedLog()
	if cmd != nil {
		t.Fatalf("did not expect load command for terminal state")
	}
	if m.selectedState != simplifiedStateSucceeded {
		t.Fatalf("expected succeeded selected state, got %s", m.selectedState)
	}
	if m.logText != "No logs for this state" {
		t.Fatalf("expected terminal placeholder, got %q", m.logText)
	}
}

func TestTerminalSnapshotQuitsWhenExitOnCompleteEnabled(t *testing.T) {
	m := newModel(context.Background(), Config{
		RunID:          "run_1",
		Source:         &fakeMissionSource{},
		ExitOnComplete: true,
	})

	next, cmd := m.Update(snapshotLoadedMsg{
		snapshot: runnerapi.RunSnapshot{
			Run: store.Run{
				RunID: "run_1",
				State: domain.RunStateCompleted,
			},
			Instances: []runnerapi.InstanceSnapshot{{
				Instance: store.Instance{
					InstanceID: "inst_1",
					State:      domain.InstanceStateSucceeded,
				},
			}},
		},
	})

	resolved, ok := next.(*model)
	if !ok {
		t.Fatalf("unexpected model type %T", next)
	}
	if !resolved.terminal {
		t.Fatalf("expected model to record terminal run state")
	}
	if cmd == nil {
		t.Fatalf("expected quit command")
	}
	msg := cmd()
	if _, ok := msg.(tea.QuitMsg); !ok {
		t.Fatalf("expected quit message, got %T", msg)
	}
}

func TestTerminalSnapshotDoesNotQuitByDefault(t *testing.T) {
	m := newModel(context.Background(), Config{
		RunID:  "run_1",
		Source: &fakeMissionSource{},
	})

	next, cmd := m.Update(snapshotLoadedMsg{
		snapshot: runnerapi.RunSnapshot{
			Run: store.Run{
				RunID: "run_1",
				State: domain.RunStateCompleted,
			},
			Instances: []runnerapi.InstanceSnapshot{{
				Instance: store.Instance{
					InstanceID: "inst_1",
					State:      domain.InstanceStateSucceeded,
				},
			}},
		},
	})

	resolved, ok := next.(*model)
	if !ok {
		t.Fatalf("unexpected model type %T", next)
	}
	if !resolved.terminal {
		t.Fatalf("expected model to record terminal run state")
	}
	if cmd != nil {
		t.Fatalf("did not expect quit command by default")
	}
}

func TestLogsLoadMovesViewportToBottom(t *testing.T) {
	inst := runnerapi.InstanceSnapshot{
		Instance: store.Instance{
			InstanceID: "inst_1",
			State:      domain.InstanceStateTesting,
		},
		Artifacts: []store.Artifact{{
			ArtifactID: "art_stdout",
			Role:       store.ArtifactRoleTestStdout,
			StoreKey:   "instances/inst_1/test/test_stdout.txt",
		}},
	}
	source := &fakeMissionSource{
		snapshot: runnerapi.RunSnapshot{
			Run:       store.Run{RunID: "run_1", State: domain.RunStateRunning},
			Instances: []runnerapi.InstanceSnapshot{inst},
		},
		text: ArtifactText{
			Text:      strings.Repeat("line\n", 200),
			Truncated: false,
		},
	}
	m := newModel(context.Background(), Config{
		RunID:            "run_1",
		Source:           source,
		PollInterval:     20 * time.Millisecond,
		TextPreviewLimit: DefaultTextPreviewLimit,
	})
	m.focusedPane = paneRight
	m.width = 120
	m.height = 40
	m.applySnapshot(source.snapshot)

	cmd := m.maybeLoadSelectedLog()
	if cmd == nil {
		t.Fatalf("expected log load cmd")
	}
	msg := cmd()
	_, _ = m.Update(msg)
	if m.logViewport.YOffset <= 0 {
		t.Fatalf("expected log viewport to jump to bottom, got offset %d", m.logViewport.YOffset)
	}
}

func TestLogsPreserveANSIAndUnicode(t *testing.T) {
	m := newModel(context.Background(), Config{
		RunID:            "run_1",
		Source:           &fakeMissionSource{},
		PollInterval:     20 * time.Millisecond,
		TextPreviewLimit: DefaultTextPreviewLimit,
	})
	m.logKey = "k1"
	msg := logLoadedMsg{
		key:    "k1",
		stream: mustLogStreamByID(t, logStreamTestOutput),
		content: ArtifactText{
			Text: "\x1b[31mred text 🚀\x1b[0m plain \x1b]8;;https://example.com\x07link\x1b]8;;\x07",
		},
	}
	_, _ = m.Update(msg)
	if !strings.Contains(m.logText, "\x1b[31mred text 🚀\x1b[0m") {
		t.Fatalf("expected color and emoji preserved, got %q", m.logText)
	}
	if !strings.Contains(m.logText, "\x1b]8;;https://example.com\x07link\x1b]8;;\x07") {
		t.Fatalf("expected OSC hyperlink sequence preserved, got %q", m.logText)
	}
	if !strings.Contains(m.logText, "🚀") {
		t.Fatalf("expected unicode glyph preserved, got %q", m.logText)
	}
}

func TestCombinedTestOutputOrdersStdoutBeforeStderr(t *testing.T) {
	source := &fakeMissionSource{
		texts: map[string]ArtifactText{
			"art_stdout": {Text: "out line\n"},
			"art_stderr": {Text: "err line\n"},
		},
	}
	content, status, err := loadCombinedLogContent(context.Background(), source, []logArtifactTarget{
		{
			Section: "stdout",
			Artifact: store.Artifact{
				ArtifactID: "art_stdout",
				Role:       store.ArtifactRoleTestStdout,
			},
		},
		{
			Section: "stderr",
			Artifact: store.Artifact{
				ArtifactID: "art_stderr",
				Role:       store.ArtifactRoleTestStderr,
			},
		},
	}, DefaultTextPreviewLimit)
	if err != nil {
		t.Fatalf("loadCombinedLogContent() error = %v", err)
	}
	if status != "" {
		t.Fatalf("expected empty status, got %q", status)
	}
	want := "=== stdout ===\nout line\n\n=== stderr ===\nerr line\n"
	if content.Text != want {
		t.Fatalf("unexpected combined content:\nwant:\n%s\ngot:\n%s", want, content.Text)
	}
}

func TestCombinedTestOutputAggregatesTruncationAndPartialErrors(t *testing.T) {
	source := &fakeMissionSource{
		texts: map[string]ArtifactText{
			"art_stdout": {Text: "out line\n", Truncated: true, Tail: true},
		},
		errs: map[string]error{
			"art_stderr": fmt.Errorf("boom"),
		},
	}
	content, status, err := loadCombinedLogContent(context.Background(), source, []logArtifactTarget{
		{
			Section: "stdout",
			Artifact: store.Artifact{
				ArtifactID: "art_stdout",
				Role:       store.ArtifactRoleTestStdout,
			},
		},
		{
			Section: "stderr",
			Artifact: store.Artifact{
				ArtifactID: "art_stderr",
				Role:       store.ArtifactRoleTestStderr,
			},
		},
	}, DefaultTextPreviewLimit)
	if err != nil {
		t.Fatalf("loadCombinedLogContent() error = %v", err)
	}
	if !content.Truncated {
		t.Fatalf("expected combined content to be marked truncated")
	}
	if !content.Tail {
		t.Fatalf("expected combined content to preserve tail preview mode")
	}
	if !strings.Contains(content.Text, "=== stdout (truncated) ===") {
		t.Fatalf("expected truncated stdout section, got %q", content.Text)
	}
	if !strings.Contains(status, "stderr failed to load: boom") {
		t.Fatalf("expected partial failure status, got %q", status)
	}
}

func TestDefaultStateSelectionUsesTestingAgentForTestingStates(t *testing.T) {
	for _, state := range []domain.InstanceState{
		domain.InstanceStateTesting,
		domain.InstanceStateCollecting,
		domain.InstanceStateSucceeded,
		domain.InstanceStateTestFailed,
	} {
		m := &model{
			autoStateSelect: true,
			snapshot: runnerapi.RunSnapshot{
				Instances: []runnerapi.InstanceSnapshot{{
					Instance: store.Instance{
						InstanceID: "inst_1",
						State:      state,
					},
				}},
			},
		}
		_ = m.maybeLoadSelectedLog()
		want := simplifiedStateTestingAgent
		if state == domain.InstanceStateSucceeded {
			want = simplifiedStateSucceeded
		}
		if state == domain.InstanceStateTestFailed {
			want = simplifiedStateTestFailed
		}
		if got := m.selectedState; got != want {
			t.Fatalf("state %s should select %s, got %s", state, want, got)
		}
	}
}

func TestParseStructuredLogRecordsParsesStepAndOutput(t *testing.T) {
	input := strings.Join([]string{
		`{"v":1,"kind":"output","ts":"2026-03-04T15:04:05Z","source":"docker","message":"pulling base image"}`,
		`{"v":1,"kind":"step","ts":"2026-03-04T15:04:06Z","step":"image.resolve","status":"completed","message":"resolved case image","details":{"image":"ghcr.io/acme/case:latest","digest":"sha256:123"}}`,
	}, "\n") + "\n"
	records, err := parseStructuredLogRecords(input, false)
	if err != nil {
		t.Fatalf("parseStructuredLogRecords() error = %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("expected 2 records, got %d", len(records))
	}
	if records[0].Kind != "output" || records[0].Source != "docker" {
		t.Fatalf("unexpected first record: %+v", records[0])
	}
	if records[1].Kind != "step" || records[1].Step != "image.resolve" {
		t.Fatalf("unexpected second record: %+v", records[1])
	}
	if records[1].Details["digest"] != "sha256:123" {
		t.Fatalf("expected digest detail, got %v", records[1].Details)
	}
}

func TestParseStructuredLogRecordsSanitizesStructuredFields(t *testing.T) {
	input := `{"v":1,"kind":"step","ts":"2026-03-04T15:04:06Z","step":"image\u001b[31m.resolve\u001b[0m\nnext","status":"com\rpleted","source":"dock\u001b]8;;https://example.com\u0007er\u001b]8;;\u0007","message":"pull\r\nbase\u001b[31m image\u001b[0m","details":{"image":"ghcr.io/\u001b[31macme\u001b[0m\ncase"}}` + "\n"

	records, err := parseStructuredLogRecords(input, false)
	if err != nil {
		t.Fatalf("parseStructuredLogRecords() error = %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}

	got := records[0]
	if got.Step != "image.resolve next" {
		t.Fatalf("expected sanitized step, got %q", got.Step)
	}
	if got.Status != "com pleted" {
		t.Fatalf("expected sanitized status, got %q", got.Status)
	}
	if got.Source != "docker" {
		t.Fatalf("expected sanitized source, got %q", got.Source)
	}
	if got.Message != "pull\nbase image" {
		t.Fatalf("expected sanitized message, got %q", got.Message)
	}
	if got.Details["image"] != "ghcr.io/acme case" {
		t.Fatalf("expected sanitized detail value, got %q", got.Details["image"])
	}
}

func TestRenderOneCardStepLayout(t *testing.T) {
	rec := structuredLogRecord{
		V:       structuredLogVersion,
		Kind:    "step",
		TS:      "2026-03-04T15:04:06Z",
		Step:    "image.resolve",
		Status:  "completed",
		Message: "resolved case image",
		Details: map[string]string{"image": "ghcr.io/acme/case:latest"},
	}
	card := renderOneCard(rec, 60)
	plain := stripANSISequences(card)
	if !strings.Contains(plain, "STEP") {
		t.Fatalf("expected STEP badge in card, got %q", plain)
	}
	if !strings.Contains(plain, "image.resolve") {
		t.Fatalf("expected step name in card, got %q", plain)
	}
	if !strings.Contains(plain, "completed") {
		t.Fatalf("expected status in card, got %q", plain)
	}
	if !strings.Contains(plain, "15:04:06") {
		t.Fatalf("expected timestamp in card, got %q", plain)
	}
	if !strings.Contains(plain, "resolved case image") {
		t.Fatalf("expected message in card, got %q", plain)
	}
	if !strings.Contains(plain, "image: ghcr.io/acme/case:latest") {
		t.Fatalf("expected detail in card, got %q", plain)
	}
}

func TestRenderOneCardOutputLayout(t *testing.T) {
	rec := structuredLogRecord{
		V:       structuredLogVersion,
		Kind:    "output",
		TS:      "2026-03-04T15:04:05Z",
		Source:  "docker",
		Message: "pulling base image",
	}
	card := renderOneCard(rec, 60)
	plain := stripANSISequences(card)
	if !strings.Contains(plain, "OUT") {
		t.Fatalf("expected OUT badge in card, got %q", plain)
	}
	if !strings.Contains(plain, "docker") {
		t.Fatalf("expected source name in card, got %q", plain)
	}
	if !strings.Contains(plain, "pulling base image") {
		t.Fatalf("expected message in card, got %q", plain)
	}
}

func TestRenderStructuredLogCardsWidth(t *testing.T) {
	records := []structuredLogRecord{
		{V: 1, Kind: "step", TS: "2026-03-04T15:04:06Z", Step: "test.step", Status: "completed", Message: "hello"},
		{V: 1, Kind: "output", TS: "2026-03-04T15:04:05Z", Source: "docker", Message: "world"},
	}
	width := 50
	out := renderStructuredLogCards(records, width)
	for i, line := range strings.Split(out, "\n") {
		if got := xansi.StringWidth(line); got > width {
			t.Fatalf("line %d exceeds width %d: %d chars (%q)", i+1, width, got, line)
		}
	}
}

func TestCardTimestampFormat(t *testing.T) {
	got := formatCardTimestamp("2026-03-04T15:04:06Z")
	if got != "15:04:06" {
		t.Fatalf("expected 15:04:06, got %q", got)
	}
	gotNano := formatCardTimestamp("2026-03-04T15:04:06.123456789Z")
	if gotNano != "15:04:06" {
		t.Fatalf("expected 15:04:06, got %q", gotNano)
	}
}

func TestParseStructuredLogRecordsRejectsInvalidLine(t *testing.T) {
	_, err := parseStructuredLogRecords("plain text line\n", false)
	if err == nil {
		t.Fatalf("expected strict structured parse failure")
	}
	if !strings.Contains(err.Error(), "structured log parse error") {
		t.Fatalf("unexpected parse error: %v", err)
	}
}

func TestParseStructuredLogRecordsDropsPartialTrailingRecordWhenTruncated(t *testing.T) {
	input := strings.Join([]string{
		`{"v":1,"kind":"output","ts":"2026-03-04T15:04:05Z","source":"docker","message":"complete line"}`,
		`{"v":1,"kind":"step","ts":"2026-03-04T15:04:06Z","step":"x"`,
	}, "\n")
	records, err := parseStructuredLogRecords(input, true)
	if err != nil {
		t.Fatalf("parseStructuredLogRecords() error = %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 record (partial dropped), got %d", len(records))
	}
	if records[0].Message != "complete line" {
		t.Fatalf("expected complete record to remain, got %+v", records[0])
	}
}

func TestStructuredLogLoadSetsCardRenderMode(t *testing.T) {
	inst := runnerapi.InstanceSnapshot{
		Instance: store.Instance{
			InstanceID: "inst_1",
			State:      domain.InstanceStateAgentConfiguring,
		},
		Artifacts: []store.Artifact{{
			ArtifactID: "art_control",
			Role:       store.ArtifactRoleAgentControl,
			StoreKey:   "instances/inst_1/run/agent_server_control.log",
		}},
	}
	source := &fakeMissionSource{
		snapshot: runnerapi.RunSnapshot{
			Run:       store.Run{RunID: "run_1", State: domain.RunStateRunning},
			Instances: []runnerapi.InstanceSnapshot{inst},
		},
		text: ArtifactText{
			Text: `{"v":1,"kind":"step","ts":"2026-03-04T15:04:06Z","step":"boot","status":"completed","message":"done"}` + "\n",
		},
	}
	m := newModel(context.Background(), Config{
		RunID:            "run_1",
		Source:           source,
		PollInterval:     20 * time.Millisecond,
		TextPreviewLimit: DefaultTextPreviewLimit,
	})
	m.width = 80
	m.height = 40
	m.applySnapshot(source.snapshot)

	cmd := m.maybeLoadSelectedLog()
	if cmd == nil {
		t.Fatalf("expected log load cmd")
	}
	_, _ = m.Update(cmd())

	if len(m.logSections) != 3 {
		t.Fatalf("expected provisioning state to render 3 sections, got %d", len(m.logSections))
	}
	if m.logSections[2].Title != "Control" {
		t.Fatalf("expected control section last, got %q", m.logSections[2].Title)
	}
	if len(m.logSections[2].Records) != 1 {
		t.Fatalf("expected 1 parsed control record, got %d", len(m.logSections[2].Records))
	}
	if m.logSections[2].Records[0].Step != "boot" {
		t.Fatalf("expected step=boot, got %q", m.logSections[2].Records[0].Step)
	}
}

func TestRenderStructuredLogCardsNarrowWidth(t *testing.T) {
	records := []structuredLogRecord{
		{V: 1, Kind: "step", TS: "2026-03-04T15:04:06Z", Step: "x", Status: "ok", Message: "hi"},
	}
	width := 12
	out := renderStructuredLogCards(records, width)
	for i, line := range strings.Split(out, "\n") {
		if got := xansi.StringWidth(line); got > width {
			t.Fatalf("line %d exceeds width %d: %d chars (%q)", i+1, width, got, line)
		}
	}
}

func TestStructuredLogEmptyRecordsShowsPlaceholder(t *testing.T) {
	m := newModel(context.Background(), Config{
		RunID:            "run_1",
		Source:           &fakeMissionSource{},
		PollInterval:     20 * time.Millisecond,
		TextPreviewLimit: DefaultTextPreviewLimit,
	})
	m.logViewport.Width = 80
	m.logViewport.Height = 10
	m.setStructuredLogContent(nil)
	m.refreshLogViewportContent(80)
	content := m.logViewport.View()
	if !strings.Contains(plainText(content), "No log content") {
		t.Fatalf("expected 'No log content' placeholder, got %q", content)
	}
}

func TestLogLoadFailsForInvalidStructuredStreamPayload(t *testing.T) {
	m := newModel(context.Background(), Config{
		RunID:            "run_1",
		Source:           &fakeMissionSource{},
		PollInterval:     20 * time.Millisecond,
		TextPreviewLimit: DefaultTextPreviewLimit,
	})
	m.logKey = "k1"
	_, _ = m.Update(logLoadedMsg{
		key:    "k1",
		stream: mustLogStreamByID(t, logStreamAgentControl),
		content: ArtifactText{
			Text: "not-json\n",
		},
	})
	if !strings.Contains(m.logStatus, "failed to parse") {
		t.Fatalf("expected parse failure status, got %q", m.logStatus)
	}
}

func TestRuntimeStreamUsesStructuredRender(t *testing.T) {
	m := newModel(context.Background(), Config{
		RunID:            "run_1",
		Source:           &fakeMissionSource{},
		PollInterval:     20 * time.Millisecond,
		TextPreviewLimit: DefaultTextPreviewLimit,
	})
	m.logKey = "runtime_1"
	_, _ = m.Update(logLoadedMsg{
		key:    "runtime_1",
		stream: mustLogStreamByID(t, logStreamAgentRuntime),
		content: ArtifactText{
			Text: `{"v":1,"kind":"step","ts":"2026-03-04T15:04:06Z","step":"run.started","status":"info","source":"agent_server","message":"run.started"}` + "\n",
		},
	})
	if m.logActiveRender != logRenderStructuredKV {
		t.Fatalf("expected runtime stream to use structured render mode, got %d", m.logActiveRender)
	}
	if len(m.logRecords) != 1 {
		t.Fatalf("expected 1 runtime record, got %d", len(m.logRecords))
	}
	if m.logRecords[0].Status != "info" {
		t.Fatalf("expected info status, got %q", m.logRecords[0].Status)
	}
}

func TestRuntimeStreamRejectsPlaintextPayload(t *testing.T) {
	m := newModel(context.Background(), Config{
		RunID:            "run_1",
		Source:           &fakeMissionSource{},
		PollInterval:     20 * time.Millisecond,
		TextPreviewLimit: DefaultTextPreviewLimit,
	})
	m.logKey = "runtime_2"
	_, _ = m.Update(logLoadedMsg{
		key:    "runtime_2",
		stream: mustLogStreamByID(t, logStreamAgentRuntime),
		content: ArtifactText{
			Text: "level=info event=run.started run_id=abc\n",
		},
	})
	if !strings.Contains(m.logStatus, "failed to parse") {
		t.Fatalf("expected parse failure status, got %q", m.logStatus)
	}
}

func TestFormatJSONLTextPrettyPrintsEachLine(t *testing.T) {
	input := strings.Join([]string{
		`{"type":"assistant","payload":{"text":"hello"}}`,
		`["a",{"b":2}]`,
	}, "\n") + "\n"

	got, err := formatJSONLText(input, false)
	if err != nil {
		t.Fatalf("formatJSONLText() error = %v", err)
	}

	want := strings.Join([]string{
		"{",
		`  "type": "assistant",`,
		`  "payload": {`,
		`    "text": "hello"`,
		`  }`,
		"}",
		"",
		"[",
		`  "a",`,
		"  {",
		`    "b": 2`,
		"  }",
		"]",
	}, "\n")
	if got != want {
		t.Fatalf("unexpected pretty JSONL output:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestFormatJSONLTextDropsIncompleteTailWhenTruncated(t *testing.T) {
	input := "{\"ok\":true}\n{\"partial\":"

	got, err := formatJSONLText(input, true)
	if err != nil {
		t.Fatalf("formatJSONLText() error = %v", err)
	}

	want := strings.Join([]string{
		"{",
		`  "ok": true`,
		"}",
	}, "\n")
	if got != want {
		t.Fatalf("unexpected truncated JSONL output:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestFormatJSONLTextIgnoresLeadingNonJSONLinesWhenJSONExists(t *testing.T) {
	input := strings.Join([]string{
		"Performing one time database migration, may take a few minutes...",
		"sqlite-migration:done",
		"Database migration complete.",
		`{"event":"assistant","payload":{"text":"hello"}}`,
	}, "\n") + "\n"

	got, err := formatJSONLText(input, false)
	if err != nil {
		t.Fatalf("formatJSONLText() error = %v", err)
	}

	want := strings.Join([]string{
		"{",
		`  "event": "assistant",`,
		`  "payload": {`,
		`    "text": "hello"`,
		`  }`,
		"}",
	}, "\n")
	if got != want {
		t.Fatalf("unexpected mixed JSONL output:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestAgentPTYStreamUsesJSONLRender(t *testing.T) {
	m := newModel(context.Background(), Config{
		RunID:            "run_1",
		Source:           &fakeMissionSource{},
		PollInterval:     20 * time.Millisecond,
		TextPreviewLimit: DefaultTextPreviewLimit,
	})
	m.logKey = "pty_1"
	_, _ = m.Update(logLoadedMsg{
		key:    "pty_1",
		stream: mustLogStreamByID(t, logStreamAgentPTY),
		content: ArtifactText{
			Text: `{"event":"assistant","payload":{"text":"hello"}}` + "\n",
		},
	})

	if m.logActiveRender != logRenderJSONL {
		t.Fatalf("expected agent PTY stream to use JSONL render mode, got %d", m.logActiveRender)
	}
	if !strings.Contains(m.logText, `  "event": "assistant"`) {
		t.Fatalf("expected pretty-printed JSONL content, got:\n%s", m.logText)
	}
}

func TestAgentPTYStreamRejectsInvalidJSONL(t *testing.T) {
	m := newModel(context.Background(), Config{
		RunID:            "run_1",
		Source:           &fakeMissionSource{},
		PollInterval:     20 * time.Millisecond,
		TextPreviewLimit: DefaultTextPreviewLimit,
	})
	m.logKey = "pty_2"
	_, _ = m.Update(logLoadedMsg{
		key:    "pty_2",
		stream: mustLogStreamByID(t, logStreamAgentPTY),
		content: ArtifactText{
			Text: "not-json\n",
		},
	})

	if !strings.Contains(m.logStatus, "showing raw output") {
		t.Fatalf("expected parse failure status, got %q", m.logStatus)
	}
	if m.logActiveRender != logRenderRaw {
		t.Fatalf("expected raw fallback render mode, got %d", m.logActiveRender)
	}
	if m.logText != "not-json\n" {
		t.Fatalf("expected raw fallback content, got %q", m.logText)
	}
}

func TestStructuredLogsStripTerminalControlsWhileScrolled(t *testing.T) {
	m := &model{
		focusedPane:   paneRight,
		selectedState: simplifiedStateBuildingImage,
		logFollowTail: false,
		snapshot: runnerapi.RunSnapshot{
			Instances: []runnerapi.InstanceSnapshot{{
				Instance: store.Instance{
					InstanceID: "inst_1",
					State:      domain.InstanceStateImageBuilding,
				},
			}},
		},
	}

	input := strings.Join([]string{
		`{"v":1,"kind":"output","ts":"2026-03-04T15:04:05Z","source":"docker","message":"plain line"}`,
		`{"v":1,"kind":"output","ts":"2026-03-04T15:04:06Z","source":"docker","message":"https://docs.docker.com/go/buildx/\r\u001b[2K"}`,
		`{"v":1,"kind":"output","ts":"2026-03-04T15:04:07Z","source":"docker","message":"next line"}`,
	}, "\n") + "\n"

	records, err := parseStructuredLogRecords(input, false)
	if err != nil {
		t.Fatalf("parseStructuredLogRecords() error = %v", err)
	}

	m.setStructuredLogContent(records)
	_ = m.renderSelectedStateLogs(100, 6)
	m.logViewport.SetYOffset(1)
	out := m.renderSelectedStateLogs(100, 6)

	if strings.ContainsRune(out, '\r') {
		t.Fatalf("did not expect carriage returns in rendered structured logs: %q", out)
	}
	if strings.ContainsRune(out, '\x1b') {
		t.Fatalf("did not expect escape sequences in rendered structured logs: %q", out)
	}
}

func TestLogsPauseFollowWhenUserScrolls(t *testing.T) {
	inst := runnerapi.InstanceSnapshot{
		Instance: store.Instance{
			InstanceID: "inst_1",
			State:      domain.InstanceStateTesting,
		},
		Artifacts: []store.Artifact{{
			ArtifactID: "art_stdout",
			Role:       store.ArtifactRoleTestStdout,
			StoreKey:   "instances/inst_1/test/test_stdout.txt",
		}},
	}
	source := &fakeMissionSource{
		snapshot: runnerapi.RunSnapshot{
			Run:       store.Run{RunID: "run_1", State: domain.RunStateRunning},
			Instances: []runnerapi.InstanceSnapshot{inst},
		},
		text: ArtifactText{
			Text:      strings.Repeat("line\n", 220),
			Truncated: false,
		},
	}
	m := newModel(context.Background(), Config{
		RunID:            "run_1",
		Source:           source,
		PollInterval:     20 * time.Millisecond,
		TextPreviewLimit: DefaultTextPreviewLimit,
	})
	m.focusedPane = paneRight
	m.width = 120
	m.height = 40
	m.applySnapshot(source.snapshot)

	cmd := m.maybeLoadSelectedLog()
	if cmd == nil {
		t.Fatalf("expected log load cmd")
	}
	_, _ = m.Update(cmd())
	_ = m.renderSelectedStateLogs(100, 20)
	m.focusedPane = paneRight
	m.logViewport.GotoBottom()
	m.logFollowTail = true

	if !m.logFollowTail {
		t.Fatalf("expected follow mode enabled after initial load")
	}
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if m.logFollowTail {
		t.Fatalf("expected follow mode paused after manual scroll up")
	}
	offsetBefore := m.logViewport.YOffset
	_, _ = m.Update(logLoadedMsg{
		key: m.logKey,
		content: ArtifactText{
			Text: strings.Repeat("line\n", 260),
		},
	})
	if m.logViewport.YOffset != offsetBefore {
		t.Fatalf("expected viewport offset to remain stable while paused, before=%d after=%d", offsetBefore, m.logViewport.YOffset)
	}
	if m.logViewport.AtBottom() {
		t.Fatalf("expected viewport to remain off-bottom while follow is paused")
	}
}

func TestMouseWheelScrollsLogsOnlyInsideLogPane(t *testing.T) {
	m := &model{
		focusedPane:    paneRight,
		snapshotLoaded: true,
		selectedState:  simplifiedStateTestingAgent,
		snapshot: runnerapi.RunSnapshot{
			Instances: []runnerapi.InstanceSnapshot{{
				Instance: store.Instance{
					InstanceID: "inst_1",
					State:      domain.InstanceStateTesting,
				},
			}},
		},
	}

	m.setLogContent(strings.Repeat("line\n", 50))
	_ = m.View()
	layout := m.computeScreenLayout()
	if layout.RightPane.LogRect.isZero() {
		t.Fatalf("expected non-zero log rect")
	}

	logX, logY := layout.RightPane.LogRect.center()
	_, _ = m.Update(tea.MouseMsg{X: logX, Y: logY, Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress})
	if m.logViewport.YOffset == 0 {
		t.Fatalf("expected wheel in log pane to scroll viewport")
	}

	offsetAfterLogScroll := m.logViewport.YOffset
	leftX, leftY := layout.LeftPane.Outer.center()
	_, _ = m.Update(tea.MouseMsg{X: leftX, Y: leftY, Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress})
	if m.logViewport.YOffset != offsetAfterLogScroll {
		t.Fatalf("expected wheel outside log pane not to scroll viewport, before=%d after=%d", offsetAfterLogScroll, m.logViewport.YOffset)
	}
}

func TestLogsFollowResumesWhenUserReturnsBottom(t *testing.T) {
	inst := runnerapi.InstanceSnapshot{
		Instance: store.Instance{
			InstanceID: "inst_1",
			State:      domain.InstanceStateTesting,
		},
		Artifacts: []store.Artifact{{
			ArtifactID: "art_stdout",
			Role:       store.ArtifactRoleTestStdout,
			StoreKey:   "instances/inst_1/test/test_stdout.txt",
		}},
	}
	source := &fakeMissionSource{
		snapshot: runnerapi.RunSnapshot{
			Run:       store.Run{RunID: "run_1", State: domain.RunStateRunning},
			Instances: []runnerapi.InstanceSnapshot{inst},
		},
		text: ArtifactText{
			Text:      strings.Repeat("line\n", 220),
			Truncated: false,
		},
	}
	m := newModel(context.Background(), Config{
		RunID:            "run_1",
		Source:           source,
		PollInterval:     20 * time.Millisecond,
		TextPreviewLimit: DefaultTextPreviewLimit,
	})
	m.width = 120
	m.height = 40
	m.applySnapshot(source.snapshot)

	cmd := m.maybeLoadSelectedLog()
	if cmd == nil {
		t.Fatalf("expected log load cmd")
	}
	_, _ = m.Update(cmd())
	_ = m.renderSelectedStateLogs(100, 20)
	m.logViewport.GotoBottom()
	m.logFollowTail = true

	m.logViewport.GotoTop()
	m.logFollowTail = false
	m.logViewport.GotoBottom()
	m.logFollowTail = m.logViewport.AtBottom()
	if !m.logViewport.AtBottom() {
		t.Fatalf("expected viewport to return to bottom")
	}
	if !m.logFollowTail {
		t.Fatalf("expected follow mode to auto-resume at bottom")
	}
	offsetBefore := m.logViewport.YOffset
	_, _ = m.Update(logLoadedMsg{
		key: m.logKey,
		content: ArtifactText{
			Text: strings.Repeat("line\n", 260),
		},
	})
	if !m.logViewport.AtBottom() {
		t.Fatalf("expected viewport to stay at bottom when follow is active")
	}
	if m.logViewport.YOffset <= offsetBefore {
		t.Fatalf("expected viewport offset to advance while following tail, before=%d after=%d", offsetBefore, m.logViewport.YOffset)
	}
}

func TestRenderSelectedStateLogsWrapsLongLinesToPaneWidth(t *testing.T) {
	m := &model{
		selectedState: simplifiedStateTestingAgent,
		logText:       strings.Repeat("x", 120),
	}
	out := m.renderSelectedStateLogs(20, 10)
	plain := plainText(out)
	lines := strings.Split(plain, "\n")
	if len(lines) < 2 {
		t.Fatalf("expected header plus log body, got %q", plain)
	}
	for i, line := range lines[1:] {
		if got := xansi.StringWidth(line); got > 20 {
			t.Fatalf("line %d exceeds pane width: %d chars (%q)", i+2, got, line)
		}
	}
}

func TestRenderSelectedStateLogsPreservesANSIAndEmojiWhileWrapping(t *testing.T) {
	m := &model{
		selectedState: simplifiedStateTestingAgent,
		logText:       "\x1b[35m" + strings.Repeat("colored🚀text", 18) + "\x1b[0m",
	}
	out := m.renderSelectedStateLogs(24, 10)
	if !strings.Contains(m.logWrappedText, "\x1b[35m") {
		t.Fatalf("expected wrapped log buffer to keep ANSI color sequences")
	}
	plain := plainText(out)
	if !strings.Contains(plain, "🚀") {
		t.Fatalf("expected emoji preserved in wrapped output")
	}
	lines := strings.Split(plain, "\n")
	if len(lines) < 2 {
		t.Fatalf("expected header plus log body, got %q", plain)
	}
	for i, line := range lines[1:] {
		if got := xansi.StringWidth(line); got > 24 {
			t.Fatalf("line %d exceeds pane width: %d cells (%q)", i+2, got, line)
		}
	}
}
