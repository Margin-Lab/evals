# Runner Local Interface

## `NewService(cfg Config) (runnerapi.Service, error)`

Request example:

```go
svc, err := localrunner.NewService(localrunner.Config{
  RootDir:  "/tmp/marginlab-local",
  Executor: myExecutor,
})
```

Response example:

```go
svc != nil
err == nil
```

## `Start(ctx context.Context)`

Request example:

```go
svc.Start(ctx)
```

Response example:

```go
// worker and reaper loops run until context cancellation
```

## `SubmitRun(ctx, input runnerapi.SubmitInput) (store.Run, error)`

Request example:

```go
run, err := svc.SubmitRun(ctx, runnerapi.SubmitInput{
  ProjectID:     "proj_local",
  CreatedByUser: "user_local",
  Name:          "smoke",
  Bundle:        bundle,
})
```

Response example:

```go
run.RunID == "run_000001"
run.State == domain.RunStateQueued
err == nil
```

## `WaitForTerminalRun(ctx, runID, pollInterval) (store.Run, error)`

Request example:

```go
finalRun, err := svc.WaitForTerminalRun(ctx, run.RunID, 100*time.Millisecond)
```

Response example:

```go
finalRun.State.IsTerminal() == true
err == nil
```

## `GetRunSnapshot(ctx, runID, opts) (runnerapi.RunSnapshot, error)`

Request example:

```go
snap, err := svc.GetRunSnapshot(ctx, run.RunID, runnerapi.SnapshotOptions{
  IncludeRunEvents: true,
})
```

Response example:

```go
snap.Run.RunID == run.RunID
len(snap.Instances) > 0
err == nil
```

## `GetInstanceSnapshot(ctx, instanceID, opts) (runnerapi.InstanceSnapshot, error)`

Request example:

```go
instSnap, err := svc.GetInstanceSnapshot(ctx, instanceID, runnerapi.SnapshotOptions{
  IncludeInstanceResults: true,
  IncludeInstanceEvents:  true,
})
```

Response example:

```go
instSnap.Instance.InstanceID == instanceID
err == nil
```
