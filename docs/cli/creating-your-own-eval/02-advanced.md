# Advanced Eval Configuration

## Choosing your environment

**Option A: Pre-built image** — set `image` in `case.toml` to a digest-pinned reference. Faster and reproducible.

**Option B: Dockerfile** — remove `image` from `case.toml` and add `env/Dockerfile`. The runner builds it before each run. Use `--cleanup-built-images` to remove images after the run.

## Using oracle solutions

Use `oracle/` when you want a case-local reference implementation that Margin can apply with `--oracle-run`.

```
cases/fix-null-check/
  oracle/
    solve.sh
    expected-output.json
```

Rules:

- `oracle/solve.sh` is required whenever `oracle/` exists.
- Any additional files under `oracle/` are staged alongside `solve.sh`.
- In `--oracle-run`, Margin provisions the container, runs `oracle/solve.sh`, and then runs the normal case `tests/test.sh`.
- All selected cases must define `oracle/solve.sh` when `--oracle-run` is used.

At runtime, `oracle/solve.sh` executes with:

- working directory: `test_cwd`
- `MARGIN_AGENT_CWD`: the case `agent_cwd`
- `MARGIN_TEST_CWD`: the case `test_cwd`
- `MARGIN_ORACLE_DIR`: the staged oracle directory inside the container

Use this when you want a gold implementation that mutates the repo before grading, not just passive fixture files.

## Using a suite preamble

Add `preamble-prompt.md` at the suite root when you want suite-wide instructions applied to every case:

```
my-suite/
  preamble-prompt.md
  cases/
    fix-null-check/
      prompt.md
```

When present, Margin builds the agent's initial prompt as:

1. `preamble-prompt.md`
2. a blank line
3. the case's `prompt.md`

If `preamble-prompt.md` is absent, nothing changes. If it exists, it must contain non-whitespace content.

## Writing test scripts

Test scripts can combine existing test suites with inline assertions:

```bash
#!/usr/bin/env bash
set -euo pipefail

cd /work

# Run existing tests
npm test -- --grep "getUserById"

# Or write inline assertions
node -e "
const { getUserById } = require('./src/users');
const result = getUserById('nonexistent-id');
if (result !== null) {
  console.error('Expected null, got:', result);
  process.exit(1);
}
console.log('PASSED');
"
```

## Adding metadata

```toml
[metadata]
author_name = "jane"
difficulty = "<15 min fix"
category = "debugging"
tags = ["null-safety", "typescript"]
```

## Next steps

- [Add Support for a New Agent](../add-support-for-a-new-agent/01-overview.md)
