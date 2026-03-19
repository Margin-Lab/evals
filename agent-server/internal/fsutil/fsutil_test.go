package fsutil

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEnsureDirCreatesTree verifies EnsureDir creates nested directory trees.
func TestEnsureDirCreatesTree(t *testing.T) {
	target := filepath.Join(t.TempDir(), "a", "b", "c")
	if err := EnsureDir(target, 0o755); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("stat target: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("target is not a directory")
	}
}

// TestIsWritableDir verifies writable-directory detection succeeds for directories and fails for file paths.
func TestIsWritableDir(t *testing.T) {
	dir := t.TempDir()
	if err := IsWritableDir(dir); err != nil {
		t.Fatalf("IsWritableDir() error = %v", err)
	}

	notDir := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(notDir, []byte("x"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if err := IsWritableDir(notDir); err == nil {
		t.Fatalf("IsWritableDir() expected error for file path")
	}
}

// TestWriteFileAtomic verifies atomic writes create parent dirs and replace file contents on rewrite.
func TestWriteFileAtomic(t *testing.T) {
	target := filepath.Join(t.TempDir(), "nested", "file.txt")
	if err := WriteFileAtomic(target, []byte("first"), 0o644); err != nil {
		t.Fatalf("WriteFileAtomic(first) error = %v", err)
	}
	if err := WriteFileAtomic(target, []byte("second"), 0o600); err != nil {
		t.Fatalf("WriteFileAtomic(second) error = %v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if string(got) != "second" {
		t.Fatalf("content = %q, want %q", string(got), "second")
	}
}

// TestValidatePathUnderRootRejectsTraversal verifies lexical path traversal outside root is rejected.
func TestValidatePathUnderRootRejectsTraversal(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(root, "..", "outside")

	_, err := ValidatePathUnderRoot(outside, root)
	if err == nil {
		t.Fatalf("ValidatePathUnderRoot() expected error")
	}
	if !strings.Contains(err.Error(), "path must be under") {
		t.Fatalf("error = %q", err)
	}
}

// TestValidatePathUnderRootRejectsSymlinkEscape verifies symlink-resolved escapes outside root are rejected.
func TestValidatePathUnderRootRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()

	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}

	candidate := filepath.Join(root, "link", "child")
	_, err := ValidatePathUnderRoot(candidate, root)
	if err == nil {
		t.Fatalf("ValidatePathUnderRoot() expected symlink escape error")
	}
	if !strings.Contains(err.Error(), "resolved path must be under") {
		t.Fatalf("error = %q", err)
	}
}

// TestValidatePathUnderRootAllowsSymlinkWithinRoot verifies symlink resolution is allowed when final path remains under root.
func TestValidatePathUnderRootAllowsSymlinkWithinRoot(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatalf("mkdir target: %v", err)
	}
	if err := os.Symlink(target, filepath.Join(root, "link")); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}

	candidate := filepath.Join(root, "link", "child")
	got, err := ValidatePathUnderRoot(candidate, root)
	if err != nil {
		t.Fatalf("ValidatePathUnderRoot() error = %v", err)
	}
	resolvedTarget, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatalf("eval symlinks target: %v", err)
	}
	want := filepath.Join(resolvedTarget, "child")
	if got != want {
		t.Fatalf("resolved = %q, want %q", got, want)
	}
}

// TestValidateExistingDirUnderRoot verifies directory existence/type checks after root-constrained path validation.
func TestValidateExistingDirUnderRoot(t *testing.T) {
	root := t.TempDir()
	subdir := filepath.Join(root, "sub")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatalf("mkdir subdir: %v", err)
	}

	got, err := ValidateExistingDirUnderRoot(subdir, root)
	if err != nil {
		t.Fatalf("ValidateExistingDirUnderRoot() error = %v", err)
	}
	resolvedSubdir, err := filepath.EvalSymlinks(subdir)
	if err != nil {
		t.Fatalf("eval symlinks subdir: %v", err)
	}
	if got != resolvedSubdir {
		t.Fatalf("validated = %q, want %q", got, resolvedSubdir)
	}

	file := filepath.Join(root, "file.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if _, err := ValidateExistingDirUnderRoot(file, root); err == nil {
		t.Fatalf("ValidateExistingDirUnderRoot(file) expected error")
	}
}
