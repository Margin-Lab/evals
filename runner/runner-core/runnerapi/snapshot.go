package runnerapi

import (
	"context"
	"fmt"

	"github.com/marginlab/margin-eval/runner/runner-core/runresults"
	"github.com/marginlab/margin-eval/runner/runner-core/store"
)

// SnapshotOptions controls optional fields included in run and instance snapshots.
type SnapshotOptions struct {
	IncludeBundle            bool
	IncludeRunEvents         bool
	IncludeInstanceAttempts  bool
	IncludeInstanceEvents    bool
	IncludeInstanceResults   bool
	IncludeInstanceArtifacts bool
	IncludeResultsSummary    bool
}

// RunSnapshot is the canonical polling shape for run-level state.
type RunSnapshot struct {
	Run       store.Run           `json:"run"`
	Events    []store.RunEvent    `json:"events,omitempty"`
	Results   *runresults.Summary `json:"results,omitempty"`
	Instances []InstanceSnapshot  `json:"instances"`
}

// InstanceSnapshot is the canonical polling shape for per-instance state.
type InstanceSnapshot struct {
	Instance  store.Instance              `json:"instance"`
	Result    *store.StoredInstanceResult `json:"result,omitempty"`
	Attempts  []store.Attempt             `json:"attempts,omitempty"`
	Events    []store.InstanceEvent       `json:"events,omitempty"`
	Artifacts []store.Artifact            `json:"artifacts,omitempty"`
}

func BuildRunSnapshot(ctx context.Context, runStore store.RunStore, runID string, opts SnapshotOptions) (RunSnapshot, error) {
	if runStore == nil {
		return RunSnapshot{}, fmt.Errorf("run store is required")
	}

	run, err := runStore.GetRun(ctx, runID, opts.IncludeBundle)
	if err != nil {
		return RunSnapshot{}, err
	}
	instances, err := runStore.ListInstances(ctx, runID, nil)
	if err != nil {
		return RunSnapshot{}, err
	}

	out := RunSnapshot{
		Run:       run,
		Instances: make([]InstanceSnapshot, 0, len(instances)),
	}
	if opts.IncludeRunEvents {
		out.Events, err = runStore.ListRunEvents(ctx, runID)
		if err != nil {
			return RunSnapshot{}, err
		}
	}
	if opts.IncludeResultsSummary {
		summary, err := runresults.BuildFromStore(ctx, runStore, runID)
		if err != nil {
			return RunSnapshot{}, err
		}
		out.Results = &summary
	}

	for _, inst := range instances {
		item, err := buildInstanceSnapshotFromInstance(ctx, runStore, inst, opts)
		if err != nil {
			return RunSnapshot{}, err
		}
		out.Instances = append(out.Instances, item)
	}
	return out, nil
}

func BuildInstanceSnapshot(ctx context.Context, runStore store.RunStore, instanceID string, opts SnapshotOptions) (InstanceSnapshot, error) {
	if runStore == nil {
		return InstanceSnapshot{}, fmt.Errorf("run store is required")
	}
	instance, err := runStore.GetInstance(ctx, instanceID)
	if err != nil {
		return InstanceSnapshot{}, err
	}
	return buildInstanceSnapshotFromInstance(ctx, runStore, instance, opts)
}

func buildInstanceSnapshotFromInstance(ctx context.Context, runStore store.RunStore, instance store.Instance, opts SnapshotOptions) (InstanceSnapshot, error) {
	out := InstanceSnapshot{Instance: instance}

	if opts.IncludeInstanceResults {
		result, err := runStore.GetInstanceResult(ctx, instance.InstanceID)
		if err != nil {
			if err != store.ErrNotFound {
				return InstanceSnapshot{}, err
			}
		} else {
			out.Result = &result
		}
	}
	if opts.IncludeInstanceAttempts {
		attempts, err := runStore.ListInstanceAttempts(ctx, instance.InstanceID)
		if err != nil {
			return InstanceSnapshot{}, err
		}
		out.Attempts = attempts
	}
	if opts.IncludeInstanceEvents {
		events, err := runStore.ListInstanceEvents(ctx, instance.InstanceID)
		if err != nil {
			return InstanceSnapshot{}, err
		}
		out.Events = events
	}
	if opts.IncludeInstanceArtifacts {
		artifacts, err := runStore.ListArtifacts(ctx, instance.InstanceID)
		if err != nil {
			return InstanceSnapshot{}, err
		}
		out.Artifacts = artifacts
	}
	return out, nil
}
