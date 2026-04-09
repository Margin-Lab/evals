package localrunner

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/marginlab/margin-eval/runner/runner-core/engine"
	"github.com/marginlab/margin-eval/runner/runner-core/runbundle"
	"github.com/marginlab/margin-eval/runner/runner-core/runnerapi"
	"github.com/marginlab/margin-eval/runner/runner-core/runresults"
	"github.com/marginlab/margin-eval/runner/runner-core/store"
	"github.com/marginlab/margin-eval/runner/runner-local/runfs"
)

type Config struct {
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
	runStore         store.RunStore
	pool             *engine.Pool
	now              func() time.Time
	idFunc           func(prefix string) string
	pruneCoordinator *pruneCoordinator
	executorRunDirs  interface {
		RegisterRunDir(string, string) error
	}
	mu      sync.RWMutex
	runDirs map[string]string
}

var _ runnerapi.Service = (*Service)(nil)

func NewService(cfg Config) (runnerapi.Service, error) {
	if cfg.Executor == nil {
		return nil, fmt.Errorf("executor is required")
	}
	if cfg.RunStore == nil {
		cfg.RunStore = store.NewMemoryStore()
	}
	if cfg.Now == nil {
		cfg.Now = func() time.Time { return time.Now().UTC() }
	}
	if cfg.IDFunc == nil {
		cfg.IDFunc = func(prefix string) string {
			return fmt.Sprintf("%s_%d", prefix, cfg.Now().UnixNano())
		}
	}
	svc := &Service{
		now:             cfg.Now,
		idFunc:          cfg.IDFunc,
		executorRunDirs: executorRunDirRegistrar(cfg.Executor),
		runDirs:         map[string]string{},
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
	svc.runStore = newProgressStore(runStore, svc.runDir, svc.ensureTerminalRunSnapshot)
	svc.pool = engine.NewPool(svc.runStore, executor, cfg.EngineConfig)
	return svc, nil
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

	runID := strings.TrimSpace(in.RunID)
	if runID == "" {
		return store.Run{}, fmt.Errorf("run id is required")
	}
	outputDir, err := prepareOutputDir(in.OutputDir)
	if err != nil {
		return store.Run{}, err
	}
	if err := s.registerRunDir(runID, outputDir); err != nil {
		return store.Run{}, err
	}
	defer func() {
		if err != nil {
			s.unregisterRunDir(runID)
		}
	}()

	resumeFromDir := strings.TrimSpace(in.ResumeFromDir)
	if resumeFromDir != "" {
		return s.submitResumedRun(ctx, in, bundle, outputDir, resumeFromDir)
	}

	hash, err := runbundle.HashSHA256(bundle)
	if err != nil {
		return store.Run{}, fmt.Errorf("compute bundle hash: %w", err)
	}
	run, err := s.createRun(ctx, in, bundle, hash)
	if err != nil {
		return store.Run{}, err
	}
	if err := s.writeJSON(runfs.BundlePath(outputDir), bundle); err != nil {
		return store.Run{}, err
	}
	return run, nil
}

func (s *Service) createRun(ctx context.Context, in runnerapi.SubmitInput, bundle runbundle.Bundle, hash string) (store.Run, error) {
	runID := strings.TrimSpace(in.RunID)
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

func prepareOutputDir(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", fmt.Errorf("output dir is required")
	}
	absPath, err := filepath.Abs(trimmed)
	if err != nil {
		return "", fmt.Errorf("resolve output dir %q: %w", raw, err)
	}
	if info, statErr := os.Stat(absPath); statErr == nil {
		if info.IsDir() {
			return "", fmt.Errorf("output dir %q already exists", absPath)
		}
		return "", fmt.Errorf("output dir %q already exists and is not a directory", absPath)
	} else if !os.IsNotExist(statErr) {
		return "", fmt.Errorf("stat output dir %q: %w", absPath, statErr)
	}
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		return "", fmt.Errorf("create parent dir for output dir %q: %w", absPath, err)
	}
	if err := os.Mkdir(absPath, 0o755); err != nil {
		return "", fmt.Errorf("create output dir %q: %w", absPath, err)
	}
	return absPath, nil
}

func (s *Service) registerRunDir(runID, runDir string) error {
	trimmedRunID := strings.TrimSpace(runID)
	trimmedRunDir := strings.TrimSpace(runDir)
	if trimmedRunID == "" {
		return fmt.Errorf("run id is required")
	}
	if trimmedRunDir == "" {
		return fmt.Errorf("run dir is required")
	}
	if s.executorRunDirs != nil {
		if err := s.executorRunDirs.RegisterRunDir(trimmedRunID, trimmedRunDir); err != nil {
			return err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.runDirs[trimmedRunID]; ok && existing != trimmedRunDir {
		return fmt.Errorf("run dir already registered for run %s", trimmedRunID)
	}
	s.runDirs[trimmedRunID] = trimmedRunDir
	return nil
}

func executorRunDirRegistrar(executor engine.Executor) interface {
	RegisterRunDir(string, string) error
} {
	if executor == nil {
		return nil
	}
	registrar, _ := executor.(interface {
		RegisterRunDir(string, string) error
	})
	return registrar
}

func (s *Service) unregisterRunDir(runID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.runDirs, strings.TrimSpace(runID))
}

func (s *Service) runDir(runID string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	runDir := strings.TrimSpace(s.runDirs[strings.TrimSpace(runID)])
	if runDir == "" {
		return "", fmt.Errorf("run dir not registered for run %s", strings.TrimSpace(runID))
	}
	return runDir, nil
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
	runDir, err := s.runDir(runID)
	if err != nil {
		return err
	}
	if err := s.writeJSON(runfs.ManifestPath(runDir), manifest); err != nil {
		return err
	}
	results, err := s.runStore.ListInstanceResults(ctx, runID)
	if err != nil {
		return err
	}
	summary := runresults.Build(run, instances, results)
	if err := s.writeJSON(runfs.ResultsPath(runDir), summary); err != nil {
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
	if err := s.writeJSONLines(runfs.EventsPath(runDir), eventLines); err != nil {
		return err
	}
	if err := writeArtifactsIndex(runDir, artifacts); err != nil {
		return err
	}
	resultsByInstance, err := collectRunResults(ctx, s.runStore, instances)
	if err != nil {
		return err
	}
	if err := writeInstanceResults(runDir, instances, resultsByInstance, artifactsByInstance); err != nil {
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
