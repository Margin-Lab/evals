package missioncontrol

import (
	"testing"
	"time"

	"github.com/marginlab/margin-eval/runner/runner-core/domain"
	"github.com/marginlab/margin-eval/runner/runner-core/runnerapi"
	"github.com/marginlab/margin-eval/runner/runner-core/store"
)

func TestDeriveSimplifiedStateDurations(t *testing.T) {
	base := time.Date(2026, 4, 7, 12, 0, 0, 0, time.UTC)
	inst := &runnerapi.InstanceSnapshot{
		Instance: store.Instance{
			InstanceID: "inst_1",
			State:      domain.InstanceStateTesting,
		},
		Events: []store.InstanceEvent{
			{ToState: domain.InstanceStateProvisioning, CreatedAt: base.Add(5 * time.Second)},
			{ToState: domain.InstanceStateAgentServerInstalling, CreatedAt: base.Add(15 * time.Second)},
			{ToState: domain.InstanceStateAgentRunning, CreatedAt: base.Add(45 * time.Second)},
			{ToState: domain.InstanceStateAgentCollecting, CreatedAt: base.Add(75 * time.Second)},
			{ToState: domain.InstanceStateTesting, CreatedAt: base.Add(90 * time.Second)},
		},
	}

	got := deriveSimplifiedStateDurations(inst, base.Add(110*time.Second))
	if got[simplifiedStateProvisioningAgent] != 40*time.Second {
		t.Fatalf("provisioning duration = %s, want %s", got[simplifiedStateProvisioningAgent], 40*time.Second)
	}
	if got[simplifiedStateRunningAgent] != 45*time.Second {
		t.Fatalf("running duration = %s, want %s", got[simplifiedStateRunningAgent], 45*time.Second)
	}
	if got[simplifiedStateTestingAgent] != 20*time.Second {
		t.Fatalf("testing duration = %s, want %s", got[simplifiedStateTestingAgent], 20*time.Second)
	}
	if got[simplifiedStatePending] != 0 {
		t.Fatalf("pending duration = %s, want 0", got[simplifiedStatePending])
	}
}

func TestDeriveSimplifiedStateDurationsOmitsTerminalStates(t *testing.T) {
	base := time.Date(2026, 4, 7, 12, 0, 0, 0, time.UTC)
	inst := &runnerapi.InstanceSnapshot{
		Instance: store.Instance{
			InstanceID: "inst_1",
			State:      domain.InstanceStateSucceeded,
		},
		Events: []store.InstanceEvent{
			{ToState: domain.InstanceStateProvisioning, CreatedAt: base.Add(5 * time.Second)},
			{ToState: domain.InstanceStateAgentRunning, CreatedAt: base.Add(10 * time.Second)},
			{ToState: domain.InstanceStateSucceeded, CreatedAt: base.Add(20 * time.Second)},
		},
	}

	got := deriveSimplifiedStateDurations(inst, base.Add(40*time.Second))
	if got[simplifiedStateSucceeded] != 0 {
		t.Fatalf("succeeded duration = %s, want 0", got[simplifiedStateSucceeded])
	}
	if got[simplifiedStateRunningAgent] != 10*time.Second {
		t.Fatalf("running duration = %s, want %s", got[simplifiedStateRunningAgent], 10*time.Second)
	}
}

func TestFormatSimplifiedStateDuration(t *testing.T) {
	tests := []struct {
		name string
		in   time.Duration
		want string
	}{
		{name: "sub-second", in: 500 * time.Millisecond, want: ""},
		{name: "seconds", in: 43 * time.Second, want: "43s"},
		{name: "minutes", in: 2 * time.Minute, want: "2m"},
		{name: "minutes-seconds", in: 2*time.Minute + 14*time.Second, want: "2m14s"},
		{name: "hours", in: 1 * time.Hour, want: "1h"},
		{name: "hours-minutes", in: time.Hour + 2*time.Minute, want: "1h02m"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatSimplifiedStateDuration(tt.in); got != tt.want {
				t.Fatalf("formatSimplifiedStateDuration(%s) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
