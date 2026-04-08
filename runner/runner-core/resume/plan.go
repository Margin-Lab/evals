package resume

import (
	"fmt"
	"sort"
	"strings"

	"github.com/marginlab/margin-eval/runner/runner-core/runbundle"
	"github.com/marginlab/margin-eval/runner/runner-core/store"
)

type CompletedCase struct {
	CaseID           string
	SourceRunID      string
	SourceInstanceID string
	ProviderRef      string
	Result           store.StoredInstanceResult
	Artifacts        []store.Artifact
}

type Snapshot struct {
	RunID      string
	BundleHash string
	CaseIDs    []string
	Completed  map[string]CompletedCase
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
	CarryByCase      map[string]CompletedCase
	BundleHashMatch  bool
	AddedCaseIDs     []string
	DroppedCaseIDs   []string
	RerunCaseIDs     []string
	TargetCaseIDs    []string
	SourceCaseIDs    []string
	SourceBundleHash string
	TargetBundleHash string
}

func (p Plan) HasBundleMismatch() bool {
	return !p.BundleHashMatch || len(p.AddedCaseIDs) > 0 || len(p.DroppedCaseIDs) > 0
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

	bundleCaseIDs := orderedUniqueCaseIDs(bundle.ResolvedSnapshot.Cases)
	snapshotCaseIDs := orderedUniqueStrings(snapshot.CaseIDs)
	if len(snapshotCaseIDs) == 0 {
		return Plan{}, fmt.Errorf("resume snapshot case_ids is required")
	}
	if policy == BundlePolicyExact {
		if err := assertSameCaseSet(bundleCaseIDs, snapshotCaseIDs); err != nil {
			return Plan{}, err
		}
	}

	targetCaseSet := make(map[string]struct{}, len(bundleCaseIDs))
	for _, caseID := range bundleCaseIDs {
		targetCaseSet[caseID] = struct{}{}
	}

	sourceCaseSet := make(map[string]struct{}, len(snapshotCaseIDs))
	for _, caseID := range snapshotCaseIDs {
		sourceCaseSet[caseID] = struct{}{}
	}

	addedCaseIDs := make([]string, 0)
	for _, caseID := range bundleCaseIDs {
		if _, ok := sourceCaseSet[caseID]; ok {
			continue
		}
		addedCaseIDs = append(addedCaseIDs, caseID)
	}

	droppedCaseIDs := make([]string, 0)
	for _, caseID := range snapshotCaseIDs {
		if _, ok := targetCaseSet[caseID]; ok {
			continue
		}
		droppedCaseIDs = append(droppedCaseIDs, caseID)
	}

	carry := make(map[string]CompletedCase)
	for caseID, c := range snapshot.Completed {
		trimmed := strings.TrimSpace(caseID)
		if trimmed == "" {
			continue
		}
		if _, ok := targetCaseSet[trimmed]; !ok {
			continue
		}
		if !mode.ShouldCarry(c.Result.FinalState) {
			continue
		}
		if _, ok := carry[trimmed]; ok {
			return Plan{}, fmt.Errorf("duplicate completed case %q in resume snapshot", trimmed)
		}
		carry[trimmed] = c
	}

	rerunCaseIDs := make([]string, 0, len(bundleCaseIDs))
	for _, caseID := range bundleCaseIDs {
		if _, ok := carry[caseID]; ok {
			continue
		}
		rerunCaseIDs = append(rerunCaseIDs, caseID)
	}

	return Plan{
		OriginRunID:      snapshot.RunID,
		CarryByCase:      carry,
		BundleHashMatch:  bundleHashMatch,
		AddedCaseIDs:     addedCaseIDs,
		DroppedCaseIDs:   droppedCaseIDs,
		RerunCaseIDs:     rerunCaseIDs,
		TargetCaseIDs:    bundleCaseIDs,
		SourceCaseIDs:    snapshotCaseIDs,
		SourceBundleHash: snapshot.BundleHash,
		TargetBundleHash: bundleHash,
	}, nil
}

func assertSameCaseSet(a []string, b []string) error {
	if len(a) != len(b) {
		return fmt.Errorf("resume snapshot case_ids length %d does not match bundle cases length %d", len(b), len(a))
	}
	aa := append([]string(nil), a...)
	bb := append([]string(nil), b...)
	sort.Strings(aa)
	sort.Strings(bb)
	for i := range aa {
		if aa[i] != bb[i] {
			return fmt.Errorf("resume snapshot case_ids mismatch at %d: %q != %q", i, bb[i], aa[i])
		}
	}
	return nil
}

func orderedUniqueCaseIDs(cases []runbundle.Case) []string {
	out := make([]string, 0, len(cases))
	seen := map[string]struct{}{}
	for _, c := range cases {
		id := strings.TrimSpace(c.CaseID)
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
