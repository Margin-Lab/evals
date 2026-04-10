package noderuntime

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ulikunitz/xz"
)

type fakeCommandRunner struct {
	runFn func(ctx context.Context, name string, args []string, env []string, dir string) ([]byte, error)
}

func (f *fakeCommandRunner) Run(ctx context.Context, name string, args []string, env []string, dir string) ([]byte, error) {
	if f.runFn == nil {
		return nil, fmt.Errorf("unexpected command: %s %v", name, args)
	}
	return f.runFn(ctx, name, args, env, dir)
}

func TestNewRejectsInvalidConfig(t *testing.T) {
	_, err := New(Config{})
	if err == nil || !strings.Contains(err.Error(), "bin dir is required") {
		t.Fatalf("New() error = %v", err)
	}
}

func TestEnsureInstallsAndLinksManagedBinariesViaArchive(t *testing.T) {
	stateDir := t.TempDir()
	binDir := t.TempDir()
	manager, err := New(Config{
		BinDir:   binDir,
		StateDir: stateDir,
		NVMDir:   filepath.Join(stateDir, "toolchain", "node-runtime"),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	manager.now = func() time.Time { return time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC) }
	manager.detectAlpine = func() bool { return false }
	manager.detectMusl = func() bool { return false }
	manager.arch = "amd64"

	spec, err := manager.resolveArchiveSpec("24")
	if err != nil {
		t.Fatalf("resolveArchiveSpec() error = %v", err)
	}
	archive := runtimeTestArchive(t, spec.Resolved, spec.FileName)
	checksums := runtimeTestChecksums(t, spec.FileName, archive)
	manager.downloadURL = func(_ context.Context, url string) ([]byte, error) {
		switch url {
		case spec.DownloadURL:
			return archive, nil
		case spec.ChecksumsURL:
			return checksums, nil
		default:
			return nil, fmt.Errorf("unexpected download url: %s", url)
		}
	}

	info, err := manager.Ensure(context.Background(), Spec{Minimum: "20", Preferred: "24"})
	if err != nil {
		t.Fatalf("Ensure() error = %v", err)
	}
	if info.Version != "24" || info.InstallMethod != installMethodArchive {
		t.Fatalf("Ensure() info = %+v", info)
	}

	for _, name := range managedBinaries {
		linkPath := filepath.Join(manager.ManagedBinDir(), name)
		resolved, err := os.Readlink(linkPath)
		if err != nil {
			t.Fatalf("Readlink(%s) error = %v", linkPath, err)
		}
		if !strings.Contains(resolved, spec.Resolved) {
			t.Fatalf("managed %s symlink target = %q", name, resolved)
		}
	}

	manifestPath := filepath.Join(stateDir, "toolchain", "node", "runtime.json")
	manifest, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", manifestPath, err)
	}
	if !strings.Contains(string(manifest), `"install_method": "archive"`) {
		t.Fatalf("manifest missing install method: %s", string(manifest))
	}
	if !strings.Contains(string(manifest), `"resolved_version": "v24.14.0"`) {
		t.Fatalf("manifest missing resolved version: %s", string(manifest))
	}
}

