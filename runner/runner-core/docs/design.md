# Runner Core Design

`runner-core` is the shared execution library used by Margin runner services.

## Package Layout

1. `runbundle`: canonical run input schema, validation, hashing, and rerun cloning.
2. `domain`: run and instance states plus transition rules.
3. `store`: run store interfaces and in-memory implementation.
4. `store/postgres`: Postgres-backed run store implementation for environments that persist run state in PostgreSQL.
5. `engine`: worker pool and execution lifecycle orchestration.

## Core Responsibilities

1. Validate immutable run bundle inputs before execution.
2. Enforce instance transition ordering.
3. Claim work with attempt/lease semantics.
4. Heartbeat active attempts and reap expired leases.
5. Finalize terminal outcomes and artifact metadata.

## State Model

## Run States

1. `queued`
2. `running`
3. `canceling`
4. terminal: `completed`, `failed`, `canceled`

## Instance States

1. `pending`
2. `provisioning`
3. `agent_server_installing`
4. `booting`
5. `agent_configuring`
6. `agent_installing`
7. `agent_running`
8. `agent_collecting`
9. `testing`
10. `collecting_artifacts`
11. terminal: `succeeded`, `failed`, `canceled`

## Worker Execution Lifecycle

1. Claim pending instance.
2. Apply non-terminal phase transitions.
3. Call executor.
4. Normalize result to terminal state.
5. Finalize attempt with result + artifacts.

## Determinism Guarantees

1. Bundle hash is computed from canonical JSON bytes.
2. Execution consumes stored bundle snapshot, not mutable definitions.
3. Rerun cloning keeps execution snapshot semantics while issuing new identity metadata.
