package trajectory

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

func Decode(raw []byte) (Trajectory, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()

	var traj Trajectory
	if err := decoder.Decode(&traj); err != nil {
		return Trajectory{}, err
	}
	if err := decoder.Decode(&struct{}{}); err != nil && !errors.Is(err, io.EOF) {
		return Trajectory{}, fmt.Errorf("unexpected trailing data: %w", err)
	}
	if err := traj.Validate(); err != nil {
		return Trajectory{}, err
	}
	return traj, nil
}

func Validate(raw []byte) error {
	_, err := Decode(raw)
	return err
}

func (t Trajectory) Validate() error {
	if _, ok := supportedSchemaVersions[strings.TrimSpace(t.SchemaVersion)]; !ok {
		return fmt.Errorf("schema_version must be a supported ATIF version")
	}
	if strings.TrimSpace(t.SessionID) == "" {
		return fmt.Errorf("session_id is required")
	}
	if strings.TrimSpace(t.Agent.Name) == "" {
		return fmt.Errorf("agent.name is required")
	}
	if strings.TrimSpace(t.Agent.Version) == "" {
		return fmt.Errorf("agent.version is required")
	}
	if len(t.Steps) == 0 {
		return fmt.Errorf("steps must contain at least one step")
	}
	for idx, step := range t.Steps {
		if step.StepID != idx+1 {
			return fmt.Errorf("steps[%d].step_id must equal %d", idx, idx+1)
		}
		if err := validateStep(step); err != nil {
			return fmt.Errorf("steps[%d]: %w", idx, err)
		}
	}
	return nil
}

func validateStep(step Step) error {
	switch step.Source {
	case "system", "user", "agent":
	default:
		return fmt.Errorf("source must be one of system, user, agent")
	}
	if strings.TrimSpace(step.Timestamp) != "" {
		if _, err := time.Parse(time.RFC3339Nano, strings.ReplaceAll(step.Timestamp, "Z", "Z")); err != nil {
			return fmt.Errorf("timestamp must be RFC3339/ISO 8601")
		}
	}
	if !step.Message.IsSet() || step.Message.IsNull() {
		return fmt.Errorf("message is required")
	}
	if err := validateTextOrParts(step.Message, true); err != nil {
		return fmt.Errorf("message: %w", err)
	}
	if step.Source != "agent" {
		if strings.TrimSpace(step.ModelName) != "" {
			return fmt.Errorf("model_name is only valid for agent steps")
		}
		if step.ReasoningEffort != nil {
			return fmt.Errorf("reasoning_effort is only valid for agent steps")
		}
		if strings.TrimSpace(step.ReasoningContent) != "" {
			return fmt.Errorf("reasoning_content is only valid for agent steps")
		}
		if step.ToolCalls != nil {
			return fmt.Errorf("tool_calls is only valid for agent steps")
		}
		if step.Metrics != nil {
			return fmt.Errorf("metrics is only valid for agent steps")
		}
	}
	if step.ReasoningEffort != nil {
		switch step.ReasoningEffort.(type) {
		case string, float64:
		default:
			return fmt.Errorf("reasoning_effort must be a string or number")
		}
	}
	toolCallIDs := map[string]struct{}{}
	for idx, call := range step.ToolCalls {
		if strings.TrimSpace(call.ToolCallID) == "" {
			return fmt.Errorf("tool_calls[%d].tool_call_id is required", idx)
		}
		if strings.TrimSpace(call.FunctionName) == "" {
			return fmt.Errorf("tool_calls[%d].function_name is required", idx)
		}
		if call.Arguments == nil {
			return fmt.Errorf("tool_calls[%d].arguments is required", idx)
		}
		if _, exists := toolCallIDs[call.ToolCallID]; exists {
			return fmt.Errorf("tool_calls[%d].tool_call_id must be unique within a step", idx)
		}
		toolCallIDs[call.ToolCallID] = struct{}{}
	}
	if step.Observation != nil {
		if len(step.Observation.Results) == 0 {
			return fmt.Errorf("observation.results must contain at least one result")
		}
		for idx, result := range step.Observation.Results {
			if strings.TrimSpace(result.SourceCallID) != "" {
				if _, ok := toolCallIDs[result.SourceCallID]; !ok {
					return fmt.Errorf("observation.results[%d].source_call_id must reference a tool_call_id in the same step", idx)
				}
			}
			if result.Content.IsSet() {
				if err := validateTextOrParts(result.Content, false); err != nil {
					return fmt.Errorf("observation.results[%d].content: %w", idx, err)
				}
			}
			for refIdx, ref := range result.SubagentTrajectoryRef {
				if strings.TrimSpace(ref.SessionID) == "" {
					return fmt.Errorf("observation.results[%d].subagent_trajectory_ref[%d].session_id is required", idx, refIdx)
				}
			}
		}
	}
	return nil
}

func validateTextOrParts(value TextOrParts, requireValue bool) error {
	if !value.IsSet() {
		if requireValue {
			return fmt.Errorf("value is required")
		}
		return nil
	}
	if text, ok := value.Text(); ok {
		_ = text
		return nil
	}
	parts := value.Parts()
	if parts == nil {
		if requireValue {
			return fmt.Errorf("value is required")
		}
		return nil
	}
	if len(parts) == 0 {
		return fmt.Errorf("content part arrays must not be empty")
	}
	for idx, part := range parts {
		switch part.Type {
		case "text":
			if part.Text == "" {
				return fmt.Errorf("parts[%d].text is required when type=text", idx)
			}
			if part.Source != nil {
				return fmt.Errorf("parts[%d].source is not allowed when type=text", idx)
			}
		case "image":
			if part.Source == nil {
				return fmt.Errorf("parts[%d].source is required when type=image", idx)
			}
			if strings.TrimSpace(part.Text) != "" {
				return fmt.Errorf("parts[%d].text is not allowed when type=image", idx)
			}
			switch part.Source.MediaType {
			case "image/jpeg", "image/png", "image/gif", "image/webp":
			default:
				return fmt.Errorf("parts[%d].source.media_type must be a supported image mime type", idx)
			}
			if strings.TrimSpace(part.Source.Path) == "" {
				return fmt.Errorf("parts[%d].source.path is required", idx)
			}
		default:
			return fmt.Errorf("parts[%d].type must be text or image", idx)
		}
	}
	return nil
}
