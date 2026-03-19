package agentserverembed

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/marginlab/margin-eval/runner/runner-local/localexecutor"
)

type manifest struct {
	SchemaVersion string       `json:"schema_version"`
	Binaries      []binarySpec `json:"binaries"`
}

type binarySpec struct {
	Platform string `json:"platform"`
	Filename string `json:"filename"`
	SHA256   string `json:"sha256"`
}

type Provider struct {
	cacheRoot string
	fsys      fs.FS
	entries   map[string]binarySpec
}

var _ localexecutor.AgentServerBinaryProvider = (*Provider)(nil)

func NewProvider() (*Provider, error) {
	cacheRoot, err := os.UserCacheDir()
	if err != nil {
		return nil, fmt.Errorf("resolve user cache dir: %w", err)
	}
	return newProvider(cacheRoot, embeddedFiles)
}

func newProvider(cacheRoot string, fsys fs.FS) (*Provider, error) {
	cacheRoot = strings.TrimSpace(cacheRoot)
	if cacheRoot == "" {
		return nil, fmt.Errorf("cache root is required")
	}
	manifestBytes, err := fs.ReadFile(fsys, "generated/manifest.json")
	if err != nil {
		return nil, fmt.Errorf("read embedded agent-server manifest: %w", err)
	}
	var manifestData manifest
	if err := json.Unmarshal(manifestBytes, &manifestData); err != nil {
		return nil, fmt.Errorf("decode embedded agent-server manifest: %w", err)
	}
	if strings.TrimSpace(manifestData.SchemaVersion) == "" {
		return nil, fmt.Errorf("embedded agent-server manifest is missing schema_version")
	}
	entries := make(map[string]binarySpec, len(manifestData.Binaries))
	for _, entry := range manifestData.Binaries {
		platform := strings.TrimSpace(entry.Platform)
		filename := strings.TrimSpace(entry.Filename)
		digest := strings.ToLower(strings.TrimSpace(entry.SHA256))
		if platform == "" || filename == "" || digest == "" {
			return nil, fmt.Errorf("embedded agent-server manifest contains an incomplete binary entry")
		}
		if _, err := fs.Stat(fsys, path.Join("generated", filename)); err != nil {
			return nil, fmt.Errorf("embedded agent-server payload %q: %w", filename, err)
		}
		entries[platform] = binarySpec{
			Platform: platform,
			Filename: filename,
			SHA256:   digest,
		}
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("embedded agent-server manifest does not contain any payloads")
	}
	return &Provider{
		cacheRoot: cacheRoot,
		fsys:      fsys,
		entries:   entries,
	}, nil
}

func (p *Provider) ResolveAgentServerBinary(_ context.Context, platform string) (string, error) {
	platform = strings.ToLower(strings.TrimSpace(platform))
	entry, ok := p.entries[platform]
	if !ok {
		available := make([]string, 0, len(p.entries))
		for key := range p.entries {
			available = append(available, key)
		}
		sort.Strings(available)
		return "", fmt.Errorf(
			"embedded agent-server does not support platform %q (available: %s)",
			platform,
			strings.Join(available, ", "),
		)
	}
	targetPath := filepath.Join(p.cacheRoot, "margin", "agent-server", entry.SHA256, entry.Filename)
	if err := p.ensureCachedBinary(targetPath, entry); err != nil {
		return "", err
	}
	return targetPath, nil
}

func (p *Provider) ensureCachedBinary(targetPath string, entry binarySpec) error {
	if hashMatches(targetPath, entry.SHA256) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return fmt.Errorf("create embedded agent-server cache dir: %w", err)
	}
	payload, err := fs.ReadFile(p.fsys, path.Join("generated", entry.Filename))
	if err != nil {
		return fmt.Errorf("read embedded agent-server payload %q: %w", entry.Filename, err)
	}
	tempFile, err := os.CreateTemp(filepath.Dir(targetPath), entry.Filename+".tmp-*")
	if err != nil {
		return fmt.Errorf("create embedded agent-server temp file: %w", err)
	}
	tempPath := tempFile.Name()
	defer os.Remove(tempPath)
	if _, err := tempFile.Write(payload); err != nil {
		_ = tempFile.Close()
		return fmt.Errorf("write embedded agent-server temp file: %w", err)
	}
	if err := tempFile.Chmod(0o755); err != nil {
		_ = tempFile.Close()
		return fmt.Errorf("chmod embedded agent-server temp file: %w", err)
	}
	if err := tempFile.Close(); err != nil {
		return fmt.Errorf("close embedded agent-server temp file: %w", err)
	}
	if err := os.Rename(tempPath, targetPath); err != nil {
		return fmt.Errorf("install embedded agent-server cache file: %w", err)
	}
	if !hashMatches(targetPath, entry.SHA256) {
		return fmt.Errorf("embedded agent-server cache file %q failed checksum verification", targetPath)
	}
	return nil
}

func hashMatches(path string, want string) bool {
	got, err := fileSHA256(path)
	return err == nil && strings.EqualFold(got, want)
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}
