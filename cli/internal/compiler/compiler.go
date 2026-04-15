package compiler

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	toml "github.com/pelletier/go-toml/v2"

	"github.com/marginlab/margin-eval/runner/runner-core/agentdef"
	"github.com/marginlab/margin-eval/runner/runner-core/runbundle"
	"github.com/marginlab/margin-eval/runner/runner-core/testassets"
)

const (
	defaultProjectID        = "proj_local"
	defaultPTYCols          = 120
	defaultPTYRows          = 40
	suitePreamblePromptFile = "preamble-prompt.md"
	casePromptFile          = "prompt.md"
	caseOracleDir           = "oracle"
	caseOracleSolveScript   = "solve.sh"
)

type oracleRequirementError struct {
	caseID string
}

func (e *oracleRequirementError) Error() string {
	return fmt.Sprintf("case %q is missing oracle/%s", e.caseID, caseOracleSolveScript)
}

func Compile(in CompileInput) (runbundle.Bundle, error) {
	suitePath, err := requireDir(in.SuitePath, "suite")
	if err != nil {
		return runbundle.Bundle{}, err
	}
	agentConfigPath, err := requireDir(in.AgentConfigPath, "agent config")
	if err != nil {
		return runbundle.Bundle{}, err
	}
	evalPath, err := requireFile(in.EvalPath, "eval")
	if err != nil {
		return runbundle.Bundle{}, err
	}

	progress := in.Progress

	executionMode := normalizeExecutionMode(in.ExecutionMode)

	suiteDoc, cases, err := compileSuite(suitePath, executionMode, progress)
	if err != nil {
		return runbundle.Bundle{}, err
	}
	agentSpec, err := compileAgent(agentConfigPath)
	if err != nil {
		return runbundle.Bundle{}, err
	}
	evalDoc, err := compileEval(evalPath)
	if err != nil {
		return runbundle.Bundle{}, err
	}

	bundleID := strings.TrimSpace(in.BundleID)
	if bundleID == "" {
		bundleID = fmt.Sprintf("bun_%d", time.Now().UTC().UnixNano())
	}
	createdAt := in.CreatedAt.UTC()
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	submitProjectID := strings.TrimSpace(in.SubmitProjectID)
	if submitProjectID == "" {
		submitProjectID = defaultProjectID
	}

	bundleName := strings.TrimSpace(evalDoc.Name)
	if bundleName == "" {
		bundleName = strings.TrimSpace(suiteDoc.Name)
	}

	bundle := runbundle.Bundle{
		SchemaVersion: runbundle.SchemaVersionV1,
		BundleID:      bundleID,
		CreatedAt:     createdAt,
		Source: runbundle.Source{
			Kind:            runbundle.SourceKindLocalFiles,
			SubmitProjectID: submitProjectID,
		},
		ResolvedSnapshot: runbundle.ResolvedSnapshot{
			Name: bundleName,
			Execution: runbundle.Execution{
				Mode:                  executionMode,
				MaxConcurrency:        evalDoc.MaxConcurrency,
				FailFast:              evalDoc.FailFast,
				RetryCount:            valueOrZero(evalDoc.RetryCount),
				InstanceTimeoutSecond: evalDoc.InstanceTimeoutSecond,
			},
			Agent:       agentSpec,
			RunDefaults: compileRunDefaults(in),
			Cases:       cases,
		},
	}

	if err := runbundle.Validate(bundle); err != nil {
		return runbundle.Bundle{}, fmt.Errorf("bundle validation failed: %w", err)
	}
	if progress != nil {
		progress(CompileProgress{
			Stage:          CompileStageComplete,
			CompletedCases: len(cases),
			TotalCases:     len(cases),
			Message:        "bundle compilation complete",
		})
	}
	return bundle, nil
}

func normalizeExecutionMode(mode runbundle.ExecutionMode) runbundle.ExecutionMode {
	if strings.TrimSpace(string(mode)) == "" {
		return runbundle.ExecutionModeFull
	}
	return mode
}

