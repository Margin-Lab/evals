package runfs

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/marginlab/margin-eval/runner/runner-core/store"
)

const (
	dirInternal  = "internal"
	dirInstances = "instances"
	dirImage     = "image"
	dirBootstrap = "bootstrap"
	dirRun       = "run"
	dirOracle    = "oracle"
	dirTest      = "test"
	dirExtra     = "extra"
)

type ArtifactView struct {
	Stage string
	Key   string
}

func RunDir(baseDir, runID string) string {
	return filepath.Join(baseDir, "runs", strings.TrimSpace(runID))
}

func InternalDir(runDir string) string {
	return filepath.Join(strings.TrimSpace(runDir), dirInternal)
}

func InstancesDir(runDir string) string {
	return filepath.Join(strings.TrimSpace(runDir), dirInstances)
}

func InstanceDir(runDir, instanceID string) string {
	return filepath.Join(InstancesDir(runDir), strings.TrimSpace(instanceID))
}

func BundlePath(runDir string) string {
	return filepath.Join(InternalDir(runDir), "bundle.json")
}

func ManifestPath(runDir string) string {
	return filepath.Join(InternalDir(runDir), "manifest.json")
}

func ProgressPath(runDir string) string {
	return filepath.Join(InternalDir(runDir), "progress.json")
}

func EventsPath(runDir string) string {
	return filepath.Join(InternalDir(runDir), "events.jsonl")
}

func ArtifactsIndexPath(runDir string) string {
	return filepath.Join(InternalDir(runDir), "artifacts.json")
}

func ResultsPath(runDir string) string {
	return filepath.Join(strings.TrimSpace(runDir), "results.json")
}

func InstanceResultPath(runDir, instanceID string) string {
	return filepath.Join(InstanceDir(runDir, instanceID), "result.json")
}

func RelativeInstanceResultPath(instanceID string) string {
	return filepath.ToSlash(filepath.Join(dirInstances, strings.TrimSpace(instanceID), "result.json"))
}

func AbsoluteArtifactPath(runDir, instanceID, role string) (string, string, ArtifactView, bool) {
	rel, view, ok := RelativePathForRole(instanceID, role)
	if !ok {
		return "", "", ArtifactView{}, false
	}
	return filepath.Join(strings.TrimSpace(runDir), filepath.FromSlash(rel)), rel, view, true
}

func RelativePathForRole(instanceID, role string) (string, ArtifactView, bool) {
	fileName, ok := store.DefaultArtifactFilename(role)
	if !ok {
		return "", ArtifactView{}, false
	}
	stage, key, ok := viewForRole(role)
	if !ok {
		return "", ArtifactView{}, false
	}
	if stage == "" {
		return filepath.ToSlash(filepath.Join(dirInstances, strings.TrimSpace(instanceID), fileName)), ArtifactView{Stage: stage, Key: key}, true
	}
	return filepath.ToSlash(filepath.Join(dirInstances, strings.TrimSpace(instanceID), stage, fileName)), ArtifactView{Stage: stage, Key: key}, true
}

func RelativePathForArtifact(instanceID string, art store.Artifact, sourcePath string) (string, ArtifactView) {
	if rel, view, ok := RelativePathForRole(instanceID, art.Role); ok {
		return rel, view
	}
	name := sanitizeArtifactBaseName(art)
	ext := filepath.Ext(strings.TrimSpace(sourcePath))
	if ext != "" && !strings.HasSuffix(strings.ToLower(name), strings.ToLower(ext)) {
		name += ext
	}
	return filepath.ToSlash(filepath.Join(dirInstances, strings.TrimSpace(instanceID), dirRun, dirExtra, name)), ArtifactView{
		Stage: filepath.ToSlash(filepath.Join(dirRun, dirExtra)),
		Key:   strings.TrimSpace(art.Role),
	}
}

func ViewForRole(role string) (ArtifactView, bool) {
	stage, key, ok := viewForRole(role)
	if !ok {
		return ArtifactView{}, false
	}
	return ArtifactView{Stage: stage, Key: key}, true
}

func viewForRole(role string) (string, string, bool) {
	switch strings.TrimSpace(role) {
	case store.ArtifactRoleTrajectory:
		return "", "trajectory", true
	case store.ArtifactRoleDockerBuild:
		return dirImage, "docker_build_log", true
	case store.ArtifactRoleAgentBoot:
		return dirBootstrap, "agent_server_bootstrap_log", true
	case store.ArtifactRoleAgentControl:
		return dirRun, "agent_server_control_log", true
	case store.ArtifactRoleAgentRuntime:
		return dirRun, "agent_server_runtime_log", true
	case store.ArtifactRoleAgentPTY:
		return dirRun, "agent_server_pty_log", true
	case store.ArtifactRoleOracleStdout:
		return dirOracle, "oracle_stdout", true
	case store.ArtifactRoleOracleStderr:
		return dirOracle, "oracle_stderr", true
	case store.ArtifactRoleTestStdout:
		return dirTest, "test_stdout", true
	case store.ArtifactRoleTestStderr:
		return dirTest, "test_stderr", true
	default:
		return "", "", false
	}
}

func sanitizeArtifactBaseName(art store.Artifact) string {
	base := strings.TrimSpace(art.ArtifactID)
	if base == "" {
		base = strings.TrimSpace(art.Role)
	}
	if base == "" {
		base = fmt.Sprintf("artifact-%d", art.Ordinal)
	}
	replacer := strings.NewReplacer("/", "-", "\\", "-", " ", "-", "\t", "-", "\n", "-")
	return replacer.Replace(base)
}
