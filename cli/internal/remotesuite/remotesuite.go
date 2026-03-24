package remotesuite

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/marginlab/margin-eval/runner/runner-core/runbundle"
)

const (
	metadataSchemaVersion = 1
	defaultLockTimeout    = 30 * time.Second
	defaultLockPoll       = 100 * time.Millisecond
)

type ResolveInput struct {
	Suite   string
	Refresh bool
}

type Result struct {
	LocalPath string
	CacheDir  string
	Fetched   bool
	SuiteGit  *runbundle.SuiteGitRef
}

type cacheMetadata struct {
	SchemaVersion  int       `json:"schema_version"`
	RepoURL        string    `json:"repo_url"`
	ResolvedCommit string    `json:"resolved_commit"`
	Subdir         string    `json:"subdir,omitempty"`
	FetchedAt      time.Time `json:"fetched_at"`
}

type gitRunner interface {
	CheckAvailable(context.Context) error
	Fetch(context.Context, string, string) (string, error)
}

type Resolver struct {
	homeDir     func() (string, error)
	git         gitRunner
	now         func() time.Time
	lockTimeout time.Duration
	lockPoll    time.Duration
}

func New() *Resolver {
	return &Resolver{
		homeDir:     os.UserHomeDir,
		git:         shellGitRunner{binary: "git"},
		now:         func() time.Time { return time.Now().UTC() },
		lockTimeout: defaultLockTimeout,
		lockPoll:    defaultLockPoll,
	}
}

func Resolve(ctx context.Context, in ResolveInput) (Result, error) {
	return New().Resolve(ctx, in)
}

func (r *Resolver) Resolve(ctx context.Context, in ResolveInput) (Result, error) {
	parsed, err := parseSuite(in.Suite)
	if err != nil {
		return Result{}, err
	}

	homeDir, err := r.homeDir()
	if err != nil {
		return Result{}, fmt.Errorf("resolve user home directory: %w", err)
	}
	rootDir := filepath.Join(homeDir, ".margin", "suites", ".remote")
	if err := os.MkdirAll(rootDir, 0o755); err != nil {
		return Result{}, fmt.Errorf("create remote suite cache root: %w", err)
	}

	cacheKey := requestKey(parsed.RepoURL, parsed.Subdir)
	cacheDir := filepath.Join(rootDir, cacheKey)
	unlock, err := r.acquireLock(ctx, cacheDir+".lock")
	if err != nil {
		return Result{}, err
	}
	defer unlock()

	if !in.Refresh {
		if result, ok, err := loadCached(cacheDir, parsed.RepoURL, parsed.Subdir); err != nil {
			return Result{}, err
		} else if ok {
			return result, nil
		}
	}

	if err := r.git.CheckAvailable(ctx); err != nil {
		return Result{}, err
	}

	stageDir, err := os.MkdirTemp(rootDir, cacheKey+"-stage-")
	if err != nil {
		return Result{}, fmt.Errorf("create remote suite staging directory: %w", err)
	}
	defer os.RemoveAll(stageDir)

	checkoutDir := filepath.Join(stageDir, "checkout")
	if err := os.MkdirAll(checkoutDir, 0o755); err != nil {
		return Result{}, fmt.Errorf("create remote suite checkout directory: %w", err)
	}
	resolvedCommit, err := r.git.Fetch(ctx, parsed.RepoURL, checkoutDir)
	if err != nil {
		return Result{}, err
	}

	suiteSourceDir := checkoutDir
	if parsed.Subdir != "" {
		suiteSourceDir = filepath.Join(checkoutDir, filepath.FromSlash(parsed.Subdir))
	}
	if err := validateSuiteRoot(suiteSourceDir); err != nil {
		return Result{}, err
	}

	stagedEntry := filepath.Join(stageDir, "entry")
	stagedSuiteDir := filepath.Join(stagedEntry, "suite")
	if err := copySuiteDir(suiteSourceDir, stagedSuiteDir); err != nil {
		return Result{}, err
	}
	metadata := cacheMetadata{
		SchemaVersion:  metadataSchemaVersion,
		RepoURL:        parsed.RepoURL,
		ResolvedCommit: resolvedCommit,
		Subdir:         parsed.Subdir,
		FetchedAt:      r.now(),
	}
	if err := writeMetadata(filepath.Join(stagedEntry, "metadata.json"), metadata); err != nil {
		return Result{}, err
	}
	if err := replaceDir(cacheDir, stagedEntry); err != nil {
		return Result{}, err
	}

	return Result{
		LocalPath: filepath.Join(cacheDir, "suite"),
		CacheDir:  cacheDir,
		Fetched:   true,
		SuiteGit: &runbundle.SuiteGitRef{
			RepoURL:        metadata.RepoURL,
			ResolvedCommit: metadata.ResolvedCommit,
			Subdir:         metadata.Subdir,
		},
	}, nil
}

func IsRemoteSuite(raw string) bool {
	trimmed := strings.TrimSpace(raw)
	return strings.HasPrefix(trimmed, "https://") || strings.HasPrefix(trimmed, "git::https://")
}

