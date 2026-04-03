# Claude Code Definition: Commands by Stage

This is the current repo-owned Claude Code flow implemented by `configs/agent-definitions/claude-code/`.

## Toolchain

The manifest requests managed Node/npm:

```toml
[toolchains.node]
minimum = "18"
preferred = "24"
```

## Install Check

The install check probes:

```bash
<install_dir>/bin/claude --version
```

Version comparison normalizes common `v` prefixes and version text before matching against `config.input.claude_version`.

## Install Run

If installation is needed, the hook runs:

```bash
npm install --global --prefix <install_dir> @anthropic-ai/claude-code[@<version>]
```

Then it probes:

```bash
<install_dir>/bin/claude --version
```

## Run Prepare

The run hook:

1. writes `config.input.settings_json` to `<run_home>/.claude/settings.json`
2. optionally merges `config.input.mcp_json` into `<run_home>/.claude/.claude.json`
3. writes `<run_home>/.claude.json` to mark onboarding complete
   - when `ANTHROPIC_API_KEY` is present, it also caches approval for the active API key
   - when `<run_home>/.claude/.credentials.json` was materialized by the caller, no API-key cache is written
4. sets `DISABLE_AUTOUPDATER=1`
5. launches Claude in stream-json mode, leaving stdout on the PTY so `agent-server` captures the JSON stream in `pty.log`
6. tees stderr to an artifact file

```bash
<bin_path> --dangerously-skip-permissions --verbose --output-format=stream-json --session-id <session_id> <startup_args...> <run_args...> -p "<initial_prompt>"
```

Wrapped as:

```bash
bash -c '<command> 2> >(tee <artifacts_dir>/claude.stderr.log >&2)'
```

## Snapshot Prepare

The snapshot hook returns:

```bash
<bin_path> --dangerously-skip-permissions --resume <session_id> -p "Repeat your last assistant response exactly and nothing else."
```

It also keeps `DISABLE_AUTOUPDATER=1` and `CLAUDE_CONFIG_DIR=<run_home>/.claude` in the snapshot env.

## Trajectory Collect

The repo-owned trajectory hook reads Claude session JSONL under `CLAUDE_CONFIG_DIR/projects`, converts it to `ATIF-v1.6`, and returns the final ATIF payload on stdout.
