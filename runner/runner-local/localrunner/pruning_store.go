package localrunner

import (
	"context"
	"time"

	"github.com/marginlab/margin-eval/runner/runner-core/store"
)

type pruningStore struct {
	store.RunStore
	coordinator *pruneCoordinator
}

func newPruningStore(runStore store.RunStore, coordinator *pruneCoordinator) store.RunStore {
	if coordinator == nil {
		return runStore
	}
	return &pruningStore{RunStore: runStore, coordinator: coordinator}
}

func (p *pruningStore) ClaimPendingInstance(ctx context.Context, workerID string, leaseDuration time.Duration, at time.Time) (store.ClaimedWork, bool, error) {
	if p.coordinator.claimsBlocked() {
		return store.ClaimedWork{}, false, nil
	}
	return p.RunStore.ClaimPendingInstance(ctx, workerID, leaseDuration, at)
}

func (p *pruningStore) FinalizeAttempt(ctx context.Context, in store.FinalizeInput, at time.Time) error {
	if err := p.RunStore.FinalizeAttempt(ctx, in, at); err != nil {
		return err
	}
	run, err := p.RunStore.GetRun(ctx, in.RunID, false)
	if err != nil {
		return nil
	}
	p.coordinator.observeFinalize(in.RunID, run.State.IsTerminal())
	return nil
}
