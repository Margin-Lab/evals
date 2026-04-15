package localrunner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/marginlab/margin-eval/runner/runner-core/domain"
	"github.com/marginlab/margin-eval/runner/runner-core/store"
	"github.com/marginlab/margin-eval/runner/runner-core/usage"
	"github.com/marginlab/margin-eval/runner/runner-local/runfs"
)

type instanceResultFile struct {
	InstanceID      string                  `json:"instance_id"`
	Ordinal         int                     `json:"ordinal"`
	CaseID          string                  `json:"case_id"`
	FinalState      domain.InstanceState    `json:"final_state"`
	ProviderRef     string                  `json:"provider_ref,omitempty"`
	AgentRunID      string                  `json:"agent_run_id,omitempty"`
	AgentExitCode   *int                    `json:"agent_exit_code,omitempty"`
	Usage           *usage.Metrics          `json:"usage,omitempty"`
	OracleExitCode  *int                    `json:"oracle_exit_code,omitempty"`
	TestExitCode    *int                    `json:"test_exit_code,omitempty"`
	ErrorCode       string                  `json:"error_code,omitempty"`
	ErrorMessage    string                  `json:"error_message,omitempty"`
	ErrorDetails    map[string]any          `json:"error_details,omitempty"`
	ProvisionedAt   *timeValue              `json:"provisioned_at,omitempty"`
	AgentStartedAt  *timeValue              `json:"agent_started_at,omitempty"`
	AgentEndedAt    *timeValue              `json:"agent_ended_at,omitempty"`
	OracleStartedAt *timeValue              `json:"oracle_started_at,omitempty"`
	OracleEndedAt   *timeValue              `json:"oracle_ended_at,omitempty"`
	TestStartedAt   *timeValue              `json:"test_started_at,omitempty"`
	TestEndedAt     *timeValue              `json:"test_ended_at,omitempty"`
	Trajectory      string                  `json:"trajectory,omitempty"`
	Files           instanceResultStageInfo `json:"files"`
}

type instanceResultStageInfo struct {
	Image     map[string]string `json:"image,omitempty"`
	Bootstrap map[string]string `json:"bootstrap,omitempty"`
	Run       map[string]string `json:"run,omitempty"`
	Oracle    map[string]string `json:"oracle,omitempty"`
	Test      map[string]string `json:"test,omitempty"`
}

type timeValue struct {
	value string
}

func (t timeValue) MarshalJSON() ([]byte, error) {
	return []byte(`"` + t.value + `"`), nil
}

func writeArtifactsIndex(runDir string, artifacts []store.Artifact) error {
	return writeJSONAtomic(runfs.ArtifactsIndexPath(runDir), artifacts)
}

func writeInstanceResults(runDir string, instances []store.Instance, resultsByInstance map[string]store.StoredInstanceResult, artifactsByInstance map[string][]store.Artifact) error {
	for _, inst := range instances {
		result, ok := resultsByInstance[inst.InstanceID]
		if !ok || !result.FinalState.IsTerminal() {
			continue
		}
		if err := writeJSONAtomic(runfs.InstanceResultPath(runDir, inst.InstanceID), buildInstanceResultFile(inst, result, artifactsByInstance[inst.InstanceID])); err != nil {
			return err
		}
	}
	return nil
}