func compileSuite(suitePath string, executionMode runbundle.ExecutionMode, progress CompileProgressFunc) (suiteFile, []runbundle.Case, error) {
	suiteTomlPath := filepath.Join(suitePath, "suite.toml")
	var suite suiteFile
	raw, err := decodeTOMLFile(suiteTomlPath, &suite)
	if err != nil {
		return suiteFile{}, nil, err
	}
	if err := rejectVersionFields(raw, suiteTomlPath); err != nil {
		return suiteFile{}, nil, err
	}
	if strings.TrimSpace(suite.Kind) != "test_suite" {
		return suiteFile{}, nil, fmt.Errorf("%s kind must be %q", suiteTomlPath, "test_suite")
	}
	if len(suite.Cases) == 0 {
		return suiteFile{}, nil, fmt.Errorf("%s cases must contain at least one case", suiteTomlPath)
	}

	casesRoot := filepath.Join(suitePath, "cases")
	if _, err := requireDir(casesRoot, "suite cases"); err != nil {
		return suiteFile{}, nil, err
	}
	suitePreamble, err := readOptionalPromptFile(filepath.Join(suitePath, suitePreamblePromptFile))
	if err != nil {
		return suiteFile{}, nil, err
	}

	cases := make([]runbundle.Case, 0, len(suite.Cases))
	missingOracles := make([]string, 0)
	if progress != nil {
		progress(CompileProgress{
			Stage:      CompileStageCasesDiscovered,
			TotalCases: len(suite.Cases),
			Message:    "compiling cases",
		})
	}
	for _, caseName := range suite.Cases {
		currentCase := len(cases) + 1
		trimmed := strings.TrimSpace(caseName)
		if trimmed == "" {
			return suiteFile{}, nil, fmt.Errorf("%s cases must not contain empty values", suiteTomlPath)
		}
		if progress != nil {
			progress(CompileProgress{
				Stage:       CompileStageCaseStart,
				CaseID:      trimmed,
				CurrentCase: currentCase,
				TotalCases:  len(suite.Cases),
				Message:     "compiling case",
			})
		}
		compiled, err := compileCase(filepath.Join(casesRoot, trimmed), trimmed, suitePreamble, executionMode)
		if err != nil {
			var missingOracleErr *oracleRequirementError
			if errors.As(err, &missingOracleErr) {
				missingOracles = append(missingOracles, missingOracleErr.caseID)
				continue
			}
			return suiteFile{}, nil, err
		}
		cases = append(cases, compiled)
		if progress != nil {
			progress(CompileProgress{
				Stage:          CompileStageCaseDone,
				CaseID:         trimmed,
				CurrentCase:    currentCase,
				CompletedCases: len(cases),
				TotalCases:     len(suite.Cases),
				Message:        "case compiled",
			})
		}
	}
	if len(missingOracles) > 0 {
		return suite, nil, fmt.Errorf(
			"oracle_run requires every case to include oracle/%s; missing for cases: %s",
			caseOracleSolveScript,
			strings.Join(missingOracles, ", "),
		)
	}
	return suite, cases, nil
}

