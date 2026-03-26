package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/marginlab/margin-eval/cli/internal/agentserverembed"
	"github.com/marginlab/margin-eval/cli/internal/compiler"
	"github.com/marginlab/margin-eval/cli/internal/datasource"
	"github.com/marginlab/margin-eval/cli/internal/missioncontrol"
	"github.com/marginlab/margin-eval/cli/internal/plaincontrol"
	"github.com/marginlab/margin-eval/cli/internal/remotesuite"

	"github.com/marginlab/margin-eval/runner/runner-core/engine"
	"github.com/marginlab/margin-eval/runner/runner-core/runbundle"
	"github.com/marginlab/margin-eval/runner/runner-core/runnerapi"
	"github.com/marginlab/margin-eval/runner/runner-local/localexecutor"
	"github.com/marginlab/margin-eval/runner/runner-local/localrunner"

	"github.com/charmbracelet/x/term"
)

var (
	compileBundle    = compiler.Compile
	newLocalExecutor = func(cfg localexecutor.Config) (engine.Executor, error) {
		return localexecutor.New(cfg)
	}
	newAgentServerBinaryProvider = func() (localexecutor.AgentServerBinaryProvider, error) {
		return agentserverembed.NewProvider()
	}
	newLocalRunnerService = localrunner.NewService
	launchMissionControl  = missioncontrol.Run
	launchPlainControl    = func(ctx context.Context, cfg plaincontrol.Config) (missioncontrol.Outcome, error) {
		outcome, err := plaincontrol.Run(ctx, cfg)
		if err != nil {
			return missioncontrol.Outcome{}, err
		}
		return missioncontrol.Outcome{
			FinalRun: outcome.FinalRun,
			Aborted:  outcome.Aborted,
		}, nil
	}
	runConfirmationInput io.Reader = os.Stdin
	shouldConfirmRun               = func(out io.Writer) bool {
		fdWriter, ok := out.(interface{ Fd() uintptr })
		if !ok {
			return false
		}
		return term.IsTerminal(fdWriter.Fd()) && term.IsTerminal(os.Stdin.Fd())
	}
	launchRunConfirmation = runConfirmationTUI
)

const (
	defaultLocalProjectID = "proj_local"
	defaultLocalUserID    = "user_local"
)

