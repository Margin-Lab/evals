# Margin Eval

Margin Eval is an open-source local eval runtime for coding agents. It runs suites against agents like Claude Code, Codex, and OpenCode, packages each run as an immutable `RunBundle`, executes cases inside containerized environments, and writes standardized local outputs with traces, usage, logs, and results.

## Components

1. `cli/`
   - The `margin` command-line interface for compiling suites/configs into bundles and running local evals.

2. `runner/runner-core/`
   - Shared run bundle contract, state machine, worker engine, and run-store interfaces.

3. `runner/runner-local/`
   - Local runner service built on `runner-core`.
   - Persists run outputs to the filesystem under `runs/<run-id>/`.

4. `agent-server/`
   - Runtime server launched inside execution environments.

5. `configs/`
   - Repo-owned agent definitions and example agent/eval configs.

6. `suites/`
   - Built-in eval suites, including local smoke suites and larger benchmark corpora.

## Core Principles

1. All execution is bundle-first: `RunBundle` is the canonical execution input.
2. Workers execute only the persisted `resolved_snapshot` data captured in the bundle.
3. Local runs are reproducible: the bundle, results, events, and artifact metadata are written to disk.
4. Agent integrations share one CLI, one config model, and one output format.

## Key Docs

- CLI contract: `cli/docs/cli.md`
- Local file formats: `cli/docs/toml.md`
- Quickstart: `docs/cli/quickstart/01-install.md`
- Runner integration flow: `runner/runner-core/docs/integration.md`
- Agent server design: `agent-server/docs/design.md`
- Agent server endpoints: `agent-server/docs/endpoints.md`
