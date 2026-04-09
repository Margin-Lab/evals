package engine

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/marginlab/margin-eval/runner/runner-core/domain"
	"github.com/marginlab/margin-eval/runner/runner-core/instancestatus"
	"github.com/marginlab/margin-eval/runner/runner-core/store"
)

type Executor interface {
	ExecuteInstance(
		ctx context.Context,
		run store.Run,
		inst store.Instance,
		updateState func(domain.InstanceState) error,
		updateResolvedImage func(string) error,
	) (store.InstanceResult, []store.Artifact, error)
}

type Config struct {
	WorkerID          string
	WorkerCount       int
	PollInterval      time.Duration
	LeaseDuration     time.Duration
	HeartbeatInterval time.Duration
	ReaperInterval    time.Duration
	ReaperBatchSize   int
}

type Pool struct {
	store    store.RunStore
	executor Executor
	cfg      Config
}

func NewPool(s store.RunStore, ex Executor, cfg Config) *Pool {
	if cfg.WorkerID == "" {
		cfg.WorkerID = "runner-worker"
	}
	if cfg.WorkerCount <= 0 {
		cfg.WorkerCount = 1
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 500 * time.Millisecond
	}
	if cfg.LeaseDuration <= 0 {
		cfg.LeaseDuration = 20 * time.Second
	}
	if cfg.HeartbeatInterval <= 0 {
		cfg.HeartbeatInterval = 5 * time.Second
	}
	if cfg.ReaperInterval <= 0 {
		cfg.ReaperInterval = 3 * time.Second
	}
	if cfg.ReaperBatchSize <= 0 {
		cfg.ReaperBatchSize = 200
	}
	return &Pool{store: s, executor: ex, cfg: cfg}
}

func (p *Pool) Start(ctx context.Context) {
	for i := 0; i < p.cfg.WorkerCount; i++ {
		go p.workerLoop(ctx)
	}
	go p.reaperLoop(ctx)
}

func (p *Pool) workerLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		claim, ok, err := p.store.ClaimPendingInstance(ctx, p.cfg.WorkerID, p.cfg.LeaseDuration, time.Now().UTC())
		if err != nil {
			log.Printf("runner claim error: %v", err)
			if !sleepOrDone(ctx, p.cfg.PollInterval) {
				return
			}
			continue
		}
		if !ok {
			if !sleepOrDone(ctx, p.cfg.PollInterval) {
				return
			}
			continue
		}
		p.runClaim(ctx, claim)
	}
}

func (p *Pool) runClaim(ctx context.Context, claim store.ClaimedWork) {
	instanceTimeout := time.Duration(claim.Run.Bundle.ResolvedSnapshot.Execution.InstanceTimeoutSecond) * time.Second
	runCtx, cancel := context.WithTimeout(ctx, instanceTimeout)
	defer cancel()
	done := make(chan struct{})
	go p.heartbeatLoop(runCtx, done, claim)
	defer close(done)

	cancelWatchDone := make(chan struct{})
	go p.cancelWatchLoop(runCtx, cancelWatchDone, claim.Run.RunID, cancel)
	defer close(cancelWatchDone)

	updateState := func(st domain.InstanceState) error {
		if err := p.store.UpdateInstanceState(runCtx, claim.Run.RunID, claim.Instance.InstanceID, claim.AttemptID, st, time.Now().UTC()); err != nil {
			return err
		}
		return nil
	}
	updateResolvedImage := func(image string) error {
		if err := p.store.UpdateInstanceImage(runCtx, claim.Run.RunID, claim.Instance.InstanceID, claim.AttemptID, image, time.Now().UTC()); err != nil {
			return err
		}
		return nil
	}

	res, artifacts, err := p.executor.ExecuteInstance(runCtx, claim.Run, claim.Instance, updateState, updateResolvedImage)
	res = instancestatus.NormalizeExecutionResult(res, err)

	if res.FinalState.IsInfraFailure() && claim.Run.Bundle.ResolvedSnapshot.Execution.RetryCount > 0 {
		retried, retryErr := p.store.RequeueInfraFailure(context.Background(), store.RequeueInfraFailureInput{
			AttemptID:     claim.AttemptID,
			RunID:         claim.Run.RunID,
			InstanceID:    claim.Instance.InstanceID,
			Artifacts:     artifacts,
			Result:        res,
			MaxRetryCount: claim.Run.Bundle.ResolvedSnapshot.Execution.RetryCount,
		}, time.Now().UTC())
		if retryErr != nil {
			if errors.Is(retryErr, store.ErrLeaseLost) {
				return
			}
			log.Printf("runner infra retry error: %v", retryErr)
		} else if retried {
			return
		}
	}

	if finErr := p.store.FinalizeAttempt(context.Background(), store.FinalizeInput{
		AttemptID:  claim.AttemptID,
		RunID:      claim.Run.RunID,
		InstanceID: claim.Instance.InstanceID,
		Result:     res,
		Artifacts:  artifacts,
	}, time.Now().UTC()); finErr != nil && !errors.Is(finErr, store.ErrLeaseLost) {
		log.Printf("runner finalize error: %v", finErr)
	}
}

func (p *Pool) cancelWatchLoop(ctx context.Context, done <-chan struct{}, runID string, cancel context.CancelFunc) {
	t := time.NewTicker(p.cfg.PollInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-done:
			return
		case <-t.C:
			cancelRequested, err := p.store.RunCancelRequested(context.Background(), runID)
			if err != nil {
				continue
			}
			if cancelRequested {
				cancel()
				return
			}
		}
	}
}

func (p *Pool) heartbeatLoop(ctx context.Context, done <-chan struct{}, claim store.ClaimedWork) {
	t := time.NewTicker(p.cfg.HeartbeatInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-done:
			return
		case <-t.C:
			err := p.store.HeartbeatAttempt(context.Background(), claim.Instance.InstanceID, claim.AttemptID, claim.LeaseToken, p.cfg.WorkerID, p.cfg.LeaseDuration, time.Now().UTC())
			if err != nil {
				return
			}
		}
	}
}

func (p *Pool) reaperLoop(ctx context.Context) {
	t := time.NewTicker(p.cfg.ReaperInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			_, _ = p.store.SweepCancelingRuns(context.Background(), time.Now().UTC())
			_, _ = p.store.ReapExpiredLeases(context.Background(), time.Now().UTC(), p.cfg.ReaperBatchSize)
		}
	}
}

func sleepOrDone(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
