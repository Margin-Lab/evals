package localexecutor

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/marginlab/margin-eval/runner/runner-core/store"
	"github.com/marginlab/margin-eval/runner/runner-local/runfs"
)

type testExecutionResult struct {
	ExitCode  int
	Artifacts []store.Artifact
	StdoutRef string
	StderrRef string
}

type testOutputCapture struct {
	instanceID string

	stdoutPath     string
	stdoutStoreKey string
	stdoutFile     *os.File

	stderrPath     string
	stderrStoreKey string
	stderrFile     *os.File
}

func newTestOutputCapture(rootDir, runID, instanceID string) (*testOutputCapture, error) {
	stdoutPath, stdoutStoreKey, stdoutFile, err := openTestOutputFile(rootDir, runID, instanceID, store.ArtifactRoleTestStdout)
	if err != nil {
		return nil, err
	}
	stderrPath, stderrStoreKey, stderrFile, err := openTestOutputFile(rootDir, runID, instanceID, store.ArtifactRoleTestStderr)
	if err != nil {
		_ = stdoutFile.Close()
		return nil, err
	}
	return &testOutputCapture{
		instanceID:     instanceID,
		stdoutPath:     stdoutPath,
		stdoutStoreKey: stdoutStoreKey,
		stdoutFile:     stdoutFile,
		stderrPath:     stderrPath,
		stderrStoreKey: stderrStoreKey,
		stderrFile:     stderrFile,
	}, nil
}

func openTestOutputFile(rootDir, runID, instanceID, role string) (string, string, *os.File, error) {
	path, storeKey, _, ok := runfs.AbsoluteArtifactPath(rootDir, runID, instanceID, role)
	if !ok {
		return "", "", nil, fmt.Errorf("resolve %s artifact path", role)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", "", nil, fmt.Errorf("create %s artifact dir: %w", role, err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return "", "", nil, fmt.Errorf("open %s artifact file: %w", role, err)
	}
	return path, storeKey, f, nil
}

func (c *testOutputCapture) close() error {
	if c == nil {
		return nil
	}
	var firstErr error
	if err := closeTestOutputFile(&c.stdoutFile); err != nil && firstErr == nil {
		firstErr = fmt.Errorf("close test stdout artifact file: %w", err)
	}
	if err := closeTestOutputFile(&c.stderrFile); err != nil && firstErr == nil {
		firstErr = fmt.Errorf("close test stderr artifact file: %w", err)
	}
	return firstErr
}

func closeTestOutputFile(file **os.File) error {
	if file == nil || *file == nil {
		return nil
	}
	err := (*file).Close()
	*file = nil
	return err
}

func (c *testOutputCapture) finalize() ([]store.Artifact, string, string, error) {
	if err := c.close(); err != nil {
		return nil, "", "", err
	}
	stdoutArtifact, err := buildTestOutputArtifact(c.instanceID, store.ArtifactRoleTestStdout, 0, c.stdoutStoreKey, c.stdoutPath)
	if err != nil {
		return nil, "", "", err
	}
	stderrArtifact, err := buildTestOutputArtifact(c.instanceID, store.ArtifactRoleTestStderr, 1, c.stderrStoreKey, c.stderrPath)
	if err != nil {
		return nil, "", "", err
	}
	return []store.Artifact{stdoutArtifact, stderrArtifact}, c.stdoutStoreKey, c.stderrStoreKey, nil
}

func buildTestOutputArtifact(instanceID, role string, ordinal int, storeKey, path string) (store.Artifact, error) {
	info, err := os.Stat(path)
	if err != nil {
		return store.Artifact{}, fmt.Errorf("stat %s artifact %q: %w", role, path, err)
	}
	if info.IsDir() {
		return store.Artifact{}, fmt.Errorf("%s artifact path %q is a directory", role, path)
	}
	sum, err := fileSHA256(path)
	if err != nil {
		return store.Artifact{}, fmt.Errorf("hash %s artifact %q: %w", role, path, err)
	}
	artifactSuffix, err := testOutputArtifactSuffix(role)
	if err != nil {
		return store.Artifact{}, err
	}
	return store.Artifact{
		ArtifactID:  "art-" + sanitizeID(instanceID) + "-" + artifactSuffix,
		Role:        role,
		Ordinal:     ordinal,
		StoreKey:    storeKey,
		URI:         "file://" + path,
		ContentType: "text/plain",
		ByteSize:    info.Size(),
		SHA256:      sum,
	}, nil
}

func testOutputArtifactSuffix(role string) (string, error) {
	switch role {
	case store.ArtifactRoleTestStdout:
		return "test-stdout", nil
	case store.ArtifactRoleTestStderr:
		return "test-stderr", nil
	default:
		return "", fmt.Errorf("unsupported streamed test artifact role %q", role)
	}
}
