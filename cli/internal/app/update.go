package app

import (
	"context"
	"fmt"

	"github.com/marginlab/margin-eval/cli/internal/buildinfo"
	"github.com/marginlab/margin-eval/cli/internal/updater"
)

var runUpdater = func(ctx context.Context, currentVersion string) (updater.Result, error) {
	manager, err := updater.New(updater.Config{})
	if err != nil {
		return updater.Result{}, err
	}
	return manager.Update(ctx, currentVersion)
}

var currentBuildVersion = buildinfo.CurrentVersion

func (a *App) runUpdate(ctx context.Context, args []string) error {
	fs := newFlagSet("update", a.stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %s", fs.Arg(0))
	}

	result, err := runUpdater(ctx, currentBuildVersion())
	if err != nil {
		return err
	}
	if !result.Updated {
		fmt.Fprintf(a.stdout, "margin is already up to date (%s)\n", result.CurrentVersion)
		return nil
	}
	fmt.Fprintf(a.stdout, "updated margin from %s to %s\n", result.CurrentVersion, result.LatestVersion)
	return nil
}
