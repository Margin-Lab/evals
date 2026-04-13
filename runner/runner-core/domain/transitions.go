package domain

func ValidInstanceTransition(from, to InstanceState) bool {
	if from == to {
		return true
	}
	switch from {
	case InstanceStatePending:
		return to == InstanceStateProvisioning || to == InstanceStateCanceled || to == InstanceStateTestFailed || to == InstanceStateInfraFailed
	case InstanceStateProvisioning:
		return to == InstanceStateImageBuilding || to == InstanceStateAgentServerInstalling || to == InstanceStateCanceled || to == InstanceStateTestFailed || to == InstanceStateInfraFailed
	case InstanceStateImageBuilding:
		return to == InstanceStateAgentServerInstalling || to == InstanceStateCanceled || to == InstanceStateTestFailed || to == InstanceStateInfraFailed
	case InstanceStateAgentServerInstalling:
		return to == InstanceStateBooting || to == InstanceStateCanceled || to == InstanceStateTestFailed || to == InstanceStateInfraFailed
	case InstanceStateBooting:
		return to == InstanceStateAgentConfiguring || to == InstanceStateCanceled || to == InstanceStateTestFailed || to == InstanceStateInfraFailed
	case InstanceStateAgentConfiguring:
		return to == InstanceStateAgentInstalling || to == InstanceStateCanceled || to == InstanceStateTestFailed || to == InstanceStateInfraFailed
	case InstanceStateAgentInstalling:
		return to == InstanceStateAgentRunning || to == InstanceStateCanceled || to == InstanceStateTestFailed || to == InstanceStateInfraFailed
	case InstanceStateAgentRunning:
		return to == InstanceStateAgentCollecting || to == InstanceStateCanceled || to == InstanceStateTestFailed || to == InstanceStateInfraFailed
	case InstanceStateAgentCollecting:
		return to == InstanceStateTesting || to == InstanceStateCanceled || to == InstanceStateTestFailed || to == InstanceStateInfraFailed || to == InstanceStateCollecting
	case InstanceStateTesting:
		return to == InstanceStateCollecting || to == InstanceStateCanceled || to == InstanceStateTestFailed || to == InstanceStateInfraFailed
	case InstanceStateCollecting:
		return to == InstanceStateSucceeded || to == InstanceStateTestFailed || to == InstanceStateInfraFailed || to == InstanceStateCanceled
	default:
		return false
	}
}

func NextRunState(current RunState, counts RunCounts, cancelRequested bool) RunState {
	if current.IsTerminal() {
		return current
	}
	if cancelRequested {
		if counts.Running == 0 && counts.Pending == 0 {
			return RunStateCanceled
		}
		return RunStateCanceling
	}
	if counts.Running > 0 {
		return RunStateRunning
	}
	if counts.Pending > 0 {
		if current == RunStateQueued {
			return RunStateQueued
		}
		return RunStateRunning
	}
	if counts.Canceled > 0 {
		return RunStateCanceled
	}
	if counts.InfraFailed > 0 {
		return RunStateFailed
	}
	return RunStateCompleted
}

type RunCounts struct {
	Pending     int `json:"pending"`
	Running     int `json:"running"`
	Succeeded   int `json:"succeeded"`
	TestFailed  int `json:"test_failed"`
	InfraFailed int `json:"infra_failed"`
	Canceled    int `json:"canceled"`
}

func (c RunCounts) Failed() int {
	return c.TestFailed + c.InfraFailed
}
