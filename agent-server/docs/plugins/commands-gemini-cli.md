# Gemini CLI Definition: Commands by Stage

This is the current repo-owned Gemini CLI flow implemented by `configs/agent-definitions/gemini-cli/`.

## Toolchain

The manifest requests managed Node/npm:

```toml
[toolchains.node]
minimum = "20"
preferred = "24"
```

## Install Check

The install check probes:

```bash
<install_dir>/bin/gemini --version
```

It returns JSON describing whether the installed version satisfies `config.input.gemini_version`.

## Install Run

If installation is needed, the hook runs:

```bash
npm install --global --prefix <install_dir> @google/gemini-cli[@<version>]
```

Then it probes:

```bash
<install_dir>/bin/gemini --version
```

The hook returns JSON with `installed`, `bin_path`, `version`, `install_method`, and `package`.

## Run Prepare

The run hook:

1. writes `config.input.settings_json` to `<run_home>/.gemini/settings.json`
2. forces `context.fileName` to include both `AGENTS.md` and `GEMINI.md`
3. returns this launch command:

```bash
<bin_path> <startup_args...> --model <model> --output-format stream-json --approval-mode <approval_mode> <run_args...> -p "<initial_prompt>"
```

The command is wrapped in `bash -lc` and tees:

- stdout to `<artifacts_dir>/gemini-stream.jsonl`
- stderr to `<artifacts_dir>/gemini.stderr.log`

## Snapshot Support

Gemini CLI does not define `snapshot.prepare`.

- `supports_snapshot` is `false` in `GET /v1/state`
- `POST /v1/run/snapshot` returns `SNAPSHOT_UNSUPPORTED`

## Trajectory Collect

The repo-owned trajectory hook parses `<artifacts_dir>/gemini-stream.jsonl`, converts the canonical `stream-json` event stream to `ATIF-v1.6`, and returns the ATIF payload on stdout.
