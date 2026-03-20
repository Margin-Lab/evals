package preflight

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestDockerCheckerSuccess(t *testing.T) {
	var calls []string
	checker := &DockerChecker{
		lookPath: func(name string) (string, error) {
			if name != "docker" {
				t.Fatalf("lookPath name = %q", name)
			}
			return "/usr/bin/docker", nil
		},
		run: func(_ context.Context, name string, args ...string) ([]byte, error) {
			calls = append(calls, name+" "+strings.Join(args, " "))
			switch {
			case len(args) == 3 && args[0] == "info":
				return []byte("27.1.1\n"), nil
			case len(args) == 3 && args[0] == "run":
				return []byte("Hello from Docker!\n"), nil
			default:
				t.Fatalf("unexpected args: %v", args)
				return nil, nil
			}
		},
	}

	result, err := checker.Check(context.Background())
	if err != nil {
		t.Fatalf("Check returned error: %v", err)
	}
	if len(result.Steps) != 3 {
		t.Fatalf("expected 3 steps, got %d", len(result.Steps))
	}
	if got := result.Steps[0]; got.Name != "docker binary" || got.Status != StepStatusOK || got.Detail != "/usr/bin/docker" {
		t.Fatalf("unexpected first step: %+v", got)
	}
	if got := result.Steps[1]; got.Name != "docker daemon" || got.Status != StepStatusOK || got.Detail != "server version 27.1.1" {
		t.Fatalf("unexpected second step: %+v", got)
	}
	if got := result.Steps[2]; got.Name != "docker run" || got.Status != StepStatusOK || got.Detail != dockerSmokeImage {
		t.Fatalf("unexpected third step: %+v", got)
	}
	if len(calls) != 2 {
		t.Fatalf("expected 2 docker command calls, got %d", len(calls))
	}
}

func TestDockerCheckerFailsWhenBinaryMissing(t *testing.T) {
	checker := &DockerChecker{
		lookPath: func(string) (string, error) {
			return "", errors.New("not found")
		},
		run: func(context.Context, string, ...string) ([]byte, error) {
			t.Fatal("run should not be called when docker is missing")
			return nil, nil
		},
	}

	result, err := checker.Check(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "docker is not installed or not on PATH") {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Steps) != 1 || result.Steps[0].Status != StepStatusFailed {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestDockerCheckerFailsWhenDaemonUnavailable(t *testing.T) {
	checker := &DockerChecker{
		lookPath: func(string) (string, error) {
			return "/usr/bin/docker", nil
		},
		run: func(_ context.Context, _ string, args ...string) ([]byte, error) {
			if len(args) > 0 && args[0] == "info" {
				return []byte("Cannot connect to the Docker daemon"), errors.New("exit status 1")
			}
			t.Fatal("unexpected docker command")
			return nil, nil
		},
	}

	result, err := checker.Check(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "docker daemon is not reachable") {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Steps) != 2 || result.Steps[1].Status != StepStatusFailed {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestDockerCheckerFailsWhenSmokeRunFails(t *testing.T) {
	checker := &DockerChecker{
		lookPath: func(string) (string, error) {
			return "/usr/bin/docker", nil
		},
		run: func(_ context.Context, _ string, args ...string) ([]byte, error) {
			if len(args) == 3 && args[0] == "info" {
				return []byte("27.1.1\n"), nil
			}
			if len(args) == 3 && args[0] == "run" {
				return []byte("pull access denied"), errors.New("exit status 1")
			}
			t.Fatal("unexpected docker command")
			return nil, nil
		},
	}

	result, err := checker.Check(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "hello-world smoke test") {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Steps) != 3 || result.Steps[2].Status != StepStatusFailed {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestDockerCheckerReportsTimeout(t *testing.T) {
	checker := &DockerChecker{
		lookPath: func(string) (string, error) {
			return "/usr/bin/docker", nil
		},
		run: func(_ context.Context, _ string, args ...string) ([]byte, error) {
			if len(args) > 0 && args[0] == "info" {
				return nil, context.DeadlineExceeded
			}
			t.Fatal("unexpected docker command")
			return nil, nil
		},
	}

	_, err := checker.Check(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "timed out after") {
		t.Fatalf("unexpected error: %v", err)
	}
}
