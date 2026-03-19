# runner-core Integration

Current agent-server orchestration order:

1. `GET /v1/state`
2. `PUT /v1/agent-definition`
3. `PUT /v1/agent-config`
4. `POST /v1/agent/install`
5. `POST /v1/run`
6. poll `GET /v1/run`
7. `DELETE /v1/run`

Run bundles now store `resolved_snapshot.agent.definition` and `resolved_snapshot.agent.config`.
