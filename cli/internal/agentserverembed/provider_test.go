package agentserverembed

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
)

func TestProviderExtractsBinaryToCache(t *testing.T) {
	cacheRoot := t.TempDir()
	fsys := fstest.MapFS{
		"generated/manifest.json": {
			Data: []byte(`{"schema_version":"v1","binaries":[{"platform":"linux/amd64","filename":"agent-server-linux-amd64","sha256":"2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"}]}`),
		},
		"generated/agent-server-linux-amd64": {
			Data: []byte("hello"),
		},
	}
	provider, err := newProvider(cacheRoot, fsys)
	if err != nil {
		t.Fatalf("newProvider() error = %v", err)
	}

	path, err := provider.ResolveAgentServerBinary(context.Background(), "linux/amd64")
	if err != nil {
		t.Fatalf("ResolveAgentServerBinary() error = %v", err)
	}
	if path == "" {
		t.Fatalf("expected cached path")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat(%q) error = %v", path, err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("mode = %#o, want 0755", info.Mode().Perm())
	}
}

func TestProviderReplacesCorruptCachedBinary(t *testing.T) {
	cacheRoot := t.TempDir()
	fsys := fstest.MapFS{
		"generated/manifest.json": {
			Data: []byte(`{"schema_version":"v1","binaries":[{"platform":"linux/arm64","filename":"agent-server-linux-arm64","sha256":"2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"}]}`),
		},
		"generated/agent-server-linux-arm64": {
			Data: []byte("hello"),
		},
	}
	provider, err := newProvider(cacheRoot, fsys)
	if err != nil {
		t.Fatalf("newProvider() error = %v", err)
	}
	targetPath := filepath.Join(cacheRoot, "margin", "agent-server", "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824", "agent-server-linux-arm64")
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(targetPath, []byte("bad"), 0o755); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	path, err := provider.ResolveAgentServerBinary(context.Background(), "linux/arm64")
	if err != nil {
		t.Fatalf("ResolveAgentServerBinary() error = %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", path, err)
	}
	if string(data) != "hello" {
		t.Fatalf("cached payload = %q, want hello", string(data))
	}
}

func TestProviderRejectsUnsupportedPlatform(t *testing.T) {
	cacheRoot := t.TempDir()
	fsys := fstest.MapFS{
		"generated/manifest.json": {
			Data: []byte(`{"schema_version":"v1","binaries":[{"platform":"linux/amd64","filename":"agent-server-linux-amd64","sha256":"2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"}]}`),
		},
		"generated/agent-server-linux-amd64": {
			Data: []byte("hello"),
		},
	}
	provider, err := newProvider(cacheRoot, fsys)
	if err != nil {
		t.Fatalf("newProvider() error = %v", err)
	}

	_, err = provider.ResolveAgentServerBinary(context.Background(), "linux/s390x")
	if err == nil {
		t.Fatalf("expected unsupported platform error")
	}
}
