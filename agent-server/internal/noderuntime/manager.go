package noderuntime

import (
	"archive/tar"
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/ulikunitz/xz"

	"github.com/marginlab/margin-eval/agent-server/internal/fsutil"
	"github.com/marginlab/margin-eval/agent-server/internal/trustroots"
)

const (
	officialNodeMirrorURL    = "https://nodejs.org/dist"
	defaultMuslNodeMirrorURL = "https://unofficial-builds.nodejs.org/download/release"
	installMethodAPK         = "apk"
	installMethodArchive     = "archive"
	nvmNodeMirrorEnvKey      = "NVM_NODEJS_ORG_MIRROR"
	libcFamilyGLIBC          = "glibc"
	libcFamilyMusl           = "musl"
	defaultDownloadTimeout   = 5 * time.Minute
)

var (
	managedBinaries       = []string{"node", "npm", "npx"}
	supportedNodeVersions = map[string]string{
		"16": "v16.20.2",
		"18": "v18.20.8",
		"20": "v20.20.1",
		"22": "v22.22.1",
		"24": "v24.14.0",
	}
	muslSupportedVersions = map[string]map[string]struct{}{
		"16": {"x64": {}},
		"18": {"x64": {}},
		"20": {"x64": {}, "arm64": {}},
		"22": {"x64": {}, "arm64": {}},
		"24": {"x64": {}, "arm64": {}},
	}
)

// Config controls managed Node runtime installation and layout.
type Config struct {
	BinDir           string
	StateDir         string
	ExtraCACertsFile string
	NVMDir           string
	NVMVersion       string
}

// Spec defines the managed Node contract for an agent definition.
type Spec struct {
	Minimum   string
	Preferred string
}

// Info describes the currently selected managed Node runtime.
type Info struct {
	BinDir        string
	Environment   map[string]string
	NodePath      string
	NPMPath       string
	NPXPath       string
	Version       string
	InstallMethod string
}

// Manager installs and validates a server-managed Node runtime.
type Manager struct {
	cfg          Config
	trustRoots   *trustroots.Bundle
	runner       commandRunner
	lookPath     func(file string) (string, error)
	now          func() time.Time
	detectMusl   func() bool
	detectAlpine func() bool
	downloadURL  func(ctx context.Context, url string) ([]byte, error)
	arch         string

	mu sync.Mutex
}

type commandRunner interface {
	Run(ctx context.Context, name string, args []string, env []string, dir string) ([]byte, error)
}

type osCommandRunner struct{}

func (osCommandRunner) Run(ctx context.Context, name string, args []string, env []string, dir string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if len(env) > 0 {
		cmd.Env = env
	}
	if strings.TrimSpace(dir) != "" {
		cmd.Dir = dir
	}
	return cmd.CombinedOutput()
}

type runtimeManifest struct {
	InstalledAt     string `json:"installed_at"`
	NodeVersion     string `json:"node_version"`
	ResolvedVersion string `json:"resolved_version,omitempty"`
	InstallMethod   string `json:"install_method"`
	ManagedBinDir   string `json:"managed_bin_dir"`
	ManagedNodePath string `json:"managed_node_path"`
	ManagedNPMPath  string `json:"managed_npm_path"`
	ManagedNPXPath  string `json:"managed_npx_path"`
	LibcFamily      string `json:"libc_family,omitempty"`
	ArchiveURL      string `json:"archive_url,omitempty"`
}

type archiveSpec struct {
	Major        string
	Resolved     string
	Arch         string
	LibcFamily   string
	DownloadURL  string
	ChecksumsURL string
	FileName     string
	InstallDir   string
}