func TestEnsureFallsBackToMinimumViaArchive(t *testing.T) {
	stateDir := t.TempDir()
	binDir := t.TempDir()
	manager, err := New(Config{
		BinDir:   binDir,
		StateDir: stateDir,
		NVMDir:   filepath.Join(stateDir, "toolchain", "node-runtime"),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	manager.detectAlpine = func() bool { return false }
	manager.detectMusl = func() bool { return false }
	manager.arch = "amd64"

	minSpec, err := manager.resolveArchiveSpec("24")
	if err != nil {
		t.Fatalf("resolveArchiveSpec(minimum) error = %v", err)
	}
	minArchive := runtimeTestArchive(t, minSpec.Resolved, minSpec.FileName)
	minChecksums := runtimeTestChecksums(t, minSpec.FileName, minArchive)
	manager.downloadURL = func(_ context.Context, url string) ([]byte, error) {
		switch url {
		case minSpec.DownloadURL:
			return minArchive, nil
		case minSpec.ChecksumsURL:
			return minChecksums, nil
		default:
			return nil, fmt.Errorf("unexpected download url: %s", url)
		}
	}

	info, err := manager.Ensure(context.Background(), Spec{Minimum: "24", Preferred: "26"})
	if err != nil {
		t.Fatalf("Ensure() error = %v", err)
	}
	if info.Version != "24" || info.InstallMethod != installMethodArchive {
		t.Fatalf("Ensure() info = %+v", info)
	}
}

func TestEnsureFailsForUnsupportedMajorsViaArchive(t *testing.T) {
	stateDir := t.TempDir()
	binDir := t.TempDir()
	manager, err := New(Config{
		BinDir:   binDir,
		StateDir: stateDir,
		NVMDir:   filepath.Join(stateDir, "toolchain", "node-runtime"),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	manager.detectAlpine = func() bool { return false }
	manager.detectMusl = func() bool { return false }

	_, err = manager.Ensure(context.Background(), Spec{Minimum: "25", Preferred: "26"})
	if err == nil || !strings.Contains(err.Error(), "supported set") {
		t.Fatalf("Ensure() error = %v", err)
	}
}

func TestResolveArchiveSpecUsesMuslMirrorAndOverride(t *testing.T) {
	stateDir := t.TempDir()
	manager, err := New(Config{
		BinDir:   t.TempDir(),
		StateDir: stateDir,
		NVMDir:   filepath.Join(stateDir, "toolchain", "node-runtime"),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	manager.detectMusl = func() bool { return true }
	manager.detectAlpine = func() bool { return false }
	manager.arch = "amd64"

	spec, err := manager.resolveArchiveSpec("24")
	if err != nil {
		t.Fatalf("resolveArchiveSpec() error = %v", err)
	}
	if !strings.HasPrefix(spec.DownloadURL, defaultMuslNodeMirrorURL) {
		t.Fatalf("DownloadURL = %q", spec.DownloadURL)
	}
	if !strings.Contains(spec.FileName, "musl") {
		t.Fatalf("FileName = %q", spec.FileName)
	}

	t.Setenv(nvmNodeMirrorEnvKey, "https://example.invalid/node")
	overrideSpec, err := manager.resolveArchiveSpec("24")
	if err != nil {
		t.Fatalf("resolveArchiveSpec(override) error = %v", err)
	}
	if !strings.HasPrefix(overrideSpec.DownloadURL, "https://example.invalid/node/") {
		t.Fatalf("DownloadURL = %q", overrideSpec.DownloadURL)
	}
}

func TestEnsureInstallsLatestNodeViaAPKAndChecksMinimum(t *testing.T) {
	stateDir := t.TempDir()
	binDir := t.TempDir()
	systemBinDir := filepath.Join(t.TempDir(), "system-bin")
	if err := os.MkdirAll(systemBinDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%s) error = %v", systemBinDir, err)
	}
	nodePath := filepath.Join(systemBinDir, "node")
	npmPath := filepath.Join(systemBinDir, "npm")
	npxPath := filepath.Join(systemBinDir, "npx")
	for _, path := range []string{nodePath, npmPath, npxPath} {
		if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatalf("WriteFile(%s) error = %v", path, err)
		}
	}
	t.Setenv("PATH", strings.Join([]string{"/usr/local/bin", "/usr/local/sbin", systemBinDir, "/usr/bin"}, string(os.PathListSeparator)))

	manager, err := New(Config{
		BinDir:   binDir,
		StateDir: stateDir,
		NVMDir:   filepath.Join(stateDir, "toolchain", "node-runtime"),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	manager.detectAlpine = func() bool { return true }
	manager.lookPath = func(file string) (string, error) {
		switch file {
		case "apk":
			return "/sbin/apk", nil
		default:
			return "", fmt.Errorf("unexpected tool lookup: %s", file)
		}
	}

	installCount := 0
	manager.runner = &fakeCommandRunner{runFn: func(_ context.Context, name string, args []string, _ []string, _ string) ([]byte, error) {
		if name == "/sbin/apk" {
			installCount++
			for _, path := range []string{nodePath, npmPath, npxPath} {
				if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
					return nil, err
				}
			}
			return []byte("OK"), nil
		}
		switch {
		case name == filepath.Join(manager.ManagedBinDir(), "node") && len(args) == 1 && args[0] == "--version":
			return []byte("v24.1.0\n"), nil
		case name == filepath.Join(manager.ManagedBinDir(), "npm") && len(args) == 1 && args[0] == "--version":
			return []byte("10.1.0\n"), nil
		case name == filepath.Join(manager.ManagedBinDir(), "npx") && len(args) == 1 && args[0] == "--version":
			return []byte("10.1.0\n"), nil
		default:
			return nil, fmt.Errorf("unexpected command: %s %v", name, args)
		}
	}}

	info, err := manager.Ensure(context.Background(), Spec{Minimum: "18", Preferred: "24"})
	if err != nil {
		t.Fatalf("Ensure() error = %v", err)
	}
	if installCount != 1 {
		t.Fatalf("apk install attempts = %d, want 1", installCount)
	}
	if info.Version != "24" || info.InstallMethod != installMethodAPK {
		t.Fatalf("Ensure() info = %+v", info)
	}
	for _, name := range managedBinaries {
		linkPath := filepath.Join(manager.ManagedBinDir(), name)
		resolved, err := os.Readlink(linkPath)
		if err != nil {
			t.Fatalf("Readlink(%s) error = %v", linkPath, err)
		}
		if !strings.Contains(resolved, systemBinDir) {
			t.Fatalf("managed %s symlink target = %q, want system bin", name, resolved)
		}
	}
}

func TestEnsureAcceptsAPKInstalledNodeAboveMinimum(t *testing.T) {
	stateDir := t.TempDir()
	binDir := t.TempDir()
	systemBinDir := filepath.Join(t.TempDir(), "system-bin")
	if err := os.MkdirAll(systemBinDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(systemBinDir) error = %v", err)
	}
	nodePath := filepath.Join(systemBinDir, "node")
	npmPath := filepath.Join(systemBinDir, "npm")
	npxPath := filepath.Join(systemBinDir, "npx")
	t.Setenv("PATH", strings.Join([]string{"/usr/local/bin", systemBinDir, "/usr/bin"}, string(os.PathListSeparator)))

	manager, err := New(Config{
		BinDir:   binDir,
		StateDir: stateDir,
		NVMDir:   filepath.Join(stateDir, "toolchain", "node-runtime"),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	manager.detectAlpine = func() bool { return true }
	manager.lookPath = func(file string) (string, error) {
		switch file {
		case "apk":
			return "/sbin/apk", nil
		default:
			return "", fmt.Errorf("unexpected tool lookup: %s", file)
		}
	}

	installCount := 0
	manager.runner = &fakeCommandRunner{runFn: func(_ context.Context, name string, args []string, _ []string, _ string) ([]byte, error) {
		if name == "/sbin/apk" {
			installCount++
			for _, path := range []string{nodePath, npmPath, npxPath} {
				if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
					return nil, err
				}
			}
			return []byte("OK"), nil
		}
		switch {
		case name == filepath.Join(manager.ManagedBinDir(), "node") && len(args) == 1 && args[0] == "--version":
			return []byte("v24.1.0\n"), nil
		case name == filepath.Join(manager.ManagedBinDir(), "npm") && len(args) == 1 && args[0] == "--version":
			return []byte("10.1.0\n"), nil
		case name == filepath.Join(manager.ManagedBinDir(), "npx") && len(args) == 1 && args[0] == "--version":
			return []byte("10.1.0\n"), nil
		default:
			return nil, fmt.Errorf("unexpected command: %s %v", name, args)
		}
	}}

	info, err := manager.Ensure(context.Background(), Spec{Minimum: "18", Preferred: "24"})
	if err != nil {
		t.Fatalf("Ensure() error = %v", err)
	}
	if installCount != 1 {
		t.Fatalf("apk install attempts = %d, want 1", installCount)
	}
	if info.Version != "24" || info.InstallMethod != installMethodAPK {
		t.Fatalf("Ensure() info = %+v", info)
	}
}

func TestEnsureFailsWhenAPKInstalledNodeIsBelowMinimum(t *testing.T) {
	stateDir := t.TempDir()
	binDir := t.TempDir()
	systemBinDir := filepath.Join(t.TempDir(), "system-bin")
	if err := os.MkdirAll(systemBinDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(systemBinDir) error = %v", err)
	}
	nodePath := filepath.Join(systemBinDir, "node")
	npmPath := filepath.Join(systemBinDir, "npm")
	npxPath := filepath.Join(systemBinDir, "npx")
	t.Setenv("PATH", strings.Join([]string{"/usr/local/bin", systemBinDir, "/usr/bin"}, string(os.PathListSeparator)))

	manager, err := New(Config{
		BinDir:   binDir,
		StateDir: stateDir,
		NVMDir:   filepath.Join(stateDir, "toolchain", "node-runtime"),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	manager.detectAlpine = func() bool { return true }
	manager.lookPath = func(file string) (string, error) {
		switch file {
		case "apk":
			return "/sbin/apk", nil
		default:
			return "", fmt.Errorf("unexpected tool lookup: %s", file)
		}
	}

	manager.runner = &fakeCommandRunner{runFn: func(_ context.Context, name string, args []string, _ []string, _ string) ([]byte, error) {
		if name == "/sbin/apk" {
			for _, path := range []string{nodePath, npmPath, npxPath} {
				if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
					return nil, err
				}
			}
			return []byte("OK"), nil
		}
		switch {
		case name == filepath.Join(manager.ManagedBinDir(), "node") && len(args) == 1 && args[0] == "--version":
			return []byte("v16.20.0\n"), nil
		case name == filepath.Join(manager.ManagedBinDir(), "npm") && len(args) == 1 && args[0] == "--version":
			return []byte("9.6.6\n"), nil
		case name == filepath.Join(manager.ManagedBinDir(), "npx") && len(args) == 1 && args[0] == "--version":
			return []byte("9.6.6\n"), nil
		default:
			return nil, fmt.Errorf("unexpected command: %s %v", name, args)
		}
	}}

	_, err = manager.Ensure(context.Background(), Spec{Minimum: "18", Preferred: "24"})
	if err == nil || !strings.Contains(err.Error(), `below minimum version "18"`) {
		t.Fatalf("Ensure() error = %v", err)
	}
}

func TestEnsureFailsWhenSanitizedAPKPathMissingBinary(t *testing.T) {
	stateDir := t.TempDir()
	binDir := t.TempDir()
	systemBinDir := filepath.Join(t.TempDir(), "system-bin")
	if err := os.MkdirAll(systemBinDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(systemBinDir) error = %v", err)
	}
	nodePath := filepath.Join(systemBinDir, "node")
	npmPath := filepath.Join(systemBinDir, "npm")
	t.Setenv("PATH", strings.Join([]string{"/usr/local/bin", systemBinDir, "/usr/bin"}, string(os.PathListSeparator)))

	manager, err := New(Config{
		BinDir:   binDir,
		StateDir: stateDir,
		NVMDir:   filepath.Join(stateDir, "toolchain", "node-runtime"),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	manager.detectAlpine = func() bool { return true }
	manager.lookPath = func(file string) (string, error) {
		if file == "apk" {
			return "/sbin/apk", nil
		}
		return "", fmt.Errorf("unexpected tool lookup: %s", file)
	}
	manager.runner = &fakeCommandRunner{runFn: func(_ context.Context, name string, args []string, _ []string, _ string) ([]byte, error) {
		if name != "/sbin/apk" {
			return nil, fmt.Errorf("unexpected command: %s %v", name, args)
		}
		for _, path := range []string{nodePath, npmPath} {
			if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
				return nil, err
			}
		}
		return []byte("OK"), nil
	}}

	_, err = manager.Ensure(context.Background(), Spec{Minimum: "18", Preferred: "24"})
	if err == nil || !strings.Contains(err.Error(), `resolve npx in apk toolchain PATH`) {
		t.Fatalf("Ensure() error = %v", err)
	}
}

func TestCheckFailsWhenManagedNPMMissing(t *testing.T) {
	stateDir := t.TempDir()
	binDir := t.TempDir()
	manager, err := New(Config{
		BinDir:   binDir,
		StateDir: stateDir,
		NVMDir:   filepath.Join(stateDir, "toolchain", "node-runtime"),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if err := os.MkdirAll(manager.ManagedBinDir(), 0o755); err != nil {
		t.Fatalf("MkdirAll(managed bin) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(manager.ManagedBinDir(), "node"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(node) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(manager.ManagedBinDir(), "npx"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(npx) error = %v", err)
	}

	manifestDir := filepath.Join(stateDir, "toolchain", "node")
	if err := os.MkdirAll(manifestDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(manifestDir) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(manifestDir, "runtime.json"), []byte(`{"node_version":"24","install_method":"archive"}`+"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(runtime.json) error = %v", err)
	}

	_, err = manager.Check(context.Background(), Spec{Minimum: "20", Preferred: "24"})
	if err == nil || !strings.Contains(err.Error(), "managed npm binary is missing") {
		t.Fatalf("Check() error = %v", err)
	}
}

func TestExportPATHPrependsManagedBinOnce(t *testing.T) {
	manager, err := New(Config{
		BinDir:   t.TempDir(),
		StateDir: t.TempDir(),
		NVMDir:   t.TempDir(),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	t.Setenv("PATH", "/usr/bin")
	if err := manager.ExportPATH(); err != nil {
		t.Fatalf("ExportPATH() error = %v", err)
	}
	if err := manager.ExportPATH(); err != nil {
		t.Fatalf("ExportPATH() second call error = %v", err)
	}

	managedBin := manager.ManagedBinDir()
	path := os.Getenv("PATH")
	parts := strings.Split(path, string(os.PathListSeparator))
	if len(parts) == 0 || parts[0] != managedBin {
		t.Fatalf("PATH first entry = %q, want %q (full: %q)", parts[0], managedBin, path)
	}
	count := 0
	for _, part := range parts {
		if part == managedBin {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("managed bin appears %d times in PATH: %q", count, path)
	}
	if got := os.Getenv("NVM_DIR"); got != manager.cfg.NVMDir {
		t.Fatalf("NVM_DIR = %q, want %q", got, manager.cfg.NVMDir)
	}
	if got := os.Getenv("NODE_EXTRA_CA_CERTS"); got == "" {
		t.Fatalf("NODE_EXTRA_CA_CERTS was not exported")
	}
	if got := os.Getenv("NPM_CONFIG_CAFILE"); got == "" {
		t.Fatalf("NPM_CONFIG_CAFILE was not exported")
	}
}

func TestWithEnvironmentPrependsManagedBinToPATH(t *testing.T) {
	manager, err := New(Config{
		BinDir:   t.TempDir(),
		StateDir: t.TempDir(),
		NVMDir:   t.TempDir(),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	managedBin := manager.ManagedBinDir()
	t.Setenv("PATH", managedBin+string(os.PathListSeparator)+"/usr/bin"+string(os.PathListSeparator)+"/bin")

	env := manager.withEnvironment("", nil)
	pathValue := ""
	for _, entry := range env {
		if strings.HasPrefix(entry, "PATH=") {
			pathValue = strings.TrimPrefix(entry, "PATH=")
			break
		}
	}
	if pathValue == "" {
		t.Fatalf("PATH was not set in derived environment")
	}

	parts := strings.Split(pathValue, string(os.PathListSeparator))
	if len(parts) == 0 || parts[0] != managedBin {
		t.Fatalf("PATH first entry = %q, want %q (full: %q)", parts[0], managedBin, pathValue)
	}
	count := 0
	for _, part := range parts {
		if part == managedBin {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("managed bin appears %d times in PATH: %q", count, pathValue)
	}
	nodeExtra := ""
	npmCAFile := ""
	for _, entry := range env {
		if strings.HasPrefix(entry, "NODE_EXTRA_CA_CERTS=") {
			nodeExtra = strings.TrimPrefix(entry, "NODE_EXTRA_CA_CERTS=")
		}
		if strings.HasPrefix(entry, "NPM_CONFIG_CAFILE=") {
			npmCAFile = strings.TrimPrefix(entry, "NPM_CONFIG_CAFILE=")
		}
	}
	if nodeExtra == "" {
		t.Fatalf("NODE_EXTRA_CA_CERTS was not set in derived environment")
	}
	if npmCAFile == "" {
		t.Fatalf("NPM_CONFIG_CAFILE was not set in derived environment")
	}
}

func runtimeTestArchive(t *testing.T, version, fileName string) []byte {
	t.Helper()

	topDir := strings.TrimSuffix(fileName, ".tar.xz")
	var archive bytes.Buffer
	xzWriter, err := xz.NewWriter(&archive)
	if err != nil {
		t.Fatalf("xz.NewWriter() error = %v", err)
	}
	tarWriter := tar.NewWriter(xzWriter)
	defer func() {
		_ = tarWriter.Close()
		_ = xzWriter.Close()
	}()

	writeDir := func(name string, mode int64) {
		if err := tarWriter.WriteHeader(&tar.Header{
			Name:     name,
			Typeflag: tar.TypeDir,
			Mode:     mode,
		}); err != nil {
			t.Fatalf("WriteHeader(dir %s) error = %v", name, err)
		}
	}
	writeFile := func(name, body string, mode int64) {
		if err := tarWriter.WriteHeader(&tar.Header{
			Name:     name,
			Typeflag: tar.TypeReg,
			Mode:     mode,
			Size:     int64(len(body)),
		}); err != nil {
			t.Fatalf("WriteHeader(file %s) error = %v", name, err)
		}
		if _, err := tarWriter.Write([]byte(body)); err != nil {
			t.Fatalf("Write(file %s) error = %v", name, err)
		}
	}

	writeDir(topDir, 0o755)
	writeDir(filepath.ToSlash(filepath.Join(topDir, "bin")), 0o755)
	writeFile(filepath.ToSlash(filepath.Join(topDir, "bin", "node")), "#!/bin/sh\necho "+version+"\n", 0o755)
	writeFile(filepath.ToSlash(filepath.Join(topDir, "bin", "npm")), "#!/bin/sh\necho 10.0.0\n", 0o755)
	writeFile(filepath.ToSlash(filepath.Join(topDir, "bin", "npx")), "#!/bin/sh\necho 10.0.0\n", 0o755)

	if err := tarWriter.Close(); err != nil {
		t.Fatalf("tarWriter.Close() error = %v", err)
	}
	if err := xzWriter.Close(); err != nil {
		t.Fatalf("xzWriter.Close() error = %v", err)
	}
	return archive.Bytes()
}

func runtimeTestChecksums(t *testing.T, fileName string, archive []byte) []byte {
	t.Helper()
	sum := sha256.Sum256(archive)
	return []byte(fmt.Sprintf("%x  %s\n", sum[:], fileName))
}
