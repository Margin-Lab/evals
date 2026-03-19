package localrunner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/marginlab/margin-eval/runner/runner-core/resume"
	"github.com/marginlab/margin-eval/runner/runner-core/runbundle"
	"github.com/marginlab/margin-eval/runner/runner-core/runnerapi"
	"github.com/marginlab/margin-eval/runner/runner-core/store"
	"github.com/marginlab/margin-eval/runner/runner-core/usage"
)

func (s *Service) submitResumedRun(ctx context.Context, in runnerapi.SubmitInput, bundle runbundle.Bundle, hash, resumeFromRunID string) (store.Run, error) {
	snapshot, err := loadProgressSnapshot(s.rootDir, resumeFromRunID)
	if err != nil {
		return store.Run{}, fmt.Errorf("load local resume progress for run %s: %w", resumeFromRunID, err)
	}
	plan, err := resume.BuildPlan(bundle, hash, snapshot, in.ResumeMode)
	if err != nil {
		return store.Run{}, fmt.Errorf("build resume plan: %w", err)
	}

	bundle.Source.Kind = runbundle.SourceKindRunSnapshot
	bundle.Source.OriginRunID = plan.OriginRunID
	run, err := s.createRun(ctx, in, bundle, hash)
	if err != nil {
		return store.Run{}, err
	}
	if err := s.writeJSON(filepath.Join(s.runDir(run.RunID), "bundle.json"), bundle); err != nil {
		return store.Run{}, err
	}
	if err := s.carryForwardLocalCases(ctx, run.RunID, plan); err != nil {
		return store.Run{}, err
	}
	return run, nil
}

func (s *Service) carryForwardLocalCases(ctx context.Context, runID string, plan resume.Plan) error {
	if len(plan.CarryByCase) == 0 {
		return nil
	}
	instances, err := s.runStore.ListInstances(ctx, runID, nil)
	if err != nil {
		return err
	}
	instanceByCaseID := make(map[string]store.Instance, len(instances))
	for _, inst := range instances {
		instanceByCaseID[strings.TrimSpace(inst.Case.CaseID)] = inst
	}
	caseIDs := make([]string, 0, len(plan.CarryByCase))
	for caseID := range plan.CarryByCase {
		caseIDs = append(caseIDs, caseID)
	}
	sort.Strings(caseIDs)
	for _, caseID := range caseIDs {
		item := plan.CarryByCase[caseID]
		inst, ok := instanceByCaseID[caseID]
		if !ok {
			return fmt.Errorf("carry-forward case %q has no target instance in resumed run", caseID)
		}
		result := storedToInstanceResult(item.Result)
		artifacts, rewritten, err := s.copyCarriedArtifacts(runID, inst.InstanceID, result, item)
		if err != nil {
			return fmt.Errorf("copy carry-forward artifacts for case %q: %w", caseID, err)
		}
		if err := s.runStore.CarryForwardInstance(ctx, store.CarryForwardInput{
			RunID:            runID,
			InstanceID:       inst.InstanceID,
			SourceRunID:      item.SourceRunID,
			SourceInstanceID: item.SourceInstanceID,
			ProviderRef:      item.ProviderRef,
			Result:           rewritten,
			Artifacts:        artifacts,
		}, s.now()); err != nil {
			return fmt.Errorf("carry-forward case %q: %w", caseID, err)
		}
	}
	return nil
}

func (s *Service) copyCarriedArtifacts(runID, instanceID string, result store.InstanceResult, item resume.CompletedCase) ([]store.Artifact, store.InstanceResult, error) {
	if len(item.Artifacts) == 0 {
		return nil, result, nil
	}
	executorRoot := filepath.Join(s.rootDir, "executor-artifacts")
	destDir := filepath.Join(executorRoot, runID, instanceID, "carried")
	copied := make([]store.Artifact, 0, len(item.Artifacts))
	storeKeyMap := map[string]string{}
	uriMap := map[string]string{}
	for idx := range item.Artifacts {
		src := item.Artifacts[idx]
		sourcePath, err := fileURIPath(src.URI)
		if err != nil {
			return nil, store.InstanceResult{}, err
		}
		name := fmt.Sprintf("%03d-%s", idx+1, sanitizeArtifactName(src))
		ext := filepath.Ext(sourcePath)
		if ext != "" && !strings.HasSuffix(strings.ToLower(name), strings.ToLower(ext)) {
			name += ext
		}
		destPath := filepath.Join(destDir, name)
		if err := copyFile(sourcePath, destPath); err != nil {
			return nil, store.InstanceResult{}, err
		}
		sha, err := fileSHA256(destPath)
		if err != nil {
			return nil, store.InstanceResult{}, err
		}
		info, err := os.Stat(destPath)
		if err != nil {
			return nil, store.InstanceResult{}, err
		}
		rel, err := filepath.Rel(executorRoot, destPath)
		if err != nil {
			return nil, store.InstanceResult{}, err
		}
		storeKey := filepath.ToSlash(rel)
		meta := cloneAnyMap(src.Metadata)
		meta["carried_forward"] = true
		meta["source_run_id"] = item.SourceRunID
		meta["source_instance_id"] = item.SourceInstanceID
		copiedArt := src
		copiedArt.ArtifactID = ""
		copiedArt.RunID = runID
		copiedArt.InstanceID = instanceID
		copiedArt.AttemptID = ""
		copiedArt.StoreKey = storeKey
		copiedArt.URI = "file://" + destPath
		copiedArt.Metadata = meta
		copiedArt.SHA256 = sha
		copiedArt.ByteSize = info.Size()
		copied = append(copied, copiedArt)
		if strings.TrimSpace(src.StoreKey) != "" {
			storeKeyMap[src.StoreKey] = storeKey
		}
		if strings.TrimSpace(src.URI) != "" {
			uriMap[src.URI] = storeKey
		}
	}

	result.Trajectory = rewriteRef(result.Trajectory, storeKeyMap, uriMap)
	result.TestStdoutRef = rewriteRef(result.TestStdoutRef, storeKeyMap, uriMap)
	result.TestStderrRef = rewriteRef(result.TestStderrRef, storeKeyMap, uriMap)
	return copied, result, nil
}

func rewriteRef(ref string, byStoreKey, byURI map[string]string) string {
	trimmed := strings.TrimSpace(ref)
	if trimmed == "" {
		return ""
	}
	if v, ok := byStoreKey[trimmed]; ok {
		return v
	}
	if v, ok := byURI[trimmed]; ok {
		return v
	}
	return ref
}

func storedToInstanceResult(in store.StoredInstanceResult) store.InstanceResult {
	return store.InstanceResult{
		FinalState:     in.FinalState,
		AgentRunID:     in.AgentRunID,
		AgentExitCode:  in.AgentExitCode,
		Trajectory:     in.TrajectoryRef,
		Usage:          usage.Clone(in.Usage),
		TestExitCode:   in.TestExitCode,
		TestStdoutRef:  in.TestStdoutRef,
		TestStderrRef:  in.TestStderrRef,
		ErrorCode:      in.ErrorCode,
		ErrorMessage:   in.ErrorMessage,
		ErrorDetails:   in.ErrorDetails,
		ProvisionedAt:  in.ProvisionedAt,
		AgentStartedAt: in.AgentStartedAt,
		AgentEndedAt:   in.AgentEndedAt,
		TestStartedAt:  in.TestStartedAt,
		TestEndedAt:    in.TestEndedAt,
	}
}

func cloneAnyMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return map[string]any{}
	}
	out := make(map[string]any, len(in)+3)
	for k, v := range in {
		out[k] = v
	}
	return out
}