type suiteSpec struct {
	RepoURL string
	Subdir  string
}

func parseSuite(raw string) (suiteSpec, error) {
	trimmed := strings.TrimSpace(raw)
	if strings.HasPrefix(trimmed, "git::https://") {
		return parseGitSuite(trimmed)
	}
	repoURL, err := normalizeRepoURL(trimmed)
	if err != nil {
		return suiteSpec{}, err
	}
	return suiteSpec{RepoURL: repoURL}, nil
}

func parseGitSuite(raw string) (suiteSpec, error) {
	const prefix = "git::"
	rest := strings.TrimPrefix(strings.TrimSpace(raw), prefix)
	const httpsPrefix = "https://"
	searchFrom := len(httpsPrefix)
	if len(rest) <= searchFrom {
		return suiteSpec{}, fmt.Errorf("remote suite URL path is required")
	}
	offset := strings.Index(rest[searchFrom:], "//")
	if offset < 0 {
		return suiteSpec{}, fmt.Errorf("git remote suites must use git::https://<repo>//<subdir>")
	}
	splitAt := searchFrom + offset
	repoPart := rest[:splitAt]
	subdirPart := rest[splitAt+2:]
	repoURL, err := normalizeRepoURL(repoPart)
	if err != nil {
		return suiteSpec{}, err
	}
	subdir, err := normalizeSubdir(subdirPart)
	if err != nil {
		return suiteSpec{}, err
	}
	if subdir == "" {
		return suiteSpec{}, fmt.Errorf("git remote suites must include a non-empty subdir after //")
	}
	return suiteSpec{
		RepoURL: repoURL,
		Subdir:  subdir,
	}, nil
}

func normalizeRepoURL(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", fmt.Errorf("--suite is required")
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("parse remote suite URL %q: %w", trimmed, err)
	}
	if parsed.Scheme != "https" {
		return "", fmt.Errorf("remote suite URL must use https://")
	}
	if strings.TrimSpace(parsed.Host) == "" {
		return "", fmt.Errorf("remote suite URL host is required")
	}
	if strings.TrimSpace(parsed.Path) == "" || strings.TrimSpace(parsed.Path) == "/" {
		return "", fmt.Errorf("remote suite URL path is required")
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.Fragment = ""
	parsed.Host = strings.ToLower(parsed.Host)
	cleanPath := strings.TrimRight(parsed.Path, "/")
	cleanPath = strings.TrimSuffix(cleanPath, ".git")
	if cleanPath == "" || cleanPath == "/" {
		return "", fmt.Errorf("remote suite URL path is required")
	}
	parsed.Path = cleanPath
	return parsed.String(), nil
}

func normalizeSubdir(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", nil
	}
	if strings.Contains(trimmed, "\\") {
		return "", fmt.Errorf("remote suite subdir must use slash-separated relative paths")
	}
	cleaned := path.Clean(trimmed)
	if cleaned == "." {
		return "", nil
	}
	if strings.HasPrefix(cleaned, "/") {
		return "", fmt.Errorf("remote suite subdir must be relative")
	}
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("remote suite subdir must not escape the repository")
	}
	return cleaned, nil
}

func requestKey(repoURL, subdir string) string {
	value := repoURL + "\n" + valueOrDefault(subdir, ".")
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func valueOrDefault(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func loadCached(cacheDir, repoURL, subdir string) (Result, bool, error) {
	metadata, err := readMetadata(filepath.Join(cacheDir, "metadata.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return Result{}, false, nil
		}
		return Result{}, false, fmt.Errorf("read remote suite cache metadata: %w", err)
	}
	if metadata.SchemaVersion != metadataSchemaVersion {
		return Result{}, false, nil
	}
	if metadata.RepoURL != repoURL || metadata.Subdir != subdir {
		return Result{}, false, nil
	}
	suiteDir := filepath.Join(cacheDir, "suite")
	if err := validateSuiteRoot(suiteDir); err != nil {
		return Result{}, false, nil
	}
	return Result{
		LocalPath: suiteDir,
		CacheDir:  cacheDir,
		SuiteGit: &runbundle.SuiteGitRef{
			RepoURL:        metadata.RepoURL,
			ResolvedCommit: metadata.ResolvedCommit,
			Subdir:         metadata.Subdir,
		},
	}, true, nil
}

func readMetadata(path string) (cacheMetadata, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return cacheMetadata{}, err
	}
	var metadata cacheMetadata
	if err := json.Unmarshal(body, &metadata); err != nil {
		return cacheMetadata{}, fmt.Errorf("decode %s: %w", path, err)
	}
	return metadata, nil
}

func writeMetadata(path string, metadata cacheMetadata) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create metadata parent directory: %w", err)
	}
	body, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return fmt.Errorf("encode remote suite metadata: %w", err)
	}
	if err := os.WriteFile(path, append(body, '\n'), 0o644); err != nil {
		return fmt.Errorf("write remote suite metadata: %w", err)
	}
	return nil
}

