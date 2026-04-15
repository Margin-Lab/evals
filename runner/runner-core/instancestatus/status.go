package instancestatus

import (
	"context"
	"errors"

	"github.com/marginlab/margin-eval/runner/runner-core/domain"
	"github.com/marginlab/margin-eval/runner/runner-core/store"
)

const (
	InfraFailureReasonAgentFailed       = "agent_failed"
	InfraFailureReasonExecutorError     = "executor_error"
	InfraFailureReasonInstanceTimeout   = "instance_timeout"
	InfraFailureReasonInvalidFinalState = "invalid_final_state"
	InfraFailureReasonOracleFailed      = "oracle_failed"
	InfraFailureReasonUnknownFailure    = "unknown_failure"
)

func NormalizeExecutionResult(result store.InstanceResult, err error) store.InstanceResult {
	if err != nil {
		if !result.FinalState.IsTerminal() {
			result.FinalState = terminalStateForErr(err)
		}
		if result.ErrorCode == "" && result.FinalState.IsInfraFailure() {
			result.ErrorCode = errorCodeForErr(err)
		}
		if result.ErrorMessage == "" && result.FinalState.IsInfraFailure() {
			result.ErrorMessage = err.Error()
		}
	}
	if !result.FinalState.IsTerminal() {
		result.FinalState = domain.InstanceStateInfraFailed
		if result.ErrorCode == "" {
			result.ErrorCode = "INVALID_FINAL_STATE"
		}
		if result.ErrorMessage == "" {
			result.ErrorMessage = "executor returned non-terminal final state"
		}
	}
	return result
}

func terminalStateForErr(err error) domain.InstanceState {
	switch {
	case errors.Is(err, context.Canceled):
		return domain.InstanceStateCanceled
	default:
		return domain.InstanceStateInfraFailed
	}
}

func errorCodeForErr(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "INSTANCE_TIMEOUT"
	default:
		return "EXECUTOR_ERROR"
	}
}

func InfraFailureReason(result store.StoredInstanceResult) *string {
	if !result.FinalState.IsInfraFailure() {
		return nil
	}
	switch result.ErrorCode {
	case "EXECUTOR_ERROR":
		return strPtr(InfraFailureReasonExecutorError)
	case "INSTANCE_TIMEOUT":
		return strPtr(InfraFailureReasonInstanceTimeout)
	case "INVALID_FINAL_STATE":
		return strPtr(InfraFailureReasonInvalidFinalState)
	case "ORACLE_APPLY_FAILED", "ORACLE_TIMEOUT":
		return strPtr(InfraFailureReasonOracleFailed)
	}
	if result.AgentExitCode != nil && *result.AgentExitCode != 0 {
		return strPtr(InfraFailureReasonAgentFailed)
	}
	if result.ErrorCode != "" {
		return strPtr(InfraFailureReasonUnknownFailure)
	}
	return strPtr(InfraFailureReasonUnknownFailure)
}

func strPtr(v string) *string {
	return &v
}
