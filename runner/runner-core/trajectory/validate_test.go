package trajectory

import "testing"

func TestValidateAcceptsMinimalTrajectory(t *testing.T) {
	raw := []byte(`{
  "schema_version": "ATIF-v1.6",
  "session_id": "sess-1",
  "agent": {"name": "codex", "version": "1.0.0"},
  "steps": [
    {"step_id": 1, "source": "user", "message": "Hello"},
    {"step_id": 2, "source": "agent", "message": "Hi"}
  ]
}`)
	if err := Validate(raw); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestValidateRejectsOutOfOrderStepIDs(t *testing.T) {
	raw := []byte(`{
  "schema_version": "ATIF-v1.6",
  "session_id": "sess-1",
  "agent": {"name": "codex", "version": "1.0.0"},
  "steps": [
    {"step_id": 1, "source": "user", "message": "Hello"},
    {"step_id": 3, "source": "agent", "message": "Hi"}
  ]
}`)
	if err := Validate(raw); err == nil {
		t.Fatalf("Validate() unexpectedly succeeded")
	}
}

func TestValidateRejectsBrokenToolResultReference(t *testing.T) {
	raw := []byte(`{
  "schema_version": "ATIF-v1.6",
  "session_id": "sess-1",
  "agent": {"name": "codex", "version": "1.0.0"},
  "steps": [
    {
      "step_id": 1,
      "source": "agent",
      "message": "Using tool",
      "tool_calls": [{"tool_call_id": "call-1", "function_name": "Read", "arguments": {}}],
      "observation": {"results": [{"source_call_id": "call-2", "content": "oops"}]}
    }
  ]
}`)
	if err := Validate(raw); err == nil {
		t.Fatalf("Validate() unexpectedly succeeded")
	}
}