func validateSuiteRoot(dir string) error {
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("stat suite directory %s: %w", dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("suite path %s must be a directory", dir)
	}
	if info, err := os.Stat(filepath.Join(dir, "suite.toml")); err != nil {
		return fmt.Errorf("suite directory %s must contain suite.toml: %w", dir, err)
	} else if info.IsDir() {
		return fmt.Errorf("suite.toml in %s must be a file", dir)
	}
	if info, err := os.Stat(filepath.Join(dir, "cases")); err != nil {
		return fmt.Errorf("suite directory %s must contain cases/: %w", dir, err)
	} else if !info.IsDir() {
		return fmt.Errorf("cases in %s must be a directory", dir)
	}
	return nil
}

func copySuiteDir(src, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return fmt.Errorf("create suite destination %s: %w", dst, err)
	}
	return filepath.WalkDir(src, func(current string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, current)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if d.Name() == ".git" {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		target := filepath.Join(dst, rel)
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlinks are not supported in remote suites: %s", current)
		}
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("unsupported file type in remote suite: %s", current)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return copyFile(current, target, info.Mode().Perm())
	})
}

func copyFile(src, dst string, mode fs.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open %s: %w", src, err)
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return fmt.Errorf("create %s: %w", dst, err)
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return fmt.Errorf("copy %s to %s: %w", src, dst, err)
	}
	return nil
}

func replaceDir(dest, staged string) error {
	parent := filepath.Dir(dest)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("create cache parent directory: %w", err)
	}
	info, err := os.Stat(dest)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("stat existing cache directory: %w", err)
	}
	if err == nil && !info.IsDir() {
		return fmt.Errorf("cache path %s is not a directory", dest)
	}

	backup := ""
	if err == nil {
		backup = filepath.Join(parent, "."+filepath.Base(dest)+".backup-"+strconv.FormatInt(time.Now().UnixNano(), 10))
		if err := os.Rename(dest, backup); err != nil {
			return fmt.Errorf("move existing cache directory aside: %w", err)
		}
	}
	if err := os.Rename(staged, dest); err != nil {
		if backup != "" {
			_ = os.Rename(backup, dest)
		}
		return fmt.Errorf("promote staged cache directory: %w", err)
	}
	if backup != "" {
		if err := os.RemoveAll(backup); err != nil {
			return fmt.Errorf("remove previous cache directory: %w", err)
		}
	}
	return nil
}

func (r *Resolver) acquireLock(ctx context.Context, lockDir string) (func(), error) {
	deadline := time.Now().Add(r.lockTimeout)
	for {
		if err := os.Mkdir(lockDir, 0o755); err == nil {
			return func() { _ = os.Remove(lockDir) }, nil
		} else if !os.IsExist(err) {
			return nil, fmt.Errorf("acquire remote suite cache lock: %w", err)
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("timed out waiting for remote suite cache lock %s", lockDir)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(r.lockPoll):
		}
	}
}

type shellGitRunner struct {
	binary string
}

func (g shellGitRunner) CheckAvailable(_ context.Context) error {
	if _, err := exec.LookPath(g.binary); err != nil {
		return fmt.Errorf("remote suites require %q on PATH: %w", g.binary, err)
	}
	return nil
}

func (g shellGitRunner) Fetch(ctx context.Context, repoURL, checkoutDir string) (string, error) {
	if err := runGitCommand(ctx, checkoutDir, g.binary, "init", "--quiet"); err != nil {
		return "", fmt.Errorf("initialize git checkout: %w", err)
	}
	if err := runGitCommand(ctx, checkoutDir, g.binary, "remote", "add", "origin", repoURL); err != nil {
		return "", fmt.Errorf("configure remote suite origin: %w", err)
	}
	if err := runGitFetch(ctx, checkoutDir, g.binary); err != nil {
		return "", err
	}
	if err := runGitCommand(ctx, checkoutDir, g.binary, "checkout", "--detach", "--quiet", "FETCH_HEAD"); err != nil {
		return "", fmt.Errorf("checkout remote suite default branch: %w", err)
	}
	output, err := gitOutput(ctx, checkoutDir, g.binary, "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("resolve remote suite commit: %w", err)
	}
	return strings.TrimSpace(output), nil
}

func runGitFetch(ctx context.Context, dir, binary string) error {
	err := runGitCommand(ctx, dir, binary, "fetch", "--depth", "1", "origin", "HEAD")
	if err == nil {
		return nil
	}
	if fallbackErr := runGitCommand(ctx, dir, binary, "fetch", "origin", "HEAD"); fallbackErr != nil {
		return fmt.Errorf("fetch remote suite default branch: %v (fallback failed: %w)", err, fallbackErr)
	}
	return nil
}

func runGitCommand(ctx context.Context, dir, binary string, args ...string) error {
	_, err := gitOutput(ctx, dir, binary, args...)
	return err
}

func gitOutput(ctx context.Context, dir, binary string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s %s: %w: %s", binary, strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return string(output), nil
}
