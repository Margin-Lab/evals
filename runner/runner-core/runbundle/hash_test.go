package runbundle

import (
	"testing"
	"time"
)

func TestHashSHA256IsStableForEquivalentBundle(t *testing.T) {
	b1 := validBundle()
	b2 := validBundle()

	h1, err := HashSHA256(b1)
	if err != nil {
		t.Fatalf("hash b1: %v", err)
	}
	h2, err := HashSHA256(b2)
	if err != nil {
		t.Fatalf("hash b2: %v", err)
	}
	if h1 != h2 {
		t.Fatalf("expected stable hash, got %s vs %s", h1, h2)
	}
}

func TestWithComputedIntegritySetsIntegrity(t *testing.T) {
	b := validBundle()
	out, err := WithComputedIntegrity(b)
	if err != nil {
		t.Fatalf("with computed integrity: %v", err)
	}
	if out.Integrity == nil || out.Integrity.BundleHashSHA256 == "" {
		t.Fatalf("expected bundle hash in integrity")
	}
}

func TestCloneForRerunExactSetsSourceAndIdentity(t *testing.T) {
	b := validBundle()
	re := CloneForRerunExact(b, "bun_new", time.Date(2026, 2, 27, 0, 0, 0, 0, time.UTC), "run_1")
	if re.BundleID != "bun_new" {
		t.Fatalf("bundle id mismatch: %s", re.BundleID)
	}
	if re.Source.Kind != SourceKindRunSnapshot {
		t.Fatalf("source kind mismatch: %s", re.Source.Kind)
	}
	if re.Source.OriginRunID != "run_1" {
		t.Fatalf("origin run id mismatch: %s", re.Source.OriginRunID)
	}
	if re.Integrity != nil {
		t.Fatalf("expected integrity to be reset")
	}
}

func TestCloneForRerunExactDeepCopiesNestedFields(t *testing.T) {
	b := validBundle()
	re := CloneForRerunExact(b, "bun_new", time.Date(2026, 2, 27, 0, 0, 0, 0, time.UTC), "run_1")

	b.ResolvedSnapshot.Agent.Config.Input["command"] = []any{"sh", "-lc", "echo changed"}
	b.ResolvedSnapshot.RunDefaults.Env["TERM"] = "dumb"
	b.ResolvedSnapshot.Cases[0].TestCommand[0] = "sh"

	commandRaw := re.ResolvedSnapshot.Agent.Config.Input["command"]
	command, ok := commandRaw.([]any)
	if !ok || len(command) < 1 || command[0] != "bash" {
		t.Fatalf("expected cloned agent config to remain unchanged, got %#v", commandRaw)
	}
	if re.ResolvedSnapshot.RunDefaults.Env["TERM"] != "xterm-256color" {
		t.Fatalf("expected cloned env to remain unchanged, got %q", re.ResolvedSnapshot.RunDefaults.Env["TERM"])
	}
	if re.ResolvedSnapshot.Cases[0].TestCommand[0] != "bash" {
		t.Fatalf("expected cloned test command to remain unchanged, got %q", re.ResolvedSnapshot.Cases[0].TestCommand[0])
	}
}

func TestHashSHA256ChangesWhenExecutionModeChanges(t *testing.T) {
	b1 := validBundle()
	b2 := validBundle()
	b2.ResolvedSnapshot.Execution.Mode = ExecutionModeDryRun

	h1, err := HashSHA256(b1)
	if err != nil {
		t.Fatalf("hash b1: %v", err)
	}
	h2, err := HashSHA256(b2)
	if err != nil {
		t.Fatalf("hash b2: %v", err)
	}
	if h1 == h2 {
		t.Fatalf("expected different hashes for different execution modes")
	}
}

func TestHashSHA256ChangesWhenSuiteGitRefChanges(t *testing.T) {
	b1 := validBundle()
	b1.Source.SuiteGit = &SuiteGitRef{
		RepoURL:        "https://github.com/example/suites",
		ResolvedCommit: "0123456789abcdef0123456789abcdef01234567",
		Subdir:         "suites/smoke",
	}
	b2 := validBundle()
	b2.Source.SuiteGit = &SuiteGitRef{
		RepoURL:        "https://github.com/example/suites",
		ResolvedCommit: "fedcba9876543210fedcba9876543210fedcba98",
		Subdir:         "suites/smoke",
	}

	h1, err := HashSHA256(b1)
	if err != nil {
		t.Fatalf("hash b1: %v", err)
	}
	h2, err := HashSHA256(b2)
	if err != nil {
		t.Fatalf("hash b2: %v", err)
	}
	if h1 == h2 {
		t.Fatalf("expected different hashes for different suite commits")
	}
}