// New creates a validated manager instance.
func New(cfg Config) (*Manager, error) {
	resolved, err := normalizeConfig(cfg)
	if err != nil {
		return nil, err
	}
	trustBundle, err := trustroots.New(trustroots.Config{
		StateDir:         resolved.StateDir,
		ExtraCACertsFile: resolved.ExtraCACertsFile,
	})
	if err != nil {
		return nil, fmt.Errorf("create trust roots bundle: %w", err)
	}
	return &Manager{
		cfg:          resolved,
		trustRoots:   trustBundle,
		runner:       osCommandRunner{},
		lookPath:     exec.LookPath,
		now:          func() time.Time { return time.Now().UTC() },
		detectMusl:   hostUsesMusl,
		detectAlpine: hostIsAlpine,
		downloadURL: func(ctx context.Context, url string) ([]byte, error) {
			return downloadURL(ctx, trustBundle.HTTPClient(defaultDownloadTimeout), url)
		},
		arch: runtime.GOARCH,
	}, nil
}

func normalizeConfig(cfg Config) (Config, error) {
	binDir, err := normalizeRequiredPath(cfg.BinDir, "bin dir")
	if err != nil {
		return Config{}, err
	}
	stateDir, err := normalizeRequiredPath(cfg.StateDir, "state dir")
	if err != nil {
		return Config{}, err
	}
	nvmDir, err := normalizeRequiredPath(cfg.NVMDir, "node runtime dir")
	if err != nil {
		return Config{}, err
	}
	return Config{
		BinDir:           binDir,
		StateDir:         stateDir,
		ExtraCACertsFile: strings.TrimSpace(cfg.ExtraCACertsFile),
		NVMDir:           nvmDir,
		NVMVersion:       strings.TrimSpace(cfg.NVMVersion),
	}, nil
}

func normalizeRequiredPath(value string, field string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", fmt.Errorf("%s is required", field)
	}
	absPath, err := filepath.Abs(trimmed)
	if err != nil {
		return "", fmt.Errorf("resolve %s %q: %w", field, trimmed, err)
	}
	return filepath.Clean(absPath), nil
}

func normalizeSpec(spec Spec) (Spec, error) {
	minimum, err := normalizeMajor(spec.Minimum, "node minimum version")
	if err != nil {
		return Spec{}, err
	}
	preferred, err := normalizeMajor(spec.Preferred, "node preferred version")
	if err != nil {
		return Spec{}, err
	}
	if compareNodeMajors(preferred, minimum) < 0 {
		return Spec{}, fmt.Errorf("node preferred version %q must be greater than or equal to minimum version %q", preferred, minimum)
	}
	return Spec{Minimum: minimum, Preferred: preferred}, nil
}

func normalizeMajor(value string, field string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", fmt.Errorf("%s is required", field)
	}
	if strings.ContainsAny(trimmed, " \t\r\n") {
		return "", fmt.Errorf("%s must not contain whitespace", field)
	}
	for _, ch := range trimmed {
		if ch < '0' || ch > '9' {
			return "", fmt.Errorf("%s must contain digits only", field)
		}
	}
	return trimmed, nil
}

