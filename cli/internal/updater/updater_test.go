package updater

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpdateStableInstallsLatestStable(t *testing.T) {
	tmpDir := t.TempDir()
	binaryPath := filepath.Join(tmpDir, "margin")
	restore := overrideUpdaterRuntime(t, "linux", "amd64", binaryPath)
	defer restore()
	writeExecutable(t, binaryPath, "old-version")

	metadataPath := filepath.Join(tmpDir, "install.json")
	if err := writeMetadata(metadataPath, InstallMetadata{
		SchemaVersion:    metadataSchemaVersion,
		InstalledVia:     installedViaOfficial,
		Repo:             DefaultRepo,
		Channel:          channelStable,
		BinaryPath:       binaryPath,
		InstalledVersion: "v0.1.0",
	}); err != nil {
		t.Fatalf("write metadata: %v", err)
	}

	assetName := "margin_v0.2.0_linux_amd64.tar.gz"
	checksumName := "margin_v0.2.0_SHA256SUMS.txt"
	archive := testArchive(t, "stable-version")
	checksum := mustChecksum(t, archive)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/repos/"+DefaultRepo+"/releases/latest":
			writeJSON(t, w, releaseInfo{TagName: "v0.2.0"})
		case r.URL.Path == "/"+DefaultRepo+"/releases/download/v0.2.0/"+assetName:
			_, _ = w.Write(archive)
		case r.URL.Path == "/"+DefaultRepo+"/releases/download/v0.2.0/"+checksumName:
			_, _ = w.Write([]byte(checksum + "  ./" + assetName + "\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	manager, err := New(Config{
		APIBaseURL:      server.URL,
		DownloadBaseURL: server.URL,
		MetadataPath:    metadataPath,
	})
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}

	result, err := manager.Update(context.Background(), "v0.1.0")
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if !result.Updated {
		t.Fatalf("expected update result to mark Updated")
	}
	if result.LatestVersion != "v0.2.0" {
		t.Fatalf("LatestVersion = %q", result.LatestVersion)
	}
	if got := strings.TrimSpace(readFile(t, binaryPath)); got != "stable-version" {
		t.Fatalf("binary contents = %q", got)
	}
	var metadata InstallMetadata
	body := readFile(t, metadataPath)
	if err := json.Unmarshal([]byte(body), &metadata); err != nil {
		t.Fatalf("decode metadata: %v", err)
	}
	if metadata.InstalledVersion != "v0.2.0" {
		t.Fatalf("InstalledVersion = %q", metadata.InstalledVersion)
	}
}

func TestUpdateBetaInstallsLatestBeta(t *testing.T) {
	tmpDir := t.TempDir()
	binaryPath := filepath.Join(tmpDir, "margin")
	restore := overrideUpdaterRuntime(t, "linux", "amd64", binaryPath)
	defer restore()
	writeExecutable(t, binaryPath, "old-beta")

	metadataPath := filepath.Join(tmpDir, "install.json")
	if err := writeMetadata(metadataPath, InstallMetadata{
		SchemaVersion:    metadataSchemaVersion,
		InstalledVia:     installedViaOfficial,
		Repo:             DefaultRepo,
		Channel:          channelBeta,
		BinaryPath:       binaryPath,
		InstalledVersion: "v0.1.0-beta.1",
	}); err != nil {
		t.Fatalf("write metadata: %v", err)
	}

	assetName := "margin_v0.3.0-beta.2_linux_amd64.tar.gz"
	checksumName := "margin_v0.3.0-beta.2_SHA256SUMS.txt"
	archive := testArchive(t, "beta-version")
	checksum := mustChecksum(t, archive)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/repos/"+DefaultRepo+"/releases":
			writeJSON(t, w, []releaseInfo{
				{TagName: "v0.2.0-beta.9"},
				{TagName: "v0.3.0-beta.2"},
				{TagName: "v0.3.0"},
			})
		case r.URL.Path == "/"+DefaultRepo+"/releases/download/v0.3.0-beta.2/"+assetName:
			_, _ = w.Write(archive)
		case r.URL.Path == "/"+DefaultRepo+"/releases/download/v0.3.0-beta.2/"+checksumName:
			_, _ = w.Write([]byte(checksum + "  ./" + assetName + "\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	manager, err := New(Config{
		APIBaseURL:      server.URL,
		DownloadBaseURL: server.URL,
		MetadataPath:    metadataPath,
	})
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}

	result, err := manager.Update(context.Background(), "v0.1.0-beta.1")
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if !result.Updated {
		t.Fatalf("expected update result to mark Updated")
	}
	if result.LatestVersion != "v0.3.0-beta.2" {
		t.Fatalf("LatestVersion = %q", result.LatestVersion)
	}
	if got := strings.TrimSpace(readFile(t, binaryPath)); got != "beta-version" {
		t.Fatalf("binary contents = %q", got)
	}
}

