package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/marginlab/margin-eval/runner/runner-core/resume"
	"github.com/marginlab/margin-eval/runner/runner-core/runbundle"
	"github.com/marginlab/margin-eval/runner/runner-core/runnerapi"
	"github.com/marginlab/margin-eval/runner/runner-local/localrunner"
)

type preparedRunBundle struct {
	Bundle             runbundle.Bundle
	ResumeBundlePolicy runnerapi.ResumeBundlePolicy
	ResumeWarning      *resumeWarningSummary
}

type resumeWarningSummary struct {
	SourceRunID  string
	ReusedCount  int
	RerunCount   int
	AddedCount   int
	DroppedCount int
	PolicyText   string
}

func (a *App) prepareRunBundle(ctx context.Context, in runBundleInput, resumeMode runnerapi.ResumeMode) (preparedRunBundle, error) {
	sourceMode, err := classifyRunSourceMode(in.ResumeFromRunID, in.SuitePath, in.AgentConfigPath, in.EvalPath)
	if err != nil {
		return preparedRunBundle{}, err
	}
	if err := validateRunSourceMode(sourceMode); err != nil {
		return preparedRunBundle{}, err
	}

	out := preparedRunBundle{
		ResumeBundlePolicy: resumeBundlePolicyForMode(sourceMode),
	}

	switch sourceMode {
	case runSourceModeFresh:
		bundle, err := a.compileRunBundle(in)
		if err != nil {
			return preparedRunBundle{}, err
		}
		out.Bundle = bundle
		return out, nil
	case runSourceModeResumeExact:
		bundle, err := a.loadSavedResumeBundle(in.RootDir, in.ResumeFromRunID, in.NonInteractive)
		if err != nil {
			return preparedRunBundle{}, err
		}
		out.Bundle = bundle
		return out, nil
	case runSourceModeResumeOverride:
		bundle, err := a.compileRunBundle(in)
		if err != nil {
			return preparedRunBundle{}, err
		}
		snapshot, err := localrunner.LoadProgressSnapshot(in.RootDir, in.ResumeFromRunID)
		if err != nil {
			return preparedRunBundle{}, fmt.Errorf("load local resume progress for run %s: %w", strings.TrimSpace(in.ResumeFromRunID), err)
		}
		hash, err := runbundle.HashSHA256(bundle)
		if err != nil {
			return preparedRunBundle{}, fmt.Errorf("compute compiled resume bundle hash: %w", err)
		}
		plan, err := resume.BuildPlan(bundle, hash, snapshot, resumeMode, resume.BundlePolicyAllowMismatch)
		if err != nil {
			return preparedRunBundle{}, fmt.Errorf("build resume preview plan: %w", err)
		}
		out.Bundle = bundle
		if plan.HasBundleMismatch() {
			out.ResumeWarning = &resumeWarningSummary{
				SourceRunID:  strings.TrimSpace(in.ResumeFromRunID),
				ReusedCount:  len(plan.CarryByCase),
				RerunCount:   len(plan.RerunCaseIDs),
				AddedCount:   len(plan.AddedCaseIDs),
				DroppedCount: len(plan.DroppedCaseIDs),
				PolicyText:   resumeReusePolicyText(resumeMode),
			}
		}
		return out, nil
	default:
		return preparedRunBundle{}, fmt.Errorf("unsupported run source mode %q", sourceMode)
	}
}

func resumeReusePolicyText(mode runnerapi.ResumeMode) string {
	switch mode {
	case runnerapi.ResumeModeRetryFailed:
		return "Margin will reuse earlier successful or canceled results. Failed or incomplete cases will run again with the current inputs."
	case runnerapi.ResumeModeResume:
		return "Margin will reuse earlier completed results, except infrastructure failures, which will run again with the current inputs."
	default:
		return "Margin will reuse earlier results when it can and run the remaining cases with the current inputs."
	}
}

func resumeWarningLines(summary resumeWarningSummary) []string {
	lines := []string{
		fmt.Sprintf("Warning: the current suite, agent config, or eval config differs from saved run %s.", orUnknown(summary.SourceRunID)),
		fmt.Sprintf("Margin will reuse %d earlier result(s) and execute %d case(s) with the current inputs.", summary.ReusedCount, summary.RerunCount),
	}
	if summary.AddedCount > 0 {
		lines = append(lines, fmt.Sprintf("%d new case(s) were added in the current suite and will run.", summary.AddedCount))
	}
	if summary.DroppedCount > 0 {
		lines = append(lines, fmt.Sprintf("%d case(s) from the saved run are not present in the current suite and will be skipped.", summary.DroppedCount))
	}
	if strings.TrimSpace(summary.PolicyText) != "" {
		lines = append(lines, summary.PolicyText)
	}
	return lines
}
