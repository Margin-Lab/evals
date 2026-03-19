package fsutil

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// EnsureDir creates a directory tree if it does not exist.
func EnsureDir(path string, perm os.FileMode) error {
	if err := os.MkdirAll(path, perm); err != nil {
		return fmt.Errorf("create directory %s: %w", path, err)
	}
	return nil
}

// IsWritableDir checks if a directory is writable by creating a short-lived file.
func IsWritableDir(path string) error {
	tmp, err := os.CreateTemp(path, ".writable-check-*")
	if err != nil {
		return fmt.Errorf("create temp file in %s: %w", path, err)
	}
	name := tmp.Name()
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return fmt.Errorf("close temp file %s: %w", name, err)
	}
	if err := os.Remove(name); err != nil {
		return fmt.Errorf("remove temp file %s: %w", name, err)
	}
	return nil
}

// WriteFileAtomic writes content atomically to targetPath.
func WriteFileAtomic(targetPath string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(targetPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create parent dir %s: %w", dir, err)
	}

	tmpFile, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp file in %s: %w", dir, err)
	}
	tmpName := tmpFile.Name()

	cleanup := func() {
		_ = tmpFile.Close()
		_ = os.Remove(tmpName)
	}

	if _, err := tmpFile.Write(data); err != nil {
		cleanup()
		return fmt.Errorf("write temp file %s: %w", tmpName, err)
	}
	if err := tmpFile.Chmod(perm); err != nil {
		cleanup()
		return fmt.Errorf("chmod temp file %s: %w", tmpName, err)
	}
	if err := tmpFile.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("sync temp file %s: %w", tmpName, err)
	}
	if err := tmpFile.Close(); err != nil {
		cleanup()
		return fmt.Errorf("close temp file %s: %w", tmpName, err)
	}

	if err := os.Rename(tmpName, targetPath); err != nil {
		cleanup()
		return fmt.Errorf("rename %s to %s: %w", tmpName, targetPath, err)
	}

	return nil
}

// ValidatePathUnderRoot validates a path is under root. It returns an absolute path.
//
// Behavior:
// - always enforces lexical root containment on absolute paths
// - if the path exists, also enforces resolved (symlink-aware) containment
func ValidatePathUnderRoot(candidatePath, rootPath string) (string, error) {
	absRoot, err := filepath.Abs(rootPath)
	if err != nil {
		return "", fmt.Errorf("resolve root path %s: %w", rootPath, err)
	}
	absCandidate, err := filepath.Abs(candidatePath)
	if err != nil {
		return "", fmt.Errorf("resolve candidate path %s: %w", candidatePath, err)
	}

	if !isSubpath(absCandidate, absRoot) {
		return "", fmt.Errorf("path must be under %s", absRoot)
	}

	resolvedRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		if !os.IsNotExist(err) {
			return "", fmt.Errorf("resolve root symlinks %s: %w", absRoot, err)
		}
		resolvedRoot = absRoot
	}

	resolvedCandidate, err := resolvePathWithExistingPrefix(absCandidate)
	if err != nil {
		return "", err
	}
	if !isSubpath(resolvedCandidate, resolvedRoot) {
		return "", fmt.Errorf("resolved path must be under %s", resolvedRoot)
	}
	return resolvedCandidate, nil
}

// ValidateExistingDirUnderRoot validates that an existing directory is under root.
func ValidateExistingDirUnderRoot(candidatePath, rootPath string) (string, error) {
	validatedPath, err := ValidatePathUnderRoot(candidatePath, rootPath)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(validatedPath)
	if err != nil {
		return "", fmt.Errorf("stat %s: %w", validatedPath, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory", validatedPath)
	}
	return validatedPath, nil
}

func isSubpath(candidatePath, rootPath string) bool {
	rel, err := filepath.Rel(rootPath, candidatePath)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	if rel == ".." {
		return false
	}
	return !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

func resolvePathWithExistingPrefix(candidatePath string) (string, error) {
	current := filepath.Clean(candidatePath)
	suffix := make([]string, 0)

	for {
		_, err := os.Lstat(current)
		if err == nil {
			break
		}
		if !os.IsNotExist(err) {
			return "", fmt.Errorf("stat path %s: %w", current, err)
		}

		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("no existing path segment found for %s", candidatePath)
		}
		suffix = append([]string{filepath.Base(current)}, suffix...)
		current = parent
	}

	resolvedCurrent, err := filepath.EvalSymlinks(current)
	if err != nil {
		return "", fmt.Errorf("resolve symlinks for %s: %w", current, err)
	}

	resolved := resolvedCurrent
	for _, segment := range suffix {
		resolved = filepath.Join(resolved, segment)
	}
	return resolved, nil
}
