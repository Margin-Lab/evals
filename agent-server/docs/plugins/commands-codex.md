# Codex Definition: Commands by Stage

This is the current repo-owned Codex flow implemented by `configs/agent-definitions/codex/`.

## Toolchain

The manifest requests managed Node/npm:

```toml
[toolchains.node]
minimum = "16"
preferred = "24"
```

`agent-server` ensures that toolchain exists and prepends it to `PATH` before running hooks.

## Install Check

The install check looks for an existing binary and probes its version:

```bash
<install_dir>/bin/codex --version
```

It returns JSON describing whether the installed version satisfies `config.input.codex_version`.

## Install Run

If installation is needed, the hook runs:

```bash
npm install --global --prefix <install_dir> @openai/codex[@<version>]
```

Then it probes:

```bash
<install_dir>/bin/codex --version
```

The hook returns JSON with `installed`, `bin_path`, `version`, `install_method`, and `package`.

## Run Prepare

The run hook:

1. writes `config.input.config_toml` to `<run_home>/.codex/config.toml`
2. if `OPENAI_API_KEY` is present and `<run_home>/.codex/auth.json` does not exist, runs:

```bash
<bin_path> login --with-api-key
```

If `<run_home>/.codex/auth.json` was already materialized by the caller, Codex can launch without `OPENAI_API_KEY`.

3. returns this launch command:

```bash
<bin_path> exec --dangerously-bypass-approvals-and-sandbox --skip-git-repo-check --enable unified_exec <startup_args...> <run_args...> -- "<initial_prompt>"
```

The login subprocess writes normal terminal output to `stderr` so hook `stdout` stays JSON-only.

## Snapshot Prepare

The snapshot hook returns:

```bash
<bin_path> resume <session_id-or---last> --no-alt-screen --dangerously-bypass-approvals-and-sandbox
```

If the caller provides a snapshot session id, it is used. Otherwise the hook falls back to `--last`.

## Trajectory Collect

The repo-owned trajectory hook reads the Codex session JSONL under `CODEX_HOME/sessions`, converts it to `ATIF-v1.6`, and returns the final ATIF payload on stdout.
