---
name: agent-definition-creator
description: Creates or updates Margin Eval agent definitions for new CLI coding agents. Use this skill when Codex needs to add support for a new agent, scaffold a directory under `configs/agent-definitions/`, define schemas and hooks, add example agent configs, or review an existing definition for missing auth, unified-mode, install, snapshot, or trajectory behavior.
---

# Agent Definition Creator

Create Margin agent definitions by collecting the missing runtime facts first, selecting the nearest existing definition pattern second, and only then writing `definition.toml`, `schema.json`, hooks, and example configs.

## Preflight Checklist

Do not start writing files until these questions are answered from docs, `--help` output, installed CLI behavior, or user input:

- Agent name, binary name, and install source
- Version strategy: `latest`, exact version, semver range, or non-npm install
- Auth model: single API key, local OAuth file, keychain entry, provider-qualified auth, or no auth
- Direct config surface: the exact values the hooks need in `config.input`
- Launch command: binary, args, env vars, working directory, and non-interactive flags
- Structured output source: PTY-only, stdout JSONL, event stream, or session files on disk
- Snapshot capability: whether the agent can resume or provide a lightweight snapshot command
- Unified-mode mapping: whether shared `model`, `reasoning_level`, and `mcp.servers[]` can be translated cleanly
- Skills and instruction-file behavior: skill home dir, `AGENTS.md`, `CLAUDE.md`, or some other filename
- Toolchain/runtime needs for hooks and install: Node, Python, or other prerequisites
- Semantic validation constraints beyond JSON Schema: provider/model coupling, enums, mutually dependent fields

If any item is unknown, investigate it before writing hooks. Most definition failures come from guessing auth, launch flags, or trajectory sources.

## Definition Components

Every definition lives under:

```text
configs/agent-definitions/<agent>/
├── definition.toml
├── schema.json
└── hooks/
    ├── install-check.*
    ├── install-run.*
    ├── run-prepare.*
    ├── translate-unified.*      # optional
    ├── validate-config.*        # optional
    ├── snapshot-prepare.*       # optional
    └── trajectory-collect.*     # optional
```

Required pieces:

- `definition.toml`: declare auth, schema, hook paths, toolchains, and optional features
- `schema.json`: validate the direct-mode `[input]` shape
- `hooks/install-check.*`: report whether the agent is already installed
- `hooks/install-run.*`: install the agent and return install metadata
- `hooks/run-prepare.*`: write any runtime config files and return the launch exec spec

Optional pieces:

- `hooks/translate-unified.*`: map shared unified config into direct input
- `hooks/validate-config.*`: enforce semantic rules that JSON Schema cannot express
- `hooks/snapshot-prepare.*`: enable `POST /v1/run/snapshot`
- `hooks/trajectory-collect.*`: convert the agent's native logs or session data into ATIF

Common optional manifest sections:

- `[toolchains.node]`: declare managed Node/npm for JS hooks or npm-installed CLIs
- `[auth.local_credentials]`: support local OAuth or credential file discovery
- `[auth.provider_selection]` and `[[auth.providers]]`: support provider-qualified auth selection
- `[skills]`: tell `agent-server` where to materialize packaged skills inside run home
- `[agents_md]`: tell `agent-server` which instruction filename to write into the project root
- `[config.unified]`: advertise unified-mode translation and allowed values

## Reference Files To Read

Read these repo files before creating or updating a definition:

- `docs/cli/add-support-for-a-new-agent/01-overview.md`
- `agent-server/docs/design.md`
- `agent-server/docs/unified-config.md`
- `agent-server/docs/agent-config/*.md`
- `agent-server/docs/plugins/commands-*.md`
- `configs/agent-definitions/*/definition.toml`
- `configs/example-agent-configs/*/config.toml`

## Workflow

### 1. Choose the nearest template

Do not start from a blank definition if a repo-owned definition already matches the agent's shape.

- Use the Codex pattern for single-provider agents with a config file and resumable session files
- Use the Claude Code pattern for single-provider agents with JSON settings and snapshot support
- Use the Gemini CLI pattern for agents that emit a structured stdout event stream but do not support snapshots
- Use the Opencode pattern for provider-qualified models plus config-file validation
- Use the Pi pattern for provider-qualified models where reasoning maps directly to a native runtime flag

