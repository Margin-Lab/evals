package runbundle

import (
	"fmt"
	"path"
	"regexp"
	"strings"

	"github.com/marginlab/margin-eval/runner/runner-core/agentdef"
	"github.com/marginlab/margin-eval/runner/runner-core/testassets"
)

var digestImagePattern = regexp.MustCompile(`^[^\s@]+@sha256:[a-f0-9]{64}$`)
var gitCommitPattern = regexp.MustCompile(`^[a-f0-9]{40}$`)

func Validate(b Bundle) error {
	if strings.TrimSpace(b.SchemaVersion) != SchemaVersionV1 {
		return fmt.Errorf("schema_version must be %q", SchemaVersionV1)
	}
	if strings.TrimSpace(b.BundleID) == "" {
		return fmt.Errorf("bundle_id is required")
	}
	if b.CreatedAt.IsZero() {
		return fmt.Errorf("created_at is required")
	}
	if err := validateSource(b.Source); err != nil {
		return err
	}
	if err := validateResolvedSnapshot(b.ResolvedSnapshot); err != nil {
		return err
	}
	return nil
}

func validateSource(src Source) error {
	switch src.Kind {
	case SourceKindLocalFiles, SourceKindCatalogRefs, SourceKindRunSnapshot:
	default:
		return fmt.Errorf("source.kind must be one of %q, %q, %q", SourceKindLocalFiles, SourceKindCatalogRefs, SourceKindRunSnapshot)
	}
	if src.Kind == SourceKindCatalogRefs {
		if src.CatalogRefs == nil {
			return fmt.Errorf("source.catalog_refs is required when source.kind=%q", SourceKindCatalogRefs)
		}
		if err := validateCatalogRefs(*src.CatalogRefs); err != nil {
			return err
		}
	}
	if src.SuiteGit != nil {
		if src.Kind != SourceKindLocalFiles && src.Kind != SourceKindRunSnapshot {
			return fmt.Errorf("source.suite_git requires source.kind=%q or %q", SourceKindLocalFiles, SourceKindRunSnapshot)
		}
		if err := validateSuiteGitRef(*src.SuiteGit); err != nil {
			return fmt.Errorf("source.suite_git: %w", err)
		}
	}
	return nil
}

func validateCatalogRefs(refs CatalogRefs) error {
	if refs.Suite == nil || refs.AgentConfig == nil || refs.EvalConfig == nil {
		return fmt.Errorf("catalog_refs suite, agent_config, and eval_config are required")
	}
	if err := validateCatalogRef(*refs.Suite); err != nil {
		return fmt.Errorf("suite ref: %w", err)
	}
	if refs.AgentDefinition != nil {
		if err := validateCatalogRef(*refs.AgentDefinition); err != nil {
			return fmt.Errorf("agent_definition ref: %w", err)
		}
	}
	if err := validateCatalogRef(*refs.AgentConfig); err != nil {
		return fmt.Errorf("agent_config ref: %w", err)
	}
	if err := validateCatalogRef(*refs.EvalConfig); err != nil {
		return fmt.Errorf("eval_config ref: %w", err)
	}
	return nil
}

func validateCatalogRef(ref CatalogRef) error {
	if strings.TrimSpace(ref.ResourceID) == "" {
		return fmt.Errorf("resource_id is required")
	}
	if ref.Version <= 0 {
		return fmt.Errorf("version must be > 0")
	}
	if strings.TrimSpace(ref.ProjectID) == "" {
		return fmt.Errorf("project_id is required")
	}
	if ref.Visibility != "" && ref.Visibility != VisibilityPrivate && ref.Visibility != VisibilityPublic {
		return fmt.Errorf("visibility must be %q or %q", VisibilityPrivate, VisibilityPublic)
	}
	return nil
}

func validateSuiteGitRef(ref SuiteGitRef) error {
	if strings.TrimSpace(ref.RepoURL) == "" {
		return fmt.Errorf("repo_url is required")
	}
	if strings.TrimSpace(ref.ResolvedCommit) == "" {
		return fmt.Errorf("resolved_commit is required")
	}
	if !gitCommitPattern.MatchString(strings.TrimSpace(ref.ResolvedCommit)) {
		return fmt.Errorf("resolved_commit must be a 40-character lowercase hex sha")
	}
	subdir := strings.TrimSpace(ref.Subdir)
	if subdir == "" {
		return nil
	}
	if strings.Contains(subdir, "\\") {
		return fmt.Errorf("subdir must use slash-separated relative paths")
	}
	normalized := path.Clean(subdir)
	if normalized == "." {
		return nil
	}
	if strings.HasPrefix(normalized, "/") {
		return fmt.Errorf("subdir must be relative")
	}
	if normalized == ".." || strings.HasPrefix(normalized, "../") {
		return fmt.Errorf("subdir must not escape the repository")
	}
	return nil
}