// Ensure installs/repairs managed Node tooling for the configured preferred/minimum contract.
func (m *Manager) Ensure(ctx context.Context, spec Spec) (Info, error) {
	resolvedSpec, err := normalizeSpec(spec)
	if err != nil {
		return Info{}, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if info, err := m.checkLocked(ctx, resolvedSpec); err == nil {
		return info, nil
	}
	if err := fsutil.EnsureDir(m.cfg.StateDir, 0o755); err != nil {
		return Info{}, fmt.Errorf("ensure state dir: %w", err)
	}
	if err := fsutil.EnsureDir(m.cfg.BinDir, 0o755); err != nil {
		return Info{}, fmt.Errorf("ensure bin dir: %w", err)
	}
	if err := fsutil.EnsureDir(m.cfg.NVMDir, 0o755); err != nil {
		return Info{}, fmt.Errorf("ensure node runtime dir: %w", err)
	}
	if err := fsutil.EnsureDir(m.managedHomeDir(), 0o755); err != nil {
		return Info{}, fmt.Errorf("ensure managed home dir: %w", err)
	}
	if err := fsutil.EnsureDir(m.ManagedBinDir(), 0o755); err != nil {
		return Info{}, fmt.Errorf("ensure managed bin dir: %w", err)
	}

	var info Info
	if m.detectAlpine != nil && m.detectAlpine() {
		info, err = m.ensureViaAPK(ctx, resolvedSpec)
	} else {
		info, err = m.ensureViaArchive(ctx, resolvedSpec)
	}
	if err != nil {
		return Info{}, err
	}
	if err := m.writeManifest(info); err != nil {
		return Info{}, err
	}
	return info, nil
}

// Check verifies managed node/npm/npx paths are present and executable for the requested contract.
func (m *Manager) Check(ctx context.Context, spec Spec) (Info, error) {
	resolvedSpec, err := normalizeSpec(spec)
	if err != nil {
		return Info{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.checkLocked(ctx, resolvedSpec)
}

func (m *Manager) checkLocked(ctx context.Context, spec Spec) (Info, error) {
	manifest, err := m.readManifest()
	if err != nil {
		return Info{}, err
	}
	if manifest.InstallMethod != installMethodAPK && manifest.InstallMethod != installMethodArchive {
		return Info{}, fmt.Errorf("managed node runtime manifest has unsupported install_method %q", manifest.InstallMethod)
	}
	info, err := m.validateManagedRuntime(ctx, spec, manifest.InstallMethod)
	if err != nil {
		return Info{}, err
	}
	if manifest.NodeVersion != info.Version {
		return Info{}, fmt.Errorf("managed node runtime manifest version %q does not match current node version %q", manifest.NodeVersion, info.Version)
	}
	info.InstallMethod = manifest.InstallMethod
	return info, nil
}

func (m *Manager) validateManagedRuntime(ctx context.Context, spec Spec, method string) (Info, error) {
	for _, name := range managedBinaries {
		path := filepath.Join(m.ManagedBinDir(), name)
		info, err := os.Stat(path)
		if err != nil {
			if os.IsNotExist(err) {
				return Info{}, fmt.Errorf("managed %s binary is missing at %s", name, path)
			}
			return Info{}, fmt.Errorf("stat managed %s binary: %w", name, err)
		}
		if info.IsDir() {
			return Info{}, fmt.Errorf("managed %s path is a directory: %s", name, path)
		}
	}

	nodeOutput, err := m.runVersionCommand(ctx, m.NodePath(), "--version")
	if err != nil {
		return Info{}, fmt.Errorf("verify managed node: %w", err)
	}
	version, err := parseNodeMajor(nodeOutput)
	if err != nil {
		return Info{}, fmt.Errorf("parse managed node version: %w", err)
	}
	switch method {
	case installMethodAPK:
		if compareNodeMajors(version, spec.Minimum) < 0 {
			return Info{}, fmt.Errorf("managed node version %q is below minimum version %q", version, spec.Minimum)
		}
	case installMethodArchive:
		if version != spec.Preferred && version != spec.Minimum {
			return Info{}, fmt.Errorf("managed node version %q does not match preferred/minimum versions %q/%q", version, spec.Preferred, spec.Minimum)
		}
	default:
		return Info{}, fmt.Errorf("unsupported install method %q", method)
	}
	if _, err := m.runVersionCommand(ctx, m.NPMPath(), "--version"); err != nil {
		return Info{}, fmt.Errorf("verify managed npm: %w", err)
	}
	if _, err := m.runVersionCommand(ctx, m.NPXPath(), "--version"); err != nil {
		return Info{}, fmt.Errorf("verify managed npx: %w", err)
	}
	return Info{
		BinDir:      m.ManagedBinDir(),
		Environment: m.trustRoots.Environment(),
		NodePath:    m.NodePath(),
		NPMPath:     m.NPMPath(),
		NPXPath:     m.NPXPath(),
		Version:     version,
	}, nil
}

func (m *Manager) ensureViaArchive(ctx context.Context, spec Spec) (Info, error) {
	candidates := []string{spec.Preferred}
	if spec.Minimum != spec.Preferred {
		candidates = append(candidates, spec.Minimum)
	}

	attempts := make([]string, 0, len(candidates))
	for _, major := range candidates {
		entry, err := m.resolveArchiveSpec(major)
		if err != nil {
			attempts = append(attempts, fmt.Sprintf("archive %s: %v", major, err))
			continue
		}
		info, err := m.installArchive(ctx, entry)
		if err != nil {
			attempts = append(attempts, fmt.Sprintf("archive %s: %v", major, err))
			continue
		}
		if _, err := m.validateManagedRuntime(ctx, spec, installMethodArchive); err != nil {
			attempts = append(attempts, fmt.Sprintf("archive %s: %v", major, err))
			continue
		}
		return info, nil
	}
	return Info{}, fmt.Errorf("install managed node via archive failed for preferred/minimum versions: %s", strings.Join(attempts, "; "))
}

func (m *Manager) ensureViaAPK(ctx context.Context, spec Spec) (Info, error) {
	apkPath, err := m.lookPath("apk")
	if err != nil {
		return Info{}, fmt.Errorf("apk not found in PATH: %w", err)
	}

	output, err := m.runner.Run(ctx, apkPath, []string{"add", "--no-cache", "nodejs", "npm"}, m.withEnvironment("", nil), m.cfg.StateDir)
	if err != nil {
		return Info{}, fmt.Errorf("install managed node via apk failed: %s", strings.TrimSpace(string(output)))
	}
	sanitizedPath, err := sanitizedAPKPath(os.Getenv("PATH"))
	if err != nil {
		return Info{}, fmt.Errorf("resolve apk toolchain PATH: %w", err)
	}
	nodePath, err := lookPathIn(sanitizedPath, "node")
	if err != nil {
		return Info{}, fmt.Errorf("resolve node in apk toolchain PATH %q: %w", sanitizedPath, err)
	}
	npmPath, err := lookPathIn(sanitizedPath, "npm")
	if err != nil {
		return Info{}, fmt.Errorf("resolve npm in apk toolchain PATH %q: %w", sanitizedPath, err)
	}
	npxPath, err := lookPathIn(sanitizedPath, "npx")
	if err != nil {
		return Info{}, fmt.Errorf("resolve npx in apk toolchain PATH %q: %w", sanitizedPath, err)
	}
	if err := m.linkManagedBinaries(filepath.Dir(nodePath)); err != nil {
		return Info{}, err
	}
	info, err := m.validateManagedRuntime(ctx, spec, installMethodAPK)
	if err != nil {
		return Info{}, err
	}
	info.NodePath = nodePath
	info.NPMPath = npmPath
	info.NPXPath = npxPath
	info.InstallMethod = installMethodAPK
	return info, nil
}

func (m *Manager) resolveArchiveSpec(major string) (archiveSpec, error) {
	version, ok := supportedNodeVersions[major]
	if !ok {
		return archiveSpec{}, fmt.Errorf("node major %q is not in the supported set", major)
	}
	arch, err := m.nodeArchiveArch()
	if err != nil {
		return archiveSpec{}, err
	}
	libcFamily := libcFamilyGLIBC
	if m.detectMusl != nil && m.detectMusl() {
		libcFamily = libcFamilyMusl
	}

	baseURL := m.nodeMirrorBaseURL(libcFamily)
	fileName := ""
	switch libcFamily {
	case libcFamilyGLIBC:
		fileName = fmt.Sprintf("node-%s-linux-%s.tar.xz", version, arch)
	case libcFamilyMusl:
		supportedArch, ok := muslSupportedVersions[major]
		if !ok {
			return archiveSpec{}, fmt.Errorf("node major %q is not supported for musl", major)
		}
		if _, ok := supportedArch[arch]; !ok {
			return archiveSpec{}, fmt.Errorf("node major %q is not supported for musl on %s", major, arch)
		}
		fileName = fmt.Sprintf("node-%s-linux-%s-musl.tar.xz", version, arch)
	default:
		return archiveSpec{}, fmt.Errorf("unsupported libc family %q", libcFamily)
	}

	return archiveSpec{
		Major:        major,
		Resolved:     version,
		Arch:         arch,
		LibcFamily:   libcFamily,
		FileName:     fileName,
		DownloadURL:  baseURL + "/" + version + "/" + fileName,
		ChecksumsURL: baseURL + "/" + version + "/SHASUMS256.txt",
		InstallDir:   filepath.Join(m.cfg.NVMDir, "versions", "node", version, libcFamily+"-"+arch),
	}, nil
}

func (m *Manager) nodeArchiveArch() (string, error) {
	switch m.arch {
	case "amd64":
		return "x64", nil
	case "arm64":
		return "arm64", nil
	default:
		return "", fmt.Errorf("unsupported node runtime architecture %q", m.arch)
	}
}

func (m *Manager) nodeMirrorBaseURL(libcFamily string) string {
	if override := strings.TrimSpace(os.Getenv(nvmNodeMirrorEnvKey)); override != "" {
		return strings.TrimRight(override, "/")
	}
	if libcFamily == libcFamilyMusl {
		return defaultMuslNodeMirrorURL
	}
	return officialNodeMirrorURL
}

func (m *Manager) installArchive(ctx context.Context, spec archiveSpec) (Info, error) {
	archiveBody, err := m.downloadURL(ctx, spec.DownloadURL)
	if err != nil {
		return Info{}, fmt.Errorf("download node archive %s: %w", spec.DownloadURL, err)
	}
	if err := m.verifyArchiveChecksum(ctx, spec, archiveBody); err != nil {
		return Info{}, err
	}
	installDir, err := m.extractArchive(spec, archiveBody)
	if err != nil {
		return Info{}, err
	}
	if err := m.linkManagedBinaries(filepath.Join(installDir, "bin")); err != nil {
		return Info{}, err
	}
	return Info{
		BinDir:        m.ManagedBinDir(),
		Environment:   m.trustRoots.Environment(),
		NodePath:      m.NodePath(),
		NPMPath:       m.NPMPath(),
		NPXPath:       m.NPXPath(),
		Version:       spec.Major,
		InstallMethod: installMethodArchive,
	}, nil
}

func (m *Manager) verifyArchiveChecksum(ctx context.Context, spec archiveSpec, archiveBody []byte) error {
	checksumsBody, err := m.downloadURL(ctx, spec.ChecksumsURL)
	if err != nil {
		return fmt.Errorf("download node checksums %s: %w", spec.ChecksumsURL, err)
	}
	want, err := checksumForFile(checksumsBody, spec.FileName)
	if err != nil {
		return err
	}
	gotBytes := sha256.Sum256(archiveBody)
	got := hex.EncodeToString(gotBytes[:])
	if !strings.EqualFold(got, want) {
		return fmt.Errorf("node archive checksum mismatch for %s", spec.FileName)
	}
	return nil
}

func checksumForFile(body []byte, fileName string) (string, error) {
	scanner := bufio.NewScanner(bytes.NewReader(body))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		name := strings.TrimPrefix(fields[1], "*")
		if name == fileName {
			return strings.ToLower(fields[0]), nil
		}
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("scan checksums: %w", err)
	}
	return "", fmt.Errorf("checksum for %s not found", fileName)
}

func (m *Manager) extractArchive(spec archiveSpec, archiveBody []byte) (string, error) {
	parentDir := filepath.Dir(spec.InstallDir)
	if err := fsutil.EnsureDir(parentDir, 0o755); err != nil {
		return "", fmt.Errorf("ensure node versions dir: %w", err)
	}
	tempRoot, err := os.MkdirTemp(parentDir, "node-install-*")
	if err != nil {
		return "", fmt.Errorf("create node install temp dir: %w", err)
	}
	defer os.RemoveAll(tempRoot)

	if err := untarXZ(tempRoot, archiveBody); err != nil {
		return "", err
	}
	entries, err := os.ReadDir(tempRoot)
	if err != nil {
		return "", fmt.Errorf("read extracted node archive: %w", err)
	}
	if len(entries) != 1 || !entries[0].IsDir() {
		return "", fmt.Errorf("unexpected node archive layout for %s", spec.FileName)
	}
	extractedRoot := filepath.Join(tempRoot, entries[0].Name())
	if err := os.RemoveAll(spec.InstallDir); err != nil {
		return "", fmt.Errorf("remove previous node install dir %s: %w", spec.InstallDir, err)
	}
	if err := os.Rename(extractedRoot, spec.InstallDir); err != nil {
		return "", fmt.Errorf("publish node install dir %s: %w", spec.InstallDir, err)
	}
	return spec.InstallDir, nil
}

func untarXZ(dest string, archiveBody []byte) error {
	reader, err := xz.NewReader(bytes.NewReader(archiveBody))
	if err != nil {
		return fmt.Errorf("open node archive xz stream: %w", err)
	}
	tarReader := tar.NewReader(reader)
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read node archive entry: %w", err)
		}
		targetPath, err := fsutil.ValidatePathUnderRoot(filepath.Join(dest, header.Name), dest)
		if err != nil {
			return fmt.Errorf("validate node archive path %q: %w", header.Name, err)
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(targetPath, header.FileInfo().Mode().Perm()); err != nil {
				return fmt.Errorf("create node archive dir %s: %w", targetPath, err)
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
				return fmt.Errorf("create parent dir for %s: %w", targetPath, err)
			}
			file, err := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, header.FileInfo().Mode().Perm())
			if err != nil {
				return fmt.Errorf("create node archive file %s: %w", targetPath, err)
			}
			if _, err := io.Copy(file, tarReader); err != nil {
				_ = file.Close()
				return fmt.Errorf("write node archive file %s: %w", targetPath, err)
			}
			if err := file.Close(); err != nil {
				return fmt.Errorf("close node archive file %s: %w", targetPath, err)
			}
		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
				return fmt.Errorf("create symlink parent dir %s: %w", targetPath, err)
			}
			if err := os.Symlink(header.Linkname, targetPath); err != nil {
				return fmt.Errorf("create symlink %s -> %s: %w", targetPath, header.Linkname, err)
			}
		default:
			return fmt.Errorf("unsupported node archive entry type %d for %s", header.Typeflag, header.Name)
		}
	}
}

