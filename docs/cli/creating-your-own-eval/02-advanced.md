# Advanced Eval Configuration

## Choosing your environment

**Option A: Pre-built image** — set `image` in `case.toml` to a digest-pinned reference. Faster and reproducible.

**Option B: Dockerfile** — remove `image` from `case.toml` and add `env/Dockerfile`. The runner builds it before each run. Use `--cleanup-built-images` to remove images after the run.

## Using the oracle directory

Place reference files (expected outputs, fixtures) in `oracle/`. They're available in the container at test time:

```
cases/fix-null-check/
  oracle/
    expected-output.json
  tests/
    test.sh
```

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
