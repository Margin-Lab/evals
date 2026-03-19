package usage

type Metrics struct {
	InputTokens  *int64 `json:"input_tokens,omitempty"`
	OutputTokens *int64 `json:"output_tokens,omitempty"`
	ToolCalls    *int64 `json:"tool_calls,omitempty"`
}

func Clone(in *Metrics) *Metrics {
	if in == nil {
		return nil
	}
	out := &Metrics{}
	if in.InputTokens != nil {
		v := *in.InputTokens
		out.InputTokens = &v
	}
	if in.OutputTokens != nil {
		v := *in.OutputTokens
		out.OutputTokens = &v
	}
	if in.ToolCalls != nil {
		v := *in.ToolCalls
		out.ToolCalls = &v
	}
	if out.InputTokens == nil && out.OutputTokens == nil && out.ToolCalls == nil {
		return nil
	}
	return out
}

func Known(in *Metrics) bool {
	return in != nil && (in.InputTokens != nil || in.OutputTokens != nil || in.ToolCalls != nil)
}
