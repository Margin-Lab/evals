package app

import (
	"context"
	"fmt"

	"github.com/marginlab/margin-eval/cli/internal/preflight"
)

var runDockerPreflight = func(ctx context.Context) (preflight.Result, error) {
	return preflight.NewDockerChecker().Check(ctx)
}

func (a *App) runCheck(ctx context.Context, args []string) error {
	fs := newFlagSet("check", a.stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %s", fs.Arg(0))
	}

	result, err := runDockerPreflight(ctx)
	for _, step := range result.Steps {
		if step.Detail != "" {
			fmt.Fprintf(a.stdout, "[check] %s: %s (%s)\n", step.Name, step.Status, step.Detail)
			continue
		}
		fmt.Fprintf(a.stdout, "[check] %s: %s\n", step.Name, step.Status)
	}
	if err != nil {
		return err
	}
	fmt.Fprintln(a.stdout, "margin check passed")
	return nil
}