func (a *App) runRun(ctx context.Context, args []string) error {
	fs := newFlagSet("run", a.stderr)

	suitePath := fs.String("suite", "", "suite path or remote spec")
	agentConfigPath := fs.String("agent-config", "", "agent config directory path")
	evalPath := fs.String("eval", "", "eval config file path")

	rootDir := fs.String("root", ".", "local runner root directory")
	runName := fs.String("name", "", "run name")
	resumeFromRunID := fs.String("resume-from", "", "source run id to resume from")
	resumeModeValue := fs.String("resume-mode", string(runnerapi.DefaultResumeMode()), "resume behavior: resume|retry-failed (resume reruns infra_failed only)")

	agentServerBinary := fs.String("agent-server-binary", "", "exact agent-server binary path on host (default embedded payloads)")
	dockerBinary := fs.String("docker-binary", "docker", "docker CLI binary path")
	authFilePath := fs.String("auth-file-path", "", "override local OAuth credential file path for the selected agent")
	nonInteractive := fs.Bool("non-interactive", false, "skip Mission Control TUI and print plain progress logs")
	pruneBuiltImage := fs.Int("prune-built-image", 0, "enable lazy-built cleanup and globally prune all unused Docker images from the selected daemon every N completed executed instances (0 disables)")
	dryRun := fs.Bool("dry-run", false, "skip agent execution after prelaunch setup")

	runTimeout := fs.Duration("run-timeout", 0, "timeout waiting for run completion")
	var agentEnv envFlag
	var agentBinds bindFlag
	fs.Var(&agentEnv, "agent-env", "agent-server container env var in KEY=VALUE form; repeatable")
	fs.Var(&agentBinds, "agent-bind", "agent-server container bind mount in HOST_PATH=CONTAINER_PATH form; repeatable")

	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := validateRunSourceFlags(*resumeFromRunID, *suitePath, *agentConfigPath, *evalPath); err != nil {
		return err
	}
	resolvedSuite, err := a.resolveSuiteInput(ctx, *suitePath)
	if err != nil {
		return err
	}
	if strings.TrimSpace(*rootDir) == "" {
		return fmt.Errorf("--root must not be empty")
	}
	if strings.TrimSpace(*dockerBinary) == "" {
		return fmt.Errorf("--docker-binary must not be empty")
	}
	if *pruneBuiltImage < 0 {
		return fmt.Errorf("--prune-built-image must be >= 0")
	}
	absRoot, err := filepath.Abs(strings.TrimSpace(*rootDir))
	if err != nil {
		return fmt.Errorf("resolve root dir %q: %w", *rootDir, err)
	}
	if *pruneBuiltImage > 0 {
		fmt.Fprintf(
			a.stderr,
			"warning: --prune-built-image=%d enables lazy-built cleanup and runs `docker image prune -a -f` after every %d completed executed instances and once at run end; this removes all unused images from the selected Docker daemon, not just eval-owned images\n",
			*pruneBuiltImage,
			*pruneBuiltImage,
		)
	}
	resumeMode, err := resolveResumeMode(*resumeFromRunID, *resumeModeValue)
	if err != nil {
		return err
	}
	resolvedAgentServerBinary, agentServerBinaryProvider, err := resolveAgentServerBinary(*agentServerBinary)
	if err != nil {
		return err
	}

	bundle, err := a.loadBundleForRun(runBundleInput{
		RootDir:         absRoot,
		SuitePath:       resolvedSuite.LocalPath,
		SuiteGit:        resolvedSuite.SuiteGit,
		AgentConfigPath: *agentConfigPath,
		EvalPath:        *evalPath,
		ResumeFromRunID: strings.TrimSpace(*resumeFromRunID),
		NonInteractive:  *nonInteractive,
	})
	if err != nil {
		return err
	}
	if *dryRun {
		bundle.ResolvedSnapshot.Execution.Mode = runbundle.ExecutionModeDryRun
	} else {
		bundle.ResolvedSnapshot.Execution.Mode = runbundle.ExecutionModeFull
	}
	workerCount, err := localWorkerCount(bundle.ResolvedSnapshot.Execution.MaxConcurrency)
	if err != nil {
		return err
	}
	if strings.TrimSpace(*authFilePath) != "" {
		localCredentials := bundle.ResolvedSnapshot.Agent.Definition.Manifest.Auth.LocalCredentials
		if len(localCredentials) != 1 {
			return fmt.Errorf("--auth-file-path requires the selected agent definition to declare exactly one auth.local_credentials entry; found %d", len(localCredentials))
		}
	}
	if !*nonInteractive && shouldConfirmRun(a.stderr) {
		authPreview, err := localexecutor.PreviewAuth(
			agentEnv.Clone(),
			bundle.ResolvedSnapshot.Agent.Definition.Manifest.Auth.RequiredEnv,
			bundle.ResolvedSnapshot.Agent.Definition.Manifest.Auth.LocalCredentials,
			strings.TrimSpace(*authFilePath),
		)
		if err != nil {
			return err
		}
		confirmed, err := launchRunConfirmation(
			a.stderr,
			buildRunConfirmationSpec(
				bundle.ResolvedSnapshot.Agent.Definition.Manifest.Name,
				authPreview,
				*pruneBuiltImage,
				*dryRun,
			),
		)
		if err != nil {
			return err
		}
		if !confirmed {
			return fmt.Errorf("run canceled before submission")
		}
	}

	executor, err := newLocalExecutor(localexecutor.Config{
		AgentServerBinary:         resolvedAgentServerBinary,
		AgentServerBinaryProvider: agentServerBinaryProvider,
		DockerBinary:              *dockerBinary,
		OutputRoot:                absRoot,
		Env:                       agentEnv.Clone(),
		Binds:                     agentBinds.Clone(),
		AuthFileOverridePath:      strings.TrimSpace(*authFilePath),
		CleanupBuiltImages:        *pruneBuiltImage > 0,
	})
	if err != nil {
		return fmt.Errorf("create agent executor: %w", err)
	}

	svc, err := newLocalRunnerService(localrunner.Config{
		RootDir:               absRoot,
		Executor:              executor,
		DockerBinary:          *dockerBinary,
		GlobalImagePruneEvery: *pruneBuiltImage,
		EngineConfig:          engine.Config{WorkerCount: workerCount},
	})
	if err != nil {
		return fmt.Errorf("create local runner service: %w", err)
	}

	dataSource, err := datasource.NewRunnerServiceSource(svc)
	if err != nil {
		return fmt.Errorf("create run datasource: %w", err)
	}

	resolvedRunName := strings.TrimSpace(*runName)
	if resolvedRunName == "" {
		resolvedRunName = strings.TrimSpace(bundle.ResolvedSnapshot.Name)
	}
	if resolvedRunName == "" {
		resolvedRunName = "local-run"
	}

	run, err := dataSource.SubmitRun(ctx, runnerapi.SubmitInput{
		ProjectID:       defaultLocalProjectID,
		CreatedByUser:   defaultLocalUserID,
		Name:            resolvedRunName,
		Bundle:          bundle,
		ResumeFromRunID: strings.TrimSpace(*resumeFromRunID),
		ResumeMode:      resumeMode,
	})
	if err != nil {
		return fmt.Errorf("submit run: %w", err)
	}

	runnerCtx, runnerCancel := context.WithCancel(ctx)
	defer runnerCancel()
	svc.Start(runnerCtx)

	missionControlCtx := ctx
	missionControlCancel := func() {}
	if *runTimeout > 0 {
		missionControlCtx, missionControlCancel = context.WithTimeout(ctx, *runTimeout)
	}
	defer missionControlCancel()
	var outcome missioncontrol.Outcome
	if *nonInteractive {
		outcome, err = launchPlainControl(missionControlCtx, plaincontrol.Config{
			RunID:  run.RunID,
			RunDir: filepath.Join(absRoot, "runs", run.RunID),
			Source: dataSource,
			Out:    a.stdout,
		})
		if err != nil {
			return fmt.Errorf("run non-interactive monitor: %w", err)
		}
	} else {
		localSource, err := missioncontrol.NewLocalSource(dataSource, filepath.Join(absRoot, "runs", run.RunID))
		if err != nil {
			return fmt.Errorf("create mission-control local source: %w", err)
		}
		configureTUIRenderer(a.stdout)
		outcome, err = launchMissionControl(missionControlCtx, missioncontrol.Config{
			RunID:            run.RunID,
			Source:           localSource,
			TextPreviewLimit: missioncontrol.DefaultTextPreviewLimit,
		})
		if err != nil {
			return fmt.Errorf("run mission control: %w", err)
		}
	}
	finalRun := outcome.FinalRun
	if strings.TrimSpace(finalRun.RunID) == "" {
		finalRun = run
	}

	runDir := filepath.Join(absRoot, "runs", finalRun.RunID)
	fmt.Fprintf(a.stdout, "run_id: %s\n", finalRun.RunID)
	fmt.Fprintf(a.stdout, "state: %s\n", finalRun.State)
	fmt.Fprintf(a.stdout, "run_dir: %s\n", runDir)

	if outcome.Aborted {
		return fmt.Errorf("run %s aborted before terminal state", finalRun.RunID)
	}
	if string(finalRun.State) != "completed" {
		return fmt.Errorf("run %s finished in state %s", finalRun.RunID, finalRun.State)
	}
	return nil
}

