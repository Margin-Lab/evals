package missioncontrol

import (
	"fmt"

	"github.com/marginlab/margin-eval/runner/runner-core/domain"
	"github.com/marginlab/margin-eval/runner/runner-core/runnerapi"
)

type retrySummary struct {
	Used   int
	Budget int
}

func (m *model) retrySummary(inst *runnerapi.InstanceSnapshot) retrySummary {
	if inst == nil {
		return retrySummary{}
	}
	used := 0
	for _, ev := range inst.Events {
		if ev.Source == "retry" {
			used++
		}
	}
	budget := m.snapshot.Run.Bundle.ResolvedSnapshot.Execution.RetryCount
	if budget < 0 {
		budget = 0
	}
	return retrySummary{
		Used:   used,
		Budget: budget,
	}
}

func (s retrySummary) Visible() bool {
	return s.Used > 0
}

func (s retrySummary) CompactLabel() string {
	return fmt.Sprintf("r%d/%d", s.Used, s.Budget)
}

func (s retrySummary) DetailValue() string {
	return fmt.Sprintf("%d/%d", s.Used, s.Budget)
}

func retrySummaryStyle(summary retrySummary, state domain.InstanceState) string {
	if !summary.Visible() {
		return ""
	}
	return retryBadgeStyle(summary, state).Render(summary.CompactLabel())
}
