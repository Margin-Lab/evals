package runresults

import (
	"context"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/marginlab/margin-eval/runner/runner-core/domain"
	"github.com/marginlab/margin-eval/runner/runner-core/instancestatus"
	"github.com/marginlab/margin-eval/runner/runner-core/store"
	"github.com/marginlab/margin-eval/runner/runner-core/usage"
)

const (
	InfraFailureReasonAgentFailed       = instancestatus.InfraFailureReasonAgentFailed
	InfraFailureReasonExecutorError     = instancestatus.InfraFailureReasonExecutorError
	InfraFailureReasonInstanceTimeout   = instancestatus.InfraFailureReasonInstanceTimeout
	InfraFailureReasonInvalidFinalState = instancestatus.InfraFailureReasonInvalidFinalState
	InfraFailureReasonUnknownFailure    = instancestatus.InfraFailureReasonUnknownFailure
)

type Summary struct {
	RunID               string                   `json:"run_id"`
	State               domain.RunState          `json:"state"`
	TotalInstances      int                      `json:"total_instances"`
	Status              StatusBreakdown          `json:"status"`
	InfraFailureReasons []FailureReasonBreakdown `json:"infra_failure_reasons"`
	Usage               AggregateUsage           `json:"usage"`
	Runtime             RuntimeSummary           `json:"runtime"`
	Instances           []InstanceSummary        `json:"instances"`
}

type StatusBreakdown struct {
	Succeeded   CountPercentage `json:"succeeded"`
	TestFailed  CountPercentage `json:"test_failed"`
	InfraFailed CountPercentage `json:"infra_failed"`
	Canceled    CountPercentage `json:"canceled"`
}

type CountPercentage struct {
	Count      int     `json:"count"`
	Percentage float64 `json:"percentage"`
}

type FailureReasonBreakdown struct {
	Reason     string  `json:"reason"`
	Count      int     `json:"count"`
	Percentage float64 `json:"percentage"`
}

type AggregateUsage struct {
	InputTokens           int64 `json:"input_tokens"`
	OutputTokens          int64 `json:"output_tokens"`
	ToolCalls             int64 `json:"tool_calls"`
	InstancesWithUsage    int   `json:"instances_with_usage"`
	InstancesWithoutUsage int   `json:"instances_without_usage"`
}

type RuntimeSummary struct {
	RunMS *int64 `json:"run_ms"`
}

type InstanceSummary struct {
	InstanceID         string               `json:"instance_id"`
	Ordinal            int                  `json:"ordinal"`
	CaseID             string               `json:"case_id"`
	FinalState         domain.InstanceState `json:"final_state"`
	InfraFailureReason *string              `json:"infra_failure_reason"`
	RuntimeMS          int64                `json:"runtime_ms"`
	Usage              *usage.Metrics       `json:"usage"`
}

func BuildFromStore(ctx context.Context, runStore store.RunStore, runID string) (Summary, error) {
	if runStore == nil {
		return Summary{}, fmt.Errorf("run store is required")
	}
	run, err := runStore.GetRun(ctx, runID, false)
	if err != nil {
		return Summary{}, err
	}
	instances, err := runStore.ListInstances(ctx, runID, nil)
	if err != nil {
		return Summary{}, err
	}
	results, err := runStore.ListInstanceResults(ctx, runID)
	if err != nil {
		return Summary{}, err
	}
	return Build(run, instances, results), nil
}

