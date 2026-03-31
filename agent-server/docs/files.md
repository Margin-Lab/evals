# agent-server Files

## Main Entrypoints

- `cmd/agent-server/`: process entrypoint and HTTP server wiring
- `README.md`: package-level overview and test instructions
- `docs/`: architecture, API, built-in profile, and trajectory docs

## Runtime Packages

- `internal/agentruntime/`
  - definition materialization
  - direct and unified config resolution
  - config translation and validation
  - hook execution
  - install/run/snapshot/trajectory hook orchestration
- `internal/api/`
  - HTTP routing
  - request validation
  - JSON response and error mapping
- `internal/run/`
  - run startup
  - PTY process supervision
  - snapshot capture
  - trajectory collection
- `internal/ptyws/`
  - WebSocket PTY fanout
  - replay buffer
  - resize/input protocol
- `internal/state/`
  - persisted server state
  - agent/run state transitions
- `internal/config/`
  - env-driven server configuration
  - path and timeout defaults
- `internal/noderuntime/`
  - managed Node/npm bootstrap used by definitions that declare `[toolchains.node]`
- `internal/apperr/`
  - structured API error envelopes
- `internal/fsutil/`
  - filesystem helpers for safe writes and path validation
- `internal/logutil/`
  - structured logging helpers

## Tests

- `integration/`
  - Docker-backed end-to-end coverage
  - real-agent matrix tests behind `integration_model`
- `integration/testdata/`
  - integration image build context

## Related Repo Areas

These are outside `agent-server/`, but they are the main inputs consumed by the runtime:

- `configs/agent-definitions/`: repo-owned definition packages
- `configs/example-agent-configs/`: repo-owned direct and unified config profiles
- `runner/runner-core/agentdef/`: shared definition/config snapshot types

## Legacy Code

`internal/plugin/` still exists on disk as legacy code, but the live server path no longer uses it. The active runtime is the generic `internal/agentruntime/` path.