func compileCase(caseDir, expectedName, suitePreamble string, executionMode runbundle.ExecutionMode) (runbundle.Case, error) {
	if _, err := requireDir(caseDir, "case"); err != nil {
		return runbundle.Case{}, err
	}

	caseTomlPath := filepath.Join(caseDir, "case.toml")
	var c caseFile
	raw, err := decodeTOMLFile(caseTomlPath, &c)
	if err != nil {
		return runbundle.Case{}, err
	}
	if err := rejectVersionFields(raw, caseTomlPath); err != nil {
		return runbundle.Case{}, err
	}
	if strings.TrimSpace(c.Kind) != "test_case" {
		return runbundle.Case{}, fmt.Errorf("%s kind must be %q", caseTomlPath, "test_case")
	}
	if strings.TrimSpace(c.Name) != expectedName {
		return runbundle.Case{}, fmt.Errorf("%s name %q must match directory name %q", caseTomlPath, c.Name, expectedName)
	}
	oracleAssets, err := compileCaseOracleAssets(caseDir, expectedName, executionMode)
	if err != nil {
		return runbundle.Case{}, err
	}

	prompt, err := readRequiredPromptFile(filepath.Join(caseDir, casePromptFile))
	if err != nil {
		return runbundle.Case{}, err
	}
	initialPrompt := composeInitialPrompt(suitePreamble, prompt)

	testsDir := filepath.Join(caseDir, "tests")
	if _, err := requireDir(testsDir, "case tests directory"); err != nil {
		return runbundle.Case{}, err
	}
	testScriptPath := filepath.Join(testsDir, "test.sh")
	if _, err := requireFile(testScriptPath, "case test script"); err != nil {
		return runbundle.Case{}, err
	}
	packedAssets, err := testassets.PackDir(testsDir)
	if err != nil {
		return runbundle.Case{}, fmt.Errorf("package %s: %w", testsDir, err)
	}
	resolvedImage := strings.TrimSpace(c.Image)
	var imageBuild *runbundle.CaseImageBuild
	if resolvedImage == "" {
		envDir := filepath.Join(caseDir, "env")
		dockerfilePath := filepath.Join(envDir, "Dockerfile")
		if _, err := requireFile(dockerfilePath, "case env dockerfile"); err != nil {
			return runbundle.Case{}, fmt.Errorf("case %q must set image or include env/Dockerfile", expectedName)
		}
		packedBuildContext, err := testassets.PackDir(envDir)
		if err != nil {
			return runbundle.Case{}, fmt.Errorf("package %s: %w", envDir, err)
		}
		imageBuild = &runbundle.CaseImageBuild{
			Context:           packedBuildContext,
			DockerfileRelPath: "Dockerfile",
		}
	}

	return runbundle.Case{
		CaseID:            strings.TrimSpace(c.Name),
		Image:             strings.TrimSpace(resolvedImage),
		ImageBuild:        imageBuild,
		OracleAssets:      oracleAssets,
		InitialPrompt:     initialPrompt,
		AgentCwd:          strings.TrimSpace(c.AgentCwd),
		TestCommand:       []string{"bash", "-c", "tests/test.sh"},
		TestCwd:           strings.TrimSpace(c.TestCwd),
		TestTimeoutSecond: c.TestTimeoutSeconds,
		TestAssets:        packedAssets,
	}, nil
}

func compileCaseOracleAssets(caseDir, caseID string, executionMode runbundle.ExecutionMode) (*runbundle.OracleAssets, error) {
	oracleDir := filepath.Join(caseDir, caseOracleDir)
	info, err := os.Stat(oracleDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if executionMode == runbundle.ExecutionModeOracleRun {
				return nil, &oracleRequirementError{caseID: caseID}
			}
			return nil, nil
		}
		return nil, fmt.Errorf("stat case oracle dir %s: %w", oracleDir, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("case oracle path %s must be a directory", oracleDir)
	}
	solveScriptPath := filepath.Join(oracleDir, caseOracleSolveScript)
	if _, err := requireFile(solveScriptPath, "case oracle solve script"); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			err = fmt.Errorf("case %q defines oracle/ but is missing oracle/%s", caseID, caseOracleSolveScript)
		}
		if executionMode == runbundle.ExecutionModeOracleRun {
			return nil, &oracleRequirementError{caseID: caseID}
		}
		return nil, err
	}
	if executionMode != runbundle.ExecutionModeOracleRun {
		return nil, nil
	}
	packedAssets, err := testassets.PackDir(oracleDir)
	if err != nil {
		return nil, fmt.Errorf("package %s: %w", oracleDir, err)
	}
	return &packedAssets, nil
}

func readRequiredPromptFile(path string) (string, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	return normalizePromptText(path, body)
}

func readOptionalPromptFile(path string) (string, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	return normalizePromptText(path, body)
}

