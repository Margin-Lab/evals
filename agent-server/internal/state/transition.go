package state

import (
	"fmt"

	"github.com/marginlab/margin-eval/agent-server/internal/apperr"
)

var runTransitions = map[RunState]map[RunState]struct{}{
	RunStateIdle: {
		RunStateIdle:     {},
		RunStateStarting: {},
	},
	RunStateStarting: {
		RunStateStarting: {},
		RunStateRunning:  {},
		RunStateExited:   {},
		RunStateIdle:     {},
	},
	RunStateRunning: {
		RunStateRunning:    {},
		RunStateCollecting: {},
	},
	RunStateCollecting: {
		RunStateCollecting: {},
		RunStateExited:     {},
	},
	RunStateExited: {
		RunStateExited: {},
		RunStateIdle:   {},
	},
}

func ValidateAgentStateTransition(from, to AgentState) error {
	if from == to {
		return nil
	}
	switch from {
	case AgentStateEmpty:
		if to == AgentStateDefinitionLoaded {
			return nil
		}
	case AgentStateDefinitionLoaded:
		if to == AgentStateDefinitionLoaded || to == AgentStateInstalled || to == AgentStateConfigured || to == AgentStateEmpty {
			return nil
		}
	case AgentStateInstalled:
		if to == AgentStateInstalled || to == AgentStateConfigured || to == AgentStateDefinitionLoaded || to == AgentStateEmpty {
			return nil
		}
	case AgentStateConfigured:
		if to == AgentStateConfigured || to == AgentStateInstalled || to == AgentStateDefinitionLoaded || to == AgentStateEmpty {
			return nil
		}
	}
	return fmt.Errorf("invalid agent state transition: %s -> %s", from, to)
}

func ValidateRunStateTransition(from, to RunState) error {
	allowed := runTransitions[from]
	if _, ok := allowed[to]; ok {
		return nil
	}
	return fmt.Errorf("invalid run state transition: %s -> %s", from, to)
}

func ValidateLoadDefinitionTransition(s ServerState) error {
	if IsRunActive(s.Run.State) {
		return apperr.NewConflict(apperr.CodeRunAlreadyActive, "a run is already active", map[string]any{
			"run_id": s.Run.RunID,
		})
	}
	if err := ValidateAgentStateTransition(s.Agent.State, AgentStateDefinitionLoaded); err != nil {
		return apperr.NewConflict(apperr.CodeInvalidAgentState, err.Error(), nil)
	}
	return nil
}

func ValidateInstallTransition(s ServerState) error {
	if IsRunActive(s.Run.State) {
		return apperr.NewConflict(apperr.CodeRunAlreadyActive, "a run is already active", map[string]any{
			"run_id": s.Run.RunID,
		})
	}
	if s.Agent.Definition == nil {
		return apperr.NewConflict(apperr.CodeAgentNotConfigured, "agent definition must be loaded before installation", nil)
	}
	if s.Agent.State != AgentStateDefinitionLoaded && s.Agent.State != AgentStateInstalled && s.Agent.State != AgentStateConfigured {
		return apperr.NewConflict(apperr.CodeInvalidAgentState, "agent definition must be loaded before installation", map[string]any{
			"agent_state": s.Agent.State,
		})
	}
	if err := ValidateAgentStateTransition(s.Agent.State, AgentStateInstalled); err != nil {
		return apperr.NewConflict(apperr.CodeInvalidAgentState, err.Error(), nil)
	}
	return nil
}

func ValidateConfigureTransition(s ServerState) error {
	if IsRunActive(s.Run.State) {
		return apperr.NewConflict(apperr.CodeRunAlreadyActive, "a run is already active", map[string]any{
			"run_id": s.Run.RunID,
		})
	}
	if s.Agent.Definition == nil {
		return apperr.NewConflict(apperr.CodeAgentNotConfigured, "agent definition must be loaded before configuration", nil)
	}
	if s.Agent.State != AgentStateDefinitionLoaded && s.Agent.State != AgentStateInstalled && s.Agent.State != AgentStateConfigured {
		return apperr.NewConflict(apperr.CodeInvalidAgentState, "agent definition must be loaded before configuration", map[string]any{
			"agent_state": s.Agent.State,
		})
	}
	if err := ValidateAgentStateTransition(s.Agent.State, AgentStateConfigured); err != nil {
		return apperr.NewConflict(apperr.CodeInvalidAgentState, err.Error(), nil)
	}
	return nil
}

func ValidateStartRunTransition(s ServerState) error {
	if IsRunActive(s.Run.State) {
		return apperr.NewConflict(apperr.CodeRunAlreadyActive, "a run is already active", map[string]any{
			"run_id": s.Run.RunID,
		})
	}
	if s.Run.State == RunStateExited {
		return apperr.NewConflict(apperr.CodeRunNotCleared, "previous run is exited and must be cleared", map[string]any{
			"run_id": s.Run.RunID,
		})
	}
	if s.Agent.Definition == nil || s.Agent.Install == nil || s.Agent.Config == nil || s.Agent.State != AgentStateConfigured {
		return apperr.NewConflict(apperr.CodeAgentNotConfigured, "agent must be loaded, installed, and configured before starting a run", nil)
	}
	if err := ValidateRunStateTransition(s.Run.State, RunStateStarting); err != nil {
		return apperr.NewConflict(apperr.CodeInvalidRunState, err.Error(), nil)
	}
	return nil
}

func ValidateRunClearTransition(s ServerState) error {
	if s.Run.State != RunStateExited {
		return apperr.NewConflict(apperr.CodeRunNotClearable, "Run can only be cleared from exited state", map[string]any{
			"run_state": s.Run.State,
		})
	}
	if err := ValidateRunStateTransition(s.Run.State, RunStateIdle); err != nil {
		return apperr.NewConflict(apperr.CodeInvalidRunState, err.Error(), nil)
	}
	return nil
}
