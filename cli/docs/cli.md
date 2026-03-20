# CLI

The CLI now works with two explicit resources:

- `agent_definition`
- `agent_config`

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

Before the first run, verify Docker is installed and usable:

```bash
margin check
```

`margin check` verifies that the `docker` binary is on `PATH`, that the Docker daemon responds, and that a `hello-world` container can be started successfully.

Resume an existing local run with its saved bundle:

```bash
margin run \
  --resume-from run_123 \
  --root .
```

When `--resume-from` is set, the CLI loads `runs/<run-id>/bundle.json` from the selected `--root` and does not accept `--suite`, `--agent-config`, or `--eval`.

To validate setup without starting the agent or spending tokens, add `--dry-run`:

```bash
margin run \
  --suite ./suites/smoke \
  --agent-config ./configs/agent-configs/my-agent-default \
  --eval ./configs/evals/local.toml \
  --dry-run
```

Dry-run validates the prelaunch path through `run.prepare`, skill materialization, auth-file setup, and `agents.md` writing, then skips agent execution and case tests.

The `margin` binary embeds the supported Linux `agent-server` payloads and extracts the required one into the user cache on demand.

Use `--agent-server-binary /path/to/agent-server` only to force one exact host-side binary path.

In interactive mode, `margin run` asks for a pre-run confirmation before submission. It reports whether the selected agent will use an API key or OAuth credential file, and repeats the Docker prune warning when `--prune-built-image` is enabled.

For repo-owned Codex and Claude Code configs, the local runner auto-discovers OAuth credentials from the standard home-directory files declared by the selected definition:

- Codex: `~/.codex/auth.json`
- Claude Code: `~/.claude/.credentials.json`

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