type runBundleInput struct {
	RootDir         string
	SuitePath       string
	SuiteGit        *runbundle.SuiteGitRef
	AgentConfigPath string
	EvalPath        string
	ResumeFromRunID string
	NonInteractive  bool
}

func (a *App) loadBundleForRun(in runBundleInput) (runbundle.Bundle, error) {
	if in.ResumeFromRunID != "" {
		return a.loadSavedResumeBundle(in.RootDir, in.ResumeFromRunID, in.NonInteractive)
	}
	return a.compileRunBundle(in)
}

func (a *App) compileRunBundle(in runBundleInput) (runbundle.Bundle, error) {
	compileProgress := newCompileProgressReporter(a.stdout, a.stderr, in.NonInteractive)
	if in.NonInteractive {
		fmt.Fprintln(a.stdout, "[compile] start")
	}
	bundle, err := compileBundle(compiler.CompileInput{
		SuitePath:       in.SuitePath,
		AgentConfigPath: in.AgentConfigPath,
		EvalPath:        in.EvalPath,
		SubmitProjectID: defaultLocalProjectID,
		Progress:        compileProgress.Update,
	})
	if err != nil {
		compileProgress.Finish(false)
		return runbundle.Bundle{}, fmt.Errorf("compile bundle: %w", err)
	}
	if in.SuiteGit != nil {
		bundle.Source.SuiteGit = &runbundle.SuiteGitRef{
			RepoURL:        in.SuiteGit.RepoURL,
			ResolvedCommit: in.SuiteGit.ResolvedCommit,
			Subdir:         in.SuiteGit.Subdir,
		}
		if err := runbundle.Validate(bundle); err != nil {
			compileProgress.Finish(false)
			return runbundle.Bundle{}, fmt.Errorf("compile bundle: bundle validation failed after applying remote suite metadata: %w", err)
		}
	}
	compileProgress.Finish(true)
	return bundle, nil
}

