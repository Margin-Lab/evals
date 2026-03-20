<p align="center">
  <a href="https://marginlab.ai"><img src="assets/logo.png" alt="Margin Eval" width="600"></a>
</p>

<p align="center">
  Open-source eval runtime for coding agents.
  <br /><br />
  <a href="https://marginlab.ai">marginlab.ai</a> · <a href="https://docs.marginlab.ai">Documentation</a>
  <br />
  <a href="https://x.com/themarginguy"><img src="https://img.shields.io/twitter/follow/themarginguy?style=social" alt="Follow on X"></a>
</p>

---

Margin Eval runs containerized evaluations against coding agents like **Claude Code**, **Codex**, and **OpenCode**. It measures accuracy, token usage, runtime, and captures full execution traces — all in a standardized, reproducible local format.

- **Test any configuration** — agents, models, MCPs, skills, prompting strategies
- **Compare side-by-side** — unified CLI, config format, and output across all agents
- **Reproduce any run** — every run is compiled into an immutable, self-contained bundle
- **Resume on failure** — automatically retry infra failures without re-running completed cases

## Quickstart

### Prerequisites

- [Docker](https://docs.docker.com/get-docker/) installed and running
- An API key or OAuth credentials for your agent provider

### Install

Install the latest stable release:

```bash
curl -fsSL https://raw.githubusercontent.com/Margin-Lab/evals/main/scripts/install.sh | bash
```

Install a specific beta release:

```bash
MARGIN_VERSION=v0.1.0-beta.1 curl -fsSL https://raw.githubusercontent.com/Margin-Lab/evals/main/scripts/install.sh | bash
```

Update an installer-managed binary later:

```bash
margin update
```

Build from source instead:

```bash
git clone https://github.com/Margin-Lab/evals.git
cd evals
scripts/build-cli-agent-server.sh
```

This produces a self-contained binary at `./bin/margin`. Verify it works:

```bash
./bin/margin help
```

`margin update` is available only for binaries installed by the official installer. Source-built binaries stay on the `dev` channel.

### Set credentials

**API key:**

```bash
export ANTHROPIC_API_KEY=<your-key>
```

**OAuth (Claude Code Pro/Max, Codex Pro, etc.):** Margin automatically detects valid OAuth credentials at their standard paths. To use a specific credential file:

```bash
margin run ... --auth-file-path /path/to/credentials.json
```

### Create an eval config

Scaffold a default eval config to control concurrency and timeouts:

```bash
margin init eval-config --eval ./my-eval.toml
```

This creates a TOML file you can customize:

```toml
kind = "eval_config"
name = "my-eval"
max_concurrency = 2
fail_fast = false
retry_count = 1
instance_timeout_seconds = 1800
```

### Run your first eval

```bash
margin run \
  --suite ./suites/swe-minimal-test-suite \
  --agent-config ./configs/example-agent-configs/claude-code-default \
  --eval ./my-eval.toml
```

This runs `swe-minimal-test-suite`, a 3-case subset of SWE-Bench Verified designed for quick local testing. Results are saved to `runs/<run-id>/`.

### More examples

**Run Claude Code on SWE-Bench Pro:**

```bash
margin run \
  --suite ./suites/swe-bench-pro \
  --agent-config ./configs/example-agent-configs/claude-code-default \
  --eval ./my-eval.toml
```

**Run Codex with unified config:**

```bash
margin run \
  --suite ./suites/terminal-bench-2 \
  --agent-config ./configs/example-agent-configs/codex-unified \
  --eval ./my-eval.toml
```

**Resume a failed run:**

```bash
margin run --resume-from <run-id>
```

**Scaffold a new agent config:**

```bash
margin init agent-config \
  --agent-config ./configs/my-agent-configs/claude-sonnet \
  --definition ./configs/agent-definitions/claude-code
```

## Built-in eval suites

| Suite | Description |
|-------|-------------|
| `swe-minimal-test-suite` | 3-case smoke test (astropy, django, sphinx) |
| `swe-bench-verified` | Full SWE-Bench Verified benchmark |
| `swe-bench-pro` | SWE-Bench Pro benchmark |
| `swe-bench-pro-curated-50` | Curated 50-case subset of SWE-Bench Pro |
| `terminal-bench-2` | 30+ terminal task cases |

## Supported agents

| Agent | Config examples |
|-------|----------------|
| **Claude Code** | `claude-code-default`, `claude-code-unified` |
| **Codex** | `codex-default`, `codex-unified` |
| **OpenCode** | `opencode-default`, `opencode-unified` |

Agent configs support two modes: **direct** (full agent-specific control) and **unified** (model + reasoning level for apples-to-apples comparison across agents).

## Project structure

```
cli/                    CLI tool — compiles configs into run bundles
runner/runner-core/     Shared engine — state machine, worker pool, run store
runner/runner-local/    Local runner — Docker executor, filesystem persistence
agent-server/           In-container runtime — agent lifecycle, trajectory capture
configs/                Agent definitions and example configs
suites/                 Built-in eval suites
docs/                   Documentation
```

## Documentation

- [What is Margin?](https://docs.marginlab.ai/welcome/01-what-is-margin)
- [Installation](https://docs.marginlab.ai/quickstart/01-install)
- [Running Your First Eval](https://docs.marginlab.ai/quickstart/02-running-your-first-eval)
- [Configuring Your Agent](https://docs.marginlab.ai/configuration/01-configuring-your-agent)
- [Configuring Your Eval](https://docs.marginlab.ai/configuration/02-configuring-your-eval)
- [Creating Your Own Eval](https://docs.marginlab.ai/creating-your-own-eval/01-quickstart)
- [Adding a New Agent](https://docs.marginlab.ai/add-support-for-a-new-agent/01-overview)

## License

This project is licensed under the [GNU Affero General Public License v3.0](LICENSE).

Please contact us at hello@marginlab.ai for questions
