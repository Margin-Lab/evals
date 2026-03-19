package apperr

const (
	CodeInternalError      = "INTERNAL_ERROR"
	CodeNotReady           = "NOT_READY"
	CodeInvalidRequest     = "INVALID_REQUEST"
	CodeServerShuttingDown = "SERVER_SHUTTING_DOWN"

	CodeInvalidAgent       = "INVALID_AGENT"
	CodeInstallFailed      = "INSTALL_FAILED"
	CodeConfigValidation   = "CONFIG_VALIDATION_FAILED"
	CodeInvalidAgentState  = "INVALID_AGENT_STATE"
	CodeAgentNotConfigured = "AGENT_NOT_CONFIGURED"
	CodeAgentStateStale    = "AGENT_STATE_STALE"

	CodeInvalidCWD           = "INVALID_CWD"
	CodeInvalidInitialPrompt = "INVALID_INITIAL_PROMPT"
	CodeInvalidPTY           = "INVALID_PTY"
	CodeInvalidRunArgs       = "INVALID_RUN_ARGS"
	CodeInvalidEnv           = "INVALID_ENV"
	CodeMissingRequiredEnv   = "MISSING_REQUIRED_ENV"
	CodeInvalidRunID         = "INVALID_RUN_ID"

	CodeRunAlreadyActive      = "RUN_ALREADY_ACTIVE"
	CodeRunNotActive          = "RUN_NOT_ACTIVE"
	CodeRunNotCleared         = "RUN_NOT_CLEARED"
	CodeRunNotClearable       = "RUN_NOT_CLEARABLE"
	CodeRunIDMismatch         = "RUN_ID_MISMATCH"
	CodeInvalidRunState       = "INVALID_RUN_STATE"
	CodeRunStateStale         = "RUN_STATE_STALE"
	CodeTrajectoryUnavailable = "TRAJECTORY_UNAVAILABLE"
	CodeSnapshotUnsupported   = "SNAPSHOT_UNSUPPORTED"
	CodeSnapshotTimeout       = "SNAPSHOT_TIMEOUT"
	CodeSnapshotFailed        = "SNAPSHOT_FAILED"

	CodePrelaunchFailed = "PRELAUNCH_FAILED"
	CodeRunStopFailed   = "RUN_STOP_FAILED"
)
