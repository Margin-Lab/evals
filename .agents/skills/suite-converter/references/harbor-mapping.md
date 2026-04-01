# Harbor → Margin Eval Field Mapping

## Config Fields: task.toml → case.toml

| Harbor (`task.toml`) | Margin Eval (`case.toml`) | Conversion |
|---|---|---|
| — | `kind = "test_case"` | Always set this literal value |
| `[task].name` | `name` | Use the task directory name (not UUID parent), sanitize for filesystem |
| — | `description` | Use first line of `instruction.md`, truncated to ~100 chars |
| `[environment].docker_image` | — | Omit; always use Dockerfile via `env/` |
| — | `test_cwd` | Parse last `WORKDIR` from Dockerfile; default `"/"` |
| `[verifier].timeout_sec` | `test_timeout_seconds` | `int(timeout_sec)`; default 1800 if missing |
| `[metadata].difficulty` | `[metadata].difficulty` | Copy as-is |
| `[metadata].category` | `[metadata].category` | Copy as-is |
| `[metadata].tags` | `[metadata].tags` | Copy as-is |
| `[metadata].author_name` | `[metadata].author_name` | Copy as-is |
| `[metadata].author_email` | `[metadata].author_email` | Copy as-is |
| `[environment].cpus` | `[metadata].harbor_cpus` | Preserve as metadata |
| `[environment].memory_mb` | `[metadata].harbor_memory_mb` | Preserve as metadata |
| `[environment].storage_mb` | `[metadata].harbor_storage_mb` | Preserve as metadata |
| `[environment].gpus` | `[metadata].harbor_gpus` | Preserve as metadata |
| `[agent].timeout_sec` | `[metadata].harbor_agent_timeout_sec` | Preserve as metadata |

## Dropped Fields

| Harbor Field | Reason |
|---|---|
| `version` | Margin uses `kind` instead |
| `[task].authors` | Not in Margin schema |
| `[task].keywords` | Covered by `[metadata].tags` |
| `[environment].build_timeout_sec` | Not in Margin case config |
| `[environment].allow_internet` | Not in Margin case config |
| `[environment].mcp_servers` | Not in Margin case config (agent-level concern) |
| `[verifier.env]` | Not in Margin case config |
| `[solution.env]` | Not in Margin case config |

## File Mapping

| Harbor | Margin Eval | Notes |
|---|---|---|
| `instruction.md` | `prompt.md` | Copy content as-is |
| `environment/Dockerfile` | `env/Dockerfile` | Copy as-is |
| `environment/<other files>` | `env/<other files>` | Copy all (setup scripts, data generators, etc.) |
| `tests/test.sh` | `tests/test.sh` | Copy; ensure executable (`chmod +x`) |
| `tests/test_*.py` | `tests/test_*.py` | Copy all supporting test files |
| `tests/<any other files>` | `tests/<same>` | Copy all files in tests/ |
| `solution/solve.sh` | `oracle/solve.sh` | Copy; ensure executable |

## Example Conversion

### Harbor input (`task.toml`):
```toml
version = "1.0"

[task]
name = "harbor/hello-world"
authors = []
keywords = []

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
