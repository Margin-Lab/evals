package localrunner

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/marginlab/margin-eval/runner/runner-core/domain"
	"github.com/marginlab/margin-eval/runner/runner-core/resume"
	"github.com/marginlab/margin-eval/runner/runner-core/store"
	"github.com/marginlab/margin-eval/runner/runner-local/runfs"
)

type progressStore struct {
	store.RunStore
	rootDir    string
	onTerminal func(context.Context, string) error
	mu         sync.Mutex
}

type progressFile struct {
	RunID       string                  `json:"run_id"`
	BundleHash  string                  `json:"bundle_hash"`
	OriginRunID string                  `json:"origin_run_id,omitempty"`
	UpdatedAt   time.Time               `json:"updated_at"`
	CaseIDs     []string                `json:"case_ids"`
	Cases       map[string]progressCase `json:"cases"`
}

type progressCase struct {
	CaseID      string                     `json:"case_id"`
	InstanceID  string                     `json:"instance_id"`
	FinalState  domain.InstanceState       `json:"final_state"`
	ProviderRef string                     `json:"provider_ref,omitempty"`
	Result      store.StoredInstanceResult `json:"result"`
	Artifacts   []store.Artifact           `json:"artifacts,omitempty"`
}

func newProgressStore(runStore store.RunStore, rootDir string, onTerminal func(context.Context, string) error) *progressStore {
	return &progressStore{RunStore: runStore, rootDir: rootDir, onTerminal: onTerminal}
}

func (p *progressStore) CreateRun(ctx context.Context, in store.CreateRunInput) (store.Run, error) {
	run, err := p.RunStore.CreateRun(ctx, in)
	if err != nil {
		return store.Run{}, err
	}
	if err := p.syncRunProgress(context.Background(), run.RunID); err != nil {
		return store.Run{}, err
	}
	return run, nil
}

func (p *progressStore) FinalizeAttempt(ctx context.Context, in store.FinalizeInput, at time.Time) error {
	if err := p.RunStore.FinalizeAttempt(ctx, in, at); err != nil {
		return err
	}
	if err := p.syncRunProgress(context.Background(), in.RunID); err != nil {
		return err
	}
	return p.maybePersistTerminalSnapshot(context.Background(), in.RunID)
}

func (p *progressStore) CarryForwardInstance(ctx context.Context, in store.CarryForwardInput, at time.Time) error {
	if err := p.RunStore.CarryForwardInstance(ctx, in, at); err != nil {
		return err
	}
	if err := p.syncRunProgress(context.Background(), in.RunID); err != nil {
		return err
	}
	return p.maybePersistTerminalSnapshot(context.Background(), in.RunID)
}

func (p *progressStore) syncRunProgress(ctx context.Context, runID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	run, err := p.RunStore.GetRun(ctx, runID, true)
	if err != nil {
		return err
	}
	instances, err := p.RunStore.ListInstances(ctx, runID, nil)
	if err != nil {
		return err
	}
	caseIDs := make([]string, 0, len(run.Bundle.ResolvedSnapshot.Cases))
	for _, c := range run.Bundle.ResolvedSnapshot.Cases {
		if strings.TrimSpace(c.CaseID) == "" {
			continue
		}
		caseIDs = append(caseIDs, c.CaseID)
	}
	cases := map[string]progressCase{}
	resultsByInstance := map[string]store.StoredInstanceResult{}
	artifactsByInstance := map[string][]store.Artifact{}
	for _, inst := range instances {
		if !inst.State.IsTerminal() {
			continue
		}
		caseID := strings.TrimSpace(inst.Case.CaseID)
		if caseID == "" {
			continue
		}
		result, err := p.RunStore.GetInstanceResult(ctx, inst.InstanceID)
		if err != nil {
			if err != store.ErrNotFound {
				return err
			}
			result = store.StoredInstanceResult{FinalState: inst.State}
		}
		if !result.FinalState.IsTerminal() {
			result.FinalState = inst.State
		}
		arts, err := p.RunStore.ListArtifacts(ctx, inst.InstanceID)
		if err != nil {
			return err
		}
		sortArtifacts(arts)
		resultsByInstance[inst.InstanceID] = result
		artifactsByInstance[inst.InstanceID] = arts
		cases[caseID] = progressCase{
			CaseID:      caseID,
			InstanceID:  inst.InstanceID,
			FinalState:  inst.State,
			ProviderRef: result.ProviderRef,
			Result:      result,
			Artifacts:   arts,
		}
	}

	payload := progressFile{
		RunID:       run.RunID,
		BundleHash:  run.BundleHash,
		OriginRunID: strings.TrimSpace(run.Bundle.Source.OriginRunID),
		UpdatedAt:   time.Now().UTC(),
		CaseIDs:     caseIDs,
		Cases:       cases,
	}
	if err := writeJSONAtomic(p.progressPath(runID), payload); err != nil {
		return err
	}
	artifacts := make([]store.Artifact, 0)
	for _, items := range artifactsByInstance {
		artifacts = append(artifacts, items...)
	}
	sortArtifacts(artifacts)
	if err := writeArtifactsIndex(p.rootDir, runID, artifacts); err != nil {
		return err
	}
	return writeInstanceResults(p.rootDir, runID, instances, resultsByInstance, artifactsByInstance)
}

func (p *progressStore) progressPath(runID string) string {
	return runfs.ProgressPath(p.rootDir, runID)
}

func (p *progressStore) maybePersistTerminalSnapshot(ctx context.Context, runID string) error {
	if p.onTerminal == nil {
		return nil
	}
	run, err := p.RunStore.GetRun(ctx, runID, false)
	if err != nil {
		return err
	}
	if !run.State.IsTerminal() {
		return nil
	}
	return p.onTerminal(ctx, runID)
}

func LoadProgressSnapshot(rootDir, runID string) (resume.Snapshot, error) {
	path := runfs.ProgressPath(rootDir, runID)
	body, err := os.ReadFile(path)
	if err != nil {
		return resume.Snapshot{}, fmt.Errorf("read progress file: %w", err)
	}
	var file progressFile
	if err := json.Unmarshal(body, &file); err != nil {
		return resume.Snapshot{}, fmt.Errorf("decode progress file: %w", err)
	}
	if strings.TrimSpace(file.RunID) == "" {
		return resume.Snapshot{}, fmt.Errorf("progress file missing run_id")
	}
	completed := make(map[string]resume.CompletedCase, len(file.Cases))
	for caseID, c := range file.Cases {
		trimmed := strings.TrimSpace(caseID)
		if trimmed == "" {
			continue
		}
		completed[trimmed] = resume.CompletedCase{
			CaseID:           trimmed,
			SourceRunID:      file.RunID,
			SourceInstanceID: c.InstanceID,
			ProviderRef:      c.ProviderRef,
			Result:           c.Result,
			Artifacts:        c.Artifacts,
		}
	}
	caseIDs := append([]string(nil), file.CaseIDs...)
	sort.Strings(caseIDs)
	return resume.Snapshot{
		RunID:      file.RunID,
		BundleHash: strings.TrimSpace(file.BundleHash),
		CaseIDs:    caseIDs,
		Completed:  completed,
	}, nil
}

func loadProgressSnapshot(rootDir, runID string) (resume.Snapshot, error) {
	return LoadProgressSnapshot(rootDir, runID)
}

func writeJSONAtomic(path string, payload any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir %q: %w", filepath.Dir(path), err)
	}
	body, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal json %q: %w", path, err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(body, '\n'), 0o644); err != nil {
		return fmt.Errorf("write temp %q: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename %q -> %q: %w", tmp, path, err)
	}
	return nil
}