func normalizePromptText(path string, body []byte) (string, error) {
	prompt := strings.TrimSpace(string(body))
	if prompt == "" {
		return "", fmt.Errorf("%s must not be empty", path)
	}
	return prompt, nil
}

func composeInitialPrompt(suitePreamble, casePrompt string) string {
	if suitePreamble == "" {
		return casePrompt
	}
	return suitePreamble + "\n\n" + casePrompt
}

func compileAgent(agentPath string) (runbundle.Agent, error) {
	configTomlPath := filepath.Join(agentPath, "config.toml")
	var cfgFile agentConfigFile
	raw, err := decodeTOMLFile(configTomlPath, &cfgFile)
	if err != nil {
		return runbundle.Agent{}, err
	}
	if err := rejectVersionFields(raw, configTomlPath); err != nil {
		return runbundle.Agent{}, err
	}
	if strings.TrimSpace(cfgFile.Kind) != "agent_config" {
		return runbundle.Agent{}, fmt.Errorf("%s kind must be %q", configTomlPath, "agent_config")
	}
	if strings.TrimSpace(cfgFile.Name) == "" {
		return runbundle.Agent{}, fmt.Errorf("%s name is required", configTomlPath)
	}
	defRef := strings.TrimSpace(cfgFile.Definition)
	if defRef == "" {
		return runbundle.Agent{}, fmt.Errorf("%s definition is required", configTomlPath)
	}
	definitionPath, err := resolveAgentDefinitionPath(agentPath, defRef)
	if err != nil {
		return runbundle.Agent{}, err
	}
	definitionTomlPath := filepath.Join(definitionPath, "definition.toml")
	var defFile agentDefinitionFile
	defRaw, err := decodeTOMLFile(definitionTomlPath, &defFile)
	if err != nil {
		return runbundle.Agent{}, err
	}
	if err := rejectVersionFields(defRaw, definitionTomlPath); err != nil {
		return runbundle.Agent{}, err
	}
	if strings.TrimSpace(defFile.Kind) != "agent_definition" {
		return runbundle.Agent{}, fmt.Errorf("%s kind must be %q", definitionTomlPath, "agent_definition")
	}
	packedDefinition, err := testassets.PackDir(definitionPath)
	if err != nil {
		return runbundle.Agent{}, fmt.Errorf("package %s: %w", definitionPath, err)
	}
	definition := agentdef.DefinitionSnapshot{
		Manifest: compileDefinitionManifest(defFile),
		Package:  packedDefinition,
	}
	config := agentdef.ConfigSpec{
		Name:        strings.TrimSpace(cfgFile.Name),
		Description: strings.TrimSpace(cfgFile.Description),
		Mode:        agentdef.ConfigMode(strings.TrimSpace(cfgFile.Mode)),
		Input:       cloneAnyMap(cfgFile.Input),
	}
	config.Skills, err = compileLocalSkills(agentPath, cfgFile.Skills)
	if err != nil {
		return runbundle.Agent{}, fmt.Errorf("resolve config skills: %w", err)
	}
	config.AgentsMD, err = compileLocalAgentsMD(agentPath, cfgFile.AgentsMD)
	if err != nil {
		return runbundle.Agent{}, fmt.Errorf("resolve config agents_md: %w", err)
	}
	if cfgFile.Unified != nil {
		unified := *cfgFile.Unified
		config.Unified = &unified
	}
	normalizedConfig, err := agentdef.ValidateAndNormalizeConfigSpec(definition, config)
	if err != nil {
		return runbundle.Agent{}, fmt.Errorf("validate agent definition/config: %w", err)
	}
	return runbundle.Agent{
		Definition: definition,
		Config:     normalizedConfig,
	}, nil
}

