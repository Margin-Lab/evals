package localrunner

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/marginlab/margin-eval/runner/runner-core/engine"
	"github.com/marginlab/margin-eval/runner/runner-core/runbundle"
	"github.com/marginlab/margin-eval/runner/runner-core/runnerapi"
	"github.com/marginlab/margin-eval/runner/runner-core/runresults"
	"github.com/marginlab/margin-eval/runner/runner-core/store"
	"github.com/marginlab/margin-eval/runner/runner-local/runfs"
)

type Config struct {
	RootDir               string
	RunStore              store.RunStore
	Executor              engine.Executor
	DockerBinary          string
	ImagePruneFunc        func(context.Context) error
	GlobalImagePruneEvery int
	EngineConfig          engine.Config
	Now                   func() time.Time
	IDFunc                func(prefix string) string
}

type Service struct {
	rootDir          string
	runStore         store.RunStore
	pool             *engine.Pool
	now              func() time.Time
	idFunc           func(prefix string) string
	pruneCoordinator *pruneCoordinator
}

var _ runnerapi.Service = (*Service)(nil)

type idGen struct {
	seq atomic.Uint64
}

func (g *idGen) Next(prefix string) string {
	n := g.seq.Add(1)
	return fmt.Sprintf("%s_%06d", prefix, n)
}

func NewService(cfg Config) (runnerapi.Service, error) {
	if strings.TrimSpace(cfg.RootDir) == "" {
		return nil, fmt.Errorf("root dir is required")
	}
	if cfg.Executor == nil {
		return nil, fmt.Errorf("executor is required")
	}
	if cfg.RunStore == nil {
		cfg.RunStore = store.NewMemoryStore()
	}
	if cfg.Now == nil {
		cfg.Now = func() time.Time { return time.Now().UTC() }
	}
	runsRoot := filepath.Join(cfg.RootDir, "runs")
	if err := os.MkdirAll(runsRoot, 0o755); err != nil {
		return nil, fmt.Errorf("create runs root: %w", err)
	}
	if cfg.IDFunc == nil {
		gen := &idGen{}
		maxRunSeq, err := detectMaxRunSequence(runsRoot)
		if err != nil {
			return nil, fmt.Errorf("detect max run sequence: %w", err)
		}
		gen.seq.Store(maxRunSeq)
		cfg.IDFunc = gen.Next
	}
	svc := &Service{
		rootDir: cfg.RootDir,
		now:     cfg.Now,
		idFunc:  cfg.IDFunc,
	}
	if cfg.GlobalImagePruneEvery < 0 {
		return nil, fmt.Errorf("global image prune interval must be >= 0")
	}
	executor := cfg.Executor
	runStore := cfg.RunStore
	if cfg.GlobalImagePruneEvery > 0 {
		coordinator, err := newPruneCoordinator(pruneCoordinatorConfig{
			Interval:       cfg.GlobalImagePruneEvery,
			DockerBinary:   cfg.DockerBinary,
			ImagePruneFunc: cfg.ImagePruneFunc,
		})
		if err != nil {
			return nil, err
		}
		svc.pruneCoordinator = coordinator
		executor = coordinator.wrapExecutor(executor)
		runStore = newPruningStore(runStore, coordinator)
	}
	svc.runStore = newProgressStore(runStore, cfg.RootDir, svc.ensureTerminalRunSnapshot)
	svc.pool = engine.NewPool(svc.runStore, executor, cfg.EngineConfig)
	return svc, nil
}

func detectMaxRunSequence(runsRoot string) (uint64, error) {
	entries, err := os.ReadDir(runsRoot)
	if err != nil {
		return 0, fmt.Errorf("read runs root %q: %w", runsRoot, err)
	}
	var max uint64
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		seq, ok := parseRunSequence(entry.Name())
		if !ok {
			continue
		}
		if seq > max {
			max = seq
		}
	}
	return max, nil
}

func parseRunSequence(runID string) (uint64, bool) {
	if !strings.HasPrefix(runID, "run_") {
		return 0, false
	}
	suffix := strings.TrimPrefix(runID, "run_")
	if suffix == "" {
		return 0, false
	}
	for _, ch := range suffix {
		if ch < '0' || ch > '9' {
			return 0, false
		}
	}
	seq, err := strconv.ParseUint(suffix, 10, 64)
	if err != nil {
		return 0, false
	}
	return seq, true
}

func (s *Service) Start(ctx context.Context) {
	if s.pruneCoordinator != nil {
		s.pruneCoordinator.Start(ctx)
	}
	s.pool.Start(ctx)
}

func (s *Service) SubmitRun(ctx context.Context, in runnerapi.SubmitInput) (store.Run, error) {
	bundle := in.Bundle
	if strings.TrimSpace(bundle.BundleID) == "" {
		bundle.BundleID = s.idFunc("bun")
	}
	if bundle.CreatedAt.IsZero() {
		bundle.CreatedAt = s.now()
	}
	if bundle.Source.SubmitProjectID == "" {
		bundle.Source.SubmitProjectID = in.ProjectID
	}

	resumeFromRunID := strings.TrimSpace(in.ResumeFromRunID)
	if resumeFromRunID != "" {
		return s.submitResumedRun(ctx, in, bundle, resumeFromRunID)
	}

	hash, err := runbundle.HashSHA256(bundle)
	if err != nil {
		return store.Run{}, fmt.Errorf("compute bundle hash: %w", err)
	}
	run, err := s.createRun(ctx, in, bundle, hash)
	if err != nil {
		return store.Run{}, err
	}
	if err := s.writeJSON(runfs.BundlePath(s.rootDir, run.RunID), bundle); err != nil {
		return store.Run{}, err
	}
	return run, nil
}