func (m *Manager) linkManagedBinaries(nodeBinDir string) error {
	for _, name := range managedBinaries {
		target := filepath.Join(nodeBinDir, name)
		if _, err := os.Stat(target); err != nil {
			return fmt.Errorf("expected %s binary in installed node runtime: %w", name, err)
		}
		linkPath := filepath.Join(m.ManagedBinDir(), name)
		if err := os.Remove(linkPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove existing managed %s link %s: %w", name, linkPath, err)
		}
		if err := os.Symlink(target, linkPath); err != nil {
			return fmt.Errorf("create managed %s link %s -> %s: %w", name, linkPath, target, err)
		}
	}
	return nil
}

func (m *Manager) runVersionCommand(ctx context.Context, binaryPath string, arg string) (string, error) {
	output, err := m.runner.Run(ctx, binaryPath, []string{arg}, m.withEnvironment("", nil), m.cfg.StateDir)
	if err != nil {
		return "", fmt.Errorf("%s %s failed: %s", binaryPath, arg, strings.TrimSpace(string(output)))
	}
	trimmed := strings.TrimSpace(string(output))
	if trimmed == "" {
		return "", fmt.Errorf("%s %s produced empty output", binaryPath, arg)
	}
	return trimmed, nil
}

func (m *Manager) withEnvironment(_ string, overrides map[string]string) []string {
	env := append([]string(nil), os.Environ()...)
	env = setEnvironmentValue(env, "NVM_DIR", m.cfg.NVMDir)
	env = setEnvironmentValue(env, "HOME", m.managedHomeDir())
	env = prependPathValue(env, m.ManagedBinDir())
	for key, value := range m.trustRoots.Environment() {
		env = setEnvironmentValue(env, key, value)
	}
	for key, value := range overrides {
		env = setEnvironmentValue(env, key, value)
	}
	return env
}

