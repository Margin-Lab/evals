package preflight

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

const (
	defaultDockerBinary = "docker"
	dockerSmokeImage    = "hello-world:latest"
	dockerCheckTimeout  = 5 * time.Minute
	maxOutputLength     = 512
)

type StepStatus string

const (
	StepStatusOK     StepStatus = "ok"
	StepStatusFailed StepStatus = "failed"
)

type Step struct {
	Name   string
	Status StepStatus
	Detail string
}

type Result struct {
	Steps []Step
}

type commandRunner func(context.Context, string, ...string) ([]byte, error)

type DockerChecker struct {
	lookPath execLookPathFunc
	run      commandRunner
}

type execLookPathFunc func(string) (string, error)

func NewDockerChecker() *DockerChecker {
	return &DockerChecker{
		lookPath: exec.LookPath,
		run: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			return exec.CommandContext(ctx, name, args...).CombinedOutput()
		},
	}
}

func (c *DockerChecker) Check(ctx context.Context) (Result, error) {
	if c == nil {
		c = NewDockerChecker()
	}
	if c.lookPath == nil {
		c.lookPath = exec.LookPath
	}
	if c.run == nil {
		c.run = func(ctx context.Context, name string, args ...string) ([]byte, error) {
			return exec.CommandContext(ctx, name, args...).CombinedOutput()
		}
	}

	checkCtx, cancel := context.WithTimeout(ctx, dockerCheckTimeout)
	defer cancel()

	result := Result{}

	dockerPath, err := c.lookPath(defaultDockerBinary)
	if err != nil {
		result.Steps = append(result.Steps, Step{
			Name:   "docker binary",
			Status: StepStatusFailed,
			Detail: "not found on PATH",
		})
		return result, fmt.Errorf("docker is not installed or not on PATH")
	}
	result.Steps = append(result.Steps, Step{
		Name:   "docker binary",
		Status: StepStatusOK,
		Detail: dockerPath,
	})

	serverVersion, err := c.run(checkCtx, dockerPath, "info", "--format", "{{.ServerVersion}}")
	if err != nil {
		detail := summarizeOutput(serverVersion)
		if detail == "" {
			detail = "daemon unavailable"
		}
		result.Steps = append(result.Steps, Step{
			Name:   "docker daemon",
			Status: StepStatusFailed,
			Detail: detail,
		})
		return result, dockerCommandError("docker daemon is not reachable; start Docker and try again", serverVersion, err)
	}
	result.Steps = append(result.Steps, Step{
		Name:   "docker daemon",
		Status: StepStatusOK,
		Detail: "server version " + summarizeOutput(serverVersion),
	})

	runOutput, err := c.run(checkCtx, dockerPath, "run", "--rm", dockerSmokeImage)
	if err != nil {
		detail := summarizeOutput(runOutput)
		if detail == "" {
			detail = "smoke test failed"
		}
		result.Steps = append(result.Steps, Step{
			Name:   "docker run",
			Status: StepStatusFailed,
			Detail: detail,
		})
		return result, dockerCommandError("docker could not run the hello-world smoke test", runOutput, err)
	}
	result.Steps = append(result.Steps, Step{
		Name:   "docker run",
		Status: StepStatusOK,
		Detail: dockerSmokeImage,
	})

	return result, nil
}

func dockerCommandError(message string, output []byte, err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%s: timed out after %s", message, dockerCheckTimeout)
	}
	text := summarizeOutput(output)
	if text == "" {
		return fmt.Errorf("%s: %w", message, err)
	}
	return fmt.Errorf("%s: %s", message, text)
}

func summarizeOutput(output []byte) string {
	trimmed := strings.TrimSpace(string(output))
	if trimmed == "" {
		return ""
	}
	if len(trimmed) <= maxOutputLength {
		return trimmed
	}
	return strings.TrimSpace(trimmed[:maxOutputLength]) + "..."
}