func TestUpdateNoOpWhenAlreadyCurrent(t *testing.T) {
	tmpDir := t.TempDir()
	binaryPath := filepath.Join(tmpDir, "margin")
	restore := overrideUpdaterRuntime(t, "linux", "amd64", binaryPath)
	defer restore()
	writeExecutable(t, binaryPath, "same-version")

	metadataPath := filepath.Join(tmpDir, "install.json")
	if err := writeMetadata(metadataPath, InstallMetadata{
		SchemaVersion:    metadataSchemaVersion,
		InstalledVia:     installedViaOfficial,
		Repo:             DefaultRepo,
		Channel:          channelStable,
		BinaryPath:       binaryPath,
		InstalledVersion: "v0.2.0",
	}); err != nil {
		t.Fatalf("write metadata: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/"+DefaultRepo+"/releases/latest" {
			http.NotFound(w, r)
			return
		}
		writeJSON(t, w, releaseInfo{TagName: "v0.2.0"})
	}))
	defer server.Close()

	manager, err := New(Config{
		APIBaseURL:      server.URL,
		DownloadBaseURL: server.URL,
		MetadataPath:    metadataPath,
	})
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}

	result, err := manager.Update(context.Background(), "v0.2.0")
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if result.Updated {
		t.Fatalf("expected no-op update")
	}
	if got := strings.TrimSpace(readFile(t, binaryPath)); got != "same-version" {
		t.Fatalf("binary contents = %q", got)
	}
}

func TestUpdateRefusesSourceBuild(t *testing.T) {
	manager, err := New(Config{MetadataPath: filepath.Join(t.TempDir(), "install.json")})
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	if _, err := manager.Update(context.Background(), "dev"); err == nil || !strings.Contains(err.Error(), "source builds") {
		t.Fatalf("expected source build refusal, got %v", err)
	}
}

func TestUpdateRefusesNonInstallerManagedBinary(t *testing.T) {
	restore := overrideUpdaterRuntime(t, "linux", "amd64", filepath.Join(t.TempDir(), "margin"))
	defer restore()

	manager, err := New(Config{MetadataPath: filepath.Join(t.TempDir(), "missing.json")})
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	if _, err := manager.Update(context.Background(), "v0.1.0"); err == nil || !strings.Contains(err.Error(), "official installer") {
		t.Fatalf("expected official installer refusal, got %v", err)
	}
}

func TestUpdateRejectsChecksumMismatch(t *testing.T) {
	tmpDir := t.TempDir()
	binaryPath := filepath.Join(tmpDir, "margin")
	restore := overrideUpdaterRuntime(t, "linux", "amd64", binaryPath)
	defer restore()
	writeExecutable(t, binaryPath, "old-version")

	metadataPath := filepath.Join(tmpDir, "install.json")
	if err := writeMetadata(metadataPath, InstallMetadata{
		SchemaVersion:    metadataSchemaVersion,
		InstalledVia:     installedViaOfficial,
		Repo:             DefaultRepo,
		Channel:          channelStable,
		BinaryPath:       binaryPath,
		InstalledVersion: "v0.1.0",
	}); err != nil {
		t.Fatalf("write metadata: %v", err)
	}

	assetName := "margin_v0.2.0_linux_amd64.tar.gz"
	checksumName := "margin_v0.2.0_SHA256SUMS.txt"
	archive := testArchive(t, "bad-update")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/repos/"+DefaultRepo+"/releases/latest":
			writeJSON(t, w, releaseInfo{TagName: "v0.2.0"})
		case r.URL.Path == "/"+DefaultRepo+"/releases/download/v0.2.0/"+assetName:
			_, _ = w.Write(archive)
		case r.URL.Path == "/"+DefaultRepo+"/releases/download/v0.2.0/"+checksumName:
			_, _ = w.Write([]byte("deadbeef  ./" + assetName + "\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	manager, err := New(Config{
		APIBaseURL:      server.URL,
		DownloadBaseURL: server.URL,
		MetadataPath:    metadataPath,
	})
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}

	if _, err := manager.Update(context.Background(), "v0.1.0"); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("expected checksum failure, got %v", err)
	}
	if got := strings.TrimSpace(readFile(t, binaryPath)); got != "old-version" {
		t.Fatalf("binary contents = %q", got)
	}
}

func overrideUpdaterRuntime(t *testing.T, goos, goarch, executablePath string) func() {
	t.Helper()
	origExec := currentExecutablePath
	origGOOS := runtimeGOOS
	origGOARCH := runtimeGOARCH
	currentExecutablePath = func() (string, error) { return executablePath, nil }
	runtimeGOOS = goos
	runtimeGOARCH = goarch
	return func() {
		currentExecutablePath = origExec
		runtimeGOOS = origGOOS
		runtimeGOARCH = origGOARCH
	}
}

func writeExecutable(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(body)
}

func writeJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatalf("encode response: %v", err)
	}
}

func testArchive(t *testing.T, marginContent string) []byte {
	t.Helper()
	archivePath := filepath.Join(t.TempDir(), "archive.tar.gz")
	file, err := os.Create(archivePath)
	if err != nil {
		t.Fatalf("create archive: %v", err)
	}
	gzWriter := gzip.NewWriter(file)
	tarWriter := tar.NewWriter(gzWriter)
	body := []byte(marginContent)
	if err := tarWriter.WriteHeader(&tar.Header{
		Name: "margin",
		Mode: 0o755,
		Size: int64(len(body)),
	}); err != nil {
		t.Fatalf("write header: %v", err)
	}
	if _, err := tarWriter.Write(body); err != nil {
		t.Fatalf("write body: %v", err)
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatalf("close tar writer: %v", err)
	}
	if err := gzWriter.Close(); err != nil {
		t.Fatalf("close gzip writer: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close archive: %v", err)
	}
	bodyBytes, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatalf("read archive: %v", err)
	}
	return bodyBytes
}

func mustChecksum(t *testing.T, body []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "asset")
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("write asset: %v", err)
	}
	checksum, err := sha256File(path)
	if err != nil {
		t.Fatalf("checksum: %v", err)
	}
	return checksum
}
