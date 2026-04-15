package localexecutor

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/marginlab/margin-eval/runner/runner-core/store"
	"github.com/marginlab/margin-eval/runner/runner-local/runfs"
)

type commandExecutionResult struct {
	ExitCode  int
	Artifacts []store.Artifact
	StdoutRef string
	StderrRef string
}

type outputCapture struct {
	instanceID string
	stdoutRole string
	stderrRole string

	stdoutPath     string
	stdoutStoreKey string
	stdoutFile     *os.File

	stderrPath     string
	stderrStoreKey string
	stderrFile     *os.File
}

func newOutputCapture(runDir, instanceID, stdoutRole, stderrRole string) (*outputCapture, error) {
	stdoutPath, stdoutStoreKey, stdoutFile, err := openOutputFile(runDir, instanceID, stdoutRole)
	if err != nil {
		return nil, err
	}
	stderrPath, stderrStoreKey, stderrFile, err := openOutputFile(runDir, instanceID, stderrRole)
	if err != nil {
		_ = stdoutFile.Close()
		return nil, err
	}
	return &outputCapture{
		instanceID:     instanceID,
		stdoutRole:     stdoutRole,
		stderrRole:     stderrRole,
		stdoutPath:     stdoutPath,
		stdoutStoreKey: stdoutStoreKey,
		stdoutFile:     stdoutFile,
		stderrPath:     stderrPath,
		stderrStoreKey: stderrStoreKey,
		stderrFile:     stderrFile,
	}, nil
}

func openOutputFile(runDir, instanceID, role string) (string, string, *os.File, error) {
	path, storeKey, _, ok := runfs.AbsoluteArtifactPath(runDir, instanceID, role)
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

func (c *outputCapture) close() error {
	if c == nil {
		return nil
	}
	var firstErr error
	if err := closeOutputFile(&c.stdoutFile); err != nil && firstErr == nil {
		firstErr = fmt.Errorf("close %s artifact file: %w", c.stdoutRole, err)
	}
	if err := closeOutputFile(&c.stderrFile); err != nil && firstErr == nil {
		firstErr = fmt.Errorf("close %s artifact file: %w", c.stderrRole, err)
	}
	return firstErr
}

func closeOutputFile(file **os.File) error {
	if file == nil || *file == nil {
		return nil
	}
	err := (*file).Close()
	*file = nil
	return err
}

func (c *outputCapture) finalize() ([]store.Artifact, string, string, error) {
	if err := c.close(); err != nil {
		return nil, "", "", err
	}
	stdoutArtifact, err := buildOutputArtifact(c.instanceID, c.stdoutRole, 0, c.stdoutStoreKey, c.stdoutPath)
	if err != nil {
		return nil, "", "", err
	}
	stderrArtifact, err := buildOutputArtifact(c.instanceID, c.stderrRole, 1, c.stderrStoreKey, c.stderrPath)
	if err != nil {
		return nil, "", "", err
	}
	return []store.Artifact{stdoutArtifact, stderrArtifact}, c.stdoutStoreKey, c.stderrStoreKey, nil
}

func buildOutputArtifact(instanceID, role string, ordinal int, storeKey, path string) (store.Artifact, error) {
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
	artifactSuffix, err := outputArtifactSuffix(role)
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

func outputArtifactSuffix(role string) (string, error) {
	switch role {
	case store.ArtifactRoleOracleStdout:
		return "oracle-stdout", nil
	case store.ArtifactRoleOracleStderr:
		return "oracle-stderr", nil
	case store.ArtifactRoleTestStdout:
		return "test-stdout", nil
	case store.ArtifactRoleTestStderr:
		return "test-stderr", nil
	default:
		return "", fmt.Errorf("unsupported streamed artifact role %q", role)
	}
}