type resolvedSuiteInput struct {
	LocalPath string
	SuiteGit  *runbundle.SuiteGitRef
}

func (a *App) resolveSuiteInput(ctx context.Context, suitePath string) (resolvedSuiteInput, error) {
	trimmedSuite := strings.TrimSpace(suitePath)
	if !remotesuite.IsRemoteSuite(trimmedSuite) {
		return resolvedSuiteInput{LocalPath: trimmedSuite}, nil
	}

	result, err := resolveRemoteSuite(ctx, remotesuite.ResolveInput{
		Suite: trimmedSuite,
	})
	if err != nil {
		return resolvedSuiteInput{}, err
	}
	return resolvedSuiteInput{
		LocalPath: result.LocalPath,
		SuiteGit:  result.SuiteGit,
	}, nil
}

func (a *App) loadSavedResumeBundle(rootDir, runID string, nonInteractive bool) (runbundle.Bundle, error) {
	path := savedRunBundlePath(rootDir, runID)
	if nonInteractive {
		fmt.Fprintf(a.stdout, "[resume] loading %s\n", path)
	}
	bundle, err := loadSavedRunBundle(rootDir, runID)
	if err != nil {
		return runbundle.Bundle{}, err
	}
	return bundle, nil
}

func resolveAgentServerBinary(raw string) (string, localexecutor.AgentServerBinaryProvider, error) {
	if trimmed := strings.TrimSpace(raw); trimmed != "" {
		return trimmed, nil, nil
	}
	provider, err := newAgentServerBinaryProvider()
	if err != nil {
		return "", nil, fmt.Errorf("resolve embedded agent-server provider: %w", err)
	}
	return "", provider, nil
}

func localWorkerCount(maxConcurrency int) (int, error) {
	if maxConcurrency <= 0 {
		return 0, fmt.Errorf("compiled bundle resolved_snapshot.execution.max_concurrency must be > 0; got %d", maxConcurrency)
	}
	return maxConcurrency, nil
}

