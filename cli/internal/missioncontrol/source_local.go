package missioncontrol

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/marginlab/margin-eval/cli/internal/datasource"

	"github.com/marginlab/margin-eval/runner/runner-core/runnerapi"
	"github.com/marginlab/margin-eval/runner/runner-core/store"
)

// LocalSource adapts a local runner datasource to the mission-control Source contract.
type LocalSource struct {
	snapshots    datasource.Source
	artifactRoot string
}

func isPTYLogRole(role string) bool {
	return strings.EqualFold(strings.TrimSpace(role), store.ArtifactRoleAgentPTY)
}

func NewLocalSource(snapshots datasource.Source, artifactRoot string) (*LocalSource, error) {
	if snapshots == nil {
		return nil, fmt.Errorf("local snapshot datasource is required")
	}
	resolvedRoot := ""
	if strings.TrimSpace(artifactRoot) != "" {
		absRoot, err := filepath.Abs(strings.TrimSpace(artifactRoot))
		if err != nil {
			return nil, fmt.Errorf("resolve artifact root: %w", err)
		}
		resolvedRoot = absRoot
	}
	return &LocalSource{snapshots: snapshots, artifactRoot: resolvedRoot}, nil
}

func (s *LocalSource) GetRunSnapshot(ctx context.Context, runID string) (runnerapi.RunSnapshot, error) {
	snapshot, err := s.snapshots.GetRunSnapshot(ctx, runID, runnerapi.SnapshotOptions{
		IncludeInstanceResults:   true,
		IncludeInstanceArtifacts: true,
	})
	if err != nil {
		return runnerapi.RunSnapshot{}, err
	}
	s.injectLiveArtifacts(&snapshot)
	return snapshot, nil
}

func (s *LocalSource) ReadArtifactText(_ context.Context, artifact store.Artifact, maxBytes int64) (ArtifactText, error) {
	path, err := s.resolveArtifactPath(artifact)
	if err != nil {
		return ArtifactText{}, err
	}
	if isPTYLogRole(artifact.Role) {
		return readTextFileTail(path, maxBytes)
	}
	return readTextFile(path, maxBytes)
}

func (s *LocalSource) resolveArtifactPath(artifact store.Artifact) (string, error) {
	uri := strings.TrimSpace(artifact.URI)
	if uri != "" {
		parsed, err := url.Parse(uri)
		if err != nil {
			return "", fmt.Errorf("parse artifact URI %q: %w", uri, err)
		}
		if parsed.Scheme == "file" {
			if parsed.Path == "" {
				return "", fmt.Errorf("artifact URI %q has empty path", uri)
			}
			return filepath.Clean(parsed.Path), nil
		}
	}

	if s.artifactRoot == "" {
		return "", fmt.Errorf("artifact %q has no file URI and no local artifact root configured", artifact.ArtifactID)
	}
	if strings.TrimSpace(artifact.StoreKey) == "" {
		return "", fmt.Errorf("artifact %q has no URI or store_key", artifact.ArtifactID)
	}
	return filepath.Clean(filepath.Join(s.artifactRoot, filepath.FromSlash(artifact.StoreKey))), nil
}

func readTextFile(path string, maxBytes int64) (ArtifactText, error) {
	if maxBytes <= 0 {
		maxBytes = DefaultTextPreviewLimit
	}
	f, err := os.Open(path)
	if err != nil {
		return ArtifactText{}, fmt.Errorf("open %q: %w", path, err)
	}
	defer f.Close()

	data, err := io.ReadAll(io.LimitReader(f, maxBytes+1))
	if err != nil {
		return ArtifactText{}, fmt.Errorf("read %q: %w", path, err)
	}
	if int64(len(data)) > maxBytes {
		return ArtifactText{Text: string(data[:maxBytes]), Truncated: true}, nil
	}
	return ArtifactText{Text: string(data)}, nil
}

func readTextFileTail(path string, maxBytes int64) (ArtifactText, error) {
	if maxBytes <= 0 {
		maxBytes = DefaultTextPreviewLimit
	}
	f, err := os.Open(path)
	if err != nil {
		return ArtifactText{}, fmt.Errorf("open %q: %w", path, err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return ArtifactText{}, fmt.Errorf("stat %q: %w", path, err)
	}
	start := int64(0)
	truncated := false
	if info.Size() > maxBytes {
		start = info.Size() - maxBytes
		truncated = true
	}
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return ArtifactText{}, fmt.Errorf("seek %q: %w", path, err)
	}

	data, err := io.ReadAll(io.LimitReader(f, maxBytes+1))
	if err != nil {
		return ArtifactText{}, fmt.Errorf("read %q: %w", path, err)
	}
	if int64(len(data)) > maxBytes {
		data = data[len(data)-int(maxBytes):]
		truncated = true
	}
	return ArtifactText{Text: string(data), Truncated: truncated}, nil
}

func (s *LocalSource) injectLiveArtifacts(snapshot *runnerapi.RunSnapshot) {
	if snapshot == nil || strings.TrimSpace(s.artifactRoot) == "" {
		return
	}
	runID := strings.TrimSpace(snapshot.Run.RunID)
	if runID == "" {
		return
	}
	liveRoles := []string{
		store.ArtifactRoleDockerBuild,
		store.ArtifactRoleAgentBoot,
		store.ArtifactRoleAgentControl,
		store.ArtifactRoleAgentRuntime,
		store.ArtifactRoleAgentPTY,
	}
	for i := range snapshot.Instances {
		inst := &snapshot.Instances[i]
		instanceID := strings.TrimSpace(inst.Instance.InstanceID)
		if instanceID == "" {
			continue
		}
		existingByRole := map[string]struct{}{}
		for _, item := range inst.Artifacts {
			existingByRole[strings.ToLower(strings.TrimSpace(item.Role))] = struct{}{}
		}
		for ord, role := range liveRoles {
			if _, exists := existingByRole[strings.ToLower(role)]; exists {
				continue
			}
			fileName, ok := store.DefaultArtifactFilename(role)
			if !ok {
				continue
			}
			path := filepath.Join(s.artifactRoot, runID, instanceID, fileName)
			info, err := os.Stat(path)
			if err != nil || info.IsDir() {
				continue
			}
			rel, err := filepath.Rel(s.artifactRoot, path)
			if err != nil {
				continue
			}
			inst.Artifacts = append(inst.Artifacts, store.Artifact{
				ArtifactID:  fmt.Sprintf("live-%s-%s", instanceID, strings.ReplaceAll(role, "_", "-")),
				RunID:       runID,
				InstanceID:  instanceID,
				Role:        role,
				Ordinal:     100 + ord,
				StoreKey:    filepath.ToSlash(rel),
				URI:         "file://" + path,
				ContentType: "text/plain",
				ByteSize:    info.Size(),
			})
		}
	}
}