func compileEval(evalPath string) (evalFile, error) {
	var ef evalFile
	raw, err := decodeTOMLFile(evalPath, &ef)
	if err != nil {
		return evalFile{}, err
	}
	if err := rejectVersionFields(raw, evalPath); err != nil {
		return evalFile{}, err
	}
	if strings.TrimSpace(ef.Kind) != "eval_config" {
		return evalFile{}, fmt.Errorf("%s kind must be %q", evalPath, "eval_config")
	}
	if ef.MaxConcurrency <= 0 {
		return evalFile{}, fmt.Errorf("%s max_concurrency must be > 0", evalPath)
	}
	retryCount := 1
	if ef.RetryCount != nil {
		retryCount = *ef.RetryCount
	}
	if retryCount < 0 {
		return evalFile{}, fmt.Errorf("%s retry_count must be >= 0", evalPath)
	}
	ef.RetryCount = &retryCount
	if ef.InstanceTimeoutSecond <= 0 {
		return evalFile{}, fmt.Errorf("%s instance_timeout_seconds must be > 0", evalPath)
	}
	return ef, nil
}

func compileDefinitionManifest(defFile agentDefinitionFile) agentdef.Manifest {
	localCredentials := make([]agentdef.AuthLocalCredential, 0, len(defFile.Auth.LocalCredentials))
	for _, item := range defFile.Auth.LocalCredentials {
		sources := make([]agentdef.AuthLocalSource, 0, len(item.Sources))
		for _, source := range item.Sources {
			sources = append(sources, agentdef.AuthLocalSource{
				Kind:        agentdef.AuthLocalSourceKind(strings.TrimSpace(source.Kind)),
				HomeRelPath: strings.TrimSpace(source.HomeRelPath),
				Service:     strings.TrimSpace(source.Service),
				Platforms:   append([]string(nil), source.Platforms...),
			})
		}
		localCredentials = append(localCredentials, agentdef.AuthLocalCredential{
			RequiredEnv:      strings.TrimSpace(item.RequiredEnv),
			RunHomeRelPath:   strings.TrimSpace(item.RunHomeRelPath),
			ValidateJSONPath: strings.TrimSpace(item.ValidateJSONPath),
			Sources:          sources,
		})
	}
	providers := make([]agentdef.AuthProvider, 0, len(defFile.Auth.Providers))
	for _, item := range defFile.Auth.Providers {
		providers = append(providers, agentdef.AuthProvider{
			Name:        strings.TrimSpace(item.Name),
			AuthMode:    agentdef.AuthProviderMode(strings.TrimSpace(item.AuthMode)),
			RequiredEnv: append([]string(nil), item.RequiredEnv...),
		})
	}
	manifest := agentdef.Manifest{
		Kind:        strings.TrimSpace(defFile.Kind),
		Name:        strings.TrimSpace(defFile.Name),
		Description: strings.TrimSpace(defFile.Description),
		Auth: agentdef.AuthSpec{
			RequiredEnv:      append([]string(nil), defFile.Auth.RequiredEnv...),
			LocalCredentials: localCredentials,
			Providers:        providers,
		},
		Toolchains: compileToolchains(defFile.Toolchains),
		Config: agentdef.DefinitionConfigSpec{
			SchemaRelPath: strings.TrimSpace(defFile.Config.Schema),
		},
		Install: agentdef.InstallSpec{},
		Run: agentdef.RunSpec{
			PrepareHook: agentdef.HookRef{Path: strings.TrimSpace(defFile.Run.Prepare)},
		},
	}
	if defFile.Auth.ProviderSelection != nil {
		manifest.Auth.ProviderSelection = &agentdef.AuthProviderSelection{
			DirectInputField:              strings.TrimSpace(defFile.Auth.ProviderSelection.DirectInputField),
			UnifiedModelProviderQualified: defFile.Auth.ProviderSelection.UnifiedModelProviderQualified,
		}
	}
	if path := strings.TrimSpace(defFile.Config.Validate); path != "" {
		manifest.Config.ValidateHook = &agentdef.HookRef{Path: path}
	}
	if defFile.Skills != nil {
		manifest.Skills = &agentdef.SkillManifestSpec{HomeRelDir: strings.TrimSpace(defFile.Skills.HomeRelDir)}
	}
	if defFile.AgentsMD != nil {
		manifest.AgentsMD = &agentdef.AgentsMDManifestSpec{Filename: strings.TrimSpace(defFile.AgentsMD.Filename)}
	}
	if defFile.Config.Unified != nil {
		manifest.Config.Unified = &agentdef.UnifiedManifestSpec{
			TranslateHook:          agentdef.HookRef{Path: strings.TrimSpace(defFile.Config.Unified.Translate)},
			AllowedModels:          append([]string(nil), defFile.Config.Unified.AllowedModels...),
			AllowedReasoningLevels: append([]string(nil), defFile.Config.Unified.AllowedReasoningLevels...),
		}
	}
	if path := strings.TrimSpace(defFile.Install.Check); path != "" {
		manifest.Install.CheckHook = &agentdef.HookRef{Path: path}
	}
	if path := strings.TrimSpace(defFile.Install.Run); path != "" {
		manifest.Install.RunHook = &agentdef.HookRef{Path: path}
	}
	if defFile.Snapshot != nil && strings.TrimSpace(defFile.Snapshot.Prepare) != "" {
		manifest.Snapshot = &agentdef.SnapshotSpec{PrepareHook: agentdef.HookRef{Path: strings.TrimSpace(defFile.Snapshot.Prepare)}}
	}
	if defFile.Trajectory != nil && strings.TrimSpace(defFile.Trajectory.Collect) != "" {
		manifest.Trajectory = &agentdef.TrajectorySpec{CollectHook: agentdef.HookRef{Path: strings.TrimSpace(defFile.Trajectory.Collect)}}
	}
	return manifest
}

