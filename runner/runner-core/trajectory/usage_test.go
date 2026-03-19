package trajectory

import "testing"

func TestExtractUsageMetricsPrefersFinalMetrics(t *testing.T) {
	promptTokens := int64(21)
	completionTokens := int64(8)

	got := ExtractUsageMetrics(Trajectory{
		Steps: []Step{
			{
				ToolCalls: []ToolCall{
					{ToolCallID: "call-1", FunctionName: "shell", Arguments: map[string]any{}},
					{ToolCallID: "call-2", FunctionName: "read", Arguments: map[string]any{}},
				},
				Metrics: &Metrics{
					PromptTokens:     int64Ptr(99),
					CompletionTokens: int64Ptr(77),
				},
			},
		},
		FinalMetrics: &FinalMetrics{
			TotalPromptTokens:     &promptTokens,
			TotalCompletionTokens: &completionTokens,
		},
	})

	if got == nil {
		t.Fatalf("expected usage metrics")
	}
	if got.InputTokens == nil || *got.InputTokens != promptTokens {
		t.Fatalf("unexpected input tokens: %#v", got.InputTokens)
	}
	if got.OutputTokens == nil || *got.OutputTokens != completionTokens {
		t.Fatalf("unexpected output tokens: %#v", got.OutputTokens)
	}
	if got.ToolCalls == nil || *got.ToolCalls != 2 {
		t.Fatalf("unexpected tool calls: %#v", got.ToolCalls)
	}
}

func TestExtractUsageMetricsFallsBackToStepMetrics(t *testing.T) {
	got := ExtractUsageMetrics(Trajectory{
		Steps: []Step{
			{
				Metrics: &Metrics{
					PromptTokens:     int64Ptr(5),
					CompletionTokens: int64Ptr(2),
				},
			},
			{
				ToolCalls: []ToolCall{
					{ToolCallID: "call-1", FunctionName: "shell", Arguments: map[string]any{}},
				},
				Metrics: &Metrics{
					PromptTokens:     int64Ptr(7),
					CompletionTokens: int64Ptr(3),
				},
			},
		},
	})

	if got == nil {
		t.Fatalf("expected usage metrics")
	}
	if got.InputTokens == nil || *got.InputTokens != 12 {
		t.Fatalf("unexpected input tokens: %#v", got.InputTokens)
	}
	if got.OutputTokens == nil || *got.OutputTokens != 5 {
		t.Fatalf("unexpected output tokens: %#v", got.OutputTokens)
	}
	if got.ToolCalls == nil || *got.ToolCalls != 1 {
		t.Fatalf("unexpected tool calls: %#v", got.ToolCalls)
	}
}

func TestExtractUsageMetricsKeepsKnownZeroToolCalls(t *testing.T) {
	got := ExtractUsageMetrics(Trajectory{
		Steps: []Step{
			{},
		},
	})

	if got == nil {
		t.Fatalf("expected usage metrics")
	}
	if got.InputTokens != nil {
		t.Fatalf("expected nil input tokens, got %#v", got.InputTokens)
	}
	if got.OutputTokens != nil {
		t.Fatalf("expected nil output tokens, got %#v", got.OutputTokens)
	}
	if got.ToolCalls == nil || *got.ToolCalls != 0 {
		t.Fatalf("unexpected tool calls: %#v", got.ToolCalls)
	}
}