### 2. Scaffold the directories

Run:

```bash
margin init agent-definition --definition ./configs/agent-definitions/<agent>
margin init agent-config --agent-config ./configs/example-agent-configs/<agent>-default --definition ./configs/agent-definitions/<agent>
```

If unified mode will be supported, also plan to add `configs/example-agent-configs/<agent>-unified`.

### 3. Design direct mode first

Design `schema.json` around the exact fields the hooks need, not around the shared unified format.

Prefer:

- explicit scalar or enum fields when the agent already has stable CLI flags
- string fields like `settings_json`, `config_jsonc`, or `config_toml` only when the agent truly consumes a raw config file
- a dedicated `provider` field when auth or model resolution depends on provider selection

Keep the direct input minimal. Every field should be used by install, run, snapshot, or validation hooks.

### 4. Write `definition.toml`

Declare:

- `kind`, `name`, `description`
- auth mode
- config schema path
- hook paths
- toolchains if needed
- optional skills, instruction filename, unified mode, snapshot, and trajectory support

Rules:

- declare `[toolchains.node]` whenever hooks are JS or install uses npm
- only declare `[snapshot]` if the agent can actually support snapshot collection
- only declare `[config.unified]` if translation is real, not aspirational
- use `[auth.provider_selection]` when required env depends on provider

### 5. Implement the hooks

All hooks:

- read `AGENT_CONTEXT_JSON`
- write only the expected JSON payload to stdout
- treat stderr as logs

Implement them in this order:

1. `install-check`
   Return installed status and any version details after probing the binary.
2. `install-run`
   Install the requested version, probe again, and return structured install metadata.
3. `run-prepare`
   Write config files into run home, set env vars, and return `{path,args,env,dir}`.
4. `validate-config` if needed
   Reject semantic mismatches such as provider/model disagreement.
5. `translate-unified` if supported
   Translate shared unified input into direct `config.input`.
6. `snapshot-prepare` if supported
   Return the command used for snapshot capture.
7. `trajectory-collect` if supported
   Convert native logs or session files into valid ATIF.

### 6. Add example configs

Create at least one direct config under `configs/example-agent-configs/<agent>-default/`.

Add a unified example only if:

- the agent can map shared `model`
- the agent can map or intentionally ignore shared `reasoning_level`
- the translator can render unified MCP servers if the agent supports them

### 7. Validate and smoke test

Run a dry-run first:

```bash
margin run \
  --suite ./suites/swe-minimal-test-suite \
  --agent-config ./configs/example-agent-configs/<agent>-default \
  --eval ./configs/example-eval-configs/default.toml \
  --dry-run
```

Then run a real smoke test if credentials are available and inspect the produced artifacts.

## Gotchas

- Do not mix unified and direct responsibilities. Install, run, snapshot, and trajectory hooks always consume resolved direct input.
- Do not write logs to hook stdout. Any stray text breaks the JSON contract.
- Do not guess auth precedence. Mirror the actual CLI's behavior for API keys versus local OAuth credentials.
- Do not overfit direct config to one example profile. Keep the schema reusable across versions and models.
- Do not claim snapshot support unless the agent has a real resumable or snapshot command.
- Do not assume `reasoning_level` means the same thing across agents. Some translators map it directly, some render it into config, and some must ignore it.
- Do not duplicate provider information in conflicting places. Add a validate hook when fields must agree.
- Do not write runtime config files into the project root unless the agent requires that. Most belong in run home.
- Do not forget skill and instruction-file integration. `skills.home_rel_dir` and `agents_md.filename` must match what the agent actually reads.
- Do not leave version checks fuzzy. Install hooks should probe the installed binary after installation and report the resolved version.
- Do not hardcode trajectory collection to stdout if the agent's real machine-readable history lives in session files or a tee'd artifact.
- Do not expose unsupported MCP translation. Only translate MCP servers if the target CLI can actually consume them.

## Finish Checklist

- `definition.toml` matches the actual auth and capability model
- `schema.json` matches direct-mode input
- hook paths in `definition.toml` exist and are executable
- example direct config exists
- example unified config exists only if supported
- dry-run passes
- real smoke test passes if credentials are available
- trajectory output validates if `[trajectory]` is declared
- snapshot behavior works if `[snapshot]` is declared
