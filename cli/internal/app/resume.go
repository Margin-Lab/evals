package app

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

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

func resolveResumeMode(resumeFromDir, raw string) (runnerapi.ResumeMode, error) {
	mode := runnerapi.ResumeMode(strings.TrimSpace(raw))
	if strings.TrimSpace(resumeFromDir) == "" {
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

func classifyRunSourceMode(resumeFromDir, suitePath, agentConfigPath, evalPath string) (runSourceMode, error) {
	resumeFrom := strings.TrimSpace(resumeFromDir)
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

func validateRunSourceFlags(resumeFromDir, suitePath, agentConfigPath, evalPath string) error {
	_, err := classifyRunSourceMode(resumeFromDir, suitePath, agentConfigPath, evalPath)
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

func savedRunBundlePath(runDir string) string {
	return runfs.BundlePath(strings.TrimSpace(runDir))
}

func loadSavedRunBundle(runDir string) (runbundle.Bundle, error) {
	path := savedRunBundlePath(runDir)
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

func defaultRunID() (string, error) {
	now := time.Now().UTC().Format("20060102_150405")
	var suffix [4]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return "", fmt.Errorf("generate run id suffix: %w", err)
	}
	return fmt.Sprintf("run_%s_%s", now, hex.EncodeToString(suffix[:])), nil
}

func resolveOutputDir(raw, runID string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		trimmed = runfs.RunDir(".", runID)
	}
	absPath, err := filepath.Abs(trimmed)
	if err != nil {
		return "", fmt.Errorf("resolve output dir %q: %w", trimmed, err)
	}
	if info, statErr := os.Stat(absPath); statErr == nil {
		if info.IsDir() {
			return "", fmt.Errorf("--output %q already exists", absPath)
		}
		return "", fmt.Errorf("--output %q already exists and is not a directory", absPath)
	} else if !os.IsNotExist(statErr) {
		return "", fmt.Errorf("stat --output %q: %w", absPath, statErr)
	}
	return absPath, nil
}

func resolveResumeFromDir(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", nil
	}
	absPath, err := filepath.Abs(trimmed)
	if err != nil {
		return "", fmt.Errorf("resolve --resume-from %q: %w", trimmed, err)
	}
	info, err := os.Stat(absPath)
	if err != nil {
		return "", fmt.Errorf("stat --resume-from %q: %w", absPath, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("--resume-from %q must be a directory", absPath)
	}
	for _, required := range []string{
		runfs.BundlePath(absPath),
		runfs.ProgressPath(absPath),
	} {
		requiredInfo, statErr := os.Stat(required)
		if statErr != nil {
			return "", fmt.Errorf("--resume-from %q is missing %s: %w", absPath, filepath.Base(required), statErr)
		}
		if requiredInfo.IsDir() {
			return "", fmt.Errorf("--resume-from %q has directory where file is required: %s", absPath, required)
		}
	}
	return absPath, nil
}

func validateRunDirRelationship(outputDir, resumeFromDir string) error {
	trimmedOutput := strings.TrimSpace(outputDir)
	trimmedResume := strings.TrimSpace(resumeFromDir)
	if trimmedOutput == "" || trimmedResume == "" {
		return nil
	}
	if trimmedOutput == trimmedResume {
		return fmt.Errorf("--output and --resume-from must refer to different directories")
	}
	outputWithSep := trimmedOutput + string(os.PathSeparator)
	resumeWithSep := trimmedResume + string(os.PathSeparator)
	if strings.HasPrefix(outputWithSep, resumeWithSep) {
		return fmt.Errorf("--output must not be nested inside --resume-from")
	}
	if strings.HasPrefix(resumeWithSep, outputWithSep) {
		return fmt.Errorf("--resume-from must not be nested inside --output")
	}
	return nil
}
