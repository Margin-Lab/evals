package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/marginlab/margin-eval/cli/internal/remotesuite"
)

var resolveRemoteSuite = func(ctx context.Context, in remotesuite.ResolveInput) (remotesuite.Result, error) {
	return remotesuite.Resolve(ctx, in)
}

func (a *App) runSuite(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing suite subcommand; expected pull")
	}
	switch args[0] {
	case "pull":
		return a.runSuitePull(ctx, args[1:])
	default:
		return fmt.Errorf("unknown suite subcommand %q", args[0])
	}
}

func (a *App) runSuitePull(ctx context.Context, args []string) error {
	fs := newFlagSet("suite pull", a.stderr)
	suite := fs.String("suite", "", "remote HTTPS git repo URL or git::https://...//subdir")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %s", fs.Arg(0))
	}
	if strings.TrimSpace(*suite) == "" {
		return fmt.Errorf("--suite is required")
	}

	result, err := resolveRemoteSuite(ctx, remotesuite.ResolveInput{
		Suite:   *suite,
		Refresh: true,
	})
	if err != nil {
		return err
	}

	fmt.Fprintf(a.stdout, "suite: %s\n", result.SuiteGit.RepoURL)
	fmt.Fprintf(a.stdout, "subdir: %s\n", displayValue(result.SuiteGit.Subdir, "."))
	fmt.Fprintf(a.stdout, "resolved_commit: %s\n", result.SuiteGit.ResolvedCommit)
	fmt.Fprintf(a.stdout, "suite_dir: %s\n", result.LocalPath)
	return nil
}

func displayValue(value, fallback string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return fallback
	}
	return trimmed
}
