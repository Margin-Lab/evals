# Configuring Your Eval

Control concurrency, timeouts, and fail-fast behavior for your eval runs.

```bash
# Scaffold a new eval config
margin init eval-config --eval ./configs/evals/fast.toml --name "fast-feedback"
```

## Quick examples

**Fast iteration — stop on first failure:**

```toml
kind = "eval_config"
name = "fast-feedback"
max_concurrency = 1
fail_fast = true
retry_count = 1
instance_timeout_seconds = 600
```

**Full benchmark — parallel execution:**

```toml
kind = "eval_config"
name = "full-benchmark"
max_concurrency = 6
fail_fast = false
retry_count = 1
instance_timeout_seconds = 1800
```

## Field reference

| Field | Required | Description |
|---|---|---|
| `kind` | Yes | Must be `"eval_config"` |
| `name` | No | Display name (defaults to filename) |
| `description` | No | Free-text description |
| `max_concurrency` | Yes | Max parallel Docker containers. Must be > 0 |
| `fail_fast` | No | Stop scheduling after first failure. Default: `false` |
| `retry_count` | No | Number of infra retries after the first attempt. Default: `1`. Must be >= 0 |
| `instance_timeout_seconds` | Yes | Hard ceiling per instance (build + bootstrap + agent + test). Must be > 0 |

## Choosing concurrency

| Scenario | Suggested `max_concurrency` |
|---|---|
| First run / debugging | `1` |
| Local development | `2`–`4` |
| Full benchmark | `4`–`8` (resource-dependent) |

Higher concurrency finishes faster but uses more CPU/memory and may hit API rate limits.

## Setting timeouts

`instance_timeout_seconds` covers the full instance lifecycle. Set it higher than the longest test case's `test_timeout_seconds`, plus buffer for image build and agent startup.

| Suite type | Suggested timeout |
|---|---|
| Quick smoke tests | `300`–`600` |
| Standard coding tasks | `1800` (30 min) |
| Complex multi-step tasks | `3600`+ |

## Run-level timeout

Cap the entire run's wall-clock time with a CLI flag:

```bash
margin run \
  --suite ./suites/my-suite \
  --agent-config ~/.margin/configs/example-agent-configs/claude-code-default \
  --eval ./configs/evals/my-eval.toml \
  --run-timeout 2h
```

Accepts Go duration strings: `30m`, `2h`, `1h30m`.

## Next steps

- [Configuring Your Agent](./01-configuring-your-agent.md)
- [Creating Your Own Eval](../creating-your-own-eval/01-quickstart.md)