func prependPathValue(env []string, pathEntry string) []string {
	trimmedEntry := strings.TrimSpace(pathEntry)
	if trimmedEntry == "" {
		return env
	}

	var currentPath string
	var hasPath bool
	for _, entry := range env {
		if !strings.HasPrefix(entry, "PATH=") {
			continue
		}
		currentPath = strings.TrimPrefix(entry, "PATH=")
		hasPath = true
		break
	}

	parts := []string{trimmedEntry}
	if hasPath {
		for _, part := range strings.Split(currentPath, string(os.PathListSeparator)) {
			if part == "" || part == trimmedEntry {
				continue
			}
			parts = append(parts, part)
		}
	}

	return setEnvironmentValue(env, "PATH", strings.Join(parts, string(os.PathListSeparator)))
}

func setEnvironmentValue(env []string, key, value string) []string {
	prefix := key + "="
	for idx, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			env[idx] = prefix + value
			return env
		}
	}
	return append(env, prefix+value)
}

func hostIsAlpine() bool {
	if runtime.GOOS != "linux" {
		return false
	}
	if fileExists("/etc/alpine-release") {
		return true
	}
	body, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return false
	}
	return strings.Contains(string(body), "ID=alpine") || strings.Contains(string(body), "ID_LIKE=alpine")
}

