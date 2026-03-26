# TOML

Local authoring is now split into:

- `definition.toml` in an `agent_definition` package
- `config.toml` in an `agent_config` package

`definition.toml` declares hooks, auth requirements, optional schema, and optional unified-translation support.

`config.toml` points at one definition and supplies either direct `[input]` values or shared `[unified]` values.

Optional shared skills live at top level and use the same syntax for both modes:

```toml
[[skills]]
path = "./skills/db-migration"
```

Each `path` must point at a directory containing a root `SKILL.md` with frontmatter that includes at least `name` and `description`. Local compilation packages the full directory and carries it in the resolved agent config.

Optional shared root instructions also live at top level and use the same syntax for both modes:

```toml
[agents_md]
path = "./AGENTS.md"
```

The file contents are copied into the resolved agent config as raw markdown. Definitions opt in with `[agents_md] filename = "AGENTS.md"` or `"CLAUDE.md"`. At runtime, `agent-server` writes that file into the actual project directory where the agent process starts.

Direct example:

```toml
kind = "agent_config"
name = "codex-default"
definition = "../../agent-definitions/codex"
mode = "direct"

[[skills]]
path = "./skills/db-migration"

[agents_md]
path = "./AGENTS.md"

[input]
codex_version = "latest"
startup_args = []
run_args = []
config_toml = """
model = "gpt-5-codex"
approval_policy = "never"
"""
```

Unified example:

```toml
kind = "agent_config"
name = "codex-unified"
definition = "../../agent-definitions/codex"
mode = "unified"

[agents_md]
path = "./AGENTS.md"

[unified]
model = "gpt-5-codex"
reasoning_level = "medium"
```

If a definition supports unified mode, `definition.toml` declares it under `[config.unified]`, including the translate hook and allowed model/reasoning values.

If a definition supports skills, `definition.toml` declares the home-relative discovery root:

```toml
[skills]
home_rel_dir = ".agents/skills"
```

Definitions may also declare local OAuth credential discovery rules for the local runner:

```toml
[auth]
required_env = ["OPENAI_API_KEY"]

[[auth.local_credentials]]
required_env = "OPENAI_API_KEY"
run_home_rel_path = ".codex/auth.json"

  [[auth.local_credentials.sources]]
  kind = "home_file"
  home_rel_path = ".codex/auth.json"
```

When the required env is unavailable in the local runner container environment, `margin run` evaluates the credential sources in order, or uses the `--auth-file-path` override when provided, and materializes the resolved payload into `run_home/<run_home_rel_path>` before the run hook executes.

If a definition supports root instructions, `definition.toml` declares which filename should be materialized into the project root:

```toml
[agents_md]
filename = "AGENTS.md"
```
