package runbundle

import (
	"time"

	"github.com/marginlab/margin-eval/runner/runner-core/agentdef"
	"github.com/marginlab/margin-eval/runner/runner-core/testassets"
)

const SchemaVersionV1 = "1.0"

type SourceKind string

const (
	SourceKindLocalFiles  SourceKind = "local_files"
	SourceKindCatalogRefs SourceKind = "catalog_refs"
	SourceKindRunSnapshot SourceKind = "run_snapshot"
)

type Visibility string

const (
	VisibilityPrivate Visibility = "private"
	VisibilityPublic  Visibility = "public"
)

type Bundle struct {
	SchemaVersion    string           `json:"schema_version"`
	BundleID         string           `json:"bundle_id"`
	CreatedAt        time.Time        `json:"created_at"`
	Source           Source           `json:"source"`
	ResolvedSnapshot ResolvedSnapshot `json:"resolved_snapshot"`
	Integrity        *Integrity       `json:"integrity,omitempty"`
}

type Source struct {
	Kind            SourceKind   `json:"kind"`
	SubmitProjectID string       `json:"submit_project_id,omitempty"`
	CatalogRefs     *CatalogRefs `json:"catalog_refs,omitempty"`
	OriginRunID     string       `json:"origin_run_id,omitempty"`
	SuiteGit        *SuiteGitRef `json:"suite_git,omitempty"`
}

type CatalogRefs struct {
	Suite           *CatalogRef `json:"suite,omitempty"`
	AgentDefinition *CatalogRef `json:"agent_definition,omitempty"`
	AgentConfig     *CatalogRef `json:"agent_config,omitempty"`
	EvalConfig      *CatalogRef `json:"eval_config,omitempty"`
}

type CatalogRef struct {
	ResourceID string     `json:"resource_id"`
	Version    int        `json:"version"`
	ProjectID  string     `json:"project_id"`
	Visibility Visibility `json:"visibility,omitempty"`
}

type SuiteGitRef struct {
	RepoURL        string `json:"repo_url"`
	ResolvedCommit string `json:"resolved_commit"`
	Subdir         string `json:"subdir,omitempty"`
}

type ResolvedSnapshot struct {
	Name        string     `json:"name"`
	Execution   Execution  `json:"execution"`
	Agent       Agent      `json:"agent"`
	RunDefaults RunDefault `json:"run_defaults"`
	Cases       []Case     `json:"cases"`
}

type ExecutionMode string

const (
	ExecutionModeFull   ExecutionMode = "full"
	ExecutionModeDryRun ExecutionMode = "dry_run"
)

type Execution struct {
	Mode                  ExecutionMode `json:"mode"`
	MaxConcurrency        int           `json:"max_concurrency"`
	FailFast              bool          `json:"fail_fast"`
	RetryCount            int           `json:"retry_count"`
	InstanceTimeoutSecond int           `json:"instance_timeout_seconds"`
}

type Agent struct {
	Definition agentdef.DefinitionSnapshot `json:"definition"`
	Config     agentdef.ConfigSpec         `json:"config"`
}

type RunDefault struct {
	Env map[string]string `json:"env"`
	PTY PTY               `json:"pty"`
}

type PTY struct {
	Cols int `json:"cols"`
	Rows int `json:"rows"`
}

type Case struct {
	CaseID            string          `json:"case_id"`
	Image             string          `json:"image"`
	ImageBuild        *CaseImageBuild `json:"image_build,omitempty"`
	InitialPrompt     string          `json:"initial_prompt"`
	AgentCwd          string          `json:"agent_cwd"`
	TestCommand       []string        `json:"test_command"`
	TestCwd           string          `json:"test_cwd"`
	TestTimeoutSecond int             `json:"test_timeout_seconds"`
	TestAssets        TestAssets      `json:"test_assets"`
}

type CaseImageBuild struct {
	Context           BuildContext `json:"context"`
	DockerfileRelPath string       `json:"dockerfile_rel_path"`
}

type Integrity struct {
	BundleHashSHA256 string `json:"bundle_hash_sha256,omitempty"`
}

type TestAssets = testassets.Descriptor
type BuildContext = testassets.Descriptor
