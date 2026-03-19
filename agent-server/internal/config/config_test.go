package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var configEnvKeys = []string{
	"AGENT_SERVER_ROOT",
	"AGENT_SERVER_BIN_DIR",
	"AGENT_SERVER_STATE_DIR",
	"AGENT_SERVER_WORKSPACES_DIR",
	"AGENT_SERVER_CONFIG_DIR",
	"AGENT_SERVER_EXTRA_CA_CERTS_FILE",
	"AGENT_SERVER_NVM_DIR",
	"AGENT_SERVER_LISTEN",
	"AGENT_SERVER_STOP_GRACE_TIMEOUT",
	"AGENT_SERVER_TRAJECTORY_COLLECT_TIMEOUT",
	"AGENT_SERVER_TRAJECTORY_POLL_INTERVAL",
	"AGENT_SERVER_REPLAY_BUFFER_BYTES",
	"AGENT_SERVER_SNAPSHOT_MAX_BYTES",
	"AGENT_SERVER_SNAPSHOT_HARD_TIMEOUT",
	"AGENT_SERVER_SNAPSHOT_IDLE_TIMEOUT",
	"AGENT_SERVER_SNAPSHOT_STOP_GRACE_TIMEOUT",
	"AGENT_SERVER_NVM_VERSION",
	"AGENT_SERVER_NODE_BOOTSTRAP_TIMEOUT",
}

func clearConfigEnv(t *testing.T) {
	t.Helper()
	for _, key := range configEnvKeys {
		unsetEnv(t, key)
	}
}

func unsetEnv(t *testing.T, key string) {
	t.Helper()
	value, ok := os.LookupEnv(key)
	if err := os.Unsetenv(key); err != nil {
		t.Fatalf("unset %s: %v", key, err)
	}
	t.Cleanup(func() {
		if ok {
			_ = os.Setenv(key, value)
			return
		}
		_ = os.Unsetenv(key)
	})
}

// TestFromEnvDefaults verifies FromEnv populates deterministic defaults when no env overrides are set.
func TestFromEnvDefaults(t *testing.T) {
	clearConfigEnv(t)

	cfg, err := FromEnv()
	if err != nil {
		t.Fatalf("FromEnv() error = %v", err)
	}

	if cfg.ListenAddr != defaultListenAddr {
		t.Fatalf("ListenAddr = %q, want %q", cfg.ListenAddr, defaultListenAddr)
	}
	if cfg.RootDir != defaultRootDir {
		t.Fatalf("RootDir = %q, want %q", cfg.RootDir, defaultRootDir)
	}
	if cfg.BinDir != filepath.Join(defaultRootDir, "bin") {
		t.Fatalf("BinDir = %q", cfg.BinDir)
	}
	if cfg.StateDir != filepath.Join(defaultRootDir, "state") {
		t.Fatalf("StateDir = %q", cfg.StateDir)
	}
	if cfg.WorkspacesDir != filepath.Join(defaultRootDir, "workspaces") {
		t.Fatalf("WorkspacesDir = %q", cfg.WorkspacesDir)
	}
	if cfg.ConfigDir != filepath.Join(defaultRootDir, "config") {
		t.Fatalf("ConfigDir = %q", cfg.ConfigDir)
	}
	if cfg.StateFile != filepath.Join(cfg.StateDir, defaultStateFileName) {
		t.Fatalf("StateFile = %q", cfg.StateFile)
	}
	if cfg.StopGraceTimeout != defaultStopGraceTimeout {
		t.Fatalf("StopGraceTimeout = %s", cfg.StopGraceTimeout)
	}
	if cfg.TrajectoryCollectTimeout != defaultCollectTimeout {
		t.Fatalf("TrajectoryCollectTimeout = %s", cfg.TrajectoryCollectTimeout)
	}
	if cfg.TrajectoryPollInterval != defaultCollectPoll {
		t.Fatalf("TrajectoryPollInterval = %s", cfg.TrajectoryPollInterval)
	}
	if cfg.ReplayBufferBytes != defaultReplayBufferBytes {
		t.Fatalf("ReplayBufferBytes = %d", cfg.ReplayBufferBytes)
	}
	if cfg.SnapshotMaxBytes != defaultSnapshotMaxBytes {
		t.Fatalf("SnapshotMaxBytes = %d", cfg.SnapshotMaxBytes)
	}
	if cfg.SnapshotHardTimeout != defaultSnapshotTimeout {
		t.Fatalf("SnapshotHardTimeout = %s", cfg.SnapshotHardTimeout)
	}
	if cfg.SnapshotIdleTimeout != defaultSnapshotIdle {
		t.Fatalf("SnapshotIdleTimeout = %s", cfg.SnapshotIdleTimeout)
	}
	if cfg.SnapshotStopGrace != defaultSnapshotStopGrace {
		t.Fatalf("SnapshotStopGrace = %s", cfg.SnapshotStopGrace)
	}
	if cfg.NVMVersion != defaultNVMVersion {
		t.Fatalf("NVMVersion = %q, want %q", cfg.NVMVersion, defaultNVMVersion)
	}
	if cfg.NVMDir != filepath.Join(cfg.StateDir, "toolchain", "nvm") {
		t.Fatalf("NVMDir = %q", cfg.NVMDir)
	}
	if cfg.NodeBootstrapTimeout != defaultNodeBootstrap {
		t.Fatalf("NodeBootstrapTimeout = %s, want %s", cfg.NodeBootstrapTimeout, defaultNodeBootstrap)
	}
}

