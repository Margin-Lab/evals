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

func validateRunSourceFlags(resumeFromRunID, suitePath, agentConfigPath, evalPath string) error {
	if strings.TrimSpace(resumeFromRunID) == "" {
		if strings.TrimSpace(suitePath) == "" {
			return fmt.Errorf("--suite is required")
		}
		if strings.TrimSpace(agentConfigPath) == "" {
			return fmt.Errorf("--agent-config is required")
		}
		if strings.TrimSpace(evalPath) == "" {
			return fmt.Errorf("--eval is required")
		}
		return nil
	}

	var forbidden []string
	if strings.TrimSpace(suitePath) != "" {
		forbidden = append(forbidden, "--suite")
	}
	if strings.TrimSpace(agentConfigPath) != "" {
		forbidden = append(forbidden, "--agent-config")
	}
	if strings.TrimSpace(evalPath) != "" {
		forbidden = append(forbidden, "--eval")
	}
	if len(forbidden) > 0 {
		return fmt.Errorf("--resume-from infers the saved bundle; do not pass %s", strings.Join(forbidden, ", "))
	}
	return nil
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
