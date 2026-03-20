package updater

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestInstallScriptInstallsLatestStableRelease(t *testing.T) {
	assetName := "margin_v0.4.0_linux_amd64.tar.gz"
	checksumName := "margin_v0.4.0_SHA256SUMS.txt"
	archive := testArchive(t, "script-installed")
	checksum := mustChecksum(t, archive)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/repos/"+DefaultRepo+"/releases/latest":
			writeJSON(t, w, releaseInfo{TagName: "v0.4.0"})
		case r.URL.Path == "/"+DefaultRepo+"/releases/download/v0.4.0/"+assetName:
			_, _ = w.Write(archive)
		case r.URL.Path == "/"+DefaultRepo+"/releases/download/v0.4.0/"+checksumName:
			_, _ = w.Write([]byte(checksum + "  ./" + assetName + "\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	tmpDir := t.TempDir()
	installDir := filepath.Join(tmpDir, "bin")
	metadataPath := filepath.Join(tmpDir, "install.json")
	cmd := exec.Command("bash", installScriptPath(t))
	cmd.Env = append(os.Environ(),
		"HOME="+tmpDir,
		"PATH="+os.Getenv("PATH"),
		"MARGIN_API_BASE_URL="+server.URL,
		"MARGIN_DOWNLOAD_BASE_URL="+server.URL,
		"MARGIN_RELEASE_REPO="+DefaultRepo,
		"MARGIN_INSTALL_DIR="+installDir,
		"MARGIN_METADATA_PATH="+metadataPath,
		"MARGIN_TARGET_OS=linux",
		"MARGIN_TARGET_ARCH=amd64",
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("install script failed: %v\n%s", err, output)
	}

	binaryPath := filepath.Join(installDir, "margin")
	if got := strings.TrimSpace(readFile(t, binaryPath)); got != "script-installed" {
		t.Fatalf("binary contents = %q", got)
	}
	body := readFile(t, metadataPath)
	for _, want := range []string{
		`"installed_via": "official-installer"`,
		`"channel": "stable"`,
		`"installed_version": "v0.4.0"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("metadata missing %q: %s", want, body)
		}
	}
}

func installScriptPath(t *testing.T) string {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatalf("resolve caller")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", "..", "..", "scripts", "install.sh"))
}
