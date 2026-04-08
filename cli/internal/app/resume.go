package app

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/marginlab/margin-eval/runner/runner-core/runbundle"
	"github.com/marginlab/margin-eval/runner/runner-core/runnerapi"
	"github.com/marginlab/margin-eval/runner/runner-local/runfs"
)

type runSourceMode string

const (
	runSourceModeFresh          runSourceMode = "fresh"
	runSourceModeResumeExact    runSourceMode = "resume_exact"
	runSourceModeResumeOverride runSourceMode = "resume_override"
)

func resolveResumeMode(resumeFromRunID, raw string) (runnerapi.ResumeMode, error) {
	mode := runnerapi.ResumeMode(strings.TrimSpace(raw))
	if strings.TrimSpace(resumeFromRunID) == "" {
		if mode != runnerapi.DefaultResumeMode() {
			return "", fmt.Errorf("--resume-mode requires --resume-from")
		}
		return runnerapi.DefaultResumeMode(), nil
	}
	if err := mode.Validate(); err != nil {
		return "", fmt.Errorf("invalid --resume-mode: %w", err)
	}
	return mode, nil
}

func classifyRunSourceMode(resumeFromRunID, suitePath, agentConfigPath, evalPath string) (runSourceMode, error) {
	resumeFrom := strings.TrimSpace(resumeFromRunID)
	suite := strings.TrimSpace(suitePath)
	agentConfig := strings.TrimSpace(agentConfigPath)
	eval := strings.TrimSpace(evalPath)
	hasOverride := suite != "" || agentConfig != "" || eval != ""

	if resumeFrom == "" {
		if suite == "" {
			return "", fmt.Errorf("--suite is required")
		}
		if agentConfig == "" {
			return "", fmt.Errorf("--agent-config is required")
		}
		if eval == "" {
			return "", fmt.Errorf("--eval is required")
		}
		return runSourceModeFresh, nil
	}
	if !hasOverride {
		return runSourceModeResumeExact, nil
	}
	if suite == "" || agentConfig == "" || eval == "" {
		return "", fmt.Errorf("--resume-from with updated inputs requires --suite, --agent-config, and --eval")
	}
	return runSourceModeResumeOverride, nil
}

func validateRunSourceFlags(resumeFromRunID, suitePath, agentConfigPath, evalPath string) error {
	_, err := classifyRunSourceMode(resumeFromRunID, suitePath, agentConfigPath, evalPath)
	return err
}

func resumeBundlePolicyForMode(mode runSourceMode) runnerapi.ResumeBundlePolicy {
	switch mode {
	case runSourceModeResumeOverride:
		return runnerapi.ResumeBundlePolicyAllowMismatch
	case runSourceModeFresh, runSourceModeResumeExact:
		return runnerapi.ResumeBundlePolicyExact
	default:
		return runnerapi.ResumeBundlePolicyExact
	}
}

func isResumeMode(mode runSourceMode) bool {
	return mode == runSourceModeResumeExact || mode == runSourceModeResumeOverride
}

func isOverrideResumeMode(mode runSourceMode) bool {
	return mode == runSourceModeResumeOverride
}

func validateRunSourceMode(mode runSourceMode) error {
	switch mode {
	case runSourceModeFresh, runSourceModeResumeExact, runSourceModeResumeOverride:
		return nil
	default:
		return fmt.Errorf("invalid run source mode %q", mode)
	}
}

func savedRunBundlePath(rootDir, runID string) string {
	return runfs.BundlePath(rootDir, strings.TrimSpace(runID))
}

func loadSavedRunBundle(rootDir, runID string) (runbundle.Bundle, error) {
	path := savedRunBundlePath(rootDir, runID)
	body, err := os.ReadFile(path)
	if err != nil {
		return runbundle.Bundle{}, fmt.Errorf("read source bundle for resume: %w", err)
	}
	var bundle runbundle.Bundle
	if err := json.Unmarshal(body, &bundle); err != nil {
		return runbundle.Bundle{}, fmt.Errorf("decode source bundle for resume: %w", err)
	}
	return bundle, nil
}
