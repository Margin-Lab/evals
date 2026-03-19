package localexecutor

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marginlab/margin-eval/runner/runner-core/imageresolver"
	"github.com/marginlab/margin-eval/runner/runner-core/runbundle"
	"github.com/marginlab/margin-eval/runner/runner-core/testassets"
	"github.com/marginlab/margin-eval/runner/runner-core/testfixture"
)

func TestResolveRebuildsWhenPersistedImageIsMissingLocally(t *testing.T) {
	const digest = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	const persisted = "marginlab-local/buildctx@sha256:" + digest

	buildContext := buildContextWithDockerfile(t)
	input := imageresolver.ResolveInput{
		CaseID: "case_1",
		Image:  persisted,
		ImageBuild: &runbundle.CaseImageBuild{
			Context:           buildContext,
			DockerfileRelPath: "Dockerfile",
		},
	}
	buildKey := imageresolver.BuildKey(input)
	tag := buildTagFromBuildKey(buildKey)

	logPath := filepath.Join(t.TempDir(), "docker.log")
	statePath := filepath.Join(t.TempDir(), "built.state")
	dockerBin := writeFakeDockerBinary(t, fmt.Sprintf(`#!/bin/sh
set -eu
echo "$@" >> %q

if [ "$1" = "image" ] && [ "$2" = "inspect" ]; then
  target="$3"
  format="$5"
  if [ "$target" = %q ]; then
    echo "Error: No such image: $target" >&2
    exit 1
  fi
  if [ "$target" = %q ]; then
    if [ -f %q ]; then
      if [ "$format" = "{{json .RepoDigests}}" ]; then
        echo "[\"marginlab-local/buildctx@sha256:%s\"]"
        exit 0
      fi
      if [ "$format" = "{{.Id}}" ]; then
        echo "sha256:%s"
        exit 0
      fi
      echo "unsupported format: $format" >&2
      exit 1
    fi
    echo "Error: No such image: $target" >&2
    exit 1
  fi
  echo "unexpected inspect target: $target" >&2
  exit 1
fi

if [ "$1" = "build" ]; then
  touch %q
  exit 0
fi

echo "unexpected docker invocation: $@" >&2
exit 1
`, logPath, persisted, tag, statePath, digest, digest, statePath))

	resolver, err := newLocalDockerImageResolver(dockerBin)
	if err != nil {
		t.Fatalf("newLocalDockerImageResolver() error = %v", err)
	}
	got, err := resolver.Resolve(context.Background(), input)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	want := "marginlab-local/buildctx@sha256:" + digest
	if got != want {
		t.Fatalf("resolved image = %q, want %q", got, want)
	}

	logRaw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read fake docker log: %v", err)
	}
	log := string(logRaw)
	if !strings.Contains(log, "image inspect "+persisted+" --format {{.Id}}") {
		t.Fatalf("expected inspect of persisted image, log:\n%s", log)
	}
	if !strings.Contains(log, "build -t "+tag+" -f") {
		t.Fatalf("expected docker build with tag %q, log:\n%s", tag, log)
	}
}

func TestCleanupRemovesResolvedRefAndBuildTag(t *testing.T) {
	const resolved = "marginlab-local/buildctx@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

	input := imageresolver.ResolveInput{
		CaseID: "case_1",
		ImageBuild: &runbundle.CaseImageBuild{
			Context:           testfixture.MinimalTestAssets(),
			DockerfileRelPath: "Dockerfile",
		},
	}
	tag := buildTagFromBuildKey(imageresolver.BuildKey(input))

	logPath := filepath.Join(t.TempDir(), "docker.log")
	dockerBin := writeFakeDockerBinary(t, fmt.Sprintf(`#!/bin/sh
set -eu
echo "$@" >> %q
if [ "$1" = "image" ] && [ "$2" = "rm" ]; then
  exit 0
fi
echo "unexpected docker invocation: $@" >&2
exit 1
`, logPath))

	resolver, err := newLocalDockerImageResolver(dockerBin)
	if err != nil {
		t.Fatalf("newLocalDockerImageResolver() error = %v", err)
	}
	if err := resolver.Cleanup(context.Background(), input, resolved); err != nil {
		t.Fatalf("Cleanup() error = %v", err)
	}

	logRaw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read fake docker log: %v", err)
	}
	log := string(logRaw)
	if !strings.Contains(log, "image rm --force "+resolved) {
		t.Fatalf("expected cleanup of resolved image ref, log:\n%s", log)
	}
	if !strings.Contains(log, "image rm --force "+tag) {
		t.Fatalf("expected cleanup of build tag %q, log:\n%s", tag, log)
	}
}

func TestResolveWithBuildLogWritesBuildOutput(t *testing.T) {
	const digest = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	input := imageresolver.ResolveInput{
		CaseID: "case_1",
		ImageBuild: &runbundle.CaseImageBuild{
			Context:           buildContextWithDockerfile(t),
			DockerfileRelPath: "Dockerfile",
		},
	}
	tag := buildTagFromBuildKey(imageresolver.BuildKey(input))
	statePath := filepath.Join(t.TempDir(), "built.state")
	dockerBin := writeFakeDockerBinary(t, fmt.Sprintf(`#!/bin/sh
set -eu
if [ "$1" = "image" ] && [ "$2" = "inspect" ]; then
  target="$3"
  format="$5"
  if [ "$target" = %q ]; then
    if [ -f %q ]; then
      if [ "$format" = "{{json .RepoDigests}}" ]; then
        echo "[\"marginlab-local/buildctx@sha256:%s\"]"
        exit 0
      fi
      if [ "$format" = "{{.Id}}" ]; then
        echo "sha256:%s"
        exit 0
      fi
    fi
    echo "Error: No such image: $target" >&2
    exit 1
  fi
fi
if [ "$1" = "build" ]; then
  echo "build output line"
  touch %q
  exit 0
fi
echo "unexpected docker invocation: $@" >&2
exit 1
`, tag, statePath, digest, digest, statePath))

	resolver, err := newLocalDockerImageResolver(dockerBin)
	if err != nil {
		t.Fatalf("newLocalDockerImageResolver() error = %v", err)
	}
	var buildLog bytes.Buffer
	if _, err := resolver.ResolveWithBuildLog(context.Background(), input, &buildLog); err != nil {
		t.Fatalf("ResolveWithBuildLog() error = %v", err)
	}
	if !strings.Contains(buildLog.String(), "build output line") {
		t.Fatalf("expected build output in build log, got:\n%s", buildLog.String())
	}
}

func buildContextWithDockerfile(t *testing.T) runbundle.BuildContext {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "Dockerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatalf("write Dockerfile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("test\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	desc, err := testassets.PackDir(root)
	if err != nil {
		t.Fatalf("pack build context: %v", err)
	}
	return desc
}

func writeFakeDockerBinary(t *testing.T, script string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "docker")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake docker binary: %v", err)
	}
	return path
}