func hostUsesMusl() bool {
	if runtime.GOOS != "linux" {
		return false
	}
	if fileExists("/etc/alpine-release") {
		return true
	}
	for _, pattern := range []string{
		"/lib/ld-musl-*.so.1",
		"/usr/lib/ld-musl-*.so.1",
		"/lib/libc.musl-*.so.1",
		"/usr/lib/libc.musl-*.so.1",
	} {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			continue
		}
		if len(matches) > 0 {
			return true
		}
	}
	return false
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func parseNodeMajor(version string) (string, error) {
	trimmed := strings.TrimSpace(version)
	trimmed = strings.TrimPrefix(trimmed, "v")
	if trimmed == "" {
		return "", fmt.Errorf("empty node version")
	}
	major := trimmed
	if idx := strings.IndexByte(trimmed, '.'); idx >= 0 {
		major = trimmed[:idx]
	}
	if major == "" {
		return "", fmt.Errorf("empty node major")
	}
	for _, ch := range major {
		if ch < '0' || ch > '9' {
			return "", fmt.Errorf("invalid node version %q", version)
		}
	}
	return major, nil
}

func sanitizedAPKPath(pathValue string) (string, error) {
	parts := strings.Split(pathValue, string(os.PathListSeparator))
	filtered := make([]string, 0, len(parts))
	seen := map[string]struct{}{}
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			continue
		}
		cleaned := filepath.Clean(trimmed)
		if cleaned == "/usr/local/bin" || cleaned == "/usr/local/sbin" {
			continue
		}
		if _, ok := seen[cleaned]; ok {
			continue
		}
		seen[cleaned] = struct{}{}
		filtered = append(filtered, cleaned)
	}
	if len(filtered) == 0 {
		return "", fmt.Errorf("sanitized PATH is empty")
	}
	return strings.Join(filtered, string(os.PathListSeparator)), nil
}

