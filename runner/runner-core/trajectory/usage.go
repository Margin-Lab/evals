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
	if traj.FinalMetrics != nil && traj.FinalMetrics.TotalPromptTokens != nil {
		return int64Ptr(*traj.FinalMetrics.TotalPromptTokens)
	}
	return sumStepMetrics(traj, func(m *Metrics) *int64 {
		if m == nil {
			return nil
		}
		return m.PromptTokens
	})
}

func totalCompletionTokens(traj Trajectory) *int64 {
	if traj.FinalMetrics != nil && traj.FinalMetrics.TotalCompletionTokens != nil {
		return int64Ptr(*traj.FinalMetrics.TotalCompletionTokens)
	}
	return sumStepMetrics(traj, func(m *Metrics) *int64 {
		if m == nil {
			return nil
		}
		return m.CompletionTokens
	})
}

func sumStepMetrics(traj Trajectory, pick func(*Metrics) *int64) *int64 {
	var total int64
	var found bool
	for _, step := range traj.Steps {
		value := pick(step.Metrics)
		if value == nil {
			continue
		}
		found = true
		total += *value
	}
	if !found {
		return nil
	}
	return int64Ptr(total)
}

func int64Ptr(v int64) *int64 {
	return &v
}
