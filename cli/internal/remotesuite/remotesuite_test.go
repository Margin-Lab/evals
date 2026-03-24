package remotesuite

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type fakeGitFetch struct {
	commit string
	files  map[string]string
	err    error
}

type fakeGitRunner struct {
	fetches []struct {
		repoURL string
	}
	queue []fakeGitFetch
}

func (f *fakeGitRunner) CheckAvailable(context.Context) error {
	return nil
}

func (f *fakeGitRunner) Fetch(_ context.Context, repoURL, checkoutDir string) (string, error) {
	f.fetches = append(f.fetches, struct {
		repoURL string
	}{repoURL: repoURL})
	if len(f.queue) == 0 {
		return "", nil
	}
	item := f.queue[0]
	f.queue = f.queue[1:]
	if item.err != nil {
		return "", item.err
	}
	for rel, body := range item.files {
		path := filepath.Join(checkoutDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			return "", err
		}
	}
	return item.commit, nil
}

func TestResolveFetchesAndCachesRemoteSuite(t *testing.T) {
	homeDir := t.TempDir()
	runner := &fakeGitRunner{
		queue: []fakeGitFetch{{
			commit: "0123456789abcdef0123456789abcdef01234567",
			files:  minimalSuiteFiles("remote-smoke"),
		}},
	}
	resolver := &Resolver{
		homeDir:     func() (string, error) { return homeDir, nil },
		git:         runner,
		now:         func() time.Time { return time.Date(2026, 3, 24, 12, 0, 0, 0, time.UTC) },
		lockTimeout: time.Second,
		lockPoll:    time.Millisecond,
	}

	result, err := resolver.Resolve(context.Background(), ResolveInput{
		Suite: "https://github.com/example/suites.git",
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if !result.Fetched {
		t.Fatalf("expected initial resolve to fetch")
	}
	if result.SuiteGit == nil {
		t.Fatal("expected suite git metadata")
	}
	if result.SuiteGit.RepoURL != "https://github.com/example/suites" {
		t.Fatalf("RepoURL = %q", result.SuiteGit.RepoURL)
	}
	if result.SuiteGit.ResolvedCommit != "0123456789abcdef0123456789abcdef01234567" {
		t.Fatalf("ResolvedCommit = %q", result.SuiteGit.ResolvedCommit)
	}
	if _, err := os.Stat(filepath.Join(result.LocalPath, "suite.toml")); err != nil {
		t.Fatalf("expected cached suite.toml: %v", err)
	}

	cached, err := resolver.Resolve(context.Background(), ResolveInput{
		Suite: "https://github.com/example/suites",
	})
	if err != nil {
		t.Fatalf("Resolve() cached error = %v", err)
	}
	if cached.Fetched {
		t.Fatalf("expected cached resolve to skip fetch")
	}
	if len(runner.fetches) != 1 {
		t.Fatalf("fetch count = %d, want 1", len(runner.fetches))
	}
}

func TestResolveRefreshUpdatesCachedSuite(t *testing.T) {
	homeDir := t.TempDir()
	runner := &fakeGitRunner{
		queue: []fakeGitFetch{
			{
				commit: "0123456789abcdef0123456789abcdef01234567",
				files:  minimalSuiteFiles("v1"),
			},
			{
				commit: "fedcba9876543210fedcba9876543210fedcba98",
				files:  minimalSuiteFiles("v2"),
			},
		},
	}
	resolver := &Resolver{
		homeDir:     func() (string, error) { return homeDir, nil },
		git:         runner,
		now:         func() time.Time { return time.Date(2026, 3, 24, 12, 0, 0, 0, time.UTC) },
		lockTimeout: time.Second,
		lockPoll:    time.Millisecond,
	}

	first, err := resolver.Resolve(context.Background(), ResolveInput{
		Suite: "https://github.com/example/suites",
	})
	if err != nil {
		t.Fatalf("first resolve: %v", err)
	}
	second, err := resolver.Resolve(context.Background(), ResolveInput{
		Suite:   "https://github.com/example/suites",
		Refresh: true,
	})
	if err != nil {
		t.Fatalf("refresh resolve: %v", err)
	}
	if !second.Fetched {
		t.Fatalf("expected refresh to fetch")
	}
	if second.SuiteGit.ResolvedCommit == first.SuiteGit.ResolvedCommit {
		t.Fatalf("expected refreshed commit to change")
	}
	body, err := os.ReadFile(filepath.Join(second.LocalPath, "suite.toml"))
	if err != nil {
		t.Fatalf("read refreshed suite.toml: %v", err)
	}
	if string(body) != "v2" {
		t.Fatalf("suite.toml = %q, want %q", string(body), "v2")
	}
}

func TestResolveSupportsSuiteSubdir(t *testing.T) {
	homeDir := t.TempDir()
	runner := &fakeGitRunner{
		queue: []fakeGitFetch{{
			commit: "0123456789abcdef0123456789abcdef01234567",
			files: map[string]string{
				"README.md":                                "repo-root\n",
				"suites/remote/suite.toml":                 "suite\n",
				"suites/remote/cases/case-1/case.toml":     "case\n",
				"suites/remote/cases/case-1/prompt.md":     "prompt\n",
				"suites/remote/cases/case-1/tests/test.sh": "#!/usr/bin/env bash\n",
				"suites/other/suite.toml":                  "other\n",
				"suites/other/cases/other/case.toml":       "case\n",
				"suites/other/cases/other/prompt.md":       "prompt\n",
				"suites/other/cases/other/tests/test.sh":   "#!/usr/bin/env bash\n",
			},
		}},
	}
	resolver := &Resolver{
		homeDir:     func() (string, error) { return homeDir, nil },
		git:         runner,
		now:         func() time.Time { return time.Date(2026, 3, 24, 12, 0, 0, 0, time.UTC) },
		lockTimeout: time.Second,
		lockPoll:    time.Millisecond,
	}

	result, err := resolver.Resolve(context.Background(), ResolveInput{
		Suite: "git::https://github.com/example/suites.git//suites/remote",
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if result.SuiteGit.Subdir != "suites/remote" {
		t.Fatalf("Subdir = %q", result.SuiteGit.Subdir)
	}
	if _, err := os.Stat(filepath.Join(result.LocalPath, "suite.toml")); err != nil {
		t.Fatalf("expected selected suite.toml: %v", err)
	}
	if _, err := os.Stat(filepath.Join(result.LocalPath, "README.md")); !os.IsNotExist(err) {
		t.Fatalf("expected repo root README to be excluded, got err=%v", err)
	}
}

func TestResolveFailedRefreshKeepsExistingCache(t *testing.T) {
	homeDir := t.TempDir()
	runner := &fakeGitRunner{
		queue: []fakeGitFetch{
			{
				commit: "0123456789abcdef0123456789abcdef01234567",
				files:  minimalSuiteFiles("stable"),
			},
			{
				err: os.ErrPermission,
			},
		},
	}
	resolver := &Resolver{
		homeDir:     func() (string, error) { return homeDir, nil },
		git:         runner,
		now:         func() time.Time { return time.Date(2026, 3, 24, 12, 0, 0, 0, time.UTC) },
		lockTimeout: time.Second,
		lockPoll:    time.Millisecond,
	}

	initial, err := resolver.Resolve(context.Background(), ResolveInput{
		Suite: "https://github.com/example/suites",
	})
	if err != nil {
		t.Fatalf("initial resolve: %v", err)
	}
	if _, err := resolver.Resolve(context.Background(), ResolveInput{
		Suite:   "https://github.com/example/suites",
		Refresh: true,
	}); err == nil {
		t.Fatal("expected refresh failure")
	}
	cached, err := resolver.Resolve(context.Background(), ResolveInput{
		Suite: "https://github.com/example/suites",
	})
	if err != nil {
		t.Fatalf("cached resolve after failed refresh: %v", err)
	}
	if cached.SuiteGit.ResolvedCommit != initial.SuiteGit.ResolvedCommit {
		t.Fatalf("cached commit = %q, want %q", cached.SuiteGit.ResolvedCommit, initial.SuiteGit.ResolvedCommit)
	}
	body, err := os.ReadFile(filepath.Join(cached.LocalPath, "suite.toml"))
	if err != nil {
		t.Fatalf("read cached suite.toml: %v", err)
	}
	if string(body) != "stable" {
		t.Fatalf("suite.toml = %q, want %q", string(body), "stable")
	}
}

func TestResolveRejectsNonHTTPSRemote(t *testing.T) {
	resolver := &Resolver{
		homeDir:     func() (string, error) { return t.TempDir(), nil },
		git:         &fakeGitRunner{},
		now:         func() time.Time { return time.Now().UTC() },
		lockTimeout: time.Second,
		lockPoll:    time.Millisecond,
	}

	if _, err := resolver.Resolve(context.Background(), ResolveInput{Suite: "git@github.com:example/suites.git"}); err == nil {
		t.Fatal("expected non-https remote to fail")
	}
}

func TestResolveRejectsGitSuiteWithoutSubdir(t *testing.T) {
	resolver := &Resolver{
		homeDir:     func() (string, error) { return t.TempDir(), nil },
		git:         &fakeGitRunner{},
		now:         func() time.Time { return time.Now().UTC() },
		lockTimeout: time.Second,
		lockPoll:    time.Millisecond,
	}

	if _, err := resolver.Resolve(context.Background(), ResolveInput{Suite: "git::https://github.com/example/suites.git"}); err == nil {
		t.Fatal("expected git suite without subdir to fail")
	}
}

func minimalSuiteFiles(suiteToml string) map[string]string {
	return map[string]string{
		"suite.toml":                 suiteToml,
		"cases/case-1/case.toml":     "case\n",
		"cases/case-1/prompt.md":     "prompt\n",
		"cases/case-1/tests/test.sh": "#!/usr/bin/env bash\n",
	}
}
