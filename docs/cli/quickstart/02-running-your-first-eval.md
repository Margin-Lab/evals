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

Run the following command to start your first eval:

```bash
margin run \
  --suite git::https://github.com/Margin-Lab/test-suites.git//swe-minimal-test-suite \
  --agent-config ~/.margin/configs/example-agent-configs/codex-unified \
  --eval ~/.margin/configs/example-eval-configs/default.toml \
```

This run uses a minimal test suite, token usage will be low but not zero. The pre-run confirmation screen shows which credentials (OAuth or API key) will be used.

Press enter on the pre-run confirmation screen to start the eval. By default, run output is saved to `runs/<run-id>/` under your current working directory. Use `--output <path>` to write the run to an exact directory instead.

### Dry runs
Sometimes you may want to confirm an eval suite and agent definition will run properly without actually consuming tokens. Margin supports a `--dry-run` mode that skips agent execution but still runs the case tests against the pristine workspace:

```bash
margin run \
  --suite git::https://github.com/Margin-Lab/test-suites.git//swe-minimal-test-suite \
  --agent-config ~/.margin/configs/example-agent-configs/codex-unified \
  --eval ~/.margin/configs/example-eval-configs/default.toml \
  --dry-run \
```

## Remote suites

`--suite` can also point at a public HTTPS Git repo. Use plain HTTPS when the suite is at repo root, or `git::https://...//subdir` when it lives under a repo subdirectory:

```bash
margin run \
  --suite git::https://github.com/Margin-Lab/test-suites.git//swe-minimal-test-suite \
  --agent-config ~/.margin/configs/example-agent-configs/codex-unified \
  --eval ~/.margin/configs/example-eval-configs/default.toml
```

The first run fetches the suite into `~/.margin/suites/.remote/` and pins it to the resolved commit. Refresh it explicitly with:

```bash
margin suite pull \
  --suite git::https://github.com/Margin-Lab/test-suites.git//swe-minimal-test-suite
```

For the full list of official hosted benchmark suites and how the remote suite registry works, see [Official Suite Registry](./03-official-suite-registry.md).

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
margin run --resume-from ./runs/run_20260409_153022_1f3a9c2d
```

Resume uses the saved bundle from `<run-dir>/internal/bundle.json`, so you don't need to re-specify suite, agent config, or eval config.

If you want to retry the run with updated inputs, pass a fresh suite, agent config, and eval config together with `--resume-from`. Margin will warn before starting when the updated inputs differ from the saved run, then it will reuse prior results according to the current resume policy and run the remaining cases with the new inputs.

## Next steps

- [Configuring Your Agent](../configuration/01-configuring-your-agent.md)
- [Configuring Your Eval](../configuration/02-configuring-your-eval.md)
- [Official Suite Registry](./03-official-suite-registry.md)
- [Creating Your Own Eval](../creating-your-own-eval/01-quickstart.md)