func validateResolvedSnapshot(s ResolvedSnapshot) error {
	if strings.TrimSpace(s.Name) == "" {
		return fmt.Errorf("resolved_snapshot.name is required")
	}
	switch s.Execution.Mode {
	case ExecutionModeFull, ExecutionModeDryRun:
	default:
		return fmt.Errorf(
			"resolved_snapshot.execution.mode must be %q or %q",
			ExecutionModeFull,
			ExecutionModeDryRun,
		)
	}
	if s.Execution.MaxConcurrency <= 0 {
		return fmt.Errorf("resolved_snapshot.execution.max_concurrency must be > 0")
	}
	if s.Execution.RetryCount < 0 {
		return fmt.Errorf("resolved_snapshot.execution.retry_count must be >= 0")
	}
	if s.Execution.InstanceTimeoutSecond <= 0 {
		return fmt.Errorf("resolved_snapshot.execution.instance_timeout_seconds must be > 0")
	}
	if err := agentdef.ValidateDefinitionSnapshot(s.Agent.Definition); err != nil {
		return fmt.Errorf("resolved_snapshot.agent.definition: %w", err)
	}
	if _, err := agentdef.ValidateAndNormalizeConfigSpec(s.Agent.Definition, s.Agent.Config); err != nil {
		return fmt.Errorf("resolved_snapshot.agent.config: %w", err)
	}
	if strings.TrimSpace(s.RunDefaults.Cwd) == "" {
		return fmt.Errorf("resolved_snapshot.run_defaults.cwd is required")
	}
	if s.RunDefaults.PTY.Cols < 0 || s.RunDefaults.PTY.Rows < 0 {
		return fmt.Errorf("resolved_snapshot.run_defaults.pty cols/rows must be >= 0")
	}
	if len(s.Cases) == 0 {
		return fmt.Errorf("resolved_snapshot.cases must be non-empty")
	}
	for i, c := range s.Cases {
		if err := validateCase(c); err != nil {
			return fmt.Errorf("case[%d]: %w", i, err)
		}
	}
	return nil
}

func validateCase(c Case) error {
	if strings.TrimSpace(c.CaseID) == "" {
		return fmt.Errorf("case_id is required")
	}
	hasImage := strings.TrimSpace(c.Image) != ""
	hasBuild := c.ImageBuild != nil
	if hasImage == hasBuild {
		return fmt.Errorf("case must set exactly one of image or image_build")
	}
	if hasImage && !digestImagePattern.MatchString(strings.TrimSpace(c.Image)) {
		return fmt.Errorf("image must be digest-pinned using @sha256")
	}
	if hasBuild {
		if err := validateCaseImageBuild(*c.ImageBuild); err != nil {
			return fmt.Errorf("image_build: %w", err)
		}
	}
	if strings.TrimSpace(c.InitialPrompt) == "" {
		return fmt.Errorf("initial_prompt is required")
	}
	if len(c.TestCommand) == 0 {
		return fmt.Errorf("test_command must be non-empty")
	}
	if strings.TrimSpace(c.TestCwd) == "" {
		return fmt.Errorf("test_cwd is required")
	}
	if c.TestTimeoutSecond <= 0 {
		return fmt.Errorf("test_timeout_seconds must be > 0")
	}
	if err := testassets.ValidateDescriptor(c.TestAssets, testassets.DefaultMaxArchiveBytes); err != nil {
		return fmt.Errorf("test_assets: %w", err)
	}
	return nil
}

func validateCaseImageBuild(spec CaseImageBuild) error {
	if err := testassets.ValidateDescriptor(spec.Context, testassets.DefaultMaxArchiveBytes); err != nil {
		return fmt.Errorf("context: %w", err)
	}
	dockerfileRelPath := strings.TrimSpace(spec.DockerfileRelPath)
	if dockerfileRelPath == "" {
		return fmt.Errorf("dockerfile_rel_path is required")
	}
	if strings.Contains(dockerfileRelPath, "\\") {
		return fmt.Errorf("dockerfile_rel_path must use slash-separated relative paths")
	}
	normalized := path.Clean(dockerfileRelPath)
	if strings.HasPrefix(normalized, "/") {
		return fmt.Errorf("dockerfile_rel_path must be relative")
	}
	if normalized == "." || normalized == ".." || strings.HasPrefix(normalized, "../") {
		return fmt.Errorf("dockerfile_rel_path must not escape build context")
	}
	return nil
}
