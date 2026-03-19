# fake-agent-server

`fake-agent-server` is a minimal test double for the hard-cutover `agent-server` API.

Supported endpoints:

- `GET /healthz`
- `GET /readyz`
- `GET /v1/state`
- `PUT /v1/agent-definition`
- `PUT /v1/agent-config`
- `POST /v1/agent/install`
- `POST /v1/run`
- `GET /v1/run`
- `DELETE /v1/run`

Removed endpoints such as `PUT /v1/agent` and `PUT /v1/agent/config` are intentionally not implemented.
