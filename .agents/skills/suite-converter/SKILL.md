---
name: suite-converter
description: Converts test suites from external eval frameworks into the Margin Eval suite format. Use this skill whenever the user wants to import, convert, translate, or migrate an eval dataset or test suite into Margin Eval format, or when they mention converting tasks from other benchmarking frameworks into Margin's structure.
---

# Suite Converter

Converts downloaded eval datasets into valid Margin Eval test suites.

## Target: Margin Eval Suite Structure

```
<suite-name>/
├── suite.toml
└── cases/
    └── <case-name>/
        ├── case.toml
        ├── prompt.md
        ├── env/
        │   └── Dockerfile
        ├── tests/
        │   ├── test.sh
        │   └── <other test files>
        └── oracle/              # optional
            └── solve.sh
```

### suite.toml

```toml
kind = "test_suite"
name = "<suite-name>"
description = "<description>"
cases = [
  "<case-1>",
  "<case-2>",
]
```

The `cases` array lists directory names under `cases/`, in the order they should run.

### case.toml

```toml
kind = "test_case"
name = "<case-name>"
description = "<one-line description>"
test_cwd = "/"
test_timeout_seconds = 1800

[metadata]
difficulty = "easy"
category = "programming"
tags = ["tag1", "tag2"]
```

`[metadata]` can be preserved for human context and future tooling, but the current Margin compiler/runtime only executes `kind`, `name`, `description`, `image`, `test_cwd`, and `test_timeout_seconds`.

Key rules:
- `name` **must** match its directory name exactly
- `kind` is always `"test_case"`
- `test_timeout_seconds` is an integer (seconds)
- `test_cwd` is the working directory where test.sh runs inside the container

**Image handling** — exactly one of:
- `image = "registry/repo@sha256:<64hex>"` for a pre-built, digest-pinned image
- Omit `image` and place a Dockerfile at `env/Dockerfile` to build at compile time

### prompt.md

The full task description sent to the agent as its initial prompt. Must not be empty. Copy the source's instruction/prompt file as-is — don't summarize or reformat it.

### tests/test.sh

The grading script. This is the evaluator — there is no separate grader abstraction. Rules:
- Must be executable (`chmod +x`)
- Exit 0 = pass, non-zero = fail
- All files in `tests/` are packaged together and staged at `{test_cwd}/tests/` in the container
- If the source suite uses absolute test-asset paths such as `/tests/...`, rewrite them to Margin-compatible paths such as `tests/...` or `"${PWD}/tests/..."`.
- If the source verifier derives test-asset paths from environment variables, normalize or override those variables to Margin-compatible values. Do not rely only on fallback expressions when the source environment may already set an incompatible path such as `/tests`.
- Ensure the verifier creates any directories it expects before writing output artifacts.
- Use exactly one authoritative test location. If the verifier copies tests from `tests/` into the workspace, its test command must avoid rediscovering the mounted `tests/` tree. If it runs tests directly from `tests/`, do not also copy them into the workspace.
- Never leave a wrapper that always exits `0`. If the underlying test command fails, the wrapper must propagate that non-zero exit code back to Margin.
- If the source suite expects status or report artifacts in addition to the exit code, preserve that behavior, but do not treat any single artifact format as universal.

### oracle/solve.sh (optional)

Reference solution. Not executed during normal eval runs. In the current Margin implementation, `oracle/` is informational only and is not part of the active compile/runtime path.

### env/Dockerfile

Container environment. Can include supporting files (setup scripts, data generators) alongside the Dockerfile — the entire `env/` directory is the build context.

---

## General Conversion Workflow

1. **Identify the source format** by inspecting the input directory (look for characteristic files like `task.toml`, `case.toml`, etc.)
2. **Choose a suite name** — derive from the dataset name or ask the user
3. **Create the output structure**: `<suite-name>/cases/`
4. **For each task/case in the source**, create a case directory and convert:
   - Config file → `case.toml`
   - Prompt/instruction → `prompt.md`
   - Test scripts → `tests/`
   - Dockerfile/environment → `env/`
   - Solution (if any) → `oracle/`
5. **Generate `suite.toml`** listing all case names (sorted alphabetically)
6. **Validate**: every case must have `case.toml`, `prompt.md`, `tests/test.sh`, and either `image` or `env/Dockerfile`
7. **Set permissions**: `chmod +x` on `tests/test.sh` and `oracle/solve.sh`
8. **Ensure unique case names**: if sanitization causes collisions, append a stable suffix (for example part of the source task ID/UUID)
9. **Smoke test a sample first**: convert a small sample of 2-5 representative cases before committing to the full dataset
10. **Run Margin on that sample**: execute the sample with `margin run` and carefully inspect the results, logs, and verifier behavior to identify conversion issues before scaling up
11. **Validate verifier behavior**: confirm the converted `tests/test.sh` locates its test assets under `{test_cwd}/tests`, returns a non-zero exit code when the underlying tests fail, and does not discover the same tests from both the workspace and `tests/`
12. **Fix and rerun before scaling up**: if the sample exposes harness issues, adjust the conversion so the verifier has a single authoritative test location, then rerun the sample before converting the full dataset

