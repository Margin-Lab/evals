package runbundle

import (
	"strings"
	"testing"
)

func TestValidateAcceptsValidBundle(t *testing.T) {
	if err := Validate(validBundle()); err != nil {
		t.Fatalf("validate valid bundle: %v", err)
	}
}

func TestValidateRejectsInvalidSourceKind(t *testing.T) {
	b := validBundle()
	b.Source.Kind = "weird"
	if err := Validate(b); err == nil || !strings.Contains(err.Error(), "source.kind") {
		t.Fatalf("expected source.kind error, got %v", err)
	}
}

func TestValidateRejectsMissingExecutionMode(t *testing.T) {
	b := validBundle()
	b.ResolvedSnapshot.Execution.Mode = ""
	if err := Validate(b); err == nil || !strings.Contains(err.Error(), "execution.mode") {
		t.Fatalf("expected execution.mode error, got %v", err)
	}
}

func TestValidateRejectsCatalogRefModeWithoutRefs(t *testing.T) {
	b := validBundle()
	b.Source.Kind = SourceKindCatalogRefs
	b.Source.CatalogRefs = nil
	if err := Validate(b); err == nil || !strings.Contains(err.Error(), "catalog_refs") {
		t.Fatalf("expected catalog_refs error, got %v", err)
	}
}

func TestValidateRejectsNegativeRetryCount(t *testing.T) {
	b := validBundle()
	b.ResolvedSnapshot.Execution.RetryCount = -1
	if err := Validate(b); err == nil || !strings.Contains(err.Error(), "retry_count") {
		t.Fatalf("expected retry_count error, got %v", err)
	}
}

func TestValidateRejectsMutableImageTag(t *testing.T) {
	b := validBundle()
	b.ResolvedSnapshot.Cases[0].Image = "ghcr.io/acme/repo:latest"
	if err := Validate(b); err == nil || !strings.Contains(err.Error(), "digest-pinned") {
		t.Fatalf("expected digest-pinned image error, got %v", err)
	}
}

func TestValidateRejectsCaseWithImageAndImageBuild(t *testing.T) {
	b := validBundle()
	b.ResolvedSnapshot.Cases[0].ImageBuild = &CaseImageBuild{
		Context:           minimalTestAssets(),
		DockerfileRelPath: "Dockerfile",
	}
	if err := Validate(b); err == nil || !strings.Contains(err.Error(), "exactly one") {
		t.Fatalf("expected exactly-one image source error, got %v", err)
	}
}

func TestValidateAcceptsCaseWithImageBuildOnly(t *testing.T) {
	b := validBundle()
	b.ResolvedSnapshot.Cases[0].Image = ""
	b.ResolvedSnapshot.Cases[0].ImageBuild = &CaseImageBuild{
		Context:           minimalTestAssets(),
		DockerfileRelPath: "Dockerfile",
	}
	if err := Validate(b); err != nil {
		t.Fatalf("expected image_build-only case to validate, got %v", err)
	}
}

func TestValidateRejectsCaseWithNeitherImageNorImageBuild(t *testing.T) {
	b := validBundle()
	b.ResolvedSnapshot.Cases[0].Image = ""
	b.ResolvedSnapshot.Cases[0].ImageBuild = nil
	if err := Validate(b); err == nil || !strings.Contains(err.Error(), "exactly one") {
		t.Fatalf("expected exactly-one image source error, got %v", err)
	}
}

func TestValidateRejectsImageBuildWithAbsoluteDockerfilePath(t *testing.T) {
	b := validBundle()
	b.ResolvedSnapshot.Cases[0].Image = ""
	b.ResolvedSnapshot.Cases[0].ImageBuild = &CaseImageBuild{
		Context:           minimalTestAssets(),
		DockerfileRelPath: "/Dockerfile",
	}
	if err := Validate(b); err == nil || !strings.Contains(err.Error(), "relative") {
		t.Fatalf("expected relative dockerfile path error, got %v", err)
	}
}

func TestValidateRejectsEmptyCases(t *testing.T) {
	b := validBundle()
	b.ResolvedSnapshot.Cases = nil
	if err := Validate(b); err == nil || !strings.Contains(err.Error(), "non-empty") {
		t.Fatalf("expected cases non-empty error, got %v", err)
	}
}

func TestValidateRejectsMissingAgentConfigInput(t *testing.T) {
	b := validBundle()
	b.ResolvedSnapshot.Agent.Config.Input = nil
	if err := Validate(b); err == nil || !strings.Contains(err.Error(), "config.input") {
		t.Fatalf("expected config.input error, got %v", err)
	}
}

func TestValidateAcceptsSuiteGitRef(t *testing.T) {
	b := validBundle()
	b.Source.SuiteGit = &SuiteGitRef{
		RepoURL:        "https://github.com/example/suites",
		ResolvedCommit: "0123456789abcdef0123456789abcdef01234567",
		Subdir:         "suites/smoke",
	}
	if err := Validate(b); err != nil {
		t.Fatalf("expected suite git ref to validate, got %v", err)
	}
}

func TestValidateRejectsSuiteGitRefOnCatalogSource(t *testing.T) {
	b := validBundle()
	b.Source.Kind = SourceKindCatalogRefs
	b.Source.CatalogRefs = &CatalogRefs{
		Suite:       &CatalogRef{ResourceID: "suite", Version: 1, ProjectID: "proj"},
		AgentConfig: &CatalogRef{ResourceID: "agent", Version: 1, ProjectID: "proj"},
		EvalConfig:  &CatalogRef{ResourceID: "eval", Version: 1, ProjectID: "proj"},
	}
	b.Source.SuiteGit = &SuiteGitRef{
		RepoURL:        "https://github.com/example/suites",
		ResolvedCommit: "0123456789abcdef0123456789abcdef01234567",
	}
	if err := Validate(b); err == nil || !strings.Contains(err.Error(), "suite_git") {
		t.Fatalf("expected suite_git error, got %v", err)
	}
}

func TestValidateRejectsInvalidSuiteGitSubdir(t *testing.T) {
	b := validBundle()
	b.Source.SuiteGit = &SuiteGitRef{
		RepoURL:        "https://github.com/example/suites",
		ResolvedCommit: "0123456789abcdef0123456789abcdef01234567",
		Subdir:         "../escape",
	}
	if err := Validate(b); err == nil || !strings.Contains(err.Error(), "subdir") {
		t.Fatalf("expected subdir validation error, got %v", err)
	}
}
