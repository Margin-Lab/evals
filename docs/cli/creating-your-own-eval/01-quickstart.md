# Creating Your Own Eval — Quickstart

Scaffold a suite, add a test case, and run it.

```bash
# Create a suite
margin init suite --suite ./suites/my-suite

# Add a test case
margin init case --suite ./suites/my-suite --case fix-null-check

# Run it
margin run \
  --suite ./suites/my-suite \
  --agent-config ~/.margin/configs/example-agent-configs/claude-code-default \
  --eval ./my-eval.toml
```

## Suite structure

```
my-suite/
  suite.toml              # Manifest listing all cases
  preamble-prompt.md      # Optional: prepended to every case prompt
  cases/
    fix-null-check/
      case.toml            # Case metadata and image config
      prompt.md            # Case-specific task description given to the agent
      tests/
        test.sh            # Verification script (exit 0 = pass)
      env/                 # Optional: Dockerfile if no pre-built image
        Dockerfile
      oracle/              # Optional: oracle solution assets
        solve.sh           # Required when oracle/ exists; used by --oracle-run
```

## 1. Scaffold the suite

```bash
margin init suite --suite ./suites/my-suite
```

Generates `suite.toml`:

```toml
kind = "test_suite"
name = "my-suite"
description = "Fast pre-merge suite"

cases = []
```

## 2. Add a test case

```bash
margin init case --suite ./suites/my-suite --case fix-null-check
```

Creates the case directory with template files and adds `"fix-null-check"` to `suite.toml`'s `cases` list. Omit `--case` to auto-generate a name.

Scaffolded `case.toml`:

```toml
kind = "test_case"
name = "fix-null-check"
description = "Describe what this case validates"

image = "ghcr.io/acme/repo@sha256:0123456789abcdef..."
agent_cwd = "/work"
test_cwd = "/work"
test_timeout_seconds = 900
```

## 3. Write the prompt

Edit `prompt.md` with the case-specific task description given to the agent:

```markdown
# Task

The `getUserById` function in `src/users.ts` crashes with a null pointer
exception when called with an ID that doesn't exist in the database.

Fix the function so that it returns `null` instead of crashing when the
user is not found.

**Repo:** `my-org/my-app`
**File:** `src/users.ts`
```

Be specific about what needs to change, describe expected behavior, and mention relevant files.

If you want shared instructions across the whole suite, add `preamble-prompt.md` at the suite root. When present, Margin prepends its contents to every case's `prompt.md` using raw concatenation with a blank line between them.

## 4. Write the test script

Edit `tests/test.sh`. Exit code 0 = pass, anything else = fail:

```bash
#!/usr/bin/env bash
set -euo pipefail

cd /work
npm test -- --grep "getUserById"
```

The agent starts in `agent_cwd`. The verification script runs inside the container after the agent finishes, in `test_cwd`, with a `test_timeout_seconds` hard limit.

## 5. Validate and run

```bash
# Validate without spending tokens
margin run \
  --suite ./suites/my-suite \
  --agent-config ~/.margin/configs/example-agent-configs/claude-code-default \
  --eval ./my-eval.toml \
  --dry-run

# Apply the oracle solution, then run tests
margin run \
  --suite ./suites/my-suite \
  --agent-config ~/.margin/configs/example-agent-configs/claude-code-default \
  --eval ./my-eval.toml \
  --oracle-run

# Run for real
margin run \
  --suite ./suites/my-suite \
  --agent-config ~/.margin/configs/example-agent-configs/claude-code-default \
  --eval ./my-eval.toml
```

`--dry-run` skips agent execution and runs tests against the pristine workspace.

`--oracle-run` skips agent execution, runs `oracle/solve.sh`, and then runs the normal case tests. Every selected case must define `oracle/solve.sh`.

## Next steps

- [Advanced Eval Configuration](./02-advanced.md) — environments, oracle solutions, metadata
