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
- Convention: write `echo 1 > /logs/verifier/reward.txt` on pass, `echo 0` on fail
- All files in `tests/` are packaged together and staged at `{test_cwd}/tests/` in the container

### oracle/solve.sh (optional)

Reference solution. Not executed during normal eval runs.

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

### Inferring test_cwd

Parse the Dockerfile for `WORKDIR` directives. Use the last `WORKDIR` value found. If none exists, default to `"/"`.

### Generating test.sh wrappers

If the source has test scripts (e.g., pytest files) but no `test.sh`, generate a wrapper:

```bash
#!/bin/bash
<dependency installation commands>
<test runner command, e.g., pytest /tests/test_outputs.py -rA>

if [ $? -eq 0 ]; then
  echo 1 > /logs/verifier/reward.txt
else
  echo 0 > /logs/verifier/reward.txt
fi
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
   c. Create `cases/<case-name>/`
   d. Generate `case.toml` per the mapping in `references/harbor-mapping.md`
   e. Copy `instruction.md` → `prompt.md`
   f. Copy entire `environment/` → `env/` (preserves Dockerfile + setup scripts)
   g. Copy entire `tests/` → `tests/`
   h. If `solution/` exists, copy to `oracle/`
3. **Parse `WORKDIR`** from `env/Dockerfile` to set `test_cwd`
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

Resource constraints are useful context even though Margin doesn't enforce them at the case level. Store them in `[metadata]`:

```toml
[metadata]
# ... standard fields ...
harbor_cpus = 1
harbor_memory_mb = 2048
harbor_gpus = 0
harbor_agent_timeout_sec = 120
```
