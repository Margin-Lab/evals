package localrunner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/marginlab/margin-eval/runner/runner-core/engine"
	"github.com/marginlab/margin-eval/runner/runner-core/runbundle"
	"github.com/marginlab/margin-eval/runner/runner-core/runnerapi"
	"github.com/marginlab/margin-eval/runner/runner-core/runresults"
	"github.com/marginlab/margin-eval/runner/runner-core/store"
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
	hash, err := runbundle.HashSHA256(bundle)
	if err != nil {
		return store.Run{}, fmt.Errorf("compute bundle hash: %w", err)
	}

	resumeFromRunID := strings.TrimSpace(in.ResumeFromRunID)
	if resumeFromRunID != "" {
		return s.submitResumedRun(ctx, in, bundle, hash, resumeFromRunID)
	}
	run, err := s.createRun(ctx, in, bundle, hash)
	if err != nil {
		return store.Run{}, err
	}
	if err := s.writeJSON(filepath.Join(s.runDir(run.RunID), "bundle.json"), bundle); err != nil {
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
	if err := s.writeJSON(filepath.Join(s.runDir(runID), "manifest.json"), manifest); err != nil {
		return err
	}
	results, err := s.runStore.ListInstanceResults(ctx, runID)
	if err != nil {
		return err
	}
	summary := runresults.Build(run, instances, results)
	if err := s.writeJSON(filepath.Join(s.runDir(runID), "results.json"), summary); err != nil {
		return err
	}

	eventLines := make([]map[string]any, 0, len(runEvents)+len(instances)*2)
	for _, ev := range runEvents {
		eventLines = append(eventLines, map[string]any{"type": "run_event", "event": ev})
	}
	artifacts := make([]store.Artifact, 0)
	for _, inst := range instances {
		instanceEvents, err := s.runStore.ListInstanceEvents(ctx, inst.InstanceID)
		if err != nil {
			return err
		}
		for _, ev := range instanceEvents {
			eventLines = append(eventLines, map[string]any{"type": "instance_event", "event": ev})
		}
		items, err := s.runStore.ListArtifacts(ctx, inst.InstanceID)
		if err != nil {
			return err
		}
		for _, item := range items {
			localized, err := s.localizeArtifact(runID, item)
			if err != nil {
				return err
			}
			artifacts = append(artifacts, localized)
		}
	}
	sort.Slice(artifacts, func(i, j int) bool {
		if artifacts[i].Role == artifacts[j].Role {
			return artifacts[i].Ordinal < artifacts[j].Ordinal
		}
		return artifacts[i].Role < artifacts[j].Role
	})
	if err := s.writeJSONLines(filepath.Join(s.runDir(runID), "events.jsonl"), eventLines); err != nil {
		return err
	}
	if err := s.writeJSON(filepath.Join(s.runDir(runID), "artifacts", "metadata.json"), artifacts); err != nil {
		return err
	}
	return nil
}

func (s *Service) runDir(runID string) string {
	return filepath.Join(s.rootDir, "runs", runID)
}

func (s *Service) localizeArtifact(runID string, item store.Artifact) (store.Artifact, error) {
	sourcePath, err := fileURIPath(item.URI)
	if err != nil {
		return store.Artifact{}, fmt.Errorf("artifact %q has unsupported URI %q: %w", item.ArtifactID, item.URI, err)
	}
	info, err := os.Stat(sourcePath)
	if err != nil {
		return store.Artifact{}, fmt.Errorf("stat artifact source %q: %w", sourcePath, err)
	}
	if info.IsDir() {
		return store.Artifact{}, fmt.Errorf("artifact source %q must be a file", sourcePath)
	}

	artifactName := sanitizeArtifactName(item)
	ext := filepath.Ext(sourcePath)
	if ext != "" && !strings.HasSuffix(strings.ToLower(artifactName), strings.ToLower(ext)) {
		artifactName += ext
	}
	destRelPath := filepath.ToSlash(filepath.Join("artifacts", "files", artifactName))
	destPath := filepath.Join(s.runDir(runID), filepath.FromSlash(destRelPath))
	if err := copyFile(sourcePath, destPath); err != nil {
		return store.Artifact{}, fmt.Errorf("copy artifact payload %q -> %q: %w", sourcePath, destPath, err)
	}

	sha, err := fileSHA256(destPath)
	if err != nil {
		return store.Artifact{}, err
	}
	item.StoreKey = destRelPath
	item.URI = "file://" + destPath
	item.ByteSize = info.Size()
	item.SHA256 = sha
	return item, nil
}

func fileURIPath(rawURI string) (string, error) {
	u, err := url.Parse(rawURI)
	if err != nil {
		return "", fmt.Errorf("parse URI: %w", err)
	}
	if u.Scheme != "file" {
		return "", fmt.Errorf("only file:// artifact URIs are supported")
	}
	if u.Path == "" {
		return "", fmt.Errorf("empty file URI path")
	}
	return filepath.Clean(u.Path), nil
}

func sanitizeArtifactName(item store.Artifact) string {
	base := strings.TrimSpace(item.ArtifactID)
	if base == "" {
		base = strings.TrimSpace(item.Role)
	}
	if base == "" {
		base = fmt.Sprintf("artifact-%d", item.Ordinal)
	}
	replacer := strings.NewReplacer("/", "-", "\\", "-", " ", "-", "\t", "-", "\n", "-")
	return replacer.Replace(base)
}

func copyFile(srcPath, destPath string) error {
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return fmt.Errorf("mkdir %q: %w", filepath.Dir(destPath), err)
	}
	src, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer src.Close()

	dest, err := os.Create(destPath)
	if err != nil {
		return err
	}
	defer dest.Close()
	if _, err := io.Copy(dest, src); err != nil {
		return err
	}
	return nil
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open artifact payload %q: %w", path, err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("hash artifact payload %q: %w", path, err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
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
