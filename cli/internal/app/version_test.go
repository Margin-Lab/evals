package app

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/marginlab/margin-eval/cli/internal/buildinfo"
)

func TestRunVersionPrintsCurrentBuildVersion(t *testing.T) {
	origCurrentBuildInfo := currentBuildInfo
	defer func() { currentBuildInfo = origCurrentBuildInfo }()
	currentBuildInfo = func() buildinfo.Info {
		return buildinfo.Info{
			Version:   "v1.2.3",
			BuildTime: "2026-04-06T18:00:00Z",
		}
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	a := New(&stdout, &stderr)

	if err := a.runVersion(nil); err != nil {
		t.Fatalf("runVersion returned error: %v", err)
	}
	if got := stdout.String(); got != "margin v1.2.3 (built 2026-04-06T18:00:00Z)\n" {
		t.Fatalf("stdout = %q", got)
	}
}

func TestRunVersionRejectsUnexpectedArguments(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	a := New(&stdout, &stderr)

	err := a.runVersion([]string{"extra"})
	if err == nil || !strings.Contains(err.Error(), "unexpected arguments") {
		t.Fatalf("expected unexpected arguments error, got %v", err)
	}
}

func TestAppRunDispatchesVersionFlag(t *testing.T) {
	origCurrentBuildInfo := currentBuildInfo
	defer func() { currentBuildInfo = origCurrentBuildInfo }()
	currentBuildInfo = func() buildinfo.Info {
		return buildinfo.Info{
			Version:   "v2.0.0",
			BuildTime: "2026-04-06T19:00:00Z",
		}
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	a := New(&stdout, &stderr)

	if err := a.Run(context.Background(), []string{"--version"}); err != nil {
		t.Fatalf("App.Run returned error: %v", err)
	}
	if got := stdout.String(); got != "margin v2.0.0 (built 2026-04-06T19:00:00Z)\n" {
		t.Fatalf("stdout = %q", got)
	}
}

func TestUsageIncludesVersionFlag(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	a := New(&stdout, &stderr)

	a.printUsage()

	if got := stdout.String(); !strings.Contains(got, "margin --version") {
		t.Fatalf("usage missing version flag: %s", got)
	}
}
