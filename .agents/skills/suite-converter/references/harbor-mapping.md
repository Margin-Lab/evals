# Harbor → Margin Eval Field Mapping

## Config Fields: task.toml → case.toml

| Harbor (`task.toml`) | Margin Eval (`case.toml`) | Conversion |
|---|---|---|
| — | `kind = "test_case"` | Always set this literal value |
| task directory name | `name` | Use the inner task directory name (not UUID parent), sanitize for filesystem, and add a stable suffix on collision |
| — | `description` | Use first line of `instruction.md`, truncated to ~100 chars |
| `[environment].docker_image` | `image` | Preserve as Margin `image` when the user wants to keep or reuse the source image |
| — | `test_cwd` | Parse last `WORKDIR` from Dockerfile; if only `docker_image` is present, infer from verifier conventions or default `"/"` |
| `[verifier].timeout_sec` | `test_timeout_seconds` | `int(timeout_sec)`; default 1800 if missing |
| `[metadata].difficulty` | `[metadata].difficulty` | Copy as-is |
| `[metadata].category` | `[metadata].category` | Copy as-is |
| `[metadata].tags` | `[metadata].tags` | Copy as-is |
| `[metadata].author_name` | `[metadata].author_name` | Copy as-is |
| `[metadata].author_email` | `[metadata].author_email` | Copy as-is |
| `[environment].cpus` | `[metadata].harbor_cpus` | Preserve as metadata |
| `[environment].memory_mb` | `[metadata].harbor_memory_mb` | Preserve as metadata |
| `[environment].memory` | `[metadata].harbor_memory` | Preserve raw string value when Harbor uses unit-suffixed strings (for example `"2G"`) |
| `[environment].storage_mb` | `[metadata].harbor_storage_mb` | Preserve as metadata |
| `[environment].storage` | `[metadata].harbor_storage` | Preserve raw string value when Harbor uses unit-suffixed strings |
| `[environment].gpus` | `[metadata].harbor_gpus` | Preserve as metadata |
| `[agent].timeout_sec` | `[metadata].harbor_agent_timeout_sec` | Preserve as metadata |

## Dropped Fields

| Harbor Field | Reason |
|---|---|
| `version` | Margin uses `kind` instead |
| `[environment].build_timeout_sec` | Not in Margin case config |
| `[environment].allow_internet` | Not in Margin case config |
| `[environment].mcp_servers` | Not in Margin case config (agent-level concern) |
| `[verifier.env]` | Not in Margin case config |
| `[solution.env]` | Not in Margin case config |

## File Mapping

| Harbor | Margin Eval | Notes |
|---|---|---|
| `instruction.md` | `prompt.md` | Copy content as-is |
| `environment/Dockerfile` | `env/Dockerfile` | Copy when building the case from Dockerfile; if Harbor also provides `docker_image` and the user has not chosen a strategy, ask before converting |
| `environment/<other files>` | `env/<other files>` | Copy supporting files when building from Dockerfile or rebuilding/publishing a new image; otherwise keep only if the user wants them as reference material |
| `tests/test.sh` | `tests/test.sh` | Copy; ensure executable (`chmod +x`) |
| `tests/test_*.py` | `tests/test_*.py` | Copy all supporting test files |
| `tests/<any other files>` | `tests/<same>` | Copy all files in tests/ |
| `solution/solve.sh` | `oracle/solve.sh` | Copy; ensure executable |

## Docker Strategy Choice

If Harbor gives you both:
- a reusable source image via `[environment].docker_image`, and
- rebuildable environment files under `environment/`

and the user has not explicitly said what they want, ask the user before converting.

Valid user choices:
- Preserve the source image in Margin `image`
- Convert to a Dockerfile-backed Margin case using `env/Dockerfile`
- Rebuild the image, publish it to a registry, and reference the published image in Margin `image`

## Verifier Path Adjustments

Margin stages copied test assets under `{test_cwd}/tests`, not absolute `/tests`.

When converted verifier files reference absolute `/tests/...` paths, rewrite them during conversion:

- `/tests/test_outputs.py` -> `tests/test_outputs.py`
- `/tests/config.json` -> `tests/config.json`
- Prefer relative paths or `"${PWD}/tests/..."` inside `tests/test.sh`

Do not leave converted verifiers pointing at `/tests/...` unless the case image explicitly creates that path itself.

If the source verifier uses environment variables such as `TEST_DIR` to locate tests, normalize or override those variables to the Margin-mounted location. A fallback like `${TEST_DIR:-${PWD}/tests}` is not sufficient when the source environment already sets `TEST_DIR=/tests`.

Also ensure the verifier creates any output directories it depends on before writing artifacts.

## Single Test Location

Converted verifiers must not run the same tests from two places.

If the conversion copies tests from `tests/` into the workspace, update the test command so it targets only the copied files or otherwise excludes the mounted `tests/` tree from discovery.

If the converted verifier runs tests directly from `tests/`, do not also copy those test files into the workspace.

During sample validation, inspect failures for signs of duplicate collection or duplicate package builds caused by both workspace files and `tests/` being active at the same time.

## Verifier Exit Semantics

Margin determines pass/fail from the verifier process exit code. A converted wrapper must not hide failures by always exiting `0`.

When converting verifiers:

- capture the underlying test command exit code
- preserve any suite-specific status or report artifacts the source verifier expects
- return the same exit code from `tests/test.sh`

Bad pattern to fix during conversion:

```bash
some-test-command
exit_code=$?
# Writes suite-specific artifacts, but then ignores the actual test result.
exit 0
```

Correct pattern:

```bash
set +e
some-test-command
exit_code=$?
set -e

# If the source suite expects additional status/report artifacts, write them
# here based on $exit_code.

exit $exit_code
```

## Metadata Semantics

Converted `[metadata]` fields are preserved for human context and future tooling. The current Margin compiler/runtime does not enforce Harbor resource hints at execution time.

## Example Conversion

### Harbor input (`task.toml`):
```toml
version = "1.0"

[metadata]
author_name = "Alex Shaw"
difficulty = "easy"
category = "programming"
tags = ["trivial"]

[verifier]
timeout_sec = 120.0

[agent]
timeout_sec = 120.0

[environment]
build_timeout_sec = 600.0
docker_image = "ghcr.io/example/hello-world:1.0"
cpus = 1
memory_mb = 2048
storage_mb = 10240
gpus = 0
allow_internet = true
mcp_servers = []
```

### Margin Eval output (`case.toml`):
```toml
kind = "test_case"
name = "hello-world"
description = "Create a file called hello.txt with \"Hello, world!\" as the content."
image = "ghcr.io/example/hello-world:1.0"
test_cwd = "/app"
test_timeout_seconds = 120

[metadata]
author_name = "Alex Shaw"
difficulty = "easy"
category = "programming"
tags = ["trivial"]
harbor_cpus = 1
harbor_memory_mb = 2048
harbor_storage_mb = 10240
harbor_gpus = 0
harbor_agent_timeout_sec = 120
```
