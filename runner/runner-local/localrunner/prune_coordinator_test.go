package localrunner

import (
	"context"
	"testing"
	"time"
)

func TestPruneCoordinatorBlocksClaimsUntilActiveInstancesDrain(t *testing.T) {
	pruneStarted := make(chan struct{}, 1)
	pruneRelease := make(chan struct{})
	pruneDone := make(chan struct{}, 1)

	coordinator, err := newPruneCoordinator(pruneCoordinatorConfig{
		Interval: 1,
		ImagePruneFunc: func(context.Context) error {
			pruneStarted <- struct{}{}
			<-pruneRelease
			pruneDone <- struct{}{}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("newPruneCoordinator() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	coordinator.Start(ctx)

	coordinator.beginInstance()
	coordinator.beginInstance()
	coordinator.endInstance()
	coordinator.observeFinalize("run_1", false)

	if !coordinator.claimsBlocked() {
		t.Fatalf("expected claims to be blocked after prune threshold")
	}

	select {
	case <-pruneStarted:
		t.Fatalf("prune should wait for active instances to drain")
	case <-time.After(50 * time.Millisecond):
	}

	coordinator.endInstance()

	select {
	case <-pruneStarted:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for prune to start")
	}

	close(pruneRelease)

	select {
	case <-pruneDone:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for prune to finish")
	}

	deadline := time.Now().Add(2 * time.Second)
	for coordinator.claimsBlocked() {
		if time.Now().After(deadline) {
			t.Fatalf("claims remained blocked after prune completed")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestPruneCoordinatorRequestsFinalSweepForTerminalRemainder(t *testing.T) {
	pruneStarted := make(chan struct{}, 1)

	coordinator, err := newPruneCoordinator(pruneCoordinatorConfig{
		Interval: 32,
		ImagePruneFunc: func(context.Context) error {
			pruneStarted <- struct{}{}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("newPruneCoordinator() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	coordinator.Start(ctx)

	coordinator.beginInstance()
	coordinator.endInstance()
	coordinator.observeFinalize("run_1", true)

	select {
	case <-pruneStarted:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for final prune to start")
	}
}
