package app

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/marginlab/margin-eval/cli/internal/remotesuite"
	"github.com/marginlab/margin-eval/runner/runner-core/runbundle"
)

func TestRunSuitePullRefreshesRemoteSuiteAndPrintsResult(t *testing.T) {
	origResolveRemoteSuite := resolveRemoteSuite
	defer func() { resolveRemoteSuite = origResolveRemoteSuite }()

	resolveRemoteSuite = func(_ context.Context, in remotesuite.ResolveInput) (remotesuite.Result, error) {
		if in.Suite != "git::https://github.com/example/suites.git//suites/remote" {
			t.Fatalf("Suite = %q", in.Suite)
		}
		if !in.Refresh {
			t.Fatalf("expected suite pull to force refresh")
		}
		return remotesuite.Result{
			LocalPath: "/tmp/remote-suite-cache/suite",
			SuiteGit: &runbundle.SuiteGitRef{
				RepoURL:        "https://github.com/example/suites",
				ResolvedCommit: "0123456789abcdef0123456789abcdef01234567",
				Subdir:         "suites/remote",
			},
		}, nil
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	a := New(&stdout, &stderr)
	err := a.runSuitePull(context.Background(), []string{
		"--suite", "git::https://github.com/example/suites.git//suites/remote",
	})
	if err != nil {
		t.Fatalf("runSuitePull returned error: %v", err)
	}

	out := stdout.String()
	for _, want := range []string{
		"suite: https://github.com/example/suites",
		"subdir: suites/remote",
		"resolved_commit: 0123456789abcdef0123456789abcdef01234567",
		"suite_dir: /tmp/remote-suite-cache/suite",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("stdout missing %q:\n%s", want, out)
		}
	}
}

func TestAppRunDispatchesSuitePullCommand(t *testing.T) {
	origResolveRemoteSuite := resolveRemoteSuite
	defer func() { resolveRemoteSuite = origResolveRemoteSuite }()

	resolveRemoteSuite = func(_ context.Context, _ remotesuite.ResolveInput) (remotesuite.Result, error) {
		return remotesuite.Result{
			LocalPath: "/tmp/remote-suite-cache/suite",
			SuiteGit: &runbundle.SuiteGitRef{
				RepoURL:        "https://github.com/example/suites",
				ResolvedCommit: "0123456789abcdef0123456789abcdef01234567",
			},
		}, nil
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	a := New(&stdout, &stderr)
	if err := a.Run(context.Background(), []string{"suite", "pull", "--suite", "https://github.com/example/suites"}); err != nil {
		t.Fatalf("App.Run returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), "resolved_commit: 0123456789abcdef0123456789abcdef01234567") {
		t.Fatalf("stdout missing pull result: %s", stdout.String())
	}
}

func TestUsageIncludesSuitePullCommand(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	a := New(&stdout, &stderr)

	a.printUsage()

	if got := stdout.String(); !strings.Contains(got, "margin suite pull") {
		t.Fatalf("usage missing suite pull command: %s", got)
	}
}
