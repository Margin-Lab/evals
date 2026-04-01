---
name: suite-converter
description: Converts test suites from external eval frameworks into the Margin Eval suite format. Use this skill whenever the user wants to import, convert, translate, or migrate an eval dataset or test suite into Margin Eval format, or when they mention converting tasks from other benchmarking frameworks into Margin's structure.
---

# Suite Converter

Converts downloaded eval datasets into valid Margin Eval test suites.

## Purpose

Use this skill when converting an external eval dataset into the Margin Eval suite format.

The job has three parts:
- map the source format into Margin's filesystem contract
- preserve the source verifier's intended behavior without breaking Margin's execution model
- validate the conversion on a small sample with `margin run` before scaling up

## Target Margin Contract

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
agent_cwd = "/"
test_cwd = "/"
test_timeout_seconds = 1800

[metadata]
difficulty = "easy"
category = "programming"
tags = ["tag1", "tag2"]
```

`[metadata]` can be preserved for human context and future tooling, but the current Margin compiler/runtime primarily cares about the execution fields such as `kind`, `name`, `description`, `image`, `agent_cwd`, `test_cwd`, and `test_timeout_seconds`.

Key rules:
- `name` **must** match its directory name exactly
- `kind` is always `"test_case"`
- `test_timeout_seconds` is an integer (seconds)
- `agent_cwd` is the directory where the agent is expected to start and do its work inside the container
- `test_cwd` is the working directory where test.sh runs inside the container

Image handling, exactly one of:
- `image = "registry/repo@sha256:<64hex>"` for a pre-built, digest-pinned image
- Omit `image` and place a Dockerfile at `env/Dockerfile` to build at compile time

### prompt.md

The full task description sent to the agent as its initial prompt. Must not be empty. Copy the source's instruction/prompt file as-is — don't summarize or reformat it.

### tests/test.sh

The grading script. This is the evaluator. There is no separate grader abstraction.

### env/Dockerfile

Container environment. Can include supporting files alongside the Dockerfile. The entire `env/` directory is the build context.

### oracle/solve.sh

Optional reference solution. Not executed during normal eval runs. In the current Margin implementation, `oracle/` is informational only and is not part of the active compile/runtime path.

## Conversion Invariants

These rules are cross-source and should hold for every conversion.

### Verifier rules

- Must be executable (`chmod +x`)
- Exit 0 = pass, non-zero = fail
- All files in `tests/` are packaged together and staged at `{test_cwd}/tests/` in the container
- If the source suite uses absolute test-asset paths such as `/tests/...`, rewrite them to Margin-compatible paths such as `tests/...` or `"${PWD}/tests/..."`.
- If the source verifier derives test-asset paths from environment variables, normalize or override those variables to Margin-compatible values. Do not rely only on fallback expressions when the source environment may already set an incompatible path such as `/tests`.
- Ensure the verifier creates any directories it expects before writing output artifacts.
- Use exactly one authoritative test location. If the verifier copies tests from `tests/` into the workspace, its test command must avoid rediscovering the mounted `tests/` tree. If it runs tests directly from `tests/`, do not also copy them into the workspace.
- Never leave a wrapper that always exits `0`. If the underlying test command fails, the wrapper must propagate that non-zero exit code back to Margin.
- If the source suite expects status or report artifacts in addition to the exit code, preserve that behavior, but do not treat any single artifact format as universal.

### Case identity rules

- `case.toml.name` must match the case directory name exactly
- If source-name sanitization causes collisions, append a stable suffix such as part of the source task ID or UUID

### Working directory rules

- `agent_cwd` and `test_cwd` are separate concepts:
  - `agent_cwd` is where the agent starts
  - `test_cwd` is where the verifier runs
- Set `agent_cwd` to the directory where the source task expects the agent to operate on the codebase or files
- Set `test_cwd` to the directory where the verifier should execute
- Do not assume they are the same. Only use the same value for both when the source task clearly indicates that the agent and verifier operate from the same directory

### Validation rules

- Do not assume the conversion is correct until a sample run succeeds without harness-level issues
- Distinguish real task failures from conversion failures
- If the sample exposes harness issues, fix them and rerun the sample before scaling up

## Decision Rules

### Inferring working directories

Parse the Dockerfile for `WORKDIR` directives. Use the last `WORKDIR` value found as the default working directory inside the container.

Use that information carefully:
- infer `test_cwd` from the verifier and Dockerfile execution context
- infer `agent_cwd` from where the source task expects the agent to work on the repo or files
- do not default `agent_cwd` to `test_cwd` unless the source task clearly uses the same directory for both

If no better signal exists:
- default `test_cwd` to the last `WORKDIR`, or `"/"` if none exists
- choose `agent_cwd` from the most plausible agent workspace for the task, rather than assuming it matches `test_cwd`

### Clarifying Docker behavior

If the source suite provides more than one viable environment path and the user has not explicitly said which to use, ask before converting.

Common ambiguous cases:
- The source provides both a Dockerfile-like environment and a prebuilt image reference
- The source provides environment files that could be rebuilt, but the user may prefer to keep the original image
- The user may want you to rebuild the image, push it to a registry, and reference the new image in `case.toml`

When Docker behavior is ambiguous, ask which of these they want:
- Preserve the original image reference in `image`
- Convert using `env/Dockerfile`
- Rebuild and publish a new image, then use that in `image`

## Conversion Workflow

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
12. **Validate agent starting context**: confirm `agent_cwd` points at the directory where the agent should actually begin work for the task
13. **Fix and rerun before scaling up**: if the sample exposes harness issues, adjust the conversion so the verifier has a single authoritative test location, then rerun the sample before converting the full dataset

## Generated Wrapper Guidance

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

Common failure signatures to look for during sample validation:
- Path mismatch: the verifier still points at `/tests/...` instead of `{test_cwd}/tests/...`
- Duplicate discovery: the same tests are collected from both the workspace and `tests/`
- Masked failures: the wrapper writes artifacts but still exits `0`

## Source-Specific Adapters

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

#### Harbor-specific steps

1. **Scan** the input directory for UUID subdirectories (each contains one task folder)
2. **For each task**:
   a. Read `task.toml`
   b. Use the inner directory name, not the UUID, as the case name
   c. Sanitize the case name to be filesystem-safe
   d. If sanitization collides with an existing case name, append a stable suffix derived from the Harbor UUID
   e. Create `cases/<case-name>/`
   f. Generate `case.toml` per `references/harbor-mapping.md`
   g. Copy `instruction.md` to `prompt.md`
   h. If Harbor provides both a reusable image path and rebuildable environment files, and the user has not chosen a Docker strategy, stop and ask which behavior they want
   i. If `task.toml` declares `[environment].docker_image`, map it to Margin `image` when the user wants to preserve or reuse the source image
   j. If the user wants a Dockerfile-backed case, copy `environment/` to `env/`
   k. If the user wants a rebuilt and published image, build from `environment/`, publish to the selected registry, and write the published image reference to Margin `image`
   l. Copy `tests/` to `tests/`
   m. Apply the general verifier rules above, especially path normalization, environment-variable overrides, single test location, and exit-code propagation
   n. If `solution/` exists, copy it to `oracle/` as reference material only
3. **Parse `WORKDIR`** from `env/Dockerfile` to help infer working directories when using a Dockerfile-backed environment or rebuilding/publishing a new image. Use it directly for `test_cwd` when it matches the verifier context, and infer `agent_cwd` separately from the source task layout or expected repo workspace. If using a Harbor `docker_image` without a Dockerfile, infer both directories from the source task and verifier rather than assuming they match.
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

## Validation Checklist

Before declaring a conversion complete, verify:
- Every case has `case.toml`, `prompt.md`, `tests/test.sh`, and either `image` or `env/Dockerfile`
- `case.toml.name` matches the directory name
- `agent_cwd` points at the directory where the agent should actually start
- `test_cwd` resolves to a real working directory assumption for the case
- Required shell scripts are executable
- The sample suite runs under `margin run`
- The verifier reads test assets from the correct location
- The verifier does not discover the same tests from both the workspace and `tests/`
- The verifier propagates real failures with a non-zero exit code
- Any remaining sample failures are real task failures, not harness failures
