package domain

type RunState string

const (
	RunStateQueued    RunState = "queued"
	RunStateRunning   RunState = "running"
	RunStateCanceling RunState = "canceling"
	RunStateCompleted RunState = "completed"
	RunStateFailed    RunState = "failed"
	RunStateCanceled  RunState = "canceled"
)

func (s RunState) IsTerminal() bool {
	switch s {
	case RunStateCompleted, RunStateFailed, RunStateCanceled:
		return true
	default:
		return false
	}
}

type InstanceState string

const (
	InstanceStatePending               InstanceState = "pending"
	InstanceStateProvisioning          InstanceState = "provisioning"
	InstanceStateImageBuilding         InstanceState = "image_building"
	InstanceStateAgentServerInstalling InstanceState = "agent_server_installing"
	InstanceStateBooting               InstanceState = "booting"
	InstanceStateAgentInstalling       InstanceState = "agent_installing"
	InstanceStateAgentConfiguring      InstanceState = "agent_configuring"
	InstanceStateAgentRunning          InstanceState = "agent_running"
	InstanceStateAgentCollecting       InstanceState = "agent_collecting"
	InstanceStateTesting               InstanceState = "testing"
	InstanceStateCollecting            InstanceState = "collecting_artifacts"
	InstanceStateSucceeded             InstanceState = "succeeded"
	InstanceStateTestFailed            InstanceState = "test_failed"
	InstanceStateInfraFailed           InstanceState = "infra_failed"
	InstanceStateCanceled              InstanceState = "canceled"
)

func (s InstanceState) IsTerminal() bool {
	switch s {
	case InstanceStateSucceeded, InstanceStateTestFailed, InstanceStateInfraFailed, InstanceStateCanceled:
		return true
	default:
		return false
	}
}

func (s InstanceState) IsFailure() bool {
	switch s {
	case InstanceStateTestFailed, InstanceStateInfraFailed:
		return true
	default:
		return false
	}
}

func (s InstanceState) IsTestFailure() bool {
	return s == InstanceStateTestFailed
}

func (s InstanceState) IsInfraFailure() bool {
	return s == InstanceStateInfraFailed
}

func (s InstanceState) CountsAsRunFailure() bool {
	return s.IsFailure()
}
