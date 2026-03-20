package app

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/marginlab/margin-eval/cli/internal/preflight"
)

func TestRunCheckPrintsSuccessOutput(t *testing.T) {
	origRunDockerPreflight := runDockerPreflight
	defer func() { runDockerPreflight = origRunDockerPreflight }()

	runDockerPreflight = func(_ context.Context) (preflight.Result, error) {
		return preflight.Result{
			Steps: []preflight.Step{
				{Name: "docker binary", Status: preflight.StepStatusOK, Detail: "/usr/local/bin/docker"},
				{Name: "docker daemon", Status: preflight.StepStatusOK, Detail: "server version 27.0.0"},
				{Name: "docker run", Status: preflight.StepStatusOK, Detail: "hello-world:latest"},
			},
		}, nil
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	a := New(&stdout, &stderr)

	if err := a.runCheck(context.Background(), nil); err != nil {
		t.Fatalf("runCheck returned error: %v", err)
	}

	out := stdout.String()
	for _, want := range []string{
		"[check] docker binary: ok (/usr/local/bin/docker)",
		"[check] docker daemon: ok (server version 27.0.0)",
		"[check] docker run: ok (hello-world:latest)",
		"margin check passed",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("stdout missing %q:\n%s", want, out)
		}
	}
}

func TestRunCheckPrintsFailedStepsBeforeReturningError(t *testing.T) {
	origRunDockerPreflight := runDockerPreflight
	defer func() { runDockerPreflight = origRunDockerPreflight }()

	runDockerPreflight = func(_ context.Context) (preflight.Result, error) {
		return preflight.Result{
			Steps: []preflight.Step{
				{Name: "docker binary", Status: preflight.StepStatusOK, Detail: "/usr/local/bin/docker"},
				{Name: "docker daemon", Status: preflight.StepStatusFailed, Detail: "daemon unavailable"},
			},
		}, context.DeadlineExceeded
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	a := New(&stdout, &stderr)

	err := a.runCheck(context.Background(), nil)
	if err == nil {
		t.Fatal("expected runCheck to fail")
	}
	out := stdout.String()
	for _, want := range []string{
		"[check] docker binary: ok (/usr/local/bin/docker)",
		"[check] docker daemon: failed (daemon unavailable)",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("stdout missing %q:\n%s", want, out)
		}
	}
}

func TestRunCheckRejectsUnexpectedArguments(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	a := New(&stdout, &stderr)

	err := a.runCheck(context.Background(), []string{"extra"})
	if err == nil || !strings.Contains(err.Error(), "unexpected arguments") {
		t.Fatalf("expected unexpected arguments error, got %v", err)
	}
}

func TestAppRunDispatchesCheckCommand(t *testing.T) {
	origRunDockerPreflight := runDockerPreflight
	defer func() { runDockerPreflight = origRunDockerPreflight }()

	runDockerPreflight = func(_ context.Context) (preflight.Result, error) {
		return preflight.Result{}, nil
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	a := New(&stdout, &stderr)

	if err := a.Run(context.Background(), []string{"check"}); err != nil {
		t.Fatalf("App.Run returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), "margin check passed") {
		t.Fatalf("stdout missing success message: %s", stdout.String())
	}
}

func TestUsageIncludesCheckCommand(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	a := New(&stdout, &stderr)

	a.printUsage()

	if got := stdout.String(); !strings.Contains(got, "margin check") {
		t.Fatalf("usage missing check command: %s", got)
	}
}
