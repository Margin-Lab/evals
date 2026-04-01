# agent-server

`agent-server` is the in-container runtime that executes one selected `agent_definition` + `agent_config` pair at a time. It materializes the packaged definition, resolves direct or unified config into one validated direct snapshot, runs install hooks, launches a PTY-backed agent process, serves terminal streaming over WebSocket, and persists run state plus artifacts.

`POST /v1/run` also supports a dry-run path that executes the full prelaunch setup and exits immediately without starting the agent PTY. This is intended for validating run preparation without token usage.

The repo-owned Codex, Claude Code, Gemini CLI, Opencode, and Pi integrations now use the same hook contract as any custom definition:

- `configs/agent-definitions/codex`
- `configs/agent-definitions/claude-code`
- `configs/agent-definitions/gemini-cli`
- `configs/agent-definitions/opencode`
- `configs/agent-definitions/pi`

## Main Documentation

- Architecture and lifecycle: `docs/design.md`
- HTTP and WebSocket API: `docs/endpoints.md`
- Required env and secret handling: `docs/secrets.md`
- Codebase map: `docs/files.md`
- Repo-owned config profiles: `docs/agent-config/`
- Repo-owned command flows: `docs/plugins/`
- Trajectory capture and ATIF contract: `docs/trajectory/`
- Unified config translation: `docs/unified-config.md`

## Runtime Model

`agent-server` keeps one loaded definition, one validated config, and one active run.

`PUT /v1/agent-config` accepts:

- direct config: definition-owned `input`
- unified config: shared `unified` input translated by the selected definition into direct `input`
- optional top-level packaged `skills` shared by both modes
- optional top-level `agents_md` content shared by both modes

The runtime sequence is:

1. `PUT /v1/agent-definition`
2. `PUT /v1/agent-config`
3. `POST /v1/agent/install`
4. `POST /v1/run`
5. optional `POST /v1/run/snapshot`
6. `GET /v1/run/pty` for live terminal attach

Dependencies are handled inside the case image plus the managed Node runtime declared by a definition's `[toolchains.node]`. There is no per-agent overlay Dockerfile layer.

`agent-server` uses its own trust bundle instead of the case image's CA store. Public roots are embedded in the binary, and operators can append private roots with `AGENT_SERVER_EXTRA_CA_CERTS_FILE`. The materialized bundle is exported to hook and agent processes via `SSL_CERT_FILE`, and to managed Node/npm via `NODE_EXTRA_CA_CERTS` and `NPM_CONFIG_CAFILE`.

When a definition declares a skill discovery root, `agent-server` materializes configured skill directories into the run `HOME` before `run.prepare` executes.

When a definition declares `agents_md.filename`, `agent-server` writes the configured markdown file into the actual project directory where the agent process starts.

## Build

From `agent-server/`:

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o ../bin/agent-server-linux-amd64 ./cmd/agent-server
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o ../bin/agent-server-linux-arm64 ./cmd/agent-server
```

## Runtime Configuration

Important env vars:

- `AGENT_SERVER_LISTEN` default `:8080`
- `AGENT_SERVER_ROOT` default `/marginlab`
- `AGENT_SERVER_BIN_DIR` default `/marginlab/bin`
- `AGENT_SERVER_STATE_DIR` default `/marginlab/state`
- `AGENT_SERVER_WORKSPACES_DIR` default `/marginlab/workspaces`
- `AGENT_SERVER_CONFIG_DIR` default `/marginlab/config`
- `AGENT_SERVER_EXTRA_CA_CERTS_FILE` optional PEM file appended to the bundled public trust roots
- `AGENT_SERVER_NVM_DIR` default `/marginlab/state/toolchain/nvm` (legacy name for the managed Node cache dir)

See `internal/config/config.go` for the full set of timeout and buffer settings.

## Tests

Run package tests:

```bash
go test ./...
```

Run Docker-backed integration tests:

```bash
go test -tags=integration ./integration/... -v
```

Run real-agent model integration tests from `agent-server/`:

```bash
set -a; source ../.env; set +a
go test -tags='integration integration_model' ./integration/... -v
```

Provider credentials used by the repo-owned definitions:

- `OPENAI_API_KEY` for Codex
- `ANTHROPIC_API_KEY` for Claude Code
- `GEMINI_API_KEY` or reusable `~/.gemini/oauth_creds.json` for Gemini CLI
- provider-qualified config for Opencode and Pi:
  - `openai/*` -> `OPENAI_API_KEY`
  - `anthropic/*` -> `ANTHROPIC_API_KEY`
  - `google/*` -> `GEMINI_API_KEY`
  - `*` -> no required secret env; use `--agent-env` for manual runtime variables

Repo-owned config profiles include both direct and unified examples:

- `configs/example-agent-configs/codex-default`
- `configs/example-agent-configs/codex-unified`
- `configs/example-agent-configs/claude-code-default`
- `configs/example-agent-configs/claude-code-unified`
- `configs/example-agent-configs/gemini-cli-default`
- `configs/example-agent-configs/gemini-cli-unified`
- `configs/example-agent-configs/opencode-default`
- `configs/example-agent-configs/opencode-unified`
- `configs/example-agent-configs/pi-default`
- `configs/example-agent-configs/pi-unified`

Integration harness env behavior:

- default dotenv file: repo-root `.env`
- override with `MARGINLAB_IT_DOTENV_FILE`
- optional version fanout:
  - `MARGINLAB_IT_CODEX_VERSIONS`
  - `MARGINLAB_IT_CLAUDE_CODE_VERSIONS`
  - `MARGINLAB_IT_GEMINI_CLI_VERSIONS`
  - `MARGINLAB_IT_OPENCODE_VERSIONS`
  - `MARGINLAB_IT_PI_VERSIONS`
- optional matrix override:
  - `MARGINLAB_IT_MATRIX_FILE`
- optional artifact export:
  - `AGENT_SERVER_IT_ARTIFACTS_DIR`
