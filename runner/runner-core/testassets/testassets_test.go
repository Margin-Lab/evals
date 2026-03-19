package testassets

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPackDirRoundTripMaterialize(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "test.sh"), []byte("#!/usr/bin/env bash\necho ok\n"), 0o755)
	writeFile(t, filepath.Join(root, "data", "cases.txt"), []byte("alpha\nbeta\n"), 0o644)

	desc, err := PackDir(root)
	if err != nil {
		t.Fatalf("PackDir() error = %v", err)
	}
	if desc.ArchiveTGZBase64 == "" || desc.ArchiveTGZSHA256 == "" || desc.ArchiveTGZBytes <= 0 {
		t.Fatalf("unexpected descriptor: %+v", desc)
	}
	if err := ValidateDescriptor(desc, 0); err != nil {
		t.Fatalf("ValidateDescriptor() error = %v", err)
	}

	dest := t.TempDir()
	if err := Materialize(desc, dest, 0); err != nil {
		t.Fatalf("Materialize() error = %v", err)
	}
	requireFileBytes(t, filepath.Join(dest, "test.sh"), []byte("#!/usr/bin/env bash\necho ok\n"))
	requireFileBytes(t, filepath.Join(dest, "data", "cases.txt"), []byte("alpha\nbeta\n"))

	info, err := os.Stat(filepath.Join(dest, "test.sh"))
	if err != nil {
		t.Fatalf("stat materialized test.sh: %v", err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("expected executable mode on test.sh, got %o", info.Mode().Perm())
	}
}

func TestDecodeAndValidateRejectsDigestMismatch(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "test.sh"), []byte("echo ok\n"), 0o755)

	desc, err := PackDir(root)
	if err != nil {
		t.Fatalf("PackDir() error = %v", err)
	}
	desc.ArchiveTGZSHA256 = strings.Repeat("0", 64)
	if _, err := DecodeAndValidate(desc, 0); err == nil || !strings.Contains(err.Error(), "archive_tgz_sha256 mismatch") {
		t.Fatalf("expected sha mismatch error, got %v", err)
	}
}

func TestDecodeAndValidateRejectsTraversal(t *testing.T) {
	var payload bytes.Buffer
	gz := gzip.NewWriter(&payload)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "../escape.txt", Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len("x"))}); err != nil {
		t.Fatalf("write header: %v", err)
	}
	if _, err := tw.Write([]byte("x")); err != nil {
		t.Fatalf("write body: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	raw := payload.Bytes()
	sum := sha256.Sum256(raw)
	desc := Descriptor{
		ArchiveTGZBase64: base64.StdEncoding.EncodeToString(raw),
		ArchiveTGZSHA256: hex.EncodeToString(sum[:]),
		ArchiveTGZBytes:  len(raw),
	}
	if _, err := DecodeAndValidate(desc, 0); err == nil || !strings.Contains(err.Error(), "invalid tar entry path") {
		t.Fatalf("expected traversal validation error, got %v", err)
	}
}

func writeFile(t *testing.T, path string, body []byte, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, body, mode); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func requireFileBytes(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("file %s mismatch:\n got=%q\nwant=%q", path, string(got), string(want))
	}
}