func valueOrZero(v *int) int {
	if v == nil {
		return 0
	}
	return *v
}

func compileToolchains(spec definitionToolchainFile) agentdef.ToolchainSpec {
	var out agentdef.ToolchainSpec
	if spec.Node != nil {
		out.Node = &agentdef.NodeToolchainSpec{
			Minimum:   strings.TrimSpace(spec.Node.Minimum),
			Preferred: strings.TrimSpace(spec.Node.Preferred),
		}
	}
	return out
}

func compileRunDefaults(in CompileInput) runbundle.RunDefault {
	env := map[string]string{"TERM": "xterm-256color"}
	for k, v := range in.RunEnv {
		key := strings.TrimSpace(k)
		if key == "" {
			continue
		}
		env[key] = v
	}
	cols := in.PTYCols
	if cols <= 0 {
		cols = defaultPTYCols
	}
	rows := in.PTYRows
	if rows <= 0 {
		rows = defaultPTYRows
	}
	return runbundle.RunDefault{Env: env, PTY: runbundle.PTY{Cols: cols, Rows: rows}}
}

func compileLocalSkills(configDir string, files []agentConfigSkillFile) ([]agentdef.SkillSpec, error) {
	if len(files) == 0 {
		return nil, nil
	}
	skills := make([]agentdef.SkillSpec, 0, len(files))
	for idx, file := range files {
		rawPath := strings.TrimSpace(file.Path)
		if rawPath == "" {
			return nil, fmt.Errorf("skills[%d].path is required", idx)
		}
		skillPath := rawPath
		if !filepath.IsAbs(skillPath) {
			skillPath = filepath.Join(configDir, filepath.FromSlash(rawPath))
		}
		skillPath, err := requireDir(skillPath, fmt.Sprintf("skill %d", idx))
		if err != nil {
			return nil, err
		}
		skill, err := agentdef.LoadSkillSpecFromDir(skillPath)
		if err != nil {
			return nil, fmt.Errorf("skills[%d]: %w", idx, err)
		}
		skills = append(skills, skill)
	}
	return skills, nil
}