func lookPathIn(pathValue, file string) (string, error) {
	if strings.TrimSpace(file) == "" {
		return "", fmt.Errorf("binary name is required")
	}
	for _, part := range strings.Split(pathValue, string(os.PathListSeparator)) {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			continue
		}
		candidate := filepath.Join(trimmed, file)
		info, err := os.Stat(candidate)
		if err != nil {
			continue
		}
		if info.IsDir() {
			continue
		}
		if info.Mode()&0o111 == 0 {
			continue
		}
		return candidate, nil
	}
	return "", fmt.Errorf("binary %q not found", file)
}

func compareNodeMajors(left, right string) int {
	left = strings.TrimLeft(left, "0")
	right = strings.TrimLeft(right, "0")
	if left == "" {
		left = "0"
	}
	if right == "" {
		right = "0"
	}
	switch {
	case len(left) < len(right):
		return -1
	case len(left) > len(right):
		return 1
	case left < right:
		return -1
	case left > right:
		return 1
	default:
		return 0
	}
}

func (m *Manager) writeManifest(info Info) error {
	manifestPath := filepath.Join(m.cfg.StateDir, "toolchain", "node", "runtime.json")
	manifest := runtimeManifest{
		InstalledAt:     m.now().Format(time.RFC3339),
		NodeVersion:     info.Version,
		InstallMethod:   info.InstallMethod,
		ManagedBinDir:   m.ManagedBinDir(),
		ManagedNodePath: m.NodePath(),
		ManagedNPMPath:  m.NPMPath(),
		ManagedNPXPath:  m.NPXPath(),
	}
	if info.InstallMethod == installMethodArchive {
		if entry, err := m.resolveArchiveSpec(info.Version); err == nil {
			manifest.ResolvedVersion = entry.Resolved
			manifest.LibcFamily = entry.LibcFamily
			manifest.ArchiveURL = entry.DownloadURL
		}
	}
	payload, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal runtime manifest: %w", err)
	}
	payload = append(payload, '\n')
	if err := fsutil.WriteFileAtomic(manifestPath, payload, 0o644); err != nil {
		return fmt.Errorf("write runtime manifest: %w", err)
	}
	return nil
}

