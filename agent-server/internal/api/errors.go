package api

import (
	"net/http"

	"github.com/marginlab/margin-eval/agent-server/internal/apperr"
	"github.com/marginlab/margin-eval/agent-server/internal/state"
)

func staleAgentStateError(expectedName string, expectedState state.AgentState, actual state.AgentRecord) error {
	return apperr.NewConflict(apperr.CodeAgentStateStale, "agent state changed concurrently", map[string]any{
		"expected_name":  expectedName,
		"expected_state": expectedState,
		"actual_name":    agentName(actual),
		"actual_state":   actual.State,
	})
}

func agentName(agent state.AgentRecord) string {
	if agent.Definition == nil {
		return ""
	}
	return agent.Definition.Snapshot.Manifest.Name
}

func invalidMutationError(code, message string, err error) error {
	return apperr.NewBadRequest(code, message, map[string]any{"error": err.Error()})
}

func unavailableMutationError(code, message string, err error) error {
	return apperr.New(http.StatusServiceUnavailable, code, message, map[string]any{"error": err.Error()})
}

func internalMutationError(code, message string, err error) error {
	return apperr.NewInternal(code, message, map[string]any{"error": err.Error()})
}
