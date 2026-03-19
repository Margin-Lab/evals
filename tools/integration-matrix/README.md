# Integration Matrix

`matrix.json` now lists repo-owned definition/config pairs.

Example:

```json
{
  "cases": [
    { "definition_name": "codex", "config_name": "codex-default" }
  ]
}
```

`definition_name` must name a repo-owned `agent_definition`, and `config_name` must belong to that definition.
