---
name: suite-creator
description: Creates new Margin Eval test suites from scratch. Use this skill whenever the user wants to author, build, or scaffold a new eval test suite, define test cases for evaluating coding agents, or create tasks with Dockerfiles, prompts, and grading scripts for the Margin Eval framework.
---

# Suite Creator

Creates new Margin Eval test suites from scratch, including the directory structure, configuration files, prompts, Dockerfiles, and grading scripts.

## Suite Structure

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
        │   └── <supporting test files>
        └── oracle/              # optional
            └── solve.sh
```

### suite.toml

```toml
kind = "test_suite"
name = "<suite-name>"
description = "<what this suite evaluates>"
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

The task description sent to the agent as its initial prompt. Must not be empty.

### tests/test.sh

The grading script — this is the evaluator. It must terminate with exactly one of:
- `0` = pass
- `1` = fail
- `2` = infra

Do not write `reward.txt`. The verifier process exit code is the authoritative result. The script must be executable. All files in `tests/` are packaged together and staged at `{test_cwd}/tests/` in the container.

### oracle/solve.sh (optional)

Reference solution. Not executed during normal eval runs — useful for dry runs and validation.

### env/Dockerfile

Container environment. The entire `env/` directory is the build context, so supporting files (setup scripts, seed data, config) can live alongside the Dockerfile.

---

## Workflow

### 1. Define the suite scope

Clarify with the user:
- What capability or behavior is being evaluated?
- How many cases? What range of difficulty?
- What does the agent's environment look like? (language, frameworks, tools available)

### 2. Scaffold the directory structure

Create the suite directory, `cases/` subdirectory, and a skeleton for each case. It helps to create all case directories first, then fill them in one at a time.

### 3. Write the Dockerfile

Start with the Dockerfile because it defines the environment everything else runs in. The Dockerfile should produce a container that:
- Has all dependencies the agent might need pre-installed
- Sets a `WORKDIR` (this becomes `test_cwd` in case.toml)
- Contains any seed data, starter code, or project scaffolding the task requires
- Does **not** contain the solution or hints toward it

Keep images minimal — install only what the task requires. Use specific version tags, not `latest`.

```dockerfile
FROM python:3.12-slim

WORKDIR /app

# Pre-install project dependencies
COPY requirements.txt .
RUN pip install -r requirements.txt

# Seed the workspace with starter code
COPY src/ ./src/
```

### 4. Write the prompt

The prompt is the only input the agent receives. Write it as if briefing a developer who has just been dropped into the container. It should include:

- **What to do** — the task objective, stated clearly
- **Where things are** — relevant file paths, project structure
- **Constraints** — specific requirements, output format, things to avoid
- **Success criteria** — what "done" looks like, in concrete terms

Avoid leaking test implementation details. The agent should not know how it will be graded — only what the correct behavior is.

A good prompt is specific enough that a competent developer could complete the task without asking clarifying questions, but does not prescribe a particular implementation approach.

### 5. Write the grading script

`tests/test.sh` determines pass/fail. The grading approach depends on what's being tested:

Use a simple explicit verdict API in every new harness:

```bash
#!/bin/bash
set -euo pipefail

pass()  { printf 'VERDICT: PASS\n'; exit 0; }
fail()  { printf 'VERDICT: FAIL\n'; exit 1; }
infra() { printf 'VERDICT: INFRA\n' >&2; exit 2; }
```

Policy:
- return `pass` or `fail` only when the harness reached a trustworthy verdict about the candidate
- return `infra` when the harness cannot reach a trustworthy verdict for reasons not attributable to the candidate
- missing candidate artifact, candidate compile/import/runtime failure, candidate timeout after candidate logic starts, and wrong output are all `fail`
- verifier/bootstrap/parser/config failures are `infra`

**File/output verification** — check that the agent produced the right files with the right content:
```bash
#!/bin/bash
set -euo pipefail

pass()  { exit 0; }
fail()  { exit 1; }
infra() { exit 2; }

if [ ! -f /app/output.json ]; then
  fail
fi

set +e
pytest tests/test_outputs.py -rA
exit_code=$?
set -e

case "$exit_code" in
  0) pass ;;
  1) fail ;;
  *) infra ;;
esac
```

**Test suite pass-through** — run the project's own test suite against the agent's changes:
```bash
#!/bin/bash
set -euo pipefail

pass()  { exit 0; }
fail()  { exit 1; }
infra() { exit 2; }

cd /app
set +e
npm test
exit_code=$?
set -e

case "$exit_code" in
  0) pass ;;
  1) fail ;;
  *) infra ;;
esac
```

**Custom validation** — for tasks where correctness is more nuanced:
```bash
#!/bin/bash
set -euo pipefail
python tests/validate.py
```

Guidelines for grading scripts:
- Install test dependencies inside the script (they run in a separate context from the agent)
- Test observable outcomes, not implementation details — the agent should be free to solve the problem however it sees fit
- Make sure the script is deterministic — same agent output should always produce the same verdict
- Keep terminal verdict paths explicit and easy to audit
- Prefer verifier-level changes over Dockerfile changes when clarifying verdict semantics
- If a parser or verifier cannot interpret the result, default to `infra` unless you can directly attribute the failure to the candidate

### 6. Write the reference solution (optional)

`oracle/solve.sh` applies the known-correct fix. Useful for validating that the grading script works — run the solution, then run test.sh, and confirm it passes.

```bash
#!/bin/bash
cd /app
# Apply the fix
sed -i 's/old_pattern/new_pattern/' src/module.py
```

### 7. Write case.toml and suite.toml

Fill in the case config:
- Set `test_cwd` to match the Dockerfile's `WORKDIR`
- Set `test_timeout_seconds` generously — allow 2-3x the expected solve time
- Add meaningful metadata (difficulty, category, tags)

Then generate `suite.toml` listing all cases.

### 8. Validate

Check every case:
- [ ] `case.toml` exists, `name` matches directory name, `kind = "test_case"`
- [ ] `prompt.md` exists and is non-empty
- [ ] `tests/test.sh` exists and is executable
- [ ] `env/Dockerfile` exists (or `image` is set in case.toml)
- [ ] `suite.toml` lists all case directory names
- [ ] `chmod +x` on all `.sh` files

### 9. Smoke test

If possible, build the Docker image and verify:
1. The container starts and the workspace is set up correctly
2. Running `oracle/solve.sh` followed by `tests/test.sh` exits 0
3. Running `tests/test.sh` without the solution exits 1
4. An induced verifier/setup failure exits 2

---

## Writing Good Test Cases

### Prompt clarity

Ambiguous prompts make it hard to distinguish agent capability from prompt interpretation. If an agent fails, you want to be confident it's because the agent couldn't do the task, not because the instructions were unclear.

### Grading robustness

The grading script should accept any correct solution, not just the reference solution. Avoid:
- Checking for specific variable names or function signatures (unless the prompt requires them)
- Exact string matching on output when approximate matching would do
- Depending on file modification timestamps or execution order

### Difficulty calibration

Categorize cases by difficulty to make results more informative:
- **easy** — a competent developer solves it in under 15 minutes
- **medium** — requires understanding the codebase or domain (15 min to 1 hour)
- **hard** — requires significant reasoning, multi-file changes, or domain expertise (1+ hours)

### Independence

Each case should be self-contained. Cases should not depend on each other or share state. Every case runs in a fresh container.
