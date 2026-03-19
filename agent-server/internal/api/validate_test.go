package api

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/marginlab/margin-eval/agent-server/internal/apperr"
	"github.com/marginlab/margin-eval/agent-server/internal/config"
	"github.com/marginlab/margin-eval/agent-server/internal/run"
	"github.com/marginlab/margin-eval/runner/runner-core/agentdef"
	"github.com/marginlab/margin-eval/runner/runner-core/testassets"
)

func TestValidatePutAgentDefinitionRequest(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	hookDir := filepath.Join(root, "hooks")
	if err := os.MkdirAll(hookDir, 0o755); err != nil {
		t.Fatalf("mkdir hook dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(hookDir, "run.sh"), []byte("#!/usr/bin/env bash\nprintf '{}\\n'\n"), 0o755); err != nil {
		t.Fatalf("write hook: %v", err)
	}
	pkg, err := testassets.PackDir(root)
	if err != nil {
		t.Fatalf("pack dir: %v", err)
	}

	_, err = validatePutAgentDefinitionRequest(putAgentDefinitionRequest{
		Definition: agentdef.DefinitionSnapshot{
			Manifest: agentdef.Manifest{
				Kind: "agent_definition",
				Name: "fixture",
				Run:  agentdef.RunSpec{PrepareHook: agentdef.HookRef{Path: "hooks/run.sh"}},
			},
			Package: pkg,
		},
	})
	if err != nil {
		t.Fatalf("validatePutAgentDefinitionRequest(valid) error = %v", err)
	}

	_, err = validatePutAgentDefinitionRequest(putAgentDefinitionRequest{
		Definition: agentdef.DefinitionSnapshot{
			Manifest: agentdef.Manifest{
				Kind: "agent_definition",
				Run:  agentdef.RunSpec{PrepareHook: agentdef.HookRef{Path: "hooks/run.sh"}},
			},
			Package: pkg,
		},
	})
	assertAPIErrorCode(t, err, apperr.CodeInvalidAgent)
}

func TestValidatePutAgentConfigRequest(t *testing.T) {
	t.Parallel()

	valid, err := validatePutAgentConfigRequest(putAgentConfigRequest{
		Config: agentdef.ConfigSpec{
			Name:  "fixture-default",
			Mode:  agentdef.ConfigModeDirect,
			Input: map[string]any{"command": []any{"echo", "hello"}},
		},
	})
	if err != nil {
		t.Fatalf("validatePutAgentConfigRequest(valid) error = %v", err)
	}
	if valid.Config.Name != "fixture-default" {
		t.Fatalf("config.name = %q", valid.Config.Name)
	}

	unified, err := validatePutAgentConfigRequest(putAgentConfigRequest{
		Config: agentdef.ConfigSpec{
			Name: "fixture-unified",
			Mode: agentdef.ConfigModeUnified,
			Unified: &agentdef.UnifiedSpec{
				Model:          "gpt-5",
				ReasoningLevel: "medium",
			},
		},
	})
	if err != nil {
		t.Fatalf("validatePutAgentConfigRequest(unified) error = %v", err)
	}
	if unified.Config.Mode != agentdef.ConfigModeUnified {
		t.Fatalf("config.mode = %q", unified.Config.Mode)
	}

	_, err = validatePutAgentConfigRequest(putAgentConfigRequest{})
	assertAPIErrorCode(t, err, apperr.CodeConfigValidation)

	_, err = validatePutAgentConfigRequest(putAgentConfigRequest{
		Config: agentdef.ConfigSpec{Name: "fixture-default"},
	})
	assertAPIErrorCode(t, err, apperr.CodeConfigValidation)
}

func TestValidateStartRunRequest(t *testing.T) {
	t.Parallel()

	workspaces := t.TempDir()
	cwd := filepath.Join(workspaces, "case")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatalf("mkdir cwd: %v", err)
	}
	cfg := config.Config{WorkspacesDir: workspaces}

	valid, err := validateStartRunRequest(startRunRequestFixture(cwd), cfg)
	if err != nil {
		t.Fatalf("validateStartRunRequest(valid) error = %v", err)
	}
	if filepath.Base(valid.CWD) != filepath.Base(cwd) {
		t.Fatalf("validated cwd = %q", valid.CWD)
	}

	req := startRunRequestFixture(cwd)
	req.Env = map[string]string{"BAD=KEY": "secret"}
	_, err = validateStartRunRequest(req, cfg)
	assertAPIErrorCode(t, err, apperr.CodeInvalidEnv)

	req = startRunRequestFixture(cwd)
	req.Args = []string{"", "x"}
	_, err = validateStartRunRequest(req, cfg)
	assertAPIErrorCode(t, err, apperr.CodeInvalidRunArgs)

	req = startRunRequestFixture(cwd)
	req.AuthFiles = []run.AuthFile{{
		RequiredEnv:    "OPENAI_API_KEY",
		SourcePath:     "/tmp/marginlab/config/auth-files/OPENAI_API_KEY",
		RunHomeRelPath: ".codex/auth.json",
	}}
	valid, err = validateStartRunRequest(req, cfg)
	if err != nil {
		t.Fatalf("validateStartRunRequest(auth_files) error = %v", err)
	}
	if len(valid.AuthFiles) != 1 || valid.AuthFiles[0].RunHomeRelPath != ".codex/auth.json" {
		t.Fatalf("auth_files = %#v", valid.AuthFiles)
	}

	req = startRunRequestFixture(cwd)
	req.AuthFiles = []run.AuthFile{{
		RequiredEnv:    "OPENAI_API_KEY",
		SourcePath:     "relative/path",
		RunHomeRelPath: "../escape",
	}}
	_, err = validateStartRunRequest(req, cfg)
	assertAPIErrorCode(t, err, apperr.CodeInvalidEnv)

	req = startRunRequestFixture(cwd)
	req.DryRun = true
	valid, err = validateStartRunRequest(req, cfg)
	if err != nil {
		t.Fatalf("validateStartRunRequest(dry_run) error = %v", err)
	}
	if !valid.DryRun {
		t.Fatalf("expected dry_run to remain true")
	}
}

func startRunRequestFixture(cwd string) run.StartRequest {
	return run.StartRequest{
		CWD:           cwd,
		InitialPrompt: "hello",
		Args:          []string{"--flag"},
		Env:           map[string]string{"TERM": "xterm-256color"},
	}
}

func assertAPIErrorCode(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected API error %q, got nil", want)
	}
	apiErr, ok := apperr.As(err)
	if !ok {
		t.Fatalf("expected API error, got %T (%v)", err, err)
	}
	if apiErr.Code != want {
		t.Fatalf("error code = %q, want %q", apiErr.Code, want)
	}
}
