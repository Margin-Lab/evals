package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	defaultListenAddr        = ":8080"
	defaultRootDir           = "/marginlab"
	defaultStopGraceTimeout  = 10 * time.Second
	defaultCollectTimeout    = 15 * time.Second
	defaultCollectPoll       = 500 * time.Millisecond
	defaultReplayBufferBytes = 64 * 1024
	defaultSnapshotMaxBytes  = 2 * 1024 * 1024
	defaultSnapshotTimeout   = 8 * time.Second
	defaultSnapshotIdle      = 1 * time.Second
	defaultSnapshotStopGrace = 2 * time.Second
	defaultNVMVersion        = "v0.40.4"
	defaultNodeBootstrap     = 4 * time.Minute
	defaultStateFileName     = "server-state.json"
)

// Config is the runtime configuration for the agent server.
type Config struct {
	ListenAddr string

	RootDir       string
	BinDir        string
	StateDir      string
	WorkspacesDir string
	ConfigDir     string
	StateFile     string

	StopGraceTimeout         time.Duration
	TrajectoryCollectTimeout time.Duration
	TrajectoryPollInterval   time.Duration
	ReplayBufferBytes        int
	SnapshotMaxBytes         int
	SnapshotHardTimeout      time.Duration
	SnapshotIdleTimeout      time.Duration
	SnapshotStopGrace        time.Duration
	ExtraCACertsFile         string
	NVMVersion               string
	NVMDir                   string
	NodeBootstrapTimeout     time.Duration
}

// FromEnv loads config from environment variables with deterministic defaults.
func FromEnv() (Config, error) {
	rootRaw, _, err := getPathEnv("AGENT_SERVER_ROOT", defaultRootDir)
	if err != nil {
		return Config{}, err
	}
	root, err := toAbsPath(rootRaw, "AGENT_SERVER_ROOT")
	if err != nil {
		return Config{}, err
	}

	binRaw, binExplicit, err := getPathEnv("AGENT_SERVER_BIN_DIR", filepath.Join(root, "bin"))
	if err != nil {
		return Config{}, err
	}
	stateRaw, stateExplicit, err := getPathEnv("AGENT_SERVER_STATE_DIR", filepath.Join(root, "state"))
	if err != nil {
		return Config{}, err
	}
	workspacesRaw, workspacesExplicit, err := getPathEnv("AGENT_SERVER_WORKSPACES_DIR", filepath.Join(root, "workspaces"))
	if err != nil {
		return Config{}, err
	}
	configRaw, configExplicit, err := getPathEnv("AGENT_SERVER_CONFIG_DIR", filepath.Join(root, "config"))
	if err != nil {
		return Config{}, err
	}
	extraCACertsRaw, extraCACertsExplicit, err := getPathEnv("AGENT_SERVER_EXTRA_CA_CERTS_FILE", "")
	if err != nil {
		return Config{}, err
	}
	nvmRaw, nvmExplicit, err := getPathEnv("AGENT_SERVER_NVM_DIR", filepath.Join(stateRaw, "toolchain", "nvm"))
	if err != nil {
		return Config{}, err
	}

	binDir, err := toAbsPath(binRaw, "AGENT_SERVER_BIN_DIR")
	if err != nil {
		return Config{}, err
	}
	stateDir, err := toAbsPath(stateRaw, "AGENT_SERVER_STATE_DIR")
	if err != nil {
		return Config{}, err
	}
	workspacesDir, err := toAbsPath(workspacesRaw, "AGENT_SERVER_WORKSPACES_DIR")
	if err != nil {
		return Config{}, err
	}
	configDir, err := toAbsPath(configRaw, "AGENT_SERVER_CONFIG_DIR")
	if err != nil {
		return Config{}, err
	}
	extraCACertsFile := ""
	if extraCACertsExplicit {
		extraCACertsFile, err = toAbsPath(extraCACertsRaw, "AGENT_SERVER_EXTRA_CA_CERTS_FILE")
		if err != nil {
			return Config{}, err
		}
	}
	nvmDir, err := toAbsPath(nvmRaw, "AGENT_SERVER_NVM_DIR")
	if err != nil {
		return Config{}, err
	}

	if !binExplicit && !isSubpath(binDir, root) {
		return Config{}, fmt.Errorf("AGENT_SERVER_BIN_DIR default must be under root %s", root)
	}
	if !stateExplicit && !isSubpath(stateDir, root) {
		return Config{}, fmt.Errorf("AGENT_SERVER_STATE_DIR default must be under root %s", root)
	}
	if !workspacesExplicit && !isSubpath(workspacesDir, root) {
		return Config{}, fmt.Errorf("AGENT_SERVER_WORKSPACES_DIR default must be under root %s", root)
	}
	if !configExplicit && !isSubpath(configDir, root) {
		return Config{}, fmt.Errorf("AGENT_SERVER_CONFIG_DIR default must be under root %s", root)
	}
	if !nvmExplicit && !isSubpath(nvmDir, stateDir) {
		return Config{}, fmt.Errorf("AGENT_SERVER_NVM_DIR default must be under state dir %s", stateDir)
	}

	listenAddr := defaultListenAddr
	if value, ok := os.LookupEnv("AGENT_SERVER_LISTEN"); ok {
		if strings.TrimSpace(value) == "" {
			return Config{}, fmt.Errorf("AGENT_SERVER_LISTEN cannot be empty when set")
		}
		listenAddr = strings.TrimSpace(value)
	}

	stopGrace, err := getDurationEnvStrict("AGENT_SERVER_STOP_GRACE_TIMEOUT", defaultStopGraceTimeout)
	if err != nil {
		return Config{}, err
	}
	collectTimeout, err := getDurationEnvStrict("AGENT_SERVER_TRAJECTORY_COLLECT_TIMEOUT", defaultCollectTimeout)
	if err != nil {
		return Config{}, err
	}
	collectPoll, err := getDurationEnvStrict("AGENT_SERVER_TRAJECTORY_POLL_INTERVAL", defaultCollectPoll)
	if err != nil {
		return Config{}, err
	}
	replayBufferBytes, err := getIntEnvStrict("AGENT_SERVER_REPLAY_BUFFER_BYTES", defaultReplayBufferBytes)
	if err != nil {
		return Config{}, err
	}
	snapshotMaxBytes, err := getIntEnvStrict("AGENT_SERVER_SNAPSHOT_MAX_BYTES", defaultSnapshotMaxBytes)
	if err != nil {
		return Config{}, err
	}
	snapshotTimeout, err := getDurationEnvStrict("AGENT_SERVER_SNAPSHOT_HARD_TIMEOUT", defaultSnapshotTimeout)
	if err != nil {
		return Config{}, err
	}
	snapshotIdleTimeout, err := getDurationEnvStrict("AGENT_SERVER_SNAPSHOT_IDLE_TIMEOUT", defaultSnapshotIdle)
	if err != nil {
		return Config{}, err
	}
	snapshotStopGrace, err := getDurationEnvStrict("AGENT_SERVER_SNAPSHOT_STOP_GRACE_TIMEOUT", defaultSnapshotStopGrace)
	if err != nil {
		return Config{}, err
	}
	nvmVersion, err := getTokenEnvStrict("AGENT_SERVER_NVM_VERSION", defaultNVMVersion)
	if err != nil {
		return Config{}, err
	}
	nodeBootstrapTimeout, err := getDurationEnvStrict("AGENT_SERVER_NODE_BOOTSTRAP_TIMEOUT", defaultNodeBootstrap)
	if err != nil {
		return Config{}, err
	}

	cfg := Config{
		ListenAddr:               listenAddr,
		RootDir:                  root,
		BinDir:                   binDir,
		StateDir:                 stateDir,
		WorkspacesDir:            workspacesDir,
		ConfigDir:                configDir,
		StopGraceTimeout:         stopGrace,
		TrajectoryCollectTimeout: collectTimeout,
		TrajectoryPollInterval:   collectPoll,
		ReplayBufferBytes:        replayBufferBytes,
		SnapshotMaxBytes:         snapshotMaxBytes,
		SnapshotHardTimeout:      snapshotTimeout,
		SnapshotIdleTimeout:      snapshotIdleTimeout,
		SnapshotStopGrace:        snapshotStopGrace,
		ExtraCACertsFile:         extraCACertsFile,
		NVMVersion:               nvmVersion,
		NVMDir:                   nvmDir,
		NodeBootstrapTimeout:     nodeBootstrapTimeout,
	}
	cfg.StateFile = filepath.Join(cfg.StateDir, defaultStateFileName)
	return cfg, nil
}

