# runner-local Integration

`runner-local` no longer branches on built-in agent names or config modes.

It resolves a bundled `agent_definition` + `agent_config`, validates config before install, and injects required secrets from `definition.manifest.auth.required_env`.
