package domain

import "testing"

func TestValidInstanceTransitionGoldenPath(t *testing.T) {
	steps := []InstanceState{
		InstanceStatePending,
		InstanceStateProvisioning,
		InstanceStateImageBuilding,
		InstanceStateAgentServerInstalling,
		InstanceStateBooting,
		InstanceStateAgentConfiguring,
		InstanceStateAgentInstalling,
		InstanceStateAgentRunning,
		InstanceStateAgentCollecting,
		InstanceStateTesting,
		InstanceStateCollecting,
		InstanceStateSucceeded,
	}
	for i := 0; i < len(steps)-1; i++ {
		if !ValidInstanceTransition(steps[i], steps[i+1]) {
			t.Fatalf("expected transition %s -> %s to be valid", steps[i], steps[i+1])
		}
	}
}

func TestValidInstanceTransitionRejectsInvalidSkip(t *testing.T) {
	if ValidInstanceTransition(InstanceStatePending, InstanceStateTesting) {
		t.Fatalf("expected transition to be invalid")
	}
}

func TestValidInstanceTransitionProvisioningCanSkipImageBuild(t *testing.T) {
	if !ValidInstanceTransition(InstanceStateProvisioning, InstanceStateAgentServerInstalling) {
		t.Fatalf("expected provisioning -> agent_server_installing to be valid")
	}
}

func TestValidInstanceTransitionBootingRequiresConfigBeforeInstall(t *testing.T) {
	if ValidInstanceTransition(InstanceStateBooting, InstanceStateAgentInstalling) {
		t.Fatalf("expected booting -> agent_installing to be invalid after hard cutover")
	}
	if !ValidInstanceTransition(InstanceStateBooting, InstanceStateAgentConfiguring) {
		t.Fatalf("expected booting -> agent_configuring to be valid")
	}
}

func TestValidInstanceTransitionDryRunCanSkipTesting(t *testing.T) {
	if !ValidInstanceTransition(InstanceStateAgentCollecting, InstanceStateCollecting) {
		t.Fatalf("expected agent_collecting -> collecting_artifacts to be valid for dry-run")
	}
}

func TestNextRunStateTransitions(t *testing.T) {
	if got := NextRunState(RunStateQueued, RunCounts{Pending: 1}, false); got != RunStateQueued {
		t.Fatalf("expected queued, got %s", got)
	}
	if got := NextRunState(RunStateRunning, RunCounts{}, false); got != RunStateCompleted {
		t.Fatalf("expected completed, got %s", got)
	}
	if got := NextRunState(RunStateRunning, RunCounts{InfraFailed: 1}, false); got != RunStateFailed {
		t.Fatalf("expected failed, got %s", got)
	}
	if got := NextRunState(RunStateRunning, RunCounts{Pending: 1}, true); got != RunStateCanceling {
		t.Fatalf("expected canceling, got %s", got)
	}
	if got := NextRunState(RunStateCanceling, RunCounts{}, true); got != RunStateCanceled {
		t.Fatalf("expected canceled, got %s", got)
	}
	if got := NextRunState(RunStateRunning, RunCounts{TestFailed: 1}, false); got != RunStateCompleted {
		t.Fatalf("expected completed for test_failed-only run, got %s", got)
	}
	if got := NextRunState(RunStateRunning, RunCounts{TestFailed: 1, Canceled: 1}, false); got != RunStateCanceled {
		t.Fatalf("expected canceled precedence over completed, got %s", got)
	}
}
