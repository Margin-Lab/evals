# Runner Core Interface

## Runner Service Contract (`runnerapi`)

### `Service`

Implemented by concrete runners such as `runner-local`:

1. `Start(ctx context.Context)`
2. `SubmitRun(ctx context.Context, in runnerapi.SubmitInput) (store.Run, error)`
3. `WaitForTerminalRun(ctx context.Context, runID string, pollInterval time.Duration) (store.Run, error)`
4. `GetRunSnapshot(ctx context.Context, runID string, opts runnerapi.SnapshotOptions) (runnerapi.RunSnapshot, error)`
5. `GetInstanceSnapshot(ctx context.Context, instanceID string, opts runnerapi.SnapshotOptions) (runnerapi.InstanceSnapshot, error)`

### Snapshot Builders

1. `runnerapi.BuildRunSnapshot(ctx, runStore, runID, opts)`
2. `runnerapi.BuildInstanceSnapshot(ctx, runStore, instanceID, opts)`

## RunBundle APIs (`runbundle`)

### `Validate(bundle Bundle) error`

Request example (Go):

```go
err := runbundle.Validate(bundle)
```

Response example:

```go
// success
err == nil

// failure
err.Error() == "resolved_snapshot.cases must be non-empty"
```

### `HashSHA256(bundle Bundle) (string, error)`

Request example:

```go
hash, err := runbundle.HashSHA256(bundle)
```

Response example:

```go
hash == "9b8d..."
err == nil
```

### `CloneForRerunExact(bundle, newBundleID, createdAt, originRunID) Bundle`

Request example:

```go
rerunBundle := runbundle.CloneForRerunExact(bundle, "bun_new", now, "run_old")
```

Response example:

```go
rerunBundle.Source.Kind == runbundle.SourceKindRunSnapshot
rerunBundle.Source.OriginRunID == "run_old"
```

## State APIs (`domain`)

### `ValidInstanceTransition(from, to InstanceState) bool`

Request example:

```go
ok := domain.ValidInstanceTransition(domain.InstanceStateBooting, domain.InstanceStateAgentInstalling)
```

Response example:

```go
ok == true
```

### `NextRunState(current RunState, counts RunCounts, cancelRequested bool) RunState`

Request example:

```go
next := domain.NextRunState(domain.RunStateRunning, counts, false)
```

Response example:

```go
next == domain.RunStateCompleted
```

## Worker Engine APIs (`engine`)

### `NewPool(store, executor, cfg) *Pool`

Request example:

```go
pool := engine.NewPool(runStore, executor, cfg)
```

Response example:

```go
pool != nil
```

### `(*Pool).Start(ctx)`

Request example:

```go
pool.Start(ctx)
```

Response example:

```go
// starts worker/reaper goroutines until ctx cancellation
```

## Run Store Contract (`store.RunStore`)

Primary write/read interface methods:

1. `CreateRun`
2. `RerunExact`
3. `GetRun`
4. `ListRuns`
5. `CancelRun`
6. `ListInstances`
7. `ClaimPendingInstance`
8. `UpdateInstanceState`
9. `HeartbeatAttempt`
10. `FinalizeAttempt`
11. `ListRunEvents`
12. `ListInstanceEvents`
13. `GetInstanceResult`
14. `ListArtifacts`

`ListRuns` accepts optional filters:

1. `state`
2. `source_kind`
3. `created_by_user_id`

Request example:

```go
run, err := runStore.CreateRun(ctx, store.CreateRunInput{...})
```

Response example:

```go
run.RunID == "run_123"
err == nil
```

## Postgres Adapter (`store/postgres`)

### `Open(ctx, cfg) (*postgres.Store, error)`

Request example:

```go
pgStore, err := postgres.Open(ctx, postgres.Config{
  DSN: "postgres://marginlab:marginlab@localhost:54329/marginlab_dev?sslmode=disable",
})
```

Response example:

```go
pgStore != nil
err == nil
```

Note: the external `instance_results` schema must include nullable `installed_version text`. This repo updates the adapter and tests, but the actual schema migration is managed outside this codebase.
