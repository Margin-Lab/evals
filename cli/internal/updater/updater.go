package updater

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultRepo            = "Margin-Lab/evals"
	DefaultAPIBaseURL      = "https://api.github.com"
	DefaultDownloadBaseURL = "https://github.com"
	OfficialInstallerURL   = "https://raw.githubusercontent.com/Margin-Lab/evals/main/scripts/install.sh"

	metadataSchemaVersion = 1
	installedViaOfficial  = "official-installer"
	channelStable         = "stable"
	channelBeta           = "beta"
)

var managedStarterSubtrees = []string{
	filepath.Join("configs", "agent-definitions"),
	filepath.Join("configs", "example-agent-configs"),
	filepath.Join("configs", "example-eval-configs"),
	filepath.Join("suites", "swe-minimal-test-suite"),
}

var betaTagPattern = regexp.MustCompile(`^v([0-9]+)\.([0-9]+)\.([0-9]+)-beta\.([0-9]+)$`)

var (
	currentExecutablePath = os.Executable
	runtimeGOOS           = runtime.GOOS
	runtimeGOARCH         = runtime.GOARCH
)

type Config struct {
	Repo            string
	APIBaseURL      string
	DownloadBaseURL string
	HTTPClient      *http.Client
	MetadataPath    string
}

type Result struct {
	CurrentVersion string
	LatestVersion  string
	Updated        bool
}

type Manager struct {
	repo            string
	apiBaseURL      string
	downloadBaseURL string
	httpClient      *http.Client
	metadataPath    string
}

type InstallMetadata struct {
	SchemaVersion    int    `json:"schema_version"`
	InstalledVia     string `json:"installed_via"`
	Repo             string `json:"repo"`
	Channel          string `json:"channel"`
	BinaryPath       string `json:"binary_path"`
	InstalledVersion string `json:"installed_version"`
}

