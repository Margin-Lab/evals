# Runner Local Design

`runner-local` provides local-mode execution on top of `runner-core`.

## Responsibilities

1. Accept local run submissions.
2. Start worker pool for local processing.
3. Persist terminal run snapshots to local filesystem.

## Service Composition

`localrunner.Service` composes:

1. `runner-core/store.RunStore`
2. `runner-core/engine.Pool`
3. caller-provided `engine.Executor`

## Filesystem Persistence

For each terminal run, local service writes:

1. `<root>/runs/<run_id>/bundle.json`
2. `<root>/runs/<run_id>/manifest.json`
3. `<root>/runs/<run_id>/results.json`
4. `<root>/runs/<run_id>/events.jsonl`
5. `<root>/runs/<run_id>/artifacts/metadata.json`

## Lifecycle

1. `NewService` validates config and creates run root directory.
2. `SubmitRun` writes bundle + creates run rows.
3. `Start` launches workers.
4. `WaitForTerminalRun` blocks until terminal run state, then persists snapshots.
