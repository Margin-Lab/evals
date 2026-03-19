package state

import (
	"testing"

	"github.com/marginlab/margin-eval/agent-server/internal/apperr"
	"github.com/marginlab/margin-eval/runner/runner-core/agentdef"
)

func TestValidateAgentStateTransition(t *testing.T) {
	tests := []struct {
		name    string
		from    AgentState
		to      AgentState
		wantErr bool
	}{
		{name: "empty_to_definition_loaded", from: AgentStateEmpty, to: AgentStateDefinitionLoaded},
		{name: "definition_loaded_to_installed", from: AgentStateDefinitionLoaded, to: AgentStateInstalled},
		{name: "definition_loaded_to_configured", from: AgentStateDefinitionLoaded, to: AgentStateConfigured},
		{name: "installed_to_configured", from: AgentStateInstalled, to: AgentStateConfigured},
		{name: "configured_to_definition_loaded", from: AgentStateConfigured, to: AgentStateDefinitionLoaded},
		{name: "empty_to_configured_invalid", from: AgentStateEmpty, to: AgentStateConfigured, wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateAgentStateTransition(tc.from, tc.to)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ValidateAgentStateTransition(%s,%s) err=%v wantErr=%v", tc.from, tc.to, err, tc.wantErr)
			}
		})
	}
}

func TestValidateRunStateTransition(t *testing.T) {
	tests := []struct {
		name    string
		from    RunState
		to      RunState
		wantErr bool
	}{
		{name: "idle_to_starting", from: RunStateIdle, to: RunStateStarting},
		{name: "starting_to_exited", from: RunStateStarting, to: RunStateExited},
		{name: "running_to_collecting", from: RunStateRunning, to: RunStateCollecting},
		{name: "exited_to_idle", from: RunStateExited, to: RunStateIdle},
		{name: "running_to_idle_invalid", from: RunStateRunning, to: RunStateIdle, wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateRunStateTransition(tc.from, tc.to)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ValidateRunStateTransition(%s,%s) err=%v wantErr=%v", tc.from, tc.to, err, tc.wantErr)
			}
		})
	}
}

func TestValidateLoadDefinitionTransitionErrors(t *testing.T) {
	st := DefaultServerState()
	st.Run.State = RunStateRunning
	st.Run.RunID = "r1"
	err := ValidateLoadDefinitionTransition(st)
	assertAPIErrorCode(t, err, apperr.CodeRunAlreadyActive)
}

func TestValidateInstallTransitionErrors(t *testing.T) {
	st := DefaultServerState()
	err := ValidateInstallTransition(st)
	assertAPIErrorCode(t, err, apperr.CodeAgentNotConfigured)

	st = DefaultServerState()
	st.Agent.State = AgentStateDefinitionLoaded
	st.Agent.Definition = &DefinitionRecord{
		Snapshot: agentdef.DefinitionSnapshot{
			Manifest: agentdef.Manifest{
				Kind: "agent_definition",
				Name: "fixture",
				Run:  agentdef.RunSpec{PrepareHook: agentdef.HookRef{Path: "hooks/run.sh"}},
			},
		},
	}
	st.Run.State = RunStateRunning
	st.Run.RunID = "r1"
	err = ValidateInstallTransition(st)
	assertAPIErrorCode(t, err, apperr.CodeRunAlreadyActive)
}

func TestValidateConfigureTransitionErrors(t *testing.T) {
	st := DefaultServerState()
	err := ValidateConfigureTransition(st)
	assertAPIErrorCode(t, err, apperr.CodeAgentNotConfigured)

	st = DefaultServerState()
	st.Agent.State = AgentStateDefinitionLoaded
	st.Agent.Definition = &DefinitionRecord{
		Snapshot: agentdef.DefinitionSnapshot{
			Manifest: agentdef.Manifest{
				Kind: "agent_definition",
				Name: "fixture",
				Run:  agentdef.RunSpec{PrepareHook: agentdef.HookRef{Path: "hooks/run.sh"}},
			},
		},
	}
	if err := ValidateConfigureTransition(st); err != nil {
		t.Fatalf("ValidateConfigureTransition(definition_loaded) err = %v", err)
	}

	st.Run.State = RunStateStarting
	st.Run.RunID = "r1"
	err = ValidateConfigureTransition(st)
	assertAPIErrorCode(t, err, apperr.CodeRunAlreadyActive)
}

func TestValidateStartRunTransitionErrors(t *testing.T) {
	st := DefaultServerState()
	st.Agent.State = AgentStateConfigured
	st.Run.State = RunStateExited
	st.Run.RunID = "r_old"
	err := ValidateStartRunTransition(st)
	assertAPIErrorCode(t, err, apperr.CodeRunNotCleared)

	st = DefaultServerState()
	st.Agent.State = AgentStateConfigured
	err = ValidateStartRunTransition(st)
	assertAPIErrorCode(t, err, apperr.CodeAgentNotConfigured)

	st.Agent.Definition = &DefinitionRecord{
		Snapshot: agentdef.DefinitionSnapshot{
			Manifest: agentdef.Manifest{
				Kind: "agent_definition",
				Name: "fixture",
				Run:  agentdef.RunSpec{PrepareHook: agentdef.HookRef{Path: "hooks/run.sh"}},
			},
		},
	}
	st.Agent.Install = &InstallInfo{}
	st.Agent.Config = &ConfigRecord{Snapshot: agentdef.ConfigSnapshot{Name: "default", Mode: agentdef.ConfigModeDirect, Input: map[string]any{}}}
	st.Run.State = RunStateExited
	st.Run.RunID = "r_old"
	err = ValidateStartRunTransition(st)
	assertAPIErrorCode(t, err, apperr.CodeRunNotCleared)
}

func TestValidateRunClearTransitionErrors(t *testing.T) {
	st := DefaultServerState()
	st.Run.State = RunStateRunning
	err := ValidateRunClearTransition(st)
	assertAPIErrorCode(t, err, apperr.CodeRunNotClearable)
}

func assertAPIErrorCode(t *testing.T, err error, code string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error code %q, got nil", code)
	}
	apiErr, ok := apperr.As(err)
	if !ok {
		t.Fatalf("expected APIError, got %T (%v)", err, err)
	}
	if apiErr.Code != code {
		t.Fatalf("code = %q, want %q", apiErr.Code, code)
	}
}