type releaseInfo struct {
	TagName    string `json:"tag_name"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
}

type parsedBetaTag struct {
	major int
	minor int
	patch int
	beta  int
}

func New(cfg Config) (*Manager, error) {
	repo := strings.TrimSpace(cfg.Repo)
	if repo == "" {
		repo = DefaultRepo
	}
	apiBaseURL := strings.TrimRight(strings.TrimSpace(cfg.APIBaseURL), "/")
	if apiBaseURL == "" {
		apiBaseURL = DefaultAPIBaseURL
	}
	downloadBaseURL := strings.TrimRight(strings.TrimSpace(cfg.DownloadBaseURL), "/")
	if downloadBaseURL == "" {
		downloadBaseURL = DefaultDownloadBaseURL
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	metadataPath := strings.TrimSpace(cfg.MetadataPath)
	if metadataPath == "" {
		var err error
		metadataPath, err = DefaultMetadataPath()
		if err != nil {
			return nil, err
		}
	}
	return &Manager{
		repo:            repo,
		apiBaseURL:      apiBaseURL,
		downloadBaseURL: downloadBaseURL,
		httpClient:      client,
		metadataPath:    metadataPath,
	}, nil
}

func DefaultMetadataPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve user home: %w", err)
	}
	return filepath.Join(home, ".margin", "install.json"), nil
}

func (m *Manager) Update(ctx context.Context, currentVersion string) (Result, error) {
	current := strings.TrimSpace(currentVersion)
	if current == "" || current == "dev" {
		return Result{}, fmt.Errorf("margin update is unavailable for source builds; reinstall via %s to use this command", OfficialInstallerURL)
	}
	executablePath, err := currentExecutablePath()
	if err != nil {
		return Result{}, fmt.Errorf("resolve current executable: %w", err)
	}
	executablePath = filepath.Clean(executablePath)

	metadata, err := readMetadata(m.metadataPath)
	if err != nil {
		return Result{}, err
	}
	if err := validateMetadata(metadata, m.repo, executablePath); err != nil {
		return Result{}, err
	}

	targetVersion, err := m.resolveTargetVersion(ctx, metadata.Channel)
	if err != nil {
		return Result{}, err
	}
	result := Result{
		CurrentVersion: current,
		LatestVersion:  targetVersion,
	}
	if targetVersion == current {
		return result, nil
	}

	archiveName, checksumName, err := platformAssetNames(targetVersion)
	if err != nil {
		return Result{}, err
	}
	tempDir, err := os.MkdirTemp("", "margin-update-*")
	if err != nil {
		return Result{}, fmt.Errorf("create update temp dir: %w", err)
	}
	defer os.RemoveAll(tempDir)

	archivePath := filepath.Join(tempDir, archiveName)
	checksumPath := filepath.Join(tempDir, checksumName)
	if err := m.downloadReleaseFile(ctx, targetVersion, archiveName, archivePath); err != nil {
		return Result{}, err
	}
	if err := m.downloadReleaseFile(ctx, targetVersion, checksumName, checksumPath); err != nil {
		return Result{}, err
	}
	if err := verifyChecksumFile(archivePath, checksumPath); err != nil {
		return Result{}, err
	}

	extractDir := filepath.Join(tempDir, "release")
	if err := extractReleaseArchive(archivePath, extractDir); err != nil {
		return Result{}, err
	}
	if err := validateReleaseArchiveLayout(extractDir); err != nil {
		return Result{}, err
	}
	marginHome, err := marginHomeDir()
	if err != nil {
		return Result{}, err
	}
	if err := installStarterAssets(extractDir, marginHome); err != nil {
		return Result{}, err
	}

	replacementPath := filepath.Join(filepath.Dir(executablePath), ".margin-update-"+strconv.FormatInt(time.Now().UnixNano(), 10))
	if err := copyFile(filepath.Join(extractDir, "margin"), replacementPath, 0o755); err != nil {
		return Result{}, err
	}
	if err := os.Rename(replacementPath, executablePath); err != nil {
		_ = os.Remove(replacementPath)
		return Result{}, fmt.Errorf("replace %s: %w", executablePath, err)
	}

	metadata.InstalledVersion = targetVersion
	_ = writeMetadata(m.metadataPath, metadata)

	result.Updated = true
	return result, nil
}

func readMetadata(path string) (InstallMetadata, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return InstallMetadata{}, fmt.Errorf("margin update only supports installs created by the official installer; reinstall via %s", OfficialInstallerURL)
		}
		return InstallMetadata{}, fmt.Errorf("read install metadata: %w", err)
	}
	var metadata InstallMetadata
	if err := json.Unmarshal(body, &metadata); err != nil {
		return InstallMetadata{}, fmt.Errorf("decode install metadata: %w", err)
	}
	return metadata, nil
}

func validateMetadata(metadata InstallMetadata, repo, executablePath string) error {
	if metadata.SchemaVersion != metadataSchemaVersion {
		return fmt.Errorf("margin update only supports installs created by the official installer; reinstall via %s", OfficialInstallerURL)
	}
	if strings.TrimSpace(metadata.InstalledVia) != installedViaOfficial {
		return fmt.Errorf("margin update only supports installs created by the official installer; reinstall via %s", OfficialInstallerURL)
	}
	if strings.TrimSpace(metadata.Repo) != repo {
		return fmt.Errorf("margin update only supports installs created by the official installer; reinstall via %s", OfficialInstallerURL)
	}
	switch strings.TrimSpace(metadata.Channel) {
	case channelStable, channelBeta:
	default:
		return fmt.Errorf("invalid install metadata channel %q", metadata.Channel)
	}
	if filepath.Clean(strings.TrimSpace(metadata.BinaryPath)) != executablePath {
		return fmt.Errorf("margin update only supports the binary installed by the official installer at %s", metadata.BinaryPath)
	}
	return nil
}

func writeMetadata(path string, metadata InstallMetadata) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("ensure install metadata dir: %w", err)
	}
	body, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return fmt.Errorf("encode install metadata: %w", err)
	}
	tempPath := path + ".tmp"
	if err := os.WriteFile(tempPath, append(body, '\n'), 0o644); err != nil {
		return fmt.Errorf("write install metadata: %w", err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		_ = os.Remove(tempPath)
		return fmt.Errorf("persist install metadata: %w", err)
	}
	return nil
}

func (m *Manager) resolveTargetVersion(ctx context.Context, channel string) (string, error) {
	switch channel {
	case channelStable:
		release, err := m.fetchLatestStableRelease(ctx)
		if err != nil {
			return "", err
		}
		return release.TagName, nil
	case channelBeta:
		return m.fetchLatestBetaRelease(ctx)
	default:
		return "", fmt.Errorf("unsupported update channel %q", channel)
	}
}

func (m *Manager) fetchLatestStableRelease(ctx context.Context) (releaseInfo, error) {
	url := fmt.Sprintf("%s/repos/%s/releases/latest", m.apiBaseURL, m.repo)
	var release releaseInfo
	if err := m.getJSON(ctx, url, &release); err != nil {
		return releaseInfo{}, err
	}
	tag := strings.TrimSpace(release.TagName)
	if !isStableTag(tag) {
		return releaseInfo{}, fmt.Errorf("latest stable release tag %q is invalid", tag)
	}
	return release, nil
}

func (m *Manager) fetchLatestBetaRelease(ctx context.Context) (string, error) {
	const perPage = 100
	var best parsedBetaTag
	var bestTag string
	for page := 1; ; page++ {
		url := fmt.Sprintf("%s/repos/%s/releases?per_page=%d&page=%d", m.apiBaseURL, m.repo, perPage, page)
		var releases []releaseInfo
		if err := m.getJSON(ctx, url, &releases); err != nil {
			return "", err
		}
		for _, release := range releases {
			if release.Draft {
				continue
			}
			tag := strings.TrimSpace(release.TagName)
			parsed, ok := parseBetaTag(tag)
			if !ok {
				continue
			}
			if bestTag == "" || compareBetaTags(parsed, best) > 0 {
				best = parsed
				bestTag = tag
			}
		}
		if len(releases) < perPage {
			break
		}
	}
	if bestTag == "" {
		return "", fmt.Errorf("no beta release found in %s", m.repo)
	}
	return bestTag, nil
}

func (m *Manager) getJSON(ctx context.Context, url string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := m.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("request %s failed: %s%s", url, resp.Status, formatBodyMessage(body))
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode %s: %w", url, err)
	}
	return nil
}

func formatBodyMessage(body []byte) string {
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return ""
	}
	return ": " + trimmed
}

func platformAssetNames(tag string) (string, string, error) {
	goos := runtimeGOOS
	goarch := runtimeGOARCH
	switch goos {
	case "darwin", "linux":
	default:
		return "", "", fmt.Errorf("unsupported platform %s/%s", goos, goarch)
	}
	switch goarch {
	case "amd64", "arm64":
	default:
		return "", "", fmt.Errorf("unsupported platform %s/%s", goos, goarch)
	}
	return fmt.Sprintf("margin_%s_%s_%s.tar.gz", tag, goos, goarch),
		fmt.Sprintf("margin_%s_SHA256SUMS.txt", tag),
		nil
}

func (m *Manager) downloadReleaseFile(ctx context.Context, tag, name, destPath string) error {
	url := fmt.Sprintf("%s/%s/releases/download/%s/%s", m.downloadBaseURL, m.repo, tag, name)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("build download request: %w", err)
	}
	resp, err := m.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("download %s: %w", name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("download %s failed: %s%s", name, resp.Status, formatBodyMessage(body))
	}
	file, err := os.OpenFile(destPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("create %s: %w", destPath, err)
	}
	defer file.Close()
	if _, err := io.Copy(file, resp.Body); err != nil {
		return fmt.Errorf("write %s: %w", destPath, err)
	}
	return nil
}

func verifyChecksumFile(archivePath, checksumPath string) error {
	expected, err := checksumForAsset(filepath.Base(archivePath), checksumPath)
	if err != nil {
		return err
	}
	got, err := sha256File(archivePath)
	if err != nil {
		return err
	}
	if !strings.EqualFold(expected, got) {
		return fmt.Errorf("checksum mismatch for %s", filepath.Base(archivePath))
	}
	return nil
}

func checksumForAsset(assetName, checksumPath string) (string, error) {
	body, err := os.ReadFile(checksumPath)
	if err != nil {
		return "", fmt.Errorf("read checksum file: %w", err)
	}
	for _, line := range strings.Split(string(body), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 2 {
			continue
		}
		name := strings.TrimPrefix(fields[len(fields)-1], "./")
		if name == assetName {
			return fields[0], nil
		}
	}
	return "", fmt.Errorf("missing checksum for %s", assetName)
}

func sha256File(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open %s: %w", path, err)
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", fmt.Errorf("hash %s: %w", path, err)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func marginHomeDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve user home: %w", err)
	}
	return filepath.Join(home, ".margin"), nil
}

func extractReleaseArchive(archivePath, destDir string) error {
	file, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("open archive: %w", err)
	}
	defer file.Close()
	gzReader, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("open gzip stream: %w", err)
	}
	defer gzReader.Close()

	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return fmt.Errorf("create release extract dir: %w", err)
	}
	tarReader := tar.NewReader(gzReader)
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read tar archive: %w", err)
		}
		targetPath, err := archiveTargetPath(destDir, header.Name)
		if err != nil {
			return err
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(targetPath, header.FileInfo().Mode().Perm()); err != nil {
				return fmt.Errorf("create extracted directory %s: %w", targetPath, err)
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
				return fmt.Errorf("create extracted parent %s: %w", filepath.Dir(targetPath), err)
			}
			out, err := os.OpenFile(targetPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, header.FileInfo().Mode().Perm())
			if err != nil {
				return fmt.Errorf("create extracted file %s: %w", targetPath, err)
			}
			if _, err := io.Copy(out, tarReader); err != nil {
				out.Close()
				_ = os.Remove(targetPath)
				return fmt.Errorf("extract %s: %w", targetPath, err)
			}
			if err := out.Close(); err != nil {
				_ = os.Remove(targetPath)
				return fmt.Errorf("close extracted file %s: %w", targetPath, err)
			}
		default:
			return fmt.Errorf("unsupported tar entry %q with type %d", header.Name, header.Typeflag)
		}
	}
}

func archiveTargetPath(destDir, name string) (string, error) {
	clean := filepath.Clean(name)
	if clean == "." {
		return "", fmt.Errorf("archive contains invalid empty path")
	}
	if filepath.IsAbs(clean) {
		return "", fmt.Errorf("archive contains absolute path %q", name)
	}
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("archive path %q escapes destination", name)
	}
	targetPath := filepath.Join(destDir, clean)
	return targetPath, nil
}

func validateReleaseArchiveLayout(extractDir string) error {
	requiredDirs := append([]string(nil), managedStarterSubtrees...)
	requiredFiles := []string{"margin"}
	for _, rel := range requiredFiles {
		info, err := os.Stat(filepath.Join(extractDir, rel))
		if err != nil {
			return fmt.Errorf("release archive is missing required file %s: %w", rel, err)
		}
		if info.IsDir() {
			return fmt.Errorf("release archive path %s must be a file", rel)
		}
	}
	for _, rel := range requiredDirs {
		info, err := os.Stat(filepath.Join(extractDir, rel))
		if err != nil {
			return fmt.Errorf("release archive is missing required directory %s: %w", rel, err)
		}
		if !info.IsDir() {
			return fmt.Errorf("release archive path %s must be a directory", rel)
		}
	}
	return nil
}

func installStarterAssets(extractDir, marginHome string) error {
	for _, rel := range managedStarterSubtrees {
		sourcePath := filepath.Join(extractDir, rel)
		targetPath := filepath.Join(marginHome, rel)
		if err := replaceManagedTree(sourcePath, targetPath); err != nil {
			return fmt.Errorf("install starter asset %s: %w", rel, err)
		}
	}
	return nil
}

func replaceManagedTree(sourcePath, targetPath string) error {
	info, err := os.Stat(sourcePath)
	if err != nil {
		return fmt.Errorf("stat source %s: %w", sourcePath, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("source %s must be a directory", sourcePath)
	}

	parentDir := filepath.Dir(targetPath)
	if err := os.MkdirAll(parentDir, 0o755); err != nil {
		return fmt.Errorf("create parent %s: %w", parentDir, err)
	}

	stagePath := filepath.Join(parentDir, "."+filepath.Base(targetPath)+".tmp-"+strconv.FormatInt(time.Now().UnixNano(), 10))
	if err := copyTree(sourcePath, stagePath); err != nil {
		_ = os.RemoveAll(stagePath)
		return err
	}
	if err := os.RemoveAll(targetPath); err != nil {
		_ = os.RemoveAll(stagePath)
		return fmt.Errorf("remove existing %s: %w", targetPath, err)
	}
	if err := os.Rename(stagePath, targetPath); err != nil {
		_ = os.RemoveAll(stagePath)
		return fmt.Errorf("replace %s: %w", targetPath, err)
	}
	return nil
}

func copyTree(sourcePath, targetPath string) error {
	return filepath.WalkDir(sourcePath, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(sourcePath, path)
		if err != nil {
			return fmt.Errorf("resolve relative path for %s: %w", path, err)
		}
		dest := targetPath
		if rel != "." {
			dest = filepath.Join(targetPath, rel)
		}
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("stat %s: %w", path, err)
		}
		if entry.IsDir() {
			if err := os.MkdirAll(dest, info.Mode().Perm()); err != nil {
				return fmt.Errorf("create directory %s: %w", dest, err)
			}
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("unsupported file type at %s", path)
		}
		return copyFile(path, dest, info.Mode().Perm())
	})
}

func copyFile(sourcePath, targetPath string, mode fs.FileMode) error {
	source, err := os.Open(sourcePath)
	if err != nil {
		return fmt.Errorf("open %s: %w", sourcePath, err)
	}
	defer source.Close()

	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return fmt.Errorf("create parent %s: %w", filepath.Dir(targetPath), err)
	}
	target, err := os.OpenFile(targetPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return fmt.Errorf("create %s: %w", targetPath, err)
	}
	if _, err := io.Copy(target, source); err != nil {
		target.Close()
		_ = os.Remove(targetPath)
		return fmt.Errorf("copy %s to %s: %w", sourcePath, targetPath, err)
	}
	if err := target.Close(); err != nil {
		_ = os.Remove(targetPath)
		return fmt.Errorf("close %s: %w", targetPath, err)
	}
	if err := os.Chmod(targetPath, mode); err != nil {
		_ = os.Remove(targetPath)
		return fmt.Errorf("chmod %s: %w", targetPath, err)
	}
	return nil
}

func isStableTag(tag string) bool {
	if strings.TrimSpace(tag) == "" {
		return false
	}
	if strings.Contains(tag, "-") {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(tag, "v"), ".")
	if len(parts) != 3 {
		return false
	}
	for _, part := range parts {
		if _, err := strconv.Atoi(part); err != nil {
			return false
		}
	}
	return strings.HasPrefix(tag, "v")
}

func parseBetaTag(tag string) (parsedBetaTag, bool) {
	matches := betaTagPattern.FindStringSubmatch(strings.TrimSpace(tag))
	if matches == nil {
		return parsedBetaTag{}, false
	}
	major, _ := strconv.Atoi(matches[1])
	minor, _ := strconv.Atoi(matches[2])
	patch, _ := strconv.Atoi(matches[3])
	beta, _ := strconv.Atoi(matches[4])
	return parsedBetaTag{
		major: major,
		minor: minor,
		patch: patch,
		beta:  beta,
	}, true
}

func compareBetaTags(left, right parsedBetaTag) int {
	switch {
	case left.major != right.major:
		return cmpInt(left.major, right.major)
	case left.minor != right.minor:
		return cmpInt(left.minor, right.minor)
	case left.patch != right.patch:
		return cmpInt(left.patch, right.patch)
	default:
		return cmpInt(left.beta, right.beta)
	}
}

func cmpInt(left, right int) int {
	switch {
	case left < right:
		return -1
	case left > right:
		return 1
	default:
		return 0
	}
}