func buildRunConfirmationSpec(agentName string, authPreview []localexecutor.AuthPreview, pruneBuiltImage int, dryRun bool) runConfirmationSpec {
	spec := runConfirmationSpec{
		AgentName:       strings.TrimSpace(agentName),
		PruneBuiltImage: pruneBuiltImage,
		DryRun:          dryRun,
	}
	for _, item := range authPreview {
		authItem := runConfirmationAuthItem{
			Requirement: strings.TrimSpace(item.RequiredEnv),
		}
		switch item.Mode {
		case localexecutor.AuthPreviewModeAPIKey:
			authItem.Method = "API key"
			authItem.Source = fmt.Sprintf("%s environment variable", strings.TrimSpace(item.RequiredEnv))
		case localexecutor.AuthPreviewModeOAuth:
			switch strings.TrimSpace(item.SourceKind) {
			case "home_file", "override_file":
				authItem.Method = "OAuth credential file"
				authItem.FilePath = strings.TrimSpace(item.SourceLabel)
				authItem.Source = fmt.Sprintf("fallback for %s", strings.TrimSpace(item.RequiredEnv))
			case "macos_keychain":
				authItem.Method = "OAuth credential"
				authItem.Source = fmt.Sprintf("macOS Keychain item %q", strings.TrimSpace(item.SourceLabel))
			default:
				authItem.Method = "OAuth credential"
				authItem.Source = strings.TrimSpace(item.SourceLabel)
			}
		default:
			authItem.Method = strings.TrimSpace(item.RequiredEnv)
		}
		spec.Auth = append(spec.Auth, authItem)
	}
	if len(spec.Auth) == 0 {
		spec.Auth = append(spec.Auth, runConfirmationAuthItem{
			Method: "No agent auth declared",
		})
	}
	return spec
}

type compileProgressReporter interface {
	Update(progress compiler.CompileProgress)
	Finish(success bool)
}

func newCompileProgressReporter(stdout, stderr io.Writer, nonInteractive bool) compileProgressReporter {
	if nonInteractive {
		return newCompileProgressPrinter(stdout)
	}
	return newCompileProgressBar(stderr)
}

type compileProgressBar struct {
	out      io.Writer
	total    int
	done     int
	label    string
	rendered bool
}

func newCompileProgressBar(out io.Writer) *compileProgressBar {
	return &compileProgressBar{out: out}
}

func (p *compileProgressBar) Update(progress compiler.CompileProgress) {
	switch progress.Stage {
	case compiler.CompileStageCasesDiscovered:
		if progress.TotalCases > 0 {
			p.total = progress.TotalCases
		}
		if p.label == "" {
			p.label = "preparing cases"
		}
	case compiler.CompileStageCaseStart:
		if progress.TotalCases > 0 {
			p.total = progress.TotalCases
		}
		if progress.CaseID != "" {
			p.label = "case: " + progress.CaseID
		}
	case compiler.CompileStageCaseDone:
		if progress.TotalCases > 0 {
			p.total = progress.TotalCases
		}
		if progress.CompletedCases > p.done {
			p.done = progress.CompletedCases
		}
		if progress.CaseID != "" {
			p.label = "case done: " + progress.CaseID
		}
	case compiler.CompileStageComplete:
		if progress.TotalCases > 0 {
			p.total = progress.TotalCases
		}
		if p.total > 0 {
			p.done = p.total
		}
		p.label = "compilation complete"
	}
	p.render()
}

func (p *compileProgressBar) Finish(_ bool) {
	if !p.rendered {
		return
	}
	if p.total > 0 && p.done < p.total {
		p.done = p.total
		p.render()
	}
	_, _ = fmt.Fprintln(p.out)
}

func (p *compileProgressBar) render() {
	if p.out == nil || p.total <= 0 {
		return
	}
	const barWidth = 24
	done := p.done
	if done < 0 {
		done = 0
	}
	if done > p.total {
		done = p.total
	}
	filled := done * barWidth / p.total
	bar := strings.Repeat("=", filled) + strings.Repeat("-", barWidth-filled)
	label := strings.TrimSpace(p.label)
	if label == "" {
		label = "compiling"
	}
	_, _ = fmt.Fprintf(p.out, "\rCompiling suite [%s] %d/%d  %s", bar, done, p.total, label)
	p.rendered = true
}

type compileProgressPrinter struct {
	out        io.Writer
	total      int
	lastDone   int
	printedAny bool
}