func (s *Service) createRun(ctx context.Context, in runnerapi.SubmitInput, bundle runbundle.Bundle, hash string) (store.Run, error) {
	runID := strings.TrimSpace(in.RunID)
	if runID == "" {
		runID = s.idFunc("run")
	}
	return s.runStore.CreateRun(ctx, store.CreateRunInput{
		RunID:         runID,
		ProjectID:     in.ProjectID,
		CreatedByUser: in.CreatedByUser,
		Name:          in.Name,
		SourceKind:    bundle.Source.Kind,
		Bundle:        bundle,
		BundleHash:    hash,
		At:            s.now(),
	})
}

func (s *Service) WaitForTerminalRun(ctx context.Context, runID string, pollInterval time.Duration) (store.Run, error) {
	if pollInterval <= 0 {
		pollInterval = 200 * time.Millisecond
	}
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		run, err := s.runStore.GetRun(ctx, runID, false)
		if err != nil {
			return store.Run{}, err
		}
		if run.State.IsTerminal() {
			if err := s.ensureTerminalRunSnapshot(context.Background(), runID); err != nil {
				return store.Run{}, err
			}
			return run, nil
		}
		select {
		case <-ctx.Done():
			return store.Run{}, ctx.Err()
		case <-ticker.C:
		}
	}
}

func (s *Service) GetRunSnapshot(ctx context.Context, runID string, opts runnerapi.SnapshotOptions) (runnerapi.RunSnapshot, error) {
	return runnerapi.BuildRunSnapshot(ctx, s.runStore, runID, opts)
}

func (s *Service) GetInstanceSnapshot(ctx context.Context, instanceID string, opts runnerapi.SnapshotOptions) (runnerapi.InstanceSnapshot, error) {
	return runnerapi.BuildInstanceSnapshot(ctx, s.runStore, instanceID, opts)
}

func (s *Service) ensureTerminalRunSnapshot(ctx context.Context, runID string) error {
	run, err := s.runStore.GetRun(ctx, runID, false)
	if err != nil {
		return err
	}
	if !run.State.IsTerminal() {
		return nil
	}
	return s.persistRunSnapshot(ctx, runID)
}

func (s *Service) persistRunSnapshot(ctx context.Context, runID string) error {
	run, err := s.runStore.GetRun(ctx, runID, false)
	if err != nil {
		return err
	}
	instances, err := s.runStore.ListInstances(ctx, runID, nil)
	if err != nil {
		return err
	}
	runEvents, err := s.runStore.ListRunEvents(ctx, runID)
	if err != nil {
		return err
	}

	manifest := map[string]any{
		"run_id":         run.RunID,
		"project_id":     run.ProjectID,
		"state":          run.State,
		"source_kind":    run.SourceKind,
		"bundle_hash":    run.BundleHash,
		"execution_mode": run.Bundle.ResolvedSnapshot.Execution.Mode,
		"created_at":     run.CreatedAt,
		"started_at":     run.StartedAt,
		"ended_at":       run.EndedAt,
		"counts":         run.Counts,
	}
	if trimmed := strings.TrimSpace(run.Bundle.Source.OriginRunID); trimmed != "" {
		manifest["resume_from_run_id"] = trimmed
	}
	if trimmed := strings.TrimSpace(run.Bundle.Source.ResumeSourceBundleHash); trimmed != "" {
		manifest["resume_source_bundle_hash"] = trimmed
	}
	if run.Bundle.Source.ResumeBundleHashMatch != nil {
		manifest["resume_bundle_hash_match"] = *run.Bundle.Source.ResumeBundleHashMatch
	}
	if err := s.writeJSON(runfs.ManifestPath(s.rootDir, runID), manifest); err != nil {
		return err
	}
	results, err := s.runStore.ListInstanceResults(ctx, runID)
	if err != nil {
		return err
	}
	summary := runresults.Build(run, instances, results)
	if err := s.writeJSON(runfs.ResultsPath(s.rootDir, runID), summary); err != nil {
		return err
	}

	eventLines := make([]map[string]any, 0, len(runEvents)+len(instances)*2)
	for _, ev := range runEvents {
		eventLines = append(eventLines, map[string]any{"type": "run_event", "event": ev})
	}
	for _, inst := range instances {
		instanceEvents, err := s.runStore.ListInstanceEvents(ctx, inst.InstanceID)
		if err != nil {
			return err
		}
		for _, ev := range instanceEvents {
			eventLines = append(eventLines, map[string]any{"type": "instance_event", "event": ev})
		}
	}
	artifactsByInstance, artifacts, err := collectRunArtifacts(ctx, s.runStore, instances)
	if err != nil {
		return err
	}
	if err := s.writeJSONLines(runfs.EventsPath(s.rootDir, runID), eventLines); err != nil {
		return err
	}
	if err := writeArtifactsIndex(s.rootDir, runID, artifacts); err != nil {
		return err
	}
	resultsByInstance, err := collectRunResults(ctx, s.runStore, instances)
	if err != nil {
		return err
	}
	if err := writeInstanceResults(s.rootDir, runID, instances, resultsByInstance, artifactsByInstance); err != nil {
		return err
	}
	return nil
}

func (s *Service) writeJSON(path string, payload any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir %q: %w", filepath.Dir(path), err)
	}
	body, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal json for %q: %w", path, err)
	}
	if err := os.WriteFile(path, append(body, '\n'), 0o644); err != nil {
		return fmt.Errorf("write %q: %w", path, err)
	}
	return nil
}

func (s *Service) writeJSONLines(path string, items []map[string]any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir %q: %w", filepath.Dir(path), err)
	}
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create %q: %w", path, err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, item := range items {
		if err := enc.Encode(item); err != nil {
			return fmt.Errorf("write json line: %w", err)
		}
	}
	return nil
}