func buildInstanceResultFile(inst store.Instance, result store.StoredInstanceResult, artifacts []store.Artifact) instanceResultFile {
	out := instanceResultFile{
		InstanceID:     inst.InstanceID,
		Ordinal:        inst.Ordinal,
		CaseID:         strings.TrimSpace(inst.Case.CaseID),
		FinalState:     result.FinalState,
		ProviderRef:    result.ProviderRef,
		AgentRunID:     result.AgentRunID,
		AgentExitCode:  result.AgentExitCode,
		Usage:          result.Usage,
		OracleExitCode: result.OracleExitCode,
		TestExitCode:   result.TestExitCode,
		ErrorCode:      result.ErrorCode,
		ErrorMessage:   result.ErrorMessage,
		ErrorDetails:   cloneAnyMap(result.ErrorDetails),
		Trajectory:     strings.TrimSpace(result.TrajectoryRef),
		Files:          instanceResultStageInfo{},
	}
	out.ProvisionedAt = marshalTime(result.ProvisionedAt)
	out.AgentStartedAt = marshalTime(result.AgentStartedAt)
	out.AgentEndedAt = marshalTime(result.AgentEndedAt)
	out.OracleStartedAt = marshalTime(result.OracleStartedAt)
	out.OracleEndedAt = marshalTime(result.OracleEndedAt)
	out.TestStartedAt = marshalTime(result.TestStartedAt)
	out.TestEndedAt = marshalTime(result.TestEndedAt)

	for _, art := range artifacts {
		view, ok := runfs.ViewForRole(art.Role)
		if !ok || strings.TrimSpace(view.Stage) == "" {
			continue
		}
		storeKey := strings.TrimSpace(art.StoreKey)
		if storeKey == "" {
			continue
		}
		switch view.Stage {
		case "image":
			if out.Files.Image == nil {
				out.Files.Image = map[string]string{}
			}
			out.Files.Image[view.Key] = storeKey
		case "bootstrap":
			if out.Files.Bootstrap == nil {
				out.Files.Bootstrap = map[string]string{}
			}
			out.Files.Bootstrap[view.Key] = storeKey
		case "run":
			if out.Files.Run == nil {
				out.Files.Run = map[string]string{}
			}
			out.Files.Run[view.Key] = storeKey
		case "oracle":
			if out.Files.Oracle == nil {
				out.Files.Oracle = map[string]string{}
			}
			out.Files.Oracle[view.Key] = storeKey
		case "test":
			if out.Files.Test == nil {
				out.Files.Test = map[string]string{}
			}
			out.Files.Test[view.Key] = storeKey
		}
	}
	return out
}

func marshalTime(in *time.Time) *timeValue {
	if in == nil || in.IsZero() {
		return nil
	}
	return &timeValue{value: in.UTC().Format(time.RFC3339Nano)}
}

func collectRunArtifacts(ctx context.Context, runStore store.RunStore, instances []store.Instance) (map[string][]store.Artifact, []store.Artifact, error) {
	byInstance := make(map[string][]store.Artifact, len(instances))
	all := make([]store.Artifact, 0)
	for _, inst := range instances {
		items, err := runStore.ListArtifacts(ctx, inst.InstanceID)
		if err != nil {
			return nil, nil, err
		}
		sortArtifacts(items)
		byInstance[inst.InstanceID] = items
		all = append(all, items...)
	}
	sortArtifacts(all)
	return byInstance, all, nil
}

func collectRunResults(ctx context.Context, runStore store.RunStore, instances []store.Instance) (map[string]store.StoredInstanceResult, error) {
	byInstance := make(map[string]store.StoredInstanceResult, len(instances))
	for _, inst := range instances {
		result, err := runStore.GetInstanceResult(ctx, inst.InstanceID)
		if err != nil {
			if err == store.ErrNotFound {
				continue
			}
			return nil, err
		}
		byInstance[inst.InstanceID] = result
	}
	return byInstance, nil
}

func sortArtifacts(artifacts []store.Artifact) {
	sort.Slice(artifacts, func(i, j int) bool {
		if artifacts[i].InstanceID != artifacts[j].InstanceID {
			return artifacts[i].InstanceID < artifacts[j].InstanceID
		}
		if artifacts[i].Role != artifacts[j].Role {
			return artifacts[i].Role < artifacts[j].Role
		}
		if artifacts[i].Ordinal != artifacts[j].Ordinal {
			return artifacts[i].Ordinal < artifacts[j].Ordinal
		}
		return artifacts[i].ArtifactID < artifacts[j].ArtifactID
	})
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
