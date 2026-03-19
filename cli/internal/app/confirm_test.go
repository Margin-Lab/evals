package app

import (
	"strings"
	"testing"
)

func TestRunConfirmationViewForAPIKey(t *testing.T) {
	m := newRunConfirmationModel(runConfirmationSpec{
		AgentName: "codex",
		Auth: []runConfirmationAuthItem{{
			Method:      "API key",
			Requirement: "OPENAI_API_KEY",
		}},
	})
	m.width = 120
	m.height = 30

	out := m.View()
	for _, want := range []string{
		"Run Confirmation",
		"Authentication",
		"Will use API key",
		"OPENAI_API_KEY",
		"codex",
		"Please ensure sufficient API credits",
		"before confirming the run",
		"Enter",
		"Esc",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected view to contain %q, got:\n%s", want, out)
		}
	}
}

func TestRunConfirmationViewForOAuthAndPrune(t *testing.T) {
	m := newRunConfirmationModel(runConfirmationSpec{
		AgentName: "codex",
		Auth: []runConfirmationAuthItem{{
			Method:   "OAuth credential file",
			FilePath: "/Users/josebouza/.codex/auth.json",
		}},
		PruneBuiltImage: 5,
	})
	m.width = 120
	m.height = 30

	out := m.View()
	for _, want := range []string{
		"Authentication",
		"Will use OAuth file",
		"/Users/josebouza/.codex/auth.json",
		"Note that this",
		"will use tokens.",
		"Docker Image Pruning",
		"--prune-built-image enabled, this will prune all unused docker images intermittently",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected view to contain %q, got:\n%s", want, out)
		}
	}
}

func TestRunConfirmationViewForDryRunAPIKey(t *testing.T) {
	m := newRunConfirmationModel(runConfirmationSpec{
		AgentName: "codex",
		DryRun:    true,
		Auth: []runConfirmationAuthItem{{
			Method:      "API key",
			Requirement: "OPENAI_API_KEY",
		}},
	})
	m.width = 120
	m.height = 30

	out := m.View()
	for _, want := range []string{
		"Authentication",
		"Dry-run mode",
		"active:",
		"No token usage in this run.",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected view to contain %q, got:\n%s", want, out)
		}
	}
	for _, unwanted := range []string{
		"Will validate API key",
		"OPENAI_API_KEY",
		"codex",
		"Please ensure sufficient API credits",
		"will use tokens.",
	} {
		if strings.Contains(out, unwanted) {
			t.Fatalf("expected view to omit %q, got:\n%s", unwanted, out)
		}
	}
}
