# What is Margin?

Margin is a production-grade local eval runtime for coding agents. It runs containerized evals for agents like Claude Code, Codex, and OpenCode, and reports rich data such as accuracy, token usage, runtime statistics, and full agent traces in a standardized local format.

## What can Margin do?

Agents are complicated, and can be configured in many different ways. Margin allows you to test any agent configuration against the official remote suite collection in `https://github.com/Margin-Lab/swe-suites.git`, or against your own custom test suites. This could include:

- **Impact of MCPs and skills on agent accuracy** — Attach different MCP servers or skill sets to the same agent and measure how they affect task completion rates across a benchmark.
- **Impact of prompting strategies on agent accuracy** — Swap project instructions, system prompts, or task descriptions and compare results to find the most effective prompting approach.
- **Best model and harness configurations for specific tasks** — Run the same suite across different agents, models, and reasoning levels to identify which configuration performs best for your workload.

## Example: Claude Code on SWE-Bench-Pro

Run the latest Claude Code version against the full SWE-Bench-Pro benchmark:

```bash
margin run \
  --suite git::https://github.com/Margin-Lab/swe-suites.git//swe-bench-pro \
  --agent-config ~/.margin/configs/example-agent-configs/claude-code-default \
  --eval ~/.margin/configs/example-eval-configs/default.toml
```

## Why Margin?

Margin offers a robust runtime for managing eval containers and a simple filesystem-based interface for defining your own agents, evals, and suites. The primary focuses of Margin are:

* **Reproducibility:** Every run is compiled into a self-contained bundle that encodes every parameter — suite, agent config, eval config, and all dependencies — so any run can be reproduced or shared.
* **Unified interface:** A single CLI, config format, and output format works across all supported agents. Compare Claude Code, Codex, and OpenCode side-by-side using the same suites, eval configs, and reporting format.
* **Infrastructure Robustness:** First-class resume/retry support automatically handles cases that failed for infrastructure reasons without re-running cases that already produced results.