### Inferring test_cwd

Parse the Dockerfile for `WORKDIR` directives. Use the last `WORKDIR` value found. If none exists, default to `"/"`.

### Clarifying Docker Behavior

If the source suite provides more than one viable environment path and the user has not explicitly said which to use, ask before converting.

Common ambiguous cases:
- The source provides both a Dockerfile-like environment and a prebuilt image reference
- The source provides environment files that could be rebuilt, but the user may prefer to keep the original image
- The user may want you to rebuild the image, push it to a registry, and reference the new image in `case.toml`

When Docker behavior is ambiguous, ask the user which of these they want:
- Preserve the original image reference in `image`
- Convert using `env/Dockerfile`
- Rebuild/publish a new image and use that in `image`

### Generating test.sh wrappers

If the source has test scripts (e.g., pytest files) but no `test.sh`, generate a wrapper:

```bash
#!/bin/bash
set -euo pipefail

<dependency installation commands>

set +e
<test runner command, e.g., pytest tests/test_outputs.py -rA>
exit_code=$?
set -e

# If the source suite expects extra status/report artifacts, write them here
# based on $exit_code.

exit $exit_code
```

---

## Source-Specific Conversion

### Harbor

See `references/harbor-mapping.md` for the complete field-by-field mapping.

Harbor datasets (downloaded via `harbor datasets download`) have this layout:

```
<output-dir>/
├── <uuid-1>/<task-name>/
│   ├── task.toml
│   ├── instruction.md
│   ├── environment/
│   │   ├── Dockerfile
│   │   └── <setup scripts...>
│   ├── tests/
│   │   ├── test.sh
│   │   └── test_*.py
│   └── solution/
│       └── solve.sh
├── <uuid-2>/<task-name>/
│   └── ...
```

Each UUID directory wraps exactly one named task subdirectory.

#### Conversion Steps

1. **Scan** the input directory for UUID subdirectories (each contains one task folder)
   2. **For each task**:
   a. Read `task.toml`
   b. Use the inner directory name (not the UUID) as the case name — sanitize to be filesystem-safe (replace `/` with `-`, etc.)
   c. If sanitization collides with an existing case name, append a stable suffix derived from the Harbor UUID
   d. Create `cases/<case-name>/`
   e. Generate `case.toml` per the mapping in `references/harbor-mapping.md`
   f. Copy `instruction.md` → `prompt.md`
   g. If Harbor provides both a reusable image path and rebuildable environment files, and the user has not chosen a Docker strategy, stop and ask which behavior they want
   h. If `task.toml` declares `[environment].docker_image`, map it to Margin `image` when the user wants to preserve or reuse the source image
   i. If the user wants a Dockerfile-backed case, copy `environment/` → `env/` (preserves Dockerfile + setup scripts)
   j. If the user wants a rebuilt/published image, build from `environment/`, publish to the selected registry, and write the published image reference to Margin `image`
   k. Copy `tests/` → `tests/`
   l. Rewrite copied verifier paths from absolute `/tests/...` to Margin-compatible `tests/...` or `"${PWD}/tests/..."`
   m. If the source verifier uses environment variables such as `TEST_DIR`, rewrite or override them to point at the Margin-mounted test assets instead of preserving incompatible source defaults
   n. Ensure copied or generated verifier wrappers create any required output directories before writing artifacts
   o. Ensure each copied or generated verifier uses a single authoritative test location; if it copies tests into the workspace, adjust the test command to avoid rediscovering `{test_cwd}/tests`, and if it runs directly from `tests/`, do not also copy those tests into the workspace
   p. Audit copied or generated verifier wrappers for false-positive success behavior such as unconditional `exit 0`; preserve the underlying test exit code instead
   q. If `solution/` exists, copy to `oracle/` as reference material only
3. **Parse `WORKDIR`** from `env/Dockerfile` to set `test_cwd` when using a Dockerfile-backed environment or rebuilding/publishing a new image. If using a Harbor `docker_image` without a Dockerfile, infer `test_cwd` from the source verifier or default to `"/"`.
4. **Generate `suite.toml`** with all case names
5. **`chmod +x`** all `.sh` files in `tests/` and `oracle/`

#### What to drop

These Harbor fields have no Margin Eval equivalent and are safely dropped:
- `version` (replaced by `kind = "test_case"`)
- `[environment].build_timeout_sec`
- `[environment].allow_internet`
- `[environment].mcp_servers`
- `[verifier.env]`, `[solution.env]`

#### What to preserve as metadata

Resource constraints are useful context even though Margin doesn't enforce them at the case level. Store them in `[metadata]` for documentation only:

```toml
[metadata]
# ... standard fields ...
harbor_cpus = 1
harbor_memory_mb = 2048
harbor_gpus = 0
harbor_agent_timeout_sec = 120
```
