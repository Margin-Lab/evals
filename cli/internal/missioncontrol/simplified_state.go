package missioncontrol

import "github.com/marginlab/margin-eval/runner/runner-core/domain"

type simplifiedState string

const (
	simplifiedStatePending           simplifiedState = "pending"
	simplifiedStateBuildingImage     simplifiedState = "building_image"
	simplifiedStateProvisioningAgent simplifiedState = "provisioning_agent"
	simplifiedStateRunningAgent      simplifiedState = "running_agent"
	simplifiedStateApplyingOracle    simplifiedState = "applying_oracle"
	simplifiedStateTestingAgent      simplifiedState = "testing_agent"
	simplifiedStateSucceeded         simplifiedState = "succeeded"
	simplifiedStateTestFailed        simplifiedState = "test_failed"
	simplifiedStateInfraFailed       simplifiedState = "infra_failed"
	simplifiedStateCanceled          simplifiedState = "canceled"
)

type simplifiedStateSpec struct {
	ID          simplifiedState
	Label       string
	ShortLabel  string
	LogStreams  []logStream
	Placeholder string
}

var simplifiedStates = []simplifiedStateSpec{
	{
		ID:          simplifiedStatePending,
		Label:       "Pending",
		ShortLabel:  "Pending",
		Placeholder: "No logs for this state",
	},
	{
		ID:         simplifiedStateBuildingImage,
		Label:      "Building",
		ShortLabel: "Building",
		LogStreams: []logStream{logStreamDockerBuild},
	},
	{
		ID:         simplifiedStateProvisioningAgent,
		Label:      "Provisioning",
		ShortLabel: "Provisioning",
		LogStreams: []logStream{logStreamAgentBoot, logStreamAgentRuntime, logStreamAgentControl},
	},
	{
		ID:         simplifiedStateRunningAgent,
		Label:      "Running Agent",
		ShortLabel: "Running Agent",
		LogStreams: []logStream{logStreamAgentPTY},
	},
	{
		ID:         simplifiedStateApplyingOracle,
		Label:      "Applying Oracle",
		ShortLabel: "Oracle",
		LogStreams: []logStream{logStreamOracleOutput},
	},
	{
		ID:         simplifiedStateTestingAgent,
		Label:      "Testing",
		ShortLabel: "Testing",
		LogStreams: []logStream{logStreamTestOutput},
	},
	{
		ID:          simplifiedStateSucceeded,
		Label:       "Succeeded",
		ShortLabel:  "Pass",
		Placeholder: "No logs for this state",
	},
	{
		ID:          simplifiedStateTestFailed,
		Label:       "Test failed",
		ShortLabel:  "Fail",
		Placeholder: "No logs for this state",
	},
	{
		ID:          simplifiedStateInfraFailed,
		Label:       "Infra failed",
		ShortLabel:  "Infra",
		Placeholder: "No logs for this state",
	},
	{
		ID:          simplifiedStateCanceled,
		Label:       "Canceled",
		ShortLabel:  "Cancel",
		Placeholder: "No logs for this state",
	},
}

var alwaysVisibleSimplifiedStates = []simplifiedState{
	simplifiedStatePending,
	simplifiedStateBuildingImage,
	simplifiedStateProvisioningAgent,
	simplifiedStateRunningAgent,
	simplifiedStateApplyingOracle,
	simplifiedStateTestingAgent,
}

func simplifiedStateForInstanceState(state domain.InstanceState) simplifiedState {
	switch state {
	case domain.InstanceStatePending:
		return simplifiedStatePending
	case domain.InstanceStateImageBuilding:
		return simplifiedStateBuildingImage
	case domain.InstanceStateProvisioning:
		return simplifiedStateProvisioningAgent
	case domain.InstanceStateAgentServerInstalling,
		domain.InstanceStateBooting,
		domain.InstanceStateAgentInstalling,
		domain.InstanceStateAgentConfiguring:
		return simplifiedStateProvisioningAgent
	case domain.InstanceStateAgentRunning, domain.InstanceStateAgentCollecting:
		return simplifiedStateRunningAgent
	case domain.InstanceStateOracleApplying:
		return simplifiedStateApplyingOracle
	case domain.InstanceStateTesting, domain.InstanceStateCollecting:
		return simplifiedStateTestingAgent
	case domain.InstanceStateSucceeded:
		return simplifiedStateSucceeded
	case domain.InstanceStateTestFailed:
		return simplifiedStateTestFailed
	case domain.InstanceStateInfraFailed:
		return simplifiedStateInfraFailed
	case domain.InstanceStateCanceled:
		return simplifiedStateCanceled
	default:
		return simplifiedStatePending
	}
}

func simplifiedStateSpecByID(id simplifiedState) simplifiedStateSpec {
	for _, spec := range simplifiedStates {
		if spec.ID == id {
			return spec
		}
	}
	return simplifiedStates[0]
}

func simplifiedStateIndexByID(id simplifiedState) int {
	for idx := range simplifiedStates {
		if simplifiedStates[idx].ID == id {
			return idx
		}
	}
	return 0
}

func simplifiedStateLabelForInstanceState(state domain.InstanceState) string {
	return simplifiedStateSpecByID(simplifiedStateForInstanceState(state)).Label
}

func simplifiedStateIsTerminal(state simplifiedState) bool {
	switch state {
	case simplifiedStateSucceeded, simplifiedStateTestFailed, simplifiedStateInfraFailed, simplifiedStateCanceled:
		return true
	default:
		return false
	}
}

func visibleSimplifiedStates(current simplifiedState) []simplifiedStateSpec {
	visible := make([]simplifiedStateSpec, 0, len(alwaysVisibleSimplifiedStates)+1)
	for _, id := range alwaysVisibleSimplifiedStates {
		visible = append(visible, simplifiedStateSpecByID(id))
	}
	if simplifiedStateIsTerminal(current) {
		visible = append(visible, simplifiedStateSpecByID(current))
	}
	return visible
}