// TestFromEnvExplicitValues verifies explicit environment variables override default paths and timing settings.
func TestFromEnvExplicitValues(t *testing.T) {
	clearConfigEnv(t)

	root := t.TempDir()
	bin := t.TempDir()
	state := t.TempDir()
	workspaces := t.TempDir()
	conf := t.TempDir()
	extraCA := filepath.Join(t.TempDir(), "extra.pem")
	nvmDir := t.TempDir()
	if err := os.WriteFile(extraCA, []byte("test\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(extra.pem): %v", err)
	}

	t.Setenv("AGENT_SERVER_ROOT", root)
	t.Setenv("AGENT_SERVER_BIN_DIR", bin)
	t.Setenv("AGENT_SERVER_STATE_DIR", state)
	t.Setenv("AGENT_SERVER_WORKSPACES_DIR", workspaces)
	t.Setenv("AGENT_SERVER_CONFIG_DIR", conf)
	t.Setenv("AGENT_SERVER_EXTRA_CA_CERTS_FILE", extraCA)
	t.Setenv("AGENT_SERVER_NVM_DIR", nvmDir)
	t.Setenv("AGENT_SERVER_LISTEN", "127.0.0.1:9090")
	t.Setenv("AGENT_SERVER_STOP_GRACE_TIMEOUT", "22s")
	t.Setenv("AGENT_SERVER_TRAJECTORY_COLLECT_TIMEOUT", "33s")
	t.Setenv("AGENT_SERVER_TRAJECTORY_POLL_INTERVAL", "900ms")
	t.Setenv("AGENT_SERVER_REPLAY_BUFFER_BYTES", "8192")
	t.Setenv("AGENT_SERVER_SNAPSHOT_MAX_BYTES", "262144")
	t.Setenv("AGENT_SERVER_SNAPSHOT_HARD_TIMEOUT", "12s")
	t.Setenv("AGENT_SERVER_SNAPSHOT_IDLE_TIMEOUT", "450ms")
	t.Setenv("AGENT_SERVER_SNAPSHOT_STOP_GRACE_TIMEOUT", "4s")
	t.Setenv("AGENT_SERVER_NVM_VERSION", "v0.40.4")
	t.Setenv("AGENT_SERVER_NODE_BOOTSTRAP_TIMEOUT", "5m")
	cfg, err := FromEnv()
	if err != nil {
		t.Fatalf("FromEnv() error = %v", err)
	}

	if cfg.RootDir != root || cfg.BinDir != bin || cfg.StateDir != state || cfg.WorkspacesDir != workspaces || cfg.ConfigDir != conf || cfg.ExtraCACertsFile != extraCA || cfg.NVMDir != nvmDir {
		t.Fatalf("unexpected explicit dirs: %+v", cfg)
	}
	if cfg.ListenAddr != "127.0.0.1:9090" {
		t.Fatalf("ListenAddr = %q", cfg.ListenAddr)
	}
	if cfg.StopGraceTimeout != 22*time.Second {
		t.Fatalf("StopGraceTimeout = %s", cfg.StopGraceTimeout)
	}
	if cfg.TrajectoryCollectTimeout != 33*time.Second {
		t.Fatalf("TrajectoryCollectTimeout = %s", cfg.TrajectoryCollectTimeout)
	}
	if cfg.TrajectoryPollInterval != 900*time.Millisecond {
		t.Fatalf("TrajectoryPollInterval = %s", cfg.TrajectoryPollInterval)
	}
	if cfg.ReplayBufferBytes != 8192 {
		t.Fatalf("ReplayBufferBytes = %d", cfg.ReplayBufferBytes)
	}
	if cfg.SnapshotMaxBytes != 262144 {
		t.Fatalf("SnapshotMaxBytes = %d", cfg.SnapshotMaxBytes)
	}
	if cfg.SnapshotHardTimeout != 12*time.Second {
		t.Fatalf("SnapshotHardTimeout = %s", cfg.SnapshotHardTimeout)
	}
	if cfg.SnapshotIdleTimeout != 450*time.Millisecond {
		t.Fatalf("SnapshotIdleTimeout = %s", cfg.SnapshotIdleTimeout)
	}
	if cfg.SnapshotStopGrace != 4*time.Second {
		t.Fatalf("SnapshotStopGrace = %s", cfg.SnapshotStopGrace)
	}
	if cfg.NVMVersion != "v0.40.4" {
		t.Fatalf("NVMVersion = %q", cfg.NVMVersion)
	}
	if cfg.NodeBootstrapTimeout != 5*time.Minute {
		t.Fatalf("NodeBootstrapTimeout = %s", cfg.NodeBootstrapTimeout)
	}
}