func compileLocalAgentsMD(configDir string, file *agentConfigAgentsMDFile) (*agentdef.AgentsMDSpec, error) {
	if file == nil {
		return nil, nil
	}
	rawPath := strings.TrimSpace(file.Path)
	if rawPath == "" {
		return nil, fmt.Errorf("agents_md.path is required")
	}
	agentsMDPath := rawPath
	if !filepath.IsAbs(agentsMDPath) {
		agentsMDPath = filepath.Join(configDir, filepath.FromSlash(rawPath))
	}
	agentsMDPath, err := requireFile(agentsMDPath, "agents_md")
	if err != nil {
		return nil, err
	}
	body, err := os.ReadFile(agentsMDPath)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", agentsMDPath, err)
	}
	return &agentdef.AgentsMDSpec{Content: string(body)}, nil
}

func resolveAgentDefinitionPath(configDir, definitionRef string) (string, error) {
	trimmed := strings.TrimSpace(definitionRef)
	if trimmed == "" {
		return "", fmt.Errorf("agent definition path is required")
	}
	if filepath.IsAbs(trimmed) || strings.ContainsRune(trimmed, filepath.Separator) || strings.Contains(trimmed, "/") {
		resolved := trimmed
		if !filepath.IsAbs(resolved) {
			resolved = filepath.Join(configDir, filepath.FromSlash(trimmed))
		}
		return requireDir(resolved, "agent definition")
	}

	localCandidate := filepath.Join(configDir, trimmed)
	if info, err := os.Stat(localCandidate); err == nil {
		if !info.IsDir() {
			return "", fmt.Errorf("agent definition path %s must be a directory", localCandidate)
		}
		return filepath.Abs(localCandidate)
	} else if !errors.Is(err, os.ErrNotExist) {
		abs, absErr := filepath.Abs(localCandidate)
		if absErr != nil {
			return "", fmt.Errorf("resolve agent definition path %q: %w", localCandidate, absErr)
		}
		return "", fmt.Errorf("stat agent definition path %s: %w", abs, err)
	}

	marginHome, err := marginHomeDir()
	if err != nil {
		return "", err
	}
	return requireDir(filepath.Join(marginHome, "configs", "agent-definitions", trimmed), "agent definition")
}

func decodeTOMLFile(path string, out any) (map[string]any, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var raw map[string]any
	if err := toml.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("decode TOML %s: %w", path, err)
	}
	if err := toml.Unmarshal(body, out); err != nil {
		return nil, fmt.Errorf("decode TOML %s: %w", path, err)
	}
	return raw, nil
}

func rejectVersionFields(raw map[string]any, path string) error {
	if raw == nil {
		return nil
	}
	if _, ok := raw["version"]; ok {
		return fmt.Errorf("%s must not define %q in CLI mode", path, "version")
	}
	if _, ok := raw["base_version"]; ok {
		return fmt.Errorf("%s must not define %q in CLI mode", path, "base_version")
	}
	return nil
}

func requireDir(path string, label string) (string, error) {
	cleaned := strings.TrimSpace(path)
	if cleaned == "" {
		return "", fmt.Errorf("%s path is required", label)
	}
	abs, err := filepath.Abs(cleaned)
	if err != nil {
		return "", fmt.Errorf("resolve %s path %q: %w", label, cleaned, err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("stat %s path %s: %w", label, abs, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s path %s must be a directory", label, abs)
	}
	return abs, nil
}

func requireFile(path string, label string) (string, error) {
	cleaned := strings.TrimSpace(path)
	if cleaned == "" {
		return "", fmt.Errorf("%s path is required", label)
	}
	abs, err := filepath.Abs(cleaned)
	if err != nil {
		return "", fmt.Errorf("resolve %s path %q: %w", label, cleaned, err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("stat %s path %s: %w", label, abs, err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("%s path %s must be a file", label, abs)
	}
	return abs, nil
}

func marginHomeDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve user home: %w", err)
	}
	return filepath.Join(home, ".margin"), nil
}

func cloneAnyMap(src map[string]any) map[string]any {
	if src == nil {
		return nil
	}
	out := make(map[string]any, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}
