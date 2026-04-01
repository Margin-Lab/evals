# Opencode Definition: Commands by Stage

This is the current repo-owned Opencode flow implemented by `configs/agent-definitions/opencode/`.

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
<install_dir>/bin/opencode --version
```

It returns JSON describing whether the installed version satisfies `config.input.opencode_version`.

## Install Run

If installation is needed, the hook runs:

```bash
npm install --global --prefix <install_dir> opencode-ai[@<version>]
```

Then it probes:

```bash
<install_dir>/bin/opencode --version
```

## Run Prepare

The run hook:

1. uses `config.input.provider` as the authoritative provider selector for auth resolution
2. writes `config.input.config_jsonc` to `<run_home>/.opencode/opencode.jsonc`
3. sets `OPENCODE_CONFIG=<run_home>/.opencode/opencode.jsonc`
4. returns this launch command:

```bash
<bin_path> run --format=json <startup_args...> <run_args...> -- "<initial_prompt>"
```

There is no extra preflight subprocess in the repo-owned definition.

## Snapshot Support

Opencode does not define `snapshot.prepare`.

- `supports_snapshot` is `false` in `GET /v1/state`
- `POST /v1/run/snapshot` returns `SNAPSHOT_UNSUPPORTED`

## Trajectory Collect

The repo-owned trajectory hook parses the JSON lines written to the run PTY log, synthesizes the initial user step from `run.initial_prompt`, converts the result to `ATIF-v1.6`, and returns the ATIF payload on stdout.
