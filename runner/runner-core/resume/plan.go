package resume

import (
	"fmt"
	"sort"
	"strings"

	"github.com/marginlab/margin-eval/runner/runner-core/runbundle"
	"github.com/marginlab/margin-eval/runner/runner-core/store"
)

type CompletedInstance struct {
	InstanceKey      string
	CaseID           string
	SampleIndex      int
	SourceRunID      string
	SourceInstanceID string
	ProviderRef      string
	Result           store.StoredInstanceResult
	Artifacts        []store.Artifact
}

type Snapshot struct {
	RunID        string
	BundleHash   string
	InstanceKeys []string
	Completed    map[string]CompletedInstance
}

type BundlePolicy string

const (
	BundlePolicyExact         BundlePolicy = "exact"
	BundlePolicyAllowMismatch BundlePolicy = "allow_mismatch"
)

func (p BundlePolicy) Validate() error {
	switch p {
	case BundlePolicyExact, BundlePolicyAllowMismatch:
		return nil
	default:
		return fmt.Errorf("resume bundle policy must be one of %q, %q", BundlePolicyExact, BundlePolicyAllowMismatch)
	}
}

type Plan struct {
	OriginRunID      string
	CarryByInstance  map[string]CompletedInstance
	BundleHashMatch  bool
	AddedInstances   []string
	DroppedInstances []string
	RerunInstances   []string
	TargetInstances  []string
	SourceInstances  []string
	SourceBundleHash string
	TargetBundleHash string
}

func (p Plan) HasBundleMismatch() bool {
	return !p.BundleHashMatch || len(p.AddedInstances) > 0 || len(p.DroppedInstances) > 0
}

func BuildPlan(bundle runbundle.Bundle, bundleHash string, snapshot Snapshot, mode Mode, policy BundlePolicy) (Plan, error) {
	if err := mode.Validate(); err != nil {
		return Plan{}, err
	}
	if err := policy.Validate(); err != nil {
		return Plan{}, err
	}
	if strings.TrimSpace(snapshot.RunID) == "" {
		return Plan{}, fmt.Errorf("resume snapshot run id is required")
	}
	if strings.TrimSpace(snapshot.BundleHash) == "" {
		return Plan{}, fmt.Errorf("resume snapshot bundle hash is required")
	}
	if strings.TrimSpace(bundleHash) == "" {
		return Plan{}, fmt.Errorf("bundle hash is required")
	}
	bundleHashMatch := snapshot.BundleHash == bundleHash
	if policy == BundlePolicyExact && !bundleHashMatch {
		return Plan{}, fmt.Errorf("resume snapshot bundle hash %q does not match bundle hash %q", snapshot.BundleHash, bundleHash)
	}

	bundleInstanceKeys := orderedUniqueInstanceKeys(bundle.ResolvedSnapshot.Instances)
	snapshotInstanceKeys := orderedUniqueStrings(snapshot.InstanceKeys)
	if len(snapshotInstanceKeys) == 0 {
		return Plan{}, fmt.Errorf("resume snapshot instance_keys is required")
	}
	if policy == BundlePolicyExact {
		if err := assertSameInstanceSet(bundleInstanceKeys, snapshotInstanceKeys); err != nil {
			return Plan{}, err
		}
	}

	targetInstanceSet := make(map[string]struct{}, len(bundleInstanceKeys))
	for _, instanceKey := range bundleInstanceKeys {
		targetInstanceSet[instanceKey] = struct{}{}
	}

	sourceInstanceSet := make(map[string]struct{}, len(snapshotInstanceKeys))
	for _, instanceKey := range snapshotInstanceKeys {
		sourceInstanceSet[instanceKey] = struct{}{}
	}

	addedInstances := make([]string, 0)
	for _, instanceKey := range bundleInstanceKeys {
		if _, ok := sourceInstanceSet[instanceKey]; ok {
			continue
		}
		addedInstances = append(addedInstances, instanceKey)
	}

	droppedInstances := make([]string, 0)
	for _, instanceKey := range snapshotInstanceKeys {
		if _, ok := targetInstanceSet[instanceKey]; ok {
			continue
		}
		droppedInstances = append(droppedInstances, instanceKey)
	}

	carry := make(map[string]CompletedInstance)
	for instanceKey, c := range snapshot.Completed {
		trimmed := strings.TrimSpace(instanceKey)
		if trimmed == "" {
			continue
		}
		if _, ok := targetInstanceSet[trimmed]; !ok {
			continue
		}
		if !mode.ShouldCarry(c.Result.FinalState) {
			continue
		}
		if _, ok := carry[trimmed]; ok {
			return Plan{}, fmt.Errorf("duplicate completed instance %q in resume snapshot", trimmed)
		}
		carry[trimmed] = c
	}

	rerunInstances := make([]string, 0, len(bundleInstanceKeys))
	for _, instanceKey := range bundleInstanceKeys {
		if _, ok := carry[instanceKey]; ok {
			continue
		}
		rerunInstances = append(rerunInstances, instanceKey)
	}

	return Plan{
		OriginRunID:      snapshot.RunID,
		CarryByInstance:  carry,
		BundleHashMatch:  bundleHashMatch,
		AddedInstances:   addedInstances,
		DroppedInstances: droppedInstances,
		RerunInstances:   rerunInstances,
		TargetInstances:  bundleInstanceKeys,
		SourceInstances:  snapshotInstanceKeys,
		SourceBundleHash: snapshot.BundleHash,
		TargetBundleHash: bundleHash,
	}, nil
}

func assertSameInstanceSet(a []string, b []string) error {
	if len(a) != len(b) {
		return fmt.Errorf("resume snapshot instance_keys length %d does not match bundle instances length %d", len(b), len(a))
	}
	aa := append([]string(nil), a...)
	bb := append([]string(nil), b...)
	sort.Strings(aa)
	sort.Strings(bb)
	for i := range aa {
		if aa[i] != bb[i] {
			return fmt.Errorf("resume snapshot instance_keys mismatch at %d: %q != %q", i, bb[i], aa[i])
		}
	}
	return nil
}

func orderedUniqueInstanceKeys(instances []runbundle.InstanceSpec) []string {
	out := make([]string, 0, len(instances))
	seen := map[string]struct{}{}
	for _, inst := range instances {
		id := strings.TrimSpace(inst.InstanceKey)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func orderedUniqueStrings(in []string) []string {
	out := make([]string, 0, len(in))
	seen := map[string]struct{}{}
	for _, v := range in {
		trimmed := strings.TrimSpace(v)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}
