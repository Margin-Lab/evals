package trustroots

import (
	"crypto/tls"
	"crypto/x509"
	"embed"
	"encoding/pem"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/marginlab/margin-eval/agent-server/internal/fsutil"
)

const (
	nodeExtraCACertsEnvKey = "NODE_EXTRA_CA_CERTS"
	npmConfigCAFileEnvKey  = "NPM_CONFIG_CAFILE"
	sslCertFileEnvKey      = "SSL_CERT_FILE"
)

//go:embed public-roots.pem
var embeddedFiles embed.FS

// Config controls trust-root loading and materialization.
type Config struct {
	StateDir         string
	ExtraCACertsFile string
}

// Bundle owns the merged trust roots used by managed toolchains and agent processes.
type Bundle struct {
	bundlePEM  []byte
	bundlePath string
	pool       *x509.CertPool
	env        map[string]string
}

// New loads the embedded public roots, optionally appends an extra PEM bundle,
// validates the merged payload, and materializes it under state/.
func New(cfg Config) (*Bundle, error) {
	stateDir, err := normalizeRequiredPath(cfg.StateDir, "state dir")
	if err != nil {
		return nil, err
	}
	extraFile, err := normalizeOptionalPath(cfg.ExtraCACertsFile, "extra CA certs file")
	if err != nil {
		return nil, err
	}

	publicPEM, err := embeddedFiles.ReadFile("public-roots.pem")
	if err != nil {
		return nil, fmt.Errorf("read embedded public roots: %w", err)
	}
	if err := validatePEMBundle(publicPEM, "embedded public roots"); err != nil {
		return nil, err
	}

	mergedPEM := normalizePEM(publicPEM)
	if extraFile != "" {
		extraPEM, err := os.ReadFile(extraFile)
		if err != nil {
			return nil, fmt.Errorf("read extra CA certs file %s: %w", extraFile, err)
		}
		if err := validatePEMBundle(extraPEM, "extra CA certs file"); err != nil {
			return nil, err
		}
		mergedPEM = appendPEMBundles(mergedPEM, extraPEM)
	}

	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(mergedPEM) {
		return nil, fmt.Errorf("append merged CA bundle to cert pool: no certificates found")
	}

	bundlePath := filepath.Join(stateDir, "tls", "ca-bundle.pem")
	if err := fsutil.EnsureDir(filepath.Dir(bundlePath), 0o755); err != nil {
		return nil, fmt.Errorf("ensure trust root dir: %w", err)
	}
	if err := fsutil.WriteFileAtomic(bundlePath, mergedPEM, 0o644); err != nil {
		return nil, fmt.Errorf("write merged CA bundle: %w", err)
	}

	return &Bundle{
		bundlePEM:  mergedPEM,
		bundlePath: bundlePath,
		pool:       pool,
		env: map[string]string{
			nodeExtraCACertsEnvKey: bundlePath,
			npmConfigCAFileEnvKey:  bundlePath,
			sslCertFileEnvKey:      bundlePath,
		},
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

func normalizeOptionalPath(value string, field string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", nil
	}
	absPath, err := filepath.Abs(trimmed)
	if err != nil {
		return "", fmt.Errorf("resolve %s %q: %w", field, trimmed, err)
	}
	return filepath.Clean(absPath), nil
}

func normalizePEM(body []byte) []byte {
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return nil
	}
	return []byte(trimmed + "\n")
}

func appendPEMBundles(base []byte, extra []byte) []byte {
	normalizedBase := normalizePEM(base)
	normalizedExtra := normalizePEM(extra)
	if len(normalizedBase) == 0 {
		return normalizedExtra
	}
	if len(normalizedExtra) == 0 {
		return normalizedBase
	}
	out := make([]byte, 0, len(normalizedBase)+len(normalizedExtra)+1)
	out = append(out, normalizedBase...)
	out = append(out, '\n')
	out = append(out, normalizedExtra...)
	return out
}

func validatePEMBundle(body []byte, label string) error {
	rest := normalizePEM(body)
	if len(rest) == 0 {
		return fmt.Errorf("%s does not contain any PEM data", label)
	}

	count := 0
	for len(rest) > 0 {
		block, remaining := pem.Decode(rest)
		if block == nil {
			return fmt.Errorf("%s contains invalid PEM data", label)
		}
		if block.Type != "CERTIFICATE" {
			return fmt.Errorf("%s contains unsupported PEM block type %q", label, block.Type)
		}
		if _, err := x509.ParseCertificate(block.Bytes); err != nil {
			return fmt.Errorf("parse certificate in %s: %w", label, err)
		}
		count++
		rest = remaining
	}
	if count == 0 {
		return fmt.Errorf("%s does not contain any certificates", label)
	}
	return nil
}

// HTTPClient returns an HTTPS client that trusts only the materialized merged bundle.
func (b *Bundle) HTTPClient(timeout time.Duration) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if transport.TLSClientConfig == nil {
		transport.TLSClientConfig = &tls.Config{}
	}
	transport.TLSClientConfig = transport.TLSClientConfig.Clone()
	transport.TLSClientConfig.RootCAs = b.pool
	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
	}
}

// BundlePath returns the materialized merged CA bundle path.
func (b *Bundle) BundlePath() string {
	return b.bundlePath
}

// CertPool returns the merged cert pool.
func (b *Bundle) CertPool() *x509.CertPool {
	return b.pool
}

// Environment returns deterministic env additions for hook and agent child processes.
func (b *Bundle) Environment() map[string]string {
	out := make(map[string]string, len(b.env))
	for key, value := range b.env {
		out[key] = value
	}
	return out
}

// BundlePEM returns the merged PEM payload.
func (b *Bundle) BundlePEM() []byte {
	return append([]byte(nil), b.bundlePEM...)
}

// EnvironmentKeys returns the environment keys this bundle controls.
func EnvironmentKeys() []string {
	return []string{
		sslCertFileEnvKey,
		nodeExtraCACertsEnvKey,
		npmConfigCAFileEnvKey,
	}
}

// SortedEnvironment returns sorted KEY=value pairs for tests or logging.
func (b *Bundle) SortedEnvironment() []string {
	keys := make([]string, 0, len(b.env))
	for key := range b.env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, key+"="+b.env[key])
	}
	return out
}
