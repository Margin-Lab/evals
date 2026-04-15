package store

import "strings"

const (
	ArtifactRoleOracleStdout = "oracle_stdout"
	ArtifactRoleOracleStderr = "oracle_stderr"
	ArtifactRoleTestStdout   = "test_stdout"
	ArtifactRoleTestStderr   = "test_stderr"
	ArtifactRoleTrajectory   = "trajectory"
	ArtifactRoleDockerBuild  = "executor_docker_build_log"
	ArtifactRoleAgentBoot    = "executor_agent_server_bootstrap_log"
	ArtifactRoleAgentControl = "executor_agent_server_control_log"
	ArtifactRoleAgentRuntime = "executor_agent_server_runtime_log"
	ArtifactRoleAgentPTY     = "executor_agent_server_pty_log"
)

// DefaultArtifactFilename returns the canonical file name for known artifact roles.
func DefaultArtifactFilename(role string) (string, bool) {
	switch strings.TrimSpace(role) {
	case ArtifactRoleOracleStdout:
		return "oracle_stdout.txt", true
	case ArtifactRoleOracleStderr:
		return "oracle_stderr.txt", true
	case ArtifactRoleTestStdout:
		return "test_stdout.txt", true
	case ArtifactRoleTestStderr:
		return "test_stderr.txt", true
	case ArtifactRoleTrajectory:
		return "trajectory.json", true
	case ArtifactRoleDockerBuild:
		return "docker_build.log", true
	case ArtifactRoleAgentBoot:
		return "agent_server_bootstrap.log", true
	case ArtifactRoleAgentControl:
		return "agent_server_control.log", true
	case ArtifactRoleAgentRuntime:
		return "agent_server_runtime.log", true
	case ArtifactRoleAgentPTY:
		return "agent_server_pty.log", true
	default:
		return "", false
	}
}
