package app

import (
	"fmt"

	"github.com/marginlab/margin-eval/cli/internal/scaffold"
)

func (a *App) runInitSuite(args []string) error {
	fs := newFlagSet("init suite", a.stderr)
	suitePath := fs.String("suite", "", "suite directory path")
	name := fs.String("name", "", "suite name")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *suitePath == "" {
		return fmt.Errorf("--suite is required")
	}
	if err := scaffold.InitSuite(*suitePath, *name); err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "initialized suite at %s\n", *suitePath)
	return nil
}

func (a *App) runInitCase(args []string) error {
	fs := newFlagSet("init case", a.stderr)
	suitePath := fs.String("suite", "", "suite directory path")
	caseName := fs.String("case", "", "case name")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *suitePath == "" {
		return fmt.Errorf("--suite is required")
	}
	resolvedCaseName, err := scaffold.InitCase(*suitePath, *caseName)
	if err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "initialized case %s in %s\n", resolvedCaseName, *suitePath)
	return nil
}

func (a *App) runInitAgentDefinition(args []string) error {
	fs := newFlagSet("init agent-definition", a.stderr)
	definitionPath := fs.String("definition", "", "agent definition directory path")
	name := fs.String("name", "", "agent definition name")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *definitionPath == "" {
		return fmt.Errorf("--definition is required")
	}
	if err := scaffold.InitAgentDefinition(*definitionPath, *name); err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "initialized agent definition at %s\n", *definitionPath)
	return nil
}

func (a *App) runInitAgentConfig(args []string) error {
	fs := newFlagSet("init agent-config", a.stderr)
	agentConfigPath := fs.String("agent-config", "", "agent config directory path")
	name := fs.String("name", "", "agent config name")
	definitionRef := fs.String("definition", "", "relative or absolute path to the agent definition")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *agentConfigPath == "" {
		return fmt.Errorf("--agent-config is required")
	}
	if *definitionRef == "" {
		return fmt.Errorf("--definition is required")
	}
	if err := scaffold.InitAgentConfig(*agentConfigPath, *name, *definitionRef); err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "initialized agent config at %s\n", *agentConfigPath)
	return nil
}

func (a *App) runInitEvalConfig(args []string) error {
	fs := newFlagSet("init eval-config", a.stderr)
	evalPath := fs.String("eval", "", "eval config file path")
	name := fs.String("name", "", "eval config name")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *evalPath == "" {
		return fmt.Errorf("--eval is required")
	}
	if err := scaffold.InitEvalConfig(*evalPath, *name); err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "initialized eval config at %s\n", *evalPath)
	return nil
}