func newCompileProgressPrinter(out io.Writer) *compileProgressPrinter {
	return &compileProgressPrinter{out: out}
}

func (p *compileProgressPrinter) Update(progress compiler.CompileProgress) {
	if p.out == nil {
		return
	}
	if progress.TotalCases > 0 {
		p.total = progress.TotalCases
	}
	switch progress.Stage {
	case compiler.CompileStageCasesDiscovered:
		if p.total > 0 {
			p.printProgress(0, false)
		}
	case compiler.CompileStageCaseDone:
		done := progress.CompletedCases
		if done < 0 {
			done = 0
		}
		if done == p.lastDone && p.printedAny {
			return
		}
		if done == p.total || done == 1 || done%10 == 0 {
			p.printProgress(done, false)
		}
	case compiler.CompileStageComplete:
		done := p.total
		if progress.CompletedCases > done {
			done = progress.CompletedCases
		}
		p.printProgress(done, true)
	}
}

func (p *compileProgressPrinter) Finish(_ bool) {}

func (p *compileProgressPrinter) printProgress(done int, complete bool) {
	if p.out == nil {
		return
	}
	if done < 0 {
		done = 0
	}
	if p.total > 0 && done > p.total {
		done = p.total
	}
	label := "[compile]"
	if complete {
		_, _ = fmt.Fprintf(p.out, "%s complete %d/%d cases\n", label, done, p.total)
		p.lastDone = done
		p.printedAny = true
		return
	}
	_, _ = fmt.Fprintf(p.out, "%s %d/%d cases\n", label, done, p.total)
	p.lastDone = done
	p.printedAny = true
}

type envFlag struct {
	items map[string]string
}

func (e *envFlag) String() string {
	if e == nil || len(e.items) == 0 {
		return ""
	}
	parts := make([]string, 0, len(e.items))
	for k, v := range e.items {
		parts = append(parts, k+"="+v)
	}
	return strings.Join(parts, ",")
}

func (e *envFlag) Set(value string) error {
	raw := strings.TrimSpace(value)
	if raw == "" {
		return fmt.Errorf("env value must be KEY=VALUE")
	}
	parts := strings.SplitN(raw, "=", 2)
	if len(parts) != 2 {
		return fmt.Errorf("env value %q must be KEY=VALUE", value)
	}
	key := strings.TrimSpace(parts[0])
	if key == "" {
		return fmt.Errorf("env key must not be empty")
	}
	if e.items == nil {
		e.items = map[string]string{}
	}
	e.items[key] = parts[1]
	return nil
}

func (e *envFlag) Clone() map[string]string {
	if len(e.items) == 0 {
		return nil
	}
	out := make(map[string]string, len(e.items))
	for k, v := range e.items {
		out[k] = v
	}
	return out
}

type bindFlag struct {
	items map[string]string
}

func (b *bindFlag) String() string {
	if b == nil || len(b.items) == 0 {
		return ""
	}
	parts := make([]string, 0, len(b.items))
	for host, container := range b.items {
		parts = append(parts, host+"="+container)
	}
	return strings.Join(parts, ",")
}

func (b *bindFlag) Set(value string) error {
	raw := strings.TrimSpace(value)
	if raw == "" {
		return fmt.Errorf("bind value must be HOST_PATH=CONTAINER_PATH")
	}
	parts := strings.SplitN(raw, "=", 2)
	if len(parts) != 2 {
		return fmt.Errorf("bind value %q must be HOST_PATH=CONTAINER_PATH", value)
	}
	hostPath := strings.TrimSpace(parts[0])
	containerPath := strings.TrimSpace(parts[1])
	if hostPath == "" || containerPath == "" {
		return fmt.Errorf("bind value %q must not contain empty paths", value)
	}
	if b.items == nil {
		b.items = map[string]string{}
	}
	b.items[hostPath] = containerPath
	return nil
}

func (b *bindFlag) Clone() map[string]string {
	if len(b.items) == 0 {
		return nil
	}
	out := make(map[string]string, len(b.items))
	for host, container := range b.items {
		out[host] = container
	}
	return out
}
