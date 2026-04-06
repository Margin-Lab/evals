# CLI

The CLI now works with two explicit resources:

- `agent_definition`
- `agent_config`

Print the installed CLI version with:

```bash
margin --version
```

Initialize them with:

```bash
margin init agent-definition --definition ./configs/agent-definitions/my-agent
margin init agent-config --agent-config ./configs/agent-configs/my-agent-default --definition ./configs/agent-definitions/my-agent
```

Run with:

```bash
margin run \
  --suite ./suites/smoke \
  --agent-config ./configs/agent-configs/my-agent-default \
  --eval ./configs/evals/local.toml
```

`--suite` also accepts a public HTTPS Git repo URL. Use plain HTTPS for a suite at repo root, or `git::https://...//subdir` when the suite lives below repo root:

```bash
margin run \
  --suite git::https://github.com/Margin-Lab/test-suites.git//swe-minimal-test-suite \
  --agent-config ./configs/agent-configs/my-agent-default \
  --eval ./configs/evals/local.toml
```

Remote suites are fetched once into `~/.margin/suites/.remote/` and stay pinned to the resolved commit until you explicitly refresh them:

```bash
margin suite pull \
  --suite git::https://github.com/Margin-Lab/test-suites.git//swe-minimal-test-suite
```

Before the first run, verify Docker is installed and usable:

```bash
margin check
```

`margin check` verifies that the `docker` binary is on `PATH`, that the Docker daemon responds, and that a `hello-world` container can be started successfully.

Installer-managed starter assets are installed at:

- `~/.margin/configs`

Use those installed starter assets by passing their explicit paths:

- `--agent-config ~/.margin/configs/example-agent-configs/codex-unified`
- `--eval ~/.margin/configs/example-eval-configs/default.toml`

Official suites are hosted in `https://github.com/Margin-Lab/swe-suites.git` and are referenced directly through `--suite`, for example:

- `git::https://github.com/Margin-Lab/swe-suites.git//swe-bench-verified`
- `git::https://github.com/Margin-Lab/swe-suites.git//swe-bench-pro`
- `git::https://github.com/Margin-Lab/swe-suites.git//swe-bench-pro-curated-50`
- `git::https://github.com/Margin-Lab/swe-suites.git//terminal-bench-2`

Resume an existing local run with its saved bundle:

```bash
margin run \
  --resume-from run_123 \
  --root .
```

When `--resume-from` is set, the CLI loads `runs/<run-id>/internal/bundle.json` from the selected `--root` and does not accept `--suite`, `--agent-config`, or `--eval`.

To skip agent execution while still running the case tests and avoiding token usage, add `--dry-run`:

```bash
margin run \
  --suite ./suites/smoke \
  --agent-config ./configs/agent-configs/my-agent-default \
  --eval ./configs/evals/local.toml \
  --dry-run
```

Dry-run validates the prelaunch path through `run.prepare`, skill materialization, auth-file setup, and `agents.md` writing, skips agent execution, and still runs the case tests against the pristine workspace.

The `margin` binary embeds the supported Linux `agent-server` payloads and extracts the required one into the user cache on demand.

Use `--agent-server-binary /path/to/agent-server` only to force one exact host-side binary path.

In interactive mode, `margin run` asks for a pre-run confirmation before submission. It reports whether the selected agent will use an API key or an OAuth credential source, and repeats the Docker prune warning when `--prune-built-image` is enabled.

For repo-owned Codex, Claude Code, and Gemini CLI configs, the local runner auto-discovers OAuth credentials from the sources declared by the selected definition:

- Codex: `~/.codex/auth.json`
- Claude Code: macOS Keychain item `Claude Code-credentials`, then `~/.claude/.credentials.json`
- Gemini CLI: `~/.gemini/oauth_creds.json`

If the corresponding provider API key is available, it still takes precedence. To override the discovered OAuth file for the selected agent, pass:

```bash
margin run ... --auth-file-path /path/to/credentials.json
```

Removed:

- `margin init agent`
- `margin run --agent ...`

`agent_config/config.toml` now requires an explicit mode:

- `mode = "direct"` with `[input]`
- `mode = "unified"` with `[unified]`
