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

type Plan struct {
	OriginRunID string
	CarryByCase map[string]CompletedCase
}

func BuildPlan(bundle runbundle.Bundle, bundleHash string, snapshot Snapshot, mode Mode) (Plan, error) {
	if err := mode.Validate(); err != nil {
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
	if snapshot.BundleHash != bundleHash {
		return Plan{}, fmt.Errorf("resume snapshot bundle hash %q does not match bundle hash %q", snapshot.BundleHash, bundleHash)
	}

	bundleCaseIDs := orderedUniqueCaseIDs(bundle.ResolvedSnapshot.Cases)
	snapshotCaseIDs := orderedUniqueStrings(snapshot.CaseIDs)
	if len(snapshotCaseIDs) == 0 {
		return Plan{}, fmt.Errorf("resume snapshot case_ids is required")
	}
	if err := assertSameCaseSet(bundleCaseIDs, snapshotCaseIDs); err != nil {
		return Plan{}, err
	}

	carry := make(map[string]CompletedCase)
	for caseID, c := range snapshot.Completed {
		if !mode.ShouldCarry(c.Result.FinalState) {
			continue
		}
		if _, ok := carry[caseID]; ok {
			return Plan{}, fmt.Errorf("duplicate completed case %q in resume snapshot", caseID)
		}
		carry[caseID] = c
	}

	return Plan{OriginRunID: snapshot.RunID, CarryByCase: carry}, nil
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