func getPathEnv(key, fallback string) (string, bool, error) {
	value, ok := os.LookupEnv(key)
	if !ok {
		return fallback, false, nil
	}
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", true, fmt.Errorf("%s cannot be empty when set", key)
	}
	return trimmed, true, nil
}

func toAbsPath(value, envKey string) (string, error) {
	absPath, err := filepath.Abs(value)
	if err != nil {
		return "", fmt.Errorf("resolve %s path %q: %w", envKey, value, err)
	}
	return filepath.Clean(absPath), nil
}

func getDurationEnvStrict(key string, fallback time.Duration) (time.Duration, error) {
	value, ok := os.LookupEnv(key)
	if !ok {
		return fallback, nil
	}
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return 0, fmt.Errorf("%s cannot be empty when set", key)
	}
	parsed, err := time.ParseDuration(trimmed)
	if err != nil {
		return 0, fmt.Errorf("parse %s=%q: %w", key, value, err)
	}
	if parsed <= 0 {
		return 0, fmt.Errorf("%s must be > 0", key)
	}
	return parsed, nil
}

func getIntEnvStrict(key string, fallback int) (int, error) {
	value, ok := os.LookupEnv(key)
	if !ok {
		return fallback, nil
	}
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return 0, fmt.Errorf("%s cannot be empty when set", key)
	}
	parsed, err := strconv.Atoi(trimmed)
	if err != nil {
		return 0, fmt.Errorf("parse %s=%q: %w", key, value, err)
	}
	if parsed <= 0 {
		return 0, fmt.Errorf("%s must be > 0", key)
	}
	return parsed, nil
}

func getTokenEnvStrict(key string, fallback string) (string, error) {
	value, ok := os.LookupEnv(key)
	if !ok {
		return fallback, nil
	}
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", fmt.Errorf("%s cannot be empty when set", key)
	}
	if strings.ContainsAny(trimmed, " \t\r\n") {
		return "", fmt.Errorf("%s must not contain whitespace", key)
	}
	return trimmed, nil
}

func isSubpath(candidatePath, rootPath string) bool {
	rel, err := filepath.Rel(rootPath, candidatePath)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	if rel == ".." {
		return false
	}
	return !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}
