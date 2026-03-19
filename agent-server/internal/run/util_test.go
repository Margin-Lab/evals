package run

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/marginlab/margin-eval/agent-server/internal/apperr"
)

// TestMergeEnvironment verifies base env parsing, invalid-entry filtering, and override precedence.
func TestMergeEnvironment(t *testing.T) {
	tests := []struct {
		name      string
		base      []string
		overrides map[string]string
		want      map[string]string
	}{
		{
			name:      "base_only",
			base:      []string{"A=1", "B=2"},
			overrides: nil,
			want: map[string]string{
				"A": "1",
				"B": "2",
			},
		},
		{
			name:      "override_and_ignore_invalid_base_entries",
			base:      []string{"A=1", "B=2", "INVALID", "C=3=4", "A=old"},
			overrides: map[string]string{"A": "new", "D": "5"},
			want: map[string]string{
				"A": "new",
				"B": "2",
				"C": "3=4",
				"D": "5",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := mergeEnvironment(tc.base, tc.overrides)
			if len(got) != len(tc.want) {
				t.Fatalf("len(got) = %d, want %d (%v)", len(got), len(tc.want), got)
			}
			for key, wantValue := range tc.want {
				if got[key] != wantValue {
					t.Fatalf("got[%q] = %q, want %q", key, got[key], wantValue)
				}
			}
		})
	}
}

func TestApplyRunEnvironmentDefaults(t *testing.T) {
	env := map[string]string{
		"A":              "1",
		runHomeEnvKey:    "/tmp/old-home",
		runSandboxEnvKey: "0",
	}

	applyRunEnvironmentDefaults(env, "/tmp/new-home")

	if got := env[runHomeEnvKey]; got != "/tmp/new-home" {
		t.Fatalf("env[%q] = %q, want %q", runHomeEnvKey, got, "/tmp/new-home")
	}
	if got := env[runSandboxEnvKey]; got != "1" {
		t.Fatalf("env[%q] = %q, want %q", runSandboxEnvKey, got, "1")
	}
	if got := env["A"]; got != "1" {
		t.Fatalf("env[%q] = %q, want %q", "A", got, "1")
	}
}

func TestResolveRequiredEnvRejectsRequestedOverrides(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "server-secret")

	_, err := resolveRequiredEnv([]string{"OPENAI_API_KEY"}, map[string]string{
		"OPENAI_API_KEY": "client-secret",
	}, nil)
	if err == nil {
		t.Fatal("expected override rejection")
	}
	runErr, ok := asError(err)
	if !ok {
		t.Fatalf("expected run error, got %T (%v)", err, err)
	}
	if runErr.Code != apperr.CodeInvalidEnv {
		t.Fatalf("error code = %q, want %q", runErr.Code, apperr.CodeInvalidEnv)
	}
}

func TestResolveRequiredEnvRequiresServerEnvironment(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")

	_, err := resolveRequiredEnv([]string{"OPENAI_API_KEY", "ANTHROPIC_API_KEY"}, nil, nil)
	if err == nil {
		t.Fatal("expected missing env error")
	}
	runErr, ok := asError(err)
	if !ok {
		t.Fatalf("expected run error, got %T (%v)", err, err)
	}
	if runErr.Code != apperr.CodeMissingRequiredEnv {
		t.Fatalf("error code = %q, want %q", runErr.Code, apperr.CodeMissingRequiredEnv)
	}
}

func TestResolveRequiredEnvAllowsAuthFileFallback(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")

	resolved, err := resolveRequiredEnv([]string{"OPENAI_API_KEY"}, nil, []AuthFile{{
		RequiredEnv:    "OPENAI_API_KEY",
		SourcePath:     "/tmp/marginlab/config/auth-files/OPENAI_API_KEY",
		RunHomeRelPath: ".codex/auth.json",
	}})
	if err != nil {
		t.Fatalf("resolveRequiredEnv() error = %v", err)
	}
	if len(resolved) != 0 {
		t.Fatalf("resolved env = %#v, want no injected env", resolved)
	}
}

func TestMaterializeAuthFilesCopiesIntoRunHome(t *testing.T) {
	runHome := t.TempDir()
	sourcePath := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(sourcePath, []byte(`{"token":"oauth"}`), 0o600); err != nil {
		t.Fatalf("write source auth file: %v", err)
	}

	if err := materializeAuthFiles(runHome, []AuthFile{{
		RequiredEnv:    "OPENAI_API_KEY",
		SourcePath:     sourcePath,
		RunHomeRelPath: ".codex/auth.json",
	}}); err != nil {
		t.Fatalf("materializeAuthFiles() error = %v", err)
	}

	targetPath := filepath.Join(runHome, ".codex", "auth.json")
	body, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("read target auth file: %v", err)
	}
	if string(body) != `{"token":"oauth"}` {
		t.Fatalf("target body = %q", string(body))
	}
	info, err := os.Stat(targetPath)
	if err != nil {
		t.Fatalf("stat target auth file: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("target perms = %o, want 600", got)
	}
}
