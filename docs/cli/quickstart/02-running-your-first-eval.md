# Running Your First Eval

## Prerequisites

Margin requires an API key or OAuth credentials for your agent provider (e.g. `ANTHROPIC_API_KEY` or OAuth for Claude Code)

### API Key
The API key can be set as an environment variable before running Margin:
```bash
export ANTHROPIC_API_KEY=<API_KEY>
```

### OAuth
Margin supports using OAuth credentials for running agents. This option can be used with monthly subscriptions such as Claude Code Pro/Max, Codex Pro, etc. 
If an API key is not set, Margin will automatically detect any valid OAuth credentials at their standard global agent paths.

To use a specific credential file:

```bash
margin run ... --auth-file-path /path/to/credentials.json
```

## Running your first eval

Installer-managed starter assets live at:

- `~/.margin/configs`

You can reference those installed starter configs by shorthand instead of the full `~/.margin/...` path.

Run the following command to start your first eval:

```bash
margin run \
  --suite git::https://github.com/Margin-Lab/swe-suites.git//terminal-bench-2 \
  --agent-config example-agent-configs/codex-unified \
  --eval example-eval-configs/default.toml \
```

Official suites are hosted in `https://github.com/Margin-Lab/swe-suites.git` and fetched on demand.

The pre-run confirmation screen shows which credentials (OAuth or API key) will be used. This example runs `terminal-bench-2` from the official remote suite collection.

Press enter on the pre-run confirmation screen to start the eval. By default, run output is saved to `runs/<run-id>/` under your current working directory.

By default, `margin` uses its embedded `agent-server` payloads and does not require any adjacent binaries. Use `--agent-server-binary` only to override the exact host-side binary path.

## Remote suites

`--suite` can also point at a public HTTPS Git repo. Use plain HTTPS when the suite is at repo root, or `git::https://...//subdir` when it lives under a repo subdirectory:

```bash
margin run \
  --suite git::https://github.com/Margin-Lab/swe-suites.git//terminal-bench-2 \
  --agent-config example-agent-configs/codex-unified \
  --eval example-eval-configs/default.toml
```

The first run fetches the suite into `~/.margin/suites/.remote/` and pins it to the resolved commit. Refresh it explicitly with:

```bash
margin suite pull \
  --suite git::https://github.com/Margin-Lab/swe-suites.git//terminal-bench-2
```

## Mission Control

The mission control TUI shows live status for each test instance with detailed logs on environment setup, agent output trace, and test results.

It has two panes: instance list (left) and detail/logs (right).

| Key | Action |
|---|---|
| `Tab` | Switch pane focus |
| `Up`/`Down` arrows | Navigate instances or scroll logs |
| `Left`/`Right` arrows | Change selected lifecycle state in detail pane |
| `g`/`G` | Jump to top/bottom of logs |
| `q` | Quit (prompts if run is in progress) |

## Resuming a Run

The default resume policy retries tests that failed for infrastructure reasons or were never started, and skips tests that already produced a result:

```bash
margin run --resume-from <run-id>
```

Resume uses the saved bundle from `runs/<run-id>/bundle.json`, so you don't need to re-specify suite, agent config, or eval config.

## Next steps

- [Configuring Your Agent](../configuration/01-configuring-your-agent.md)
- [Configuring Your Eval](../configuration/02-configuring-your-eval.md)
- [Creating Your Own Eval](../creating-your-own-eval/01-quickstart.md)
