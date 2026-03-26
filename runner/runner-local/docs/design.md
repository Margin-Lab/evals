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

For each local run, the run directory is the single output root:

1. `<root>/runs/<run_id>/results.json`
2. `<root>/runs/<run_id>/internal/bundle.json`
3. `<root>/runs/<run_id>/internal/manifest.json`
4. `<root>/runs/<run_id>/internal/progress.json`
5. `<root>/runs/<run_id>/internal/events.jsonl`
6. `<root>/runs/<run_id>/internal/artifacts.json`
7. `<root>/runs/<run_id>/instances/<instance_id>/result.json`
8. `<root>/runs/<run_id>/instances/<instance_id>/trajectory.json`
9. `<root>/runs/<run_id>/instances/<instance_id>/{image,bootstrap,run,test}/...`

## Lifecycle

1. `NewService` validates config and creates run root directory.
2. `SubmitRun` writes bundle + creates run rows.
3. `Start` launches workers.
4. `WaitForTerminalRun` blocks until terminal run state, then persists snapshots.
