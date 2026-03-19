package run

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/marginlab/margin-eval/agent-server/internal/agentruntime"
	"github.com/marginlab/margin-eval/agent-server/internal/apperr"
	"github.com/marginlab/margin-eval/agent-server/internal/config"
	"github.com/marginlab/margin-eval/agent-server/internal/fsutil"
	"github.com/marginlab/margin-eval/agent-server/internal/logutil"
	"github.com/marginlab/margin-eval/agent-server/internal/ptyws"
	"github.com/marginlab/margin-eval/agent-server/internal/state"
	"github.com/creack/pty"
	"github.com/google/uuid"
)

type launcher struct {
	cfg     config.Config
	runtime *agentruntime.Runtime
}

type launchInput struct {
	RunID      string
	StartedAt  time.Time
	Request    StartRequest
	AgentState state.AgentRecord
}

type preparedLaunch struct {
	runCtx   agentruntime.RunContext
	execSpec agentruntime.ExecSpec
}

func newLauncher(cfg config.Config, runtime *agentruntime.Runtime) *launcher {
	return &launcher{
		cfg:     cfg,
		runtime: runtime,
	}
}

func (l *launcher) Prepare(ctx context.Context, input launchInput) (preparedLaunch, error) {
	runHome := filepath.Join(l.cfg.StateDir, "runs", input.RunID, "home")
	artifactsDir := filepath.Join(l.cfg.StateDir, "runs", input.RunID, "artifacts")
	if err := fsutil.EnsureDir(runHome, 0o755); err != nil {
		return preparedLaunch{}, internalError(apperr.CodePrelaunchFailed, "create run home failed", map[string]any{"run_home": runHome}, err)
	}
	if err := fsutil.EnsureDir(artifactsDir, 0o755); err != nil {
		return preparedLaunch{}, internalError(apperr.CodePrelaunchFailed, "create artifacts dir failed", map[string]any{"artifacts_dir": artifactsDir}, err)
	}

	validatedCWD, err := fsutil.ValidateExistingDirUnderRoot(input.Request.CWD, l.cfg.WorkspacesDir)
	if err != nil {
		return preparedLaunch{}, invalidError(apperr.CodeInvalidCWD, err.Error(), nil, err)
	}
	if input.AgentState.Definition == nil || input.AgentState.Install == nil || input.AgentState.Config == nil {
		return preparedLaunch{}, conflictError(apperr.CodeAgentNotConfigured, "agent definition, install, and config must be present", nil, nil)
	}

	requiredEnv, err := resolveRequiredEnv(l.runtime.RequiredEnv(input.AgentState), input.Request.Env, input.Request.AuthFiles)
	if err != nil {
		return preparedLaunch{}, err
	}
	if err := materializeAuthFiles(runHome, input.Request.AuthFiles); err != nil {
		return preparedLaunch{}, internalError(apperr.CodePrelaunchFailed, "copy auth files into run home failed", nil, err)
	}

	env := mergeEnvironment(os.Environ(), input.Request.Env)
	env = mergeEnvironmentMap(env, requiredEnv)
	applyRunEnvironmentDefaults(env, runHome)

	sessionID := uuid.NewSHA1(uuid.NameSpaceURL, []byte(input.RunID)).String()
	runCtx := agentruntime.RunContext{
		RunID:         input.RunID,
		SessionID:     sessionID,
		CWD:           validatedCWD,
		RunHome:       runHome,
		ArtifactsDir:  artifactsDir,
		Env:           env,
		RunArgs:       append([]string(nil), input.Request.Args...),
		InitialPrompt: input.Request.InitialPrompt,
	}

	execSpec, err := l.runtime.PrepareRun(ctx, input.AgentState, runCtx)
	if err != nil {
		return preparedLaunch{}, internalError(apperr.CodePrelaunchFailed, "prepare run command failed", map[string]any{"error": err.Error()}, err)
	}
	return preparedLaunch{runCtx: runCtx, execSpec: execSpec}, nil
}

func (l *launcher) Launch(ctx context.Context, input launchInput) (*activeRun, StartResponse, error) {
	prepared, err := l.Prepare(ctx, input)
	if err != nil {
		return nil, StartResponse{}, err
	}
	active, err := l.startPrepared(input, prepared)
	if err != nil {
		return nil, StartResponse{}, err
	}
	return active, buildStartResponse(input.RunID, state.RunStateRunning, input.StartedAt, active.cmd.Process.Pid), nil
}

