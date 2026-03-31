# Unified Config

Unified config is supported again, but it is definition-mediated.

`agent-server` accepts one `agent_config` request shape with two explicit modes:

- `mode = "direct"`
  - request contains definition-owned `input`
- `mode = "unified"`
  - request contains shared `unified` input
  - the loaded definition translates that payload into direct `input`

Both modes may also include the same top-level `skills` array and optional `[agents_md]` content.

There is still no compatibility alias and no legacy `config_mode` field.

## Shared Unified Contract

Unified mode uses one shared payload shape:

- `model`
- `reasoning_level`
- optional `mcp.servers[]`

The selected definition advertises whether unified mode is supported and which values are allowed through:

- `config.unified.translate`
- `config.unified.allowed_models`
- `config.unified.allowed_reasoning_levels`

## Translation Flow

When `PUT /v1/agent-config` receives `mode = "unified"`, `agent-server`:

1. validates the shared unified payload
2. checks that the loaded definition supports unified mode
3. runs the definition's `config.unified.translate` hook
4. validates the translated direct `input` against the definition schema
5. runs the optional `config.validate` hook on the translated direct input
6. persists a config snapshot with:
   - `mode = "unified"`
   - `skills = <normalized packaged skills, if any>`
   - `agents_md = <raw markdown content, if any>`
   - `unified = <submitted normalized unified payload>`
   - `input = <resolved direct payload used by install/run hooks>`

Install, run, snapshot, and trajectory hooks always consume the resolved direct `input`.

## Repo-Owned Examples

Repo-owned profiles now include both direct and unified examples:

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