func Build(run store.Run, instances []store.Instance, results []store.StoredInstanceResult) Summary {
	sortedInstances := append([]store.Instance(nil), instances...)
	sort.Slice(sortedInstances, func(i, j int) bool {
		return sortedInstances[i].Ordinal < sortedInstances[j].Ordinal
	})

	resultsByInstance := make(map[string]store.StoredInstanceResult, len(results))
	for _, result := range results {
		resultsByInstance[result.InstanceID] = result
	}

	summary := Summary{
		RunID:               run.RunID,
		State:               run.State,
		TotalInstances:      len(sortedInstances),
		InfraFailureReasons: make([]FailureReasonBreakdown, 0),
		Instances:           make([]InstanceSummary, 0, len(sortedInstances)),
	}

	failureCounts := map[string]int{}
	for _, inst := range sortedInstances {
		result, hasResult := resultsByInstance[inst.InstanceID]
		finalState := inst.State
		if hasResult && result.FinalState.IsTerminal() {
			finalState = result.FinalState
		}

		switch finalState {
		case domain.InstanceStateSucceeded:
			summary.Status.Succeeded.Count++
		case domain.InstanceStateTestFailed:
			summary.Status.TestFailed.Count++
		case domain.InstanceStateInfraFailed:
			summary.Status.InfraFailed.Count++
		case domain.InstanceStateCanceled:
			summary.Status.Canceled.Count++
		}

		instanceUsage := (*usage.Metrics)(nil)
		if hasResult {
			instanceUsage = usage.Clone(result.Usage)
		}
		if usage.Known(instanceUsage) {
			summary.Usage.InstancesWithUsage++
			if instanceUsage.InputTokens != nil {
				summary.Usage.InputTokens += *instanceUsage.InputTokens
			}
			if instanceUsage.OutputTokens != nil {
				summary.Usage.OutputTokens += *instanceUsage.OutputTokens
			}
			if instanceUsage.ToolCalls != nil {
				summary.Usage.ToolCalls += *instanceUsage.ToolCalls
			}
		}

		infraReason := (*string)(nil)
		if hasResult {
			infraReason = instancestatus.InfraFailureReason(result)
		}
		if infraReason != nil {
			failureCounts[*infraReason]++
		}

		summary.Instances = append(summary.Instances, InstanceSummary{
			InstanceID:         inst.InstanceID,
			Ordinal:            inst.Ordinal,
			CaseID:             inst.Case.CaseID,
			FinalState:         finalState,
			InfraFailureReason: infraReason,
			RuntimeMS:          instanceRuntimeMS(inst, result, hasResult),
			Usage:              instanceUsage,
		})
	}

	summary.Usage.InstancesWithoutUsage = summary.TotalInstances - summary.Usage.InstancesWithUsage
	summary.Status.Succeeded.Percentage = percentage(summary.Status.Succeeded.Count, summary.TotalInstances)
	summary.Status.TestFailed.Percentage = percentage(summary.Status.TestFailed.Count, summary.TotalInstances)
	summary.Status.InfraFailed.Percentage = percentage(summary.Status.InfraFailed.Count, summary.TotalInstances)
	summary.Status.Canceled.Percentage = percentage(summary.Status.Canceled.Count, summary.TotalInstances)
	summary.Runtime.RunMS = runRuntimeMS(run)
	summary.InfraFailureReasons = buildFailureReasonBreakdown(failureCounts)
	return summary
}

func buildFailureReasonBreakdown(counts map[string]int) []FailureReasonBreakdown {
	if len(counts) == 0 {
		return []FailureReasonBreakdown{}
	}
	total := 0
	for _, count := range counts {
		total += count
	}
	out := make([]FailureReasonBreakdown, 0, len(counts))
	for reason, count := range counts {
		out = append(out, FailureReasonBreakdown{
			Reason:     reason,
			Count:      count,
			Percentage: percentage(count, total),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count == out[j].Count {
			return out[i].Reason < out[j].Reason
		}
		return out[i].Count > out[j].Count
	})
	return out
}

func instanceRuntimeMS(inst store.Instance, result store.StoredInstanceResult, hasResult bool) int64 {
	start := inst.CreatedAt
	end := inst.UpdatedAt
	if hasResult {
		start = firstTime(start, result.ProvisionedAt, result.AgentStartedAt, result.TestStartedAt)
		end = lastTime(end, result.TestEndedAt, result.AgentEndedAt)
	}
	return durationMS(start, end)
}

func runRuntimeMS(run store.Run) *int64 {
	if run.StartedAt == nil || run.EndedAt == nil {
		return nil
	}
	value := durationMS(*run.StartedAt, *run.EndedAt)
	return &value
}

func firstTime(fallback time.Time, values ...*time.Time) time.Time {
	for _, value := range values {
		if value != nil {
			return *value
		}
	}
	return fallback
}

func lastTime(fallback time.Time, values ...*time.Time) time.Time {
	for _, value := range values {
		if value != nil {
			return *value
		}
	}
	return fallback
}

func durationMS(start, end time.Time) int64 {
	if end.Before(start) {
		return 0
	}
	return end.Sub(start).Milliseconds()
}

func percentage(count, total int) float64 {
	if total <= 0 {
		return 0
	}
	value := (float64(count) / float64(total)) * 100
	return math.Round(value*100) / 100
}

func strPtr(v string) *string {
	return &v
}