func (m *Manager) readManifest() (runtimeManifest, error) {
	manifestPath := filepath.Join(m.cfg.StateDir, "toolchain", "node", "runtime.json")
	body, err := os.ReadFile(manifestPath)
	if err != nil {
		return runtimeManifest{}, fmt.Errorf("read runtime manifest: %w", err)
	}
	var manifest runtimeManifest
	if err := json.Unmarshal(body, &manifest); err != nil {
		return runtimeManifest{}, fmt.Errorf("decode runtime manifest: %w", err)
	}
	return manifest, nil
}

func (m *Manager) managedHomeDir() string {
	return filepath.Join(m.cfg.StateDir, "toolchain", "home")
}

// ManagedBinDir is the stable symlink directory for managed node binaries.
func (m *Manager) ManagedBinDir() string {
	return filepath.Join(m.cfg.BinDir, "toolchain", "node", "current", "bin")
}

// NodePath returns the stable managed node binary path.
func (m *Manager) NodePath() string {
	return filepath.Join(m.ManagedBinDir(), "node")
}

// NPMPath returns the stable managed npm binary path.
func (m *Manager) NPMPath() string {
	return filepath.Join(m.ManagedBinDir(), "npm")
}

// NPXPath returns the stable managed npx binary path.
func (m *Manager) NPXPath() string {
	return filepath.Join(m.ManagedBinDir(), "npx")
}

// ExportPATH prepends managed binaries to process PATH.
func (m *Manager) ExportPATH() error {
	managedBin := m.ManagedBinDir()
	for key, value := range m.trustRoots.Environment() {
		if err := os.Setenv(key, value); err != nil {
			return fmt.Errorf("set %s: %w", key, err)
		}
	}
	currentPath := os.Getenv("PATH")
	if currentPath == "" {
		if err := os.Setenv("PATH", managedBin); err != nil {
			return fmt.Errorf("set PATH: %w", err)
		}
		if err := os.Setenv("NVM_DIR", m.cfg.NVMDir); err != nil {
			return fmt.Errorf("set NVM_DIR: %w", err)
		}
		return nil
	}
	parts := strings.Split(currentPath, string(os.PathListSeparator))
	if len(parts) > 0 && parts[0] == managedBin {
		if err := os.Setenv("NVM_DIR", m.cfg.NVMDir); err != nil {
			return fmt.Errorf("set NVM_DIR: %w", err)
		}
		return nil
	}
	updatedPath := managedBin + string(os.PathListSeparator) + currentPath
	if err := os.Setenv("PATH", updatedPath); err != nil {
		return fmt.Errorf("set PATH: %w", err)
	}
	if err := os.Setenv("NVM_DIR", m.cfg.NVMDir); err != nil {
		return fmt.Errorf("set NVM_DIR: %w", err)
	}
	return nil
}

func downloadURL(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %s", resp.Status)
	}
	return io.ReadAll(resp.Body)
}
