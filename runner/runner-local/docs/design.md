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

1. `<run_dir>/results.json`
2. `<run_dir>/internal/bundle.json`
3. `<run_dir>/internal/manifest.json`
4. `<run_dir>/internal/progress.json`
5. `<run_dir>/internal/events.jsonl`
6. `<run_dir>/internal/artifacts.json`
7. `<run_dir>/instances/<instance_id>/result.json`
8. `<run_dir>/instances/<instance_id>/trajectory.json`
9. `<run_dir>/instances/<instance_id>/{image,bootstrap,run,test}/...`

## Lifecycle

1. `NewService` validates config and prepares the worker service.
2. `SubmitRun` validates the requested output directory, creates it, writes the bundle, and creates run rows.
3. `Start` launches workers.
4. `WaitForTerminalRun` blocks until terminal run state, then persists snapshots.
