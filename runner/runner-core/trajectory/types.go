package trajectory

import (
	"bytes"
	"encoding/json"
	"fmt"
)

const CurrentSchemaVersion = "ATIF-v1.6"

var supportedSchemaVersions = map[string]struct{}{
	"ATIF-v1.0": {},
	"ATIF-v1.1": {},
	"ATIF-v1.2": {},
	"ATIF-v1.3": {},
	"ATIF-v1.4": {},
	"ATIF-v1.5": {},
	"ATIF-v1.6": {},
}

type Trajectory struct {
	SchemaVersion          string         `json:"schema_version"`
	SessionID              string         `json:"session_id"`
	Agent                  Agent          `json:"agent"`
	Steps                  []Step         `json:"steps"`
	Notes                  string         `json:"notes,omitempty"`
	FinalMetrics           *FinalMetrics  `json:"final_metrics,omitempty"`
	ContinuedTrajectoryRef string         `json:"continued_trajectory_ref,omitempty"`
	Extra                  map[string]any `json:"extra,omitempty"`
}

type Agent struct {
	Name            string           `json:"name"`
	Version         string           `json:"version"`
	ModelName       string           `json:"model_name,omitempty"`
	ToolDefinitions []map[string]any `json:"tool_definitions,omitempty"`
	Extra           map[string]any   `json:"extra,omitempty"`
}

type Step struct {
	StepID           int            `json:"step_id"`
	Timestamp        string         `json:"timestamp,omitempty"`
	Source           string         `json:"source"`
	ModelName        string         `json:"model_name,omitempty"`
	ReasoningEffort  any            `json:"reasoning_effort,omitempty"`
	Message          TextOrParts    `json:"message"`
	ReasoningContent string         `json:"reasoning_content,omitempty"`
	ToolCalls        []ToolCall     `json:"tool_calls,omitempty"`
	Observation      *Observation   `json:"observation,omitempty"`
	Metrics          *Metrics       `json:"metrics,omitempty"`
	IsCopiedContext  *bool          `json:"is_copied_context,omitempty"`
	Extra            map[string]any `json:"extra,omitempty"`
}

type ToolCall struct {
	ToolCallID   string         `json:"tool_call_id"`
	FunctionName string         `json:"function_name"`
	Arguments    map[string]any `json:"arguments"`
}

type Observation struct {
	Results []ObservationResult `json:"results"`
}

type ObservationResult struct {
	SourceCallID          string                  `json:"source_call_id,omitempty"`
	Content               TextOrParts             `json:"content,omitempty"`
	SubagentTrajectoryRef []SubagentTrajectoryRef `json:"subagent_trajectory_ref,omitempty"`
}

type SubagentTrajectoryRef struct {
	SessionID      string `json:"session_id"`
	TrajectoryPath string `json:"trajectory_path,omitempty"`
	Summary        string `json:"summary,omitempty"`
}

type Metrics struct {
	PromptTokens       *int64         `json:"prompt_tokens,omitempty"`
	CompletionTokens   *int64         `json:"completion_tokens,omitempty"`
	CachedTokens       *int64         `json:"cached_tokens,omitempty"`
	CostUSD            *float64       `json:"cost_usd,omitempty"`
	PromptTokenIDs     []int64        `json:"prompt_token_ids,omitempty"`
	CompletionTokenIDs []int64        `json:"completion_token_ids,omitempty"`
	Logprobs           []float64      `json:"logprobs,omitempty"`
	Extra              map[string]any `json:"extra,omitempty"`
}

type FinalMetrics struct {
	TotalPromptTokens     *int64         `json:"total_prompt_tokens,omitempty"`
	TotalCompletionTokens *int64         `json:"total_completion_tokens,omitempty"`
	TotalCachedTokens     *int64         `json:"total_cached_tokens,omitempty"`
	TotalCostUSD          *float64       `json:"total_cost_usd,omitempty"`
	TotalSteps            *int64         `json:"total_steps,omitempty"`
	Extra                 map[string]any `json:"extra,omitempty"`
}

type ContentPart struct {
	Type   string       `json:"type"`
	Text   string       `json:"text,omitempty"`
	Source *ImageSource `json:"source,omitempty"`
}

type ImageSource struct {
	MediaType string `json:"media_type"`
	Path      string `json:"path"`
}

// TextOrParts represents content encoded as either plain text or multimodal parts.
type TextOrParts struct {
	text  *string
	parts []ContentPart
	isSet bool
}

func (v *TextOrParts) UnmarshalJSON(data []byte) error {
	v.isSet = true
	trimmed := bytes.TrimSpace(data)
	if bytes.Equal(trimmed, []byte("null")) {
		v.text = nil
		v.parts = nil
		return nil
	}

	var text string
	if err := json.Unmarshal(trimmed, &text); err == nil {
		v.text = &text
		v.parts = nil
		return nil
	}

	var parts []ContentPart
	if err := json.Unmarshal(trimmed, &parts); err == nil {
		v.text = nil
		v.parts = append([]ContentPart(nil), parts...)
		return nil
	}

	return fmt.Errorf("content must be a string, an array of content parts, or null")
}

func (v TextOrParts) MarshalJSON() ([]byte, error) {
	if v.text != nil {
		return json.Marshal(*v.text)
	}
	if v.parts != nil {
		return json.Marshal(v.parts)
	}
	return []byte("null"), nil
}

func (v TextOrParts) IsSet() bool {
	return v.isSet
}

func (v TextOrParts) IsNull() bool {
	return v.isSet && v.text == nil && v.parts == nil
}

func (v TextOrParts) Text() (string, bool) {
	if v.text == nil {
		return "", false
	}
	return *v.text, true
}

func (v TextOrParts) Parts() []ContentPart {
	return append([]ContentPart(nil), v.parts...)
}
