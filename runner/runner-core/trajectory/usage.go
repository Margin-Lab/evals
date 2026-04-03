package trajectory

import "github.com/marginlab/margin-eval/runner/runner-core/usage"

func ExtractUsageMetrics(traj Trajectory) *usage.Metrics {
	inputTokens := totalPromptTokens(traj)
	outputTokens := totalCompletionTokens(traj)
	toolCalls := int64(0)
	for _, step := range traj.Steps {
		toolCalls += int64(len(step.ToolCalls))
	}

	return usage.Clone(&usage.Metrics{
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
		ToolCalls:    int64Ptr(toolCalls),
	})
}

func totalPromptTokens(traj Trajectory) *int64 {
	if traj.FinalMetrics == nil || traj.FinalMetrics.TotalPromptTokens == nil {
		return nil
	}
	return int64Ptr(*traj.FinalMetrics.TotalPromptTokens)
}

func totalCompletionTokens(traj Trajectory) *int64 {
	if traj.FinalMetrics == nil || traj.FinalMetrics.TotalCompletionTokens == nil {
		return nil
	}
	return int64Ptr(*traj.FinalMetrics.TotalCompletionTokens)
}

func int64Ptr(v int64) *int64 {
	return &v
}
