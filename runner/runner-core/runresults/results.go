package runresults

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
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
	InstalledVersion    string                   `json:"installed_version,omitempty"`
	RunID               string                   `json:"run_id"`
	State               domain.RunState          `json:"state"`
	TotalCases          int                      `json:"total_cases"`
	TotalInstances      int                      `json:"total_instances"`
	SamplesPerCase      int                      `json:"samples_per_case"`
	Status              StatusBreakdown          `json:"status"`
	InfraFailureReasons []FailureReasonBreakdown `json:"infra_failure_reasons"`
	Usage               AggregateUsage           `json:"usage"`
	Runtime             RuntimeSummary           `json:"runtime"`
	Instances           []InstanceSummary        `json:"instances"`
	Cases               []CaseSummary            `json:"cases"`
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
	InstanceKey        string               `json:"instance_key"`
	CaseID             string               `json:"case_id"`
	CaseOrdinal        int                  `json:"case_ordinal"`
	SampleIndex        int                  `json:"sample_index"`
	SampleCount        int                  `json:"sample_count"`
	FinalState         domain.InstanceState `json:"final_state"`
	InfraFailureReason *string              `json:"infra_failure_reason"`
	InstalledVersion   string               `json:"installed_version,omitempty"`
	RuntimeMS          int64                `json:"runtime_ms"`
	Usage              *usage.Metrics       `json:"usage"`
}

type CaseSummary struct {
	CaseID        string            `json:"case_id"`
	SampleCount   int               `json:"sample_count"`
	PassCount     int               `json:"pass_count"`
	PassRate      float64           `json:"pass_rate"`
	InstanceKeys  []string          `json:"instance_keys"`
	StatusCounts  map[string]int    `json:"status_counts"`
	SampleResults []InstanceSummary `json:"samples"`
}

func BuildFromStore(ctx context.Context, runStore store.RunStore, runID string) (Summary, error) {
	if runStore == nil {
		return Summary{}, fmt.Errorf("run store is required")
	}
	run, err := runStore.GetRun(ctx, runID, true)
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
		TotalCases:          len(run.Bundle.ResolvedSnapshot.Cases),
		TotalInstances:      len(sortedInstances),
		SamplesPerCase:      run.Bundle.ResolvedSnapshot.Execution.SamplesPerCase,
		InfraFailureReasons: make([]FailureReasonBreakdown, 0),
		Instances:           make([]InstanceSummary, 0, len(sortedInstances)),
		Cases:               make([]CaseSummary, 0),
	}

	failureCounts := map[string]int{}
	installedVersions := map[string]struct{}{}
	missingInstalledVersion := 0
	caseSummaries := map[string]*CaseSummary{}
	caseOrder := make([]string, 0)
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

		installedVersion := ""
		if hasResult {
			installedVersion = strings.TrimSpace(result.InstalledVersion)
		}
		if finalState.IsTerminal() {
			if installedVersion == "" {
				missingInstalledVersion++
			} else {
				installedVersions[installedVersion] = struct{}{}
			}
		}

		instanceSummary := InstanceSummary{
			InstanceID:         inst.InstanceID,
			Ordinal:            inst.Ordinal,
			InstanceKey:        inst.InstanceKey,
			CaseID:             inst.Case.CaseID,
			CaseOrdinal:        inst.CaseOrdinal,
			SampleIndex:        inst.SampleIndex,
			SampleCount:        inst.SampleCount,
			FinalState:         finalState,
			InfraFailureReason: infraReason,
			InstalledVersion:   installedVersion,
			RuntimeMS:          instanceRuntimeMS(inst, result, hasResult),
			Usage:              instanceUsage,
		}
		summary.Instances = append(summary.Instances, instanceSummary)
		caseID := strings.TrimSpace(inst.Case.CaseID)
		if caseID != "" {
			caseSummary, ok := caseSummaries[caseID]
			if !ok {
				caseOrder = append(caseOrder, caseID)
				caseSummary = &CaseSummary{
					CaseID:        caseID,
					SampleCount:   inst.SampleCount,
					InstanceKeys:  make([]string, 0),
					StatusCounts:  map[string]int{},
					SampleResults: make([]InstanceSummary, 0),
				}
				caseSummaries[caseID] = caseSummary
			}
			if caseSummary.SampleCount < inst.SampleCount {
				caseSummary.SampleCount = inst.SampleCount
			}
			if finalState == domain.InstanceStateSucceeded {
				caseSummary.PassCount++
			}
			caseSummary.StatusCounts[string(finalState)]++
			caseSummary.InstanceKeys = append(caseSummary.InstanceKeys, strings.TrimSpace(inst.InstanceKey))
			caseSummary.SampleResults = append(caseSummary.SampleResults, instanceSummary)
		}
	}

	summary.Usage.InstancesWithoutUsage = summary.TotalInstances - summary.Usage.InstancesWithUsage
	summary.Status.Succeeded.Percentage = percentage(summary.Status.Succeeded.Count, summary.TotalInstances)
	summary.Status.TestFailed.Percentage = percentage(summary.Status.TestFailed.Count, summary.TotalInstances)
	summary.Status.InfraFailed.Percentage = percentage(summary.Status.InfraFailed.Count, summary.TotalInstances)
	summary.Status.Canceled.Percentage = percentage(summary.Status.Canceled.Count, summary.TotalInstances)
	summary.Runtime.RunMS = runRuntimeMS(run)
	summary.InfraFailureReasons = buildFailureReasonBreakdown(failureCounts)
	switch len(installedVersions) {
	case 0:
	case 1:
		if missingInstalledVersion == 0 {
			for version := range installedVersions {
				summary.InstalledVersion = version
			}
		}
	default:
		summary.InstalledVersion = "multiple"
	}
	for _, caseID := range caseOrder {
		item := *caseSummaries[caseID]
		item.PassRate = percentage(item.PassCount, len(item.SampleResults))
		summary.Cases = append(summary.Cases, item)
	}
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
		start = firstTime(start, result.ProvisionedAt, result.AgentStartedAt, result.OracleStartedAt, result.TestStartedAt)
		end = lastTime(end, result.TestEndedAt, result.OracleEndedAt, result.AgentEndedAt)
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
