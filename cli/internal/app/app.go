package app

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"
)

type App struct {
	stdout io.Writer
	stderr io.Writer
}

func New(stdout, stderr io.Writer) *App {
	return &App{stdout: stdout, stderr: stderr}
}

func (a *App) Run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		a.printUsage()
		return nil
	}
	if args[0] == "--version" {
		return a.runVersion(args[1:])
	}

	switch args[0] {
	case "help", "-h", "--help":
		a.printUsage()
		return nil
	case "check":
		return a.runCheck(ctx, args[1:])
	case "init":
		return a.runInit(args[1:])
	case "run":
		return a.runRun(ctx, args[1:])
	case "suite":
		return a.runSuite(ctx, args[1:])
	case "update":
		return a.runUpdate(ctx, args[1:])
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func (a *App) runInit(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing init subcommand; expected suite|case|agent-definition|agent-config|eval-config")
	}
	switch args[0] {
	case "suite":
		return a.runInitSuite(args[1:])
	case "case":
		return a.runInitCase(args[1:])
	case "agent-definition":
		return a.runInitAgentDefinition(args[1:])
	case "agent-config":
		return a.runInitAgentConfig(args[1:])
	case "eval-config":
		return a.runInitEvalConfig(args[1:])
	default:
		return fmt.Errorf("unknown init subcommand %q", args[0])
	}
}

func (a *App) printUsage() {
	fmt.Fprintln(a.stdout, strings.TrimSpace(`margin - Robust, easy agent evals

Global flags:
  margin --version

Commands:
Preflight command:
  margin check

Eval run command:
  margin run --suite <path-or-remote> --agent-config <path> --eval <path-to-eval.toml> [options]
  margin run --resume-from <run-dir> [options]
  margin run --resume-from <run-dir> --suite <path-or-remote> --agent-config <path> --eval <path-to-eval.toml> [options]

  Run options:
    --output <path>               Exact output run directory (default ./runs/<run-id>)
    --name <name>                 Run name (default compiled bundle name)
    --resume-from <path>          Resume from source run directory; pass updated suite/agent-config/eval to resume with new inputs
    --resume-mode <mode>          Resume behavior: resume|retry-failed (default resume; resume reruns infra_failed only)
    --non-interactive             Skip Mission Control TUI and print plain progress logs
    --exit-on-complete            Exit immediately when Mission Control reaches a terminal state
    --agent-server-binary <path>  exact agent-server binary path on host (default embedded in margin)
    --docker-binary <path>        Docker CLI binary (default docker)
    --auth-file-path <path>       Override local OAuth credential file path for the selected agent
    --prune-built-image <count>   Enable lazy-built cleanup and globally prune all unused Docker images from the selected daemon every N completed executed instances
    --dry-run                     Skip agent execution but still run case tests on the pristine workspace
    --agent-env KEY=VALUE         agent-server container env var; repeatable
    --agent-bind HOST=CONTAINER   agent-server bind mount; repeatable
    --run-timeout <duration>      Wait timeout for run completion (default none)

Other commands:
  margin update
  margin suite pull --suite <https-git-url|git::https-git-url//subdir>
  margin init suite --suite <path> [--name <name>]
  margin init case --suite <suite-path> [--case <case-name>]
  margin init agent-config --agent-config <path> --definition <path> [--name <name>]
  margin init eval-config --eval <path> [--name <name>]
  margin init agent-definition --definition <path> [--name <name>]

Mission-control TUI keys:
  tab              Switch focus between instance list and detail pane
  up/down          Move instances when left focused; scroll logs when right focused
  left/right       Change selected state when right focused
  g/G              Jump to top/bottom in logs
  q                Quit (prompts before terminal state)
`))
}

func newFlagSet(name string, out io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(out)
	return fs
}