func (l *launcher) startPrepared(input launchInput, prepared preparedLaunch) (*activeRun, error) {
	cmd := exec.Command(prepared.execSpec.Path, prepared.execSpec.Args...)
	cmd.Env = prepared.execSpec.Env
	cmd.Dir = prepared.execSpec.Dir

	winsize := &pty.Winsize{Cols: 120, Rows: 40}
	if input.Request.PTY.Cols > 0 {
		winsize.Cols = uint16(input.Request.PTY.Cols)
	}
	if input.Request.PTY.Rows > 0 {
		winsize.Rows = uint16(input.Request.PTY.Rows)
	}

	ptyFile, err := pty.StartWithSize(cmd, winsize)
	if err != nil {
		return nil, internalError(apperr.CodePrelaunchFailed, "start PTY process failed", map[string]any{"error": err.Error()}, err)
	}

	ptyLogPath := filepath.Join(prepared.runCtx.ArtifactsDir, "pty.log")
	ptyLogFile, err := os.OpenFile(ptyLogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		_ = ptyFile.Close()
		_ = killProcessGroup(cmd.Process.Pid, 9)
		return nil, internalError(apperr.CodePrelaunchFailed, "open PTY log file failed", map[string]any{"path": ptyLogPath}, err)
	}

	hub := ptyws.NewHub(ptyFile, l.cfg.ReplayBufferBytes, func(cols, rows int) error {
		return pty.Setsize(ptyFile, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
	})

	active := &activeRun{
		runID:       input.RunID,
		agent:       input.AgentState,
		runContext:  prepared.runCtx,
		cmd:         cmd,
		ptyFile:     ptyFile,
		ptyLogFile:  ptyLogFile,
		hub:         hub,
		finalizedCh: make(chan struct{}),
	}
	return active, nil
}

func resolveRequiredEnv(required []string, requested map[string]string, authFiles []AuthFile) (map[string]string, error) {
	if len(required) == 0 {
		return map[string]string{}, nil
	}
	authFilesByEnv := make(map[string]AuthFile, len(authFiles))
	for _, file := range authFiles {
		key := strings.TrimSpace(file.RequiredEnv)
		if key == "" {
			continue
		}
		authFilesByEnv[key] = file
	}
	sorted := append([]string(nil), required...)
	sort.Strings(sorted)
	resolved := make(map[string]string, len(sorted))
	missing := make([]string, 0)
	for _, key := range sorted {
		trimmed := strings.TrimSpace(key)
		if trimmed == "" {
			continue
		}
		if _, requestedOverride := requested[trimmed]; requestedOverride {
			return nil, invalidError(apperr.CodeInvalidEnv, "runtime env cannot override required agent env", map[string]any{
				"env_key": trimmed,
			}, nil)
		}
		value, ok := os.LookupEnv(trimmed)
		if ok && strings.TrimSpace(value) != "" {
			resolved[trimmed] = value
			continue
		}
		if _, ok := authFilesByEnv[trimmed]; ok {
			continue
		}
		if !ok || strings.TrimSpace(value) == "" {
			missing = append(missing, trimmed)
			continue
		}
	}
	if len(missing) > 0 {
		return nil, invalidError(apperr.CodeMissingRequiredEnv, "required agent env is not configured", map[string]any{
			"missing_keys": missing,
		}, nil)
	}
	return resolved, nil
}

func materializeAuthFiles(runHome string, authFiles []AuthFile) error {
	for _, file := range authFiles {
		sourcePath := strings.TrimSpace(file.SourcePath)
		if sourcePath == "" {
			continue
		}
		payload, err := os.ReadFile(sourcePath)
		if err != nil {
			return fmt.Errorf("read auth file %q: %w", sourcePath, err)
		}
		targetPath, err := fsutil.ValidatePathUnderRoot(filepath.Join(runHome, filepath.FromSlash(file.RunHomeRelPath)), runHome)
		if err != nil {
			return fmt.Errorf("resolve auth file target %q: %w", file.RunHomeRelPath, err)
		}
		if err := fsutil.WriteFileAtomic(targetPath, payload, 0o600); err != nil {
			return fmt.Errorf("write auth file %q: %w", targetPath, err)
		}
	}
	return nil
}

func logAgentName(agent state.AgentRecord) string {
	if agent.Definition == nil {
		return ""
	}
	return agent.Definition.Snapshot.Manifest.Name
}

func logAgentStart(active *activeRun) {
	logutil.Info("run.started", map[string]any{
		"run_id": active.runID,
		"agent":  logAgentName(active.agent),
		"pid":    active.cmd.Process.Pid,
		"cwd":    active.runContext.CWD,
		"env":    logutil.RedactEnv(active.runContext.Env),
	})
}

func logDryRun(agent state.AgentRecord, runCtx agentruntime.RunContext) {
	logutil.Info("run.dry_run_completed", map[string]any{
		"run_id": runCtx.RunID,
		"agent":  logAgentName(agent),
		"cwd":    runCtx.CWD,
		"env":    logutil.RedactEnv(runCtx.Env),
	})
}

func buildStartResponse(runID string, runState state.RunState, startedAt time.Time, pid int) StartResponse {
	response := StartResponse{
		RunID:     runID,
		State:     string(runState),
		StartedAt: startedAt.Format(time.RFC3339),
	}
	if runState == state.RunStateRunning && pid > 0 {
		response.PID = &pid
		response.Attach.WSPath = "/v1/run/pty?run_id=" + runID
		response.Attach.Protocol = "pty.v1"
	}
	return response
}
