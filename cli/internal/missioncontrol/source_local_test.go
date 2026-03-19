package missioncontrol

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/marginlab/margin-eval/runner/runner-core/domain"
	"github.com/marginlab/margin-eval/runner/runner-core/runnerapi"
	"github.com/marginlab/margin-eval/runner/runner-core/store"
)

type fakeSnapshotSource struct {
	runSnapshot runnerapi.RunSnapshot
	runOpts     runnerapi.SnapshotOptions
}

func (f *fakeSnapshotSource) SubmitRun(context.Context, runnerapi.SubmitInput) (store.Run, error) {
	return store.Run{}, nil
}

func (f *fakeSnapshotSource) WaitForTerminalRun(context.Context, string, time.Duration) (store.Run, error) {
	return store.Run{}, nil
}

func (f *fakeSnapshotSource) GetRunSnapshot(_ context.Context, _ string, opts runnerapi.SnapshotOptions) (runnerapi.RunSnapshot, error) {
	f.runOpts = opts
	return f.runSnapshot, nil
}

func (f *fakeSnapshotSource) GetInstanceSnapshot(context.Context, string, runnerapi.SnapshotOptions) (runnerapi.InstanceSnapshot, error) {
	return runnerapi.InstanceSnapshot{}, nil
}

func TestLocalSourceGetRunSnapshotIncludesResultAndArtifacts(t *testing.T) {
	fake := &fakeSnapshotSource{runSnapshot: runnerapi.RunSnapshot{Run: store.Run{RunID: "run_1", State: domain.RunStateRunning}}}
	src, err := NewLocalSource(fake, "")
	if err != nil {
		t.Fatalf("new local source: %v", err)
	}
	_, err = src.GetRunSnapshot(context.Background(), "run_1")
	if err != nil {
		t.Fatalf("get run snapshot: %v", err)
	}
	if !fake.runOpts.IncludeInstanceResults || !fake.runOpts.IncludeInstanceArtifacts {
		t.Fatalf("expected results+artifacts options, got %+v", fake.runOpts)
	}
}

func TestLocalSourceReadArtifactTextFromFileURI(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "stdout.txt")
	if err := os.WriteFile(path, []byte("hello world"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	src, err := NewLocalSource(&fakeSnapshotSource{}, "")
	if err != nil {
		t.Fatalf("new local source: %v", err)
	}
	out, err := src.ReadArtifactText(context.Background(), store.Artifact{ArtifactID: "a1", URI: "file://" + path}, 5)
	if err != nil {
		t.Fatalf("read artifact text: %v", err)
	}
	if out.Text != "hello" || !out.Truncated {
		t.Fatalf("unexpected text payload: %+v", out)
	}
}

func TestLocalSourceReadArtifactTextFallsBackToStoreKey(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "run", "inst", "stderr.txt")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir fixture dir: %v", err)
	}
	if err := os.WriteFile(path, []byte("stderr payload"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	src, err := NewLocalSource(&fakeSnapshotSource{}, tmp)
	if err != nil {
		t.Fatalf("new local source: %v", err)
	}
	out, err := src.ReadArtifactText(context.Background(), store.Artifact{ArtifactID: "a1", StoreKey: "run/inst/stderr.txt"}, DefaultTextPreviewLimit)
	if err != nil {
		t.Fatalf("read artifact text: %v", err)
	}
	if out.Text != "stderr payload" || out.Truncated {
		t.Fatalf("unexpected text payload: %+v", out)
	}
}

func TestLocalSourceReadArtifactTextPTYUsesTail(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "run", "inst", "agent_server_pty.log")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir fixture dir: %v", err)
	}
	if err := os.WriteFile(path, []byte("abcdefghijklmnopqrstuvwxyz"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	src, err := NewLocalSource(&fakeSnapshotSource{}, tmp)
	if err != nil {
		t.Fatalf("new local source: %v", err)
	}
	out, err := src.ReadArtifactText(context.Background(), store.Artifact{
		ArtifactID: "a1",
		Role:       store.ArtifactRoleAgentPTY,
		StoreKey:   "run/inst/agent_server_pty.log",
	}, 8)
	if err != nil {
		t.Fatalf("read artifact text: %v", err)
	}
	if out.Text != "stuvwxyz" || !out.Truncated {
		t.Fatalf("unexpected PTY tail payload: %+v", out)
	}
}

func TestLocalSourceInjectsLiveExecutionArtifacts(t *testing.T) {
	tmp := t.TempDir()
	runID := "run_1"
	instanceID := "run_1-inst-0001"
	fileName, _ := store.DefaultArtifactFilename(store.ArtifactRoleAgentControl)
	logPath := filepath.Join(tmp, runID, instanceID, fileName)
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		t.Fatalf("mkdir live log dir: %v", err)
	}
	if err := os.WriteFile(logPath, []byte("live log line\n"), 0o644); err != nil {
		t.Fatalf("write live log: %v", err)
	}
	ptyFile, _ := store.DefaultArtifactFilename(store.ArtifactRoleAgentPTY)
	ptyPath := filepath.Join(tmp, runID, instanceID, ptyFile)
	if err := os.WriteFile(ptyPath, []byte("pty log line\n"), 0o644); err != nil {
		t.Fatalf("write live pty log: %v", err)
	}

	fake := &fakeSnapshotSource{
		runSnapshot: runnerapi.RunSnapshot{
			Run: store.Run{RunID: runID, State: domain.RunStateRunning},
			Instances: []runnerapi.InstanceSnapshot{{
				Instance: store.Instance{InstanceID: instanceID, State: domain.InstanceStateAgentRunning},
			}},
		},
	}
	src, err := NewLocalSource(fake, tmp)
	if err != nil {
		t.Fatalf("new local source: %v", err)
	}
	snapshot, err := src.GetRunSnapshot(context.Background(), runID)
	if err != nil {
		t.Fatalf("get run snapshot: %v", err)
	}
	if len(snapshot.Instances) != 1 {
		t.Fatalf("expected 1 instance, got %d", len(snapshot.Instances))
	}
	found := false
	foundPTY := false
	for _, art := range snapshot.Instances[0].Artifacts {
		if art.Role == store.ArtifactRoleAgentControl && strings.HasPrefix(art.URI, "file://") {
			found = true
		}
		if art.Role == store.ArtifactRoleAgentPTY && strings.HasPrefix(art.URI, "file://") {
			foundPTY = true
		}
	}
	if !found {
		t.Fatalf("expected injected live control-log artifact")
	}
	if !foundPTY {
		t.Fatalf("expected injected live pty artifact")
	}
}
