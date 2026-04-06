package app

import (
	"fmt"

	"github.com/marginlab/margin-eval/cli/internal/buildinfo"
)

var currentBuildInfo = buildinfo.Current

func (a *App) runVersion(args []string) error {
	if len(args) != 0 {
		return fmt.Errorf("unexpected arguments: %s", args[0])
	}
	info := currentBuildInfo()
	fmt.Fprintf(a.stdout, "margin %s (built %s)\n", info.Version, info.BuildTime)
	return nil
}
