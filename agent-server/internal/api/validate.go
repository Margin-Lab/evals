package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path"
	"regexp"
	"strings"

	"github.com/marginlab/margin-eval/agent-server/internal/apperr"
	"github.com/marginlab/margin-eval/agent-server/internal/config"
	"github.com/marginlab/margin-eval/agent-server/internal/fsutil"
	"github.com/marginlab/margin-eval/agent-server/internal/run"
	"github.com/marginlab/margin-eval/runner/runner-core/agentdef"
)

var authFileEnvNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func decodeJSON(r *http.Request, out any) error {
	defer func() { _ = r.Body.Close() }()
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return fmt.Errorf("decode JSON body: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return fmt.Errorf("request body must contain a single JSON value")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func validatePutAgentDefinitionRequest(req putAgentDefinitionRequest) (putAgentDefinitionRequest, error) {
	if err := agentdef.ValidateDefinitionSnapshot(req.Definition); err != nil {
		return putAgentDefinitionRequest{}, apperr.NewBadRequest(apperr.CodeInvalidAgent, err.Error(), nil)
	}
	return req, nil
}

func validatePutAgentConfigRequest(req putAgentConfigRequest) (putAgentConfigRequest, error) {
	if strings.TrimSpace(req.Config.Name) == "" {
		return putAgentConfigRequest{}, apperr.NewBadRequest(apperr.CodeConfigValidation, "config.name is required", nil)
	}
	if strings.TrimSpace(string(req.Config.Mode)) == "" {
		return putAgentConfigRequest{}, apperr.NewBadRequest(apperr.CodeConfigValidation, "config.mode is required", nil)
	}
	return req, nil
}

func validateStartRunRequest(req run.StartRequest, cfg config.Config) (run.StartRequest, error) {
	if strings.TrimSpace(req.CWD) == "" {
		return run.StartRequest{}, apperr.NewBadRequest(apperr.CodeInvalidCWD, "cwd is required", nil)
	}
	if strings.TrimSpace(req.InitialPrompt) == "" {
		return run.StartRequest{}, apperr.NewBadRequest(apperr.CodeInvalidInitialPrompt, "initial_prompt is required", nil)
	}
	if req.PTY.Cols < 0 || req.PTY.Rows < 0 {
		return run.StartRequest{}, apperr.NewBadRequest(apperr.CodeInvalidPTY, "pty cols/rows cannot be negative", nil)
	}
	for idx, arg := range req.Args {
		if strings.TrimSpace(arg) == "" {
			return run.StartRequest{}, apperr.NewBadRequest(apperr.CodeInvalidRunArgs, "run args cannot contain empty values", map[string]any{"index": idx})
		}
	}
	for key := range req.Env {
		if strings.TrimSpace(key) == "" {
			return run.StartRequest{}, apperr.NewBadRequest(apperr.CodeInvalidEnv, "env keys cannot be empty", nil)
		}
		if strings.Contains(key, "=") {
			return run.StartRequest{}, apperr.NewBadRequest(apperr.CodeInvalidEnv, "env keys cannot contain '='", map[string]any{"key": key})
		}
	}
	seenRequiredEnv := map[string]struct{}{}
	seenRunHomePath := map[string]struct{}{}
	for idx, file := range req.AuthFiles {
		requiredEnv := strings.TrimSpace(file.RequiredEnv)
		if requiredEnv == "" {
			return run.StartRequest{}, apperr.NewBadRequest(apperr.CodeInvalidEnv, "auth_files.required_env is required", map[string]any{"index": idx})
		}
		if !authFileEnvNamePattern.MatchString(requiredEnv) {
			return run.StartRequest{}, apperr.NewBadRequest(apperr.CodeInvalidEnv, "auth_files.required_env must be a valid env name", map[string]any{"index": idx, "required_env": requiredEnv})
		}
		if _, exists := seenRequiredEnv[requiredEnv]; exists {
			return run.StartRequest{}, apperr.NewBadRequest(apperr.CodeInvalidEnv, "auth_files.required_env must not be duplicated", map[string]any{"required_env": requiredEnv})
		}
		seenRequiredEnv[requiredEnv] = struct{}{}

		sourcePath := strings.TrimSpace(file.SourcePath)
		if !path.IsAbs(sourcePath) {
			return run.StartRequest{}, apperr.NewBadRequest(apperr.CodeInvalidEnv, "auth_files.source_path must be an absolute path", map[string]any{"index": idx, "source_path": file.SourcePath})
		}

		runHomeRelPath, err := validateRunHomeRelPath(file.RunHomeRelPath)
		if err != nil {
			return run.StartRequest{}, apperr.NewBadRequest(apperr.CodeInvalidEnv, err.Error(), map[string]any{"index": idx, "run_home_rel_path": file.RunHomeRelPath})
		}
		if _, exists := seenRunHomePath[runHomeRelPath]; exists {
			return run.StartRequest{}, apperr.NewBadRequest(apperr.CodeInvalidEnv, "auth_files.run_home_rel_path must not be duplicated", map[string]any{"run_home_rel_path": runHomeRelPath})
		}
		seenRunHomePath[runHomeRelPath] = struct{}{}
		req.AuthFiles[idx].RequiredEnv = requiredEnv
		req.AuthFiles[idx].SourcePath = sourcePath
		req.AuthFiles[idx].RunHomeRelPath = runHomeRelPath
	}

	validatedCWD, err := fsutil.ValidateExistingDirUnderRoot(req.CWD, cfg.WorkspacesDir)
	if err != nil {
		return run.StartRequest{}, apperr.NewBadRequest(apperr.CodeInvalidCWD, err.Error(), nil)
	}
	req.CWD = validatedCWD
	return req, nil
}

func validateRunHomeRelPath(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", fmt.Errorf("auth_files.run_home_rel_path is required")
	}
	if strings.Contains(trimmed, "\\") {
		return "", fmt.Errorf("auth_files.run_home_rel_path must use slash-separated relative paths")
	}
	cleaned := path.Clean(trimmed)
	if strings.HasPrefix(cleaned, "/") {
		return "", fmt.Errorf("auth_files.run_home_rel_path must be relative")
	}
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("auth_files.run_home_rel_path must not escape run home")
	}
	return cleaned, nil
}

func validateRunPTYQuery(runID string) (string, error) {
	trimmed := strings.TrimSpace(runID)
	if trimmed == "" {
		return "", apperr.NewBadRequest(apperr.CodeInvalidRunID, "run_id is required", nil)
	}
	return trimmed, nil
}

func validatePostRunSnapshotRequest(req postRunSnapshotRequest) (postRunSnapshotRequest, error) {
	req.RunID = strings.TrimSpace(req.RunID)
	if req.RunID == "" {
		return postRunSnapshotRequest{}, apperr.NewBadRequest(apperr.CodeInvalidRunID, "run_id is required", nil)
	}
	if req.PTY.Cols <= 0 || req.PTY.Rows <= 0 {
		return postRunSnapshotRequest{}, apperr.NewBadRequest(apperr.CodeInvalidPTY, "pty cols/rows must be > 0", nil)
	}
	return req, nil
}
