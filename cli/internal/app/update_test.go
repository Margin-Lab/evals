package app

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/marginlab/margin-eval/cli/internal/updater"
)

func TestRunUpdatePrintsNoOpMessage(t *testing.T) {
	origRunUpdater := runUpdater
	defer func() { runUpdater = origRunUpdater }()

	runUpdater = func(_ context.Context, currentVersion string) (updater.Result, error) {
		if currentVersion != "v0.1.0" {
			t.Fatalf("currentVersion = %q", currentVersion)
		}
		return updater.Result{
			CurrentVersion: "v0.1.0",
			LatestVersion:  "v0.1.0",
			Updated:        false,
		}, nil
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	a := New(&stdout, &stderr)

	origCurrentBuildVersion := currentBuildVersion
	defer func() { currentBuildVersion = origCurrentBuildVersion }()
	currentBuildVersion = func() string { return "v0.1.0" }

	if err := a.runUpdate(context.Background(), nil); err != nil {
		t.Fatalf("runUpdate returned error: %v", err)
	}
	if got := stdout.String(); !strings.Contains(got, "already up to date") {
		t.Fatalf("stdout = %q", got)
	}
}

func TestUsageIncludesUpdateCommand(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	a := New(&stdout, &stderr)

	a.printUsage()

	if got := stdout.String(); !strings.Contains(got, "margin update") {
		t.Fatalf("usage missing update command: %s", got)
	}
}
