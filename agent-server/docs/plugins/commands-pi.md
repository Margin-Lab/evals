# Pi Definition: Commands by Stage

This is the current repo-owned Pi flow implemented by `configs/agent-definitions/pi/`.

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
<install_dir>/bin/pi --version
```

It returns JSON describing whether the installed version satisfies `config.input.pi_version`.

## Install Run

If installation is needed, the hook runs:

```bash
npm install --global --prefix <install_dir> @mariozechner/pi-coding-agent[@<version>]
```

Then it probes:

```bash
<install_dir>/bin/pi --version
```

The hook returns JSON with `installed`, `bin_path`, `version`, `install_method`, and `package`.

## Run Prepare

The run hook:

1. sets `PI_CODING_AGENT_DIR=<run_home>/.pi/agent`
2. creates a run-local session dir at `<run_home>/.pi/sessions`
3. returns this launch command:

```bash
<bin_path> <startup_args...> --mode json --session-dir <run_home>/.pi/sessions --provider <provider> --model <model> --thinking <thinking> <run_args...> "<initial_prompt>"
```

The command is wrapped in `bash -lc` and tees:

- stdout to `<artifacts_dir>/pi-events.jsonl`
- stderr to `<artifacts_dir>/pi.stderr.log`

## Snapshot Support

Pi does not define `snapshot.prepare`.

- `supports_snapshot` is `false` in `GET /v1/state`
- `POST /v1/run/snapshot` returns `SNAPSHOT_UNSUPPORTED`

## Trajectory Collect

The repo-owned trajectory hook parses `<artifacts_dir>/pi-events.jsonl`, converts the canonical Pi JSON event stream to `ATIF-v1.6`, and returns the ATIF payload on stdout.