// TestFromEnvRejectsInvalidValues verifies invalid env values are rejected with descriptive parse/validation errors.
func TestFromEnvRejectsInvalidValues(t *testing.T) {
	clearConfigEnv(t)

	tests := []struct {
		name     string
		key      string
		value    string
		contains string
	}{
		{name: "empty_root", key: "AGENT_SERVER_ROOT", value: "   ", contains: "cannot be empty"},
		{name: "empty_listen", key: "AGENT_SERVER_LISTEN", value: " ", contains: "cannot be empty"},
		{name: "bad_duration", key: "AGENT_SERVER_STOP_GRACE_TIMEOUT", value: "abc", contains: "parse"},
		{name: "zero_duration", key: "AGENT_SERVER_TRAJECTORY_COLLECT_TIMEOUT", value: "0s", contains: "must be > 0"},
		{name: "bad_int", key: "AGENT_SERVER_REPLAY_BUFFER_BYTES", value: "nope", contains: "parse"},
		{name: "negative_int", key: "AGENT_SERVER_REPLAY_BUFFER_BYTES", value: "-1", contains: "must be > 0"},
		{name: "snapshot_bad_int", key: "AGENT_SERVER_SNAPSHOT_MAX_BYTES", value: "NaN", contains: "parse"},
		{name: "snapshot_zero_duration", key: "AGENT_SERVER_SNAPSHOT_HARD_TIMEOUT", value: "0s", contains: "must be > 0"},
		{name: "nvm_whitespace", key: "AGENT_SERVER_NVM_VERSION", value: "v0.40.4 beta", contains: "must not contain whitespace"},
		{name: "node_bootstrap_zero", key: "AGENT_SERVER_NODE_BOOTSTRAP_TIMEOUT", value: "0s", contains: "must be > 0"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clearConfigEnv(t)
			t.Setenv(tc.key, tc.value)

			_, err := FromEnv()
			if err == nil {
				t.Fatalf("FromEnv() expected error")
			}
			if !strings.Contains(err.Error(), tc.contains) {
				t.Fatalf("error = %q, want substring %q", err.Error(), tc.contains)
			}
		})
	}
}

// TestFromEnvAllowsExplicitPathsOutsideRoot verifies explicit directory overrides are allowed outside AGENT_SERVER_ROOT.
func TestFromEnvAllowsExplicitPathsOutsideRoot(t *testing.T) {
	clearConfigEnv(t)

	root := t.TempDir()
	outsideBin := t.TempDir()
	t.Setenv("AGENT_SERVER_ROOT", root)
	t.Setenv("AGENT_SERVER_BIN_DIR", outsideBin)

	cfg, err := FromEnv()
	if err != nil {
		t.Fatalf("FromEnv() error = %v", err)
	}
	if cfg.BinDir != outsideBin {
		t.Fatalf("BinDir = %q, want %q", cfg.BinDir, outsideBin)
	}
}
