# Official Suite Registry

Margin supports both local suites and remote suites.

- Use a local path like `./suites/my-suite` when you are building or iterating on your own evals.
- Use a remote Git reference when you want to run a published suite from Margin's hosted collections.

## Official hosted suites

Margin's official coding-agent benchmark suites are hosted in:

`https://github.com/Margin-Lab/swe-suites.git`

Reference a suite from that repo with `--suite`:

```bash
margin run \
  --suite git::https://github.com/Margin-Lab/swe-suites.git//swe-bench-pro \
  --agent-config example-agent-configs/claude-code-default \
  --eval example-eval-configs/default.toml
```

The `git::...//subdir` form means:

- clone or fetch the Git repo at the URL
- use the suite located at the given subdirectory

## Available official suites

| Suite | Example `--suite` value |
|---|---|
| `swe-bench-verified` | `git::https://github.com/Margin-Lab/swe-suites.git//swe-bench-verified` |
| `swe-bench-pro` | `git::https://github.com/Margin-Lab/swe-suites.git//swe-bench-pro` |
| `swe-bench-pro-curated-50` | `git::https://github.com/Margin-Lab/swe-suites.git//swe-bench-pro-curated-50` |
| `terminal-bench-2` | `git::https://github.com/Margin-Lab/swe-suites.git//terminal-bench-2` |

## Small example suites

For smoke tests and quick validation, Margin also publishes smaller example suites in:

`https://github.com/Margin-Lab/test-suites.git`

For example:

```bash
margin run \
  --suite git::https://github.com/Margin-Lab/test-suites.git//swe-minimal-test-suite \
  --agent-config example-agent-configs/codex-unified \
  --eval example-eval-configs/default.toml
```

This is the suite used in the quickstart docs because it is faster and cheaper than the larger benchmark suites.

## How remote suite fetching works

When you pass a remote suite reference, Margin fetches it into:

`~/.margin/suites/.remote/`

The fetched suite stays pinned to the resolved commit so repeated runs are reproducible. To refresh the local cached copy explicitly, run:

```bash
margin suite pull \
  --suite git::https://github.com/Margin-Lab/swe-suites.git//swe-bench-pro
```

You can also point `--suite` at a public HTTPS Git repo directly:

- plain `https://...` when the suite is at repo root
- `git::https://...//subdir` when the suite is in a subdirectory

## Choosing between local and hosted suites

Use hosted suites when you want standardized public benchmarks and comparable results across runs or agents.

Use local suites when you want:

- private or internal tasks
- fast iteration on prompts, tests, or environments
- custom pass/fail logic for your own workflow
