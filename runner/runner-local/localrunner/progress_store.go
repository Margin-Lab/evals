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
	runDirFor  func(string) (string, error)
	onTerminal func(context.Context, string) error
	mu         sync.Mutex
}

type progressFile struct {
	RunID        string                      `json:"run_id"`
	BundleHash   string                      `json:"bundle_hash"`
	OriginRunID  string                      `json:"origin_run_id,omitempty"`
	UpdatedAt    time.Time                   `json:"updated_at"`
	InstanceKeys []string                    `json:"instance_keys"`
	Instances    map[string]progressInstance `json:"instances"`
}

type progressInstance struct {
	InstanceKey string                     `json:"instance_key"`
	CaseID      string                     `json:"case_id"`
	SampleIndex int                        `json:"sample_index"`
	SampleCount int                        `json:"sample_count"`
	InstanceID  string                     `json:"instance_id"`
	FinalState  domain.InstanceState       `json:"final_state"`
	ProviderRef string                     `json:"provider_ref,omitempty"`
	Result      store.StoredInstanceResult `json:"result"`
	Artifacts   []store.Artifact           `json:"artifacts,omitempty"`
}

func newProgressStore(runStore store.RunStore, runDirFor func(string) (string, error), onTerminal func(context.Context, string) error) *progressStore {
	return &progressStore{RunStore: runStore, runDirFor: runDirFor, onTerminal: onTerminal}
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
	instanceKeys := make([]string, 0, len(run.Bundle.ResolvedSnapshot.Instances))
	for _, inst := range run.Bundle.ResolvedSnapshot.Instances {
		if strings.TrimSpace(inst.InstanceKey) == "" {
			continue
		}
		instanceKeys = append(instanceKeys, inst.InstanceKey)
	}
	progressInstances := map[string]progressInstance{}
	resultsByInstance := map[string]store.StoredInstanceResult{}
	artifactsByInstance := map[string][]store.Artifact{}
	for _, inst := range instances {
		if !inst.State.IsTerminal() {
			continue
		}
		instanceKey := strings.TrimSpace(inst.InstanceKey)
		if instanceKey == "" {
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
		progressInstances[instanceKey] = progressInstance{
			InstanceKey: instanceKey,
			CaseID:      caseID,
			SampleIndex: inst.SampleIndex,
			SampleCount: inst.SampleCount,
			InstanceID:  inst.InstanceID,
			FinalState:  inst.State,
			ProviderRef: result.ProviderRef,
			Result:      result,
			Artifacts:   arts,
		}
	}

	payload := progressFile{
		RunID:        run.RunID,
		BundleHash:   run.BundleHash,
		OriginRunID:  strings.TrimSpace(run.Bundle.Source.OriginRunID),
		UpdatedAt:    time.Now().UTC(),
		InstanceKeys: instanceKeys,
		Instances:    progressInstances,
	}
	runDir, err := p.runDir(runID)
	if err != nil {
		return err
	}
	if err := writeJSONAtomic(runfs.ProgressPath(runDir), payload); err != nil {
		return err
	}
	artifacts := make([]store.Artifact, 0)
	for _, items := range artifactsByInstance {
		artifacts = append(artifacts, items...)
	}
	sortArtifacts(artifacts)
	if err := writeArtifactsIndex(runDir, artifacts); err != nil {
		return err
	}
	return writeInstanceResults(runDir, instances, resultsByInstance, artifactsByInstance)
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

func LoadProgressSnapshot(runDir string) (resume.Snapshot, error) {
	path := runfs.ProgressPath(runDir)
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
	completed := make(map[string]resume.CompletedInstance, len(file.Instances))
	for instanceKey, c := range file.Instances {
		trimmed := strings.TrimSpace(instanceKey)
		if trimmed == "" {
			continue
		}
		completed[trimmed] = resume.CompletedInstance{
			InstanceKey:      trimmed,
			CaseID:           c.CaseID,
			SampleIndex:      c.SampleIndex,
			SourceRunID:      file.RunID,
			SourceInstanceID: c.InstanceID,
			ProviderRef:      c.ProviderRef,
			Result:           c.Result,
			Artifacts:        c.Artifacts,
		}
	}
	instanceKeys := append([]string(nil), file.InstanceKeys...)
	sort.Strings(instanceKeys)
	return resume.Snapshot{
		RunID:        file.RunID,
		BundleHash:   strings.TrimSpace(file.BundleHash),
		InstanceKeys: instanceKeys,
		Completed:    completed,
	}, nil
}

func loadProgressSnapshot(runDir string) (resume.Snapshot, error) {
	return LoadProgressSnapshot(runDir)
}

func (p *progressStore) runDir(runID string) (string, error) {
	if p.runDirFor == nil {
		return "", fmt.Errorf("run dir resolver is required")
	}
	return p.runDirFor(runID)
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
