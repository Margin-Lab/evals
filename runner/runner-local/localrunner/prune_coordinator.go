package localrunner

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/marginlab/margin-eval/runner/runner-core/domain"
	"github.com/marginlab/margin-eval/runner/runner-core/engine"
	"github.com/marginlab/margin-eval/runner/runner-core/store"
)

const (
	defaultPruneDockerBinary = "docker"
	imagePruneTimeout        = 10 * time.Minute
)

type pruneCoordinatorConfig struct {
	Interval       int
	DockerBinary   string
	ImagePruneFunc func(context.Context) error
}

type pruneCoordinator struct {
	interval   int
	imagePrune func(context.Context) error
	wakeCh     chan struct{}

	mu              sync.Mutex
	active          int
	gateClosed      bool
	prunePending    bool
	pruneInProgress bool
	runState        map[string]*pruneRunState
}

type pruneRunState struct {
	executedCompleted int
	nextThreshold     int
	lastRequested     int
	finalRequested    bool
}

func newPruneCoordinator(cfg pruneCoordinatorConfig) (*pruneCoordinator, error) {
	if cfg.Interval <= 0 {
		return nil, fmt.Errorf("prune interval must be > 0")
	}
	pruneFn := cfg.ImagePruneFunc
	if pruneFn == nil {
		binary := strings.TrimSpace(cfg.DockerBinary)
		if binary == "" {
			binary = defaultPruneDockerBinary
		}
		pruneFn = dockerImagePruneFunc(binary)
	}
	return &pruneCoordinator{
		interval:   cfg.Interval,
		imagePrune: pruneFn,
		wakeCh:     make(chan struct{}, 1),
		runState:   map[string]*pruneRunState{},
	}, nil
}

func (c *pruneCoordinator) Start(ctx context.Context) {
	go c.loop(ctx)
}

func (c *pruneCoordinator) wrapExecutor(inner engine.Executor) engine.Executor {
	return pruningExecutor{inner: inner, coordinator: c}
}

func (c *pruneCoordinator) claimsBlocked() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.gateClosed
}

func (c *pruneCoordinator) beginInstance() {
	c.mu.Lock()
	c.active++
	c.mu.Unlock()
}

func (c *pruneCoordinator) endInstance() {
	c.mu.Lock()
	if c.active > 0 {
		c.active--
	}
	shouldNotify := c.active == 0 && c.prunePending
	c.mu.Unlock()
	if shouldNotify {
		c.notify()
	}
}

func (c *pruneCoordinator) observeFinalize(runID string, terminal bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	state := c.runState[runID]
	if state == nil {
		state = &pruneRunState{nextThreshold: c.interval}
		c.runState[runID] = state
	}
	state.executedCompleted++

	shouldRequest := false
	for state.nextThreshold > 0 && state.executedCompleted >= state.nextThreshold {
		shouldRequest = true
		state.lastRequested = state.executedCompleted
		state.nextThreshold += c.interval
	}
	if terminal {
		if state.executedCompleted > state.lastRequested {
			shouldRequest = true
			state.lastRequested = state.executedCompleted
		}
		state.finalRequested = true
	}
	if !shouldRequest {
		return
	}
	c.gateClosed = true
	c.prunePending = true
	if c.active == 0 {
		c.notifyLocked()
	}
}

func (c *pruneCoordinator) loop(ctx context.Context) {
	for {
		if !c.waitForReady(ctx) {
			return
		}

		_ = c.imagePrune(ctx)

		c.mu.Lock()
		c.pruneInProgress = false
		if c.prunePending {
			if c.active == 0 {
				c.notifyLocked()
			}
		} else {
			c.gateClosed = false
		}
		c.mu.Unlock()
	}
}

func (c *pruneCoordinator) waitForReady(ctx context.Context) bool {
	for {
		c.mu.Lock()
		ready := c.prunePending && !c.pruneInProgress && c.active == 0
		if ready {
			c.prunePending = false
			c.pruneInProgress = true
			c.mu.Unlock()
			return true
		}
		c.mu.Unlock()

		select {
		case <-ctx.Done():
			return false
		case <-c.wakeCh:
		}
	}
}

func (c *pruneCoordinator) notify() {
	c.mu.Lock()
	c.notifyLocked()
	c.mu.Unlock()
}

func (c *pruneCoordinator) notifyLocked() {
	select {
	case c.wakeCh <- struct{}{}:
	default:
	}
}

func dockerImagePruneFunc(binary string) func(context.Context) error {
	return func(ctx context.Context) error {
		pruneCtx, cancel := context.WithTimeout(ctx, imagePruneTimeout)
		defer cancel()

		cmd := exec.CommandContext(pruneCtx, binary, "image", "prune", "-a", "-f")
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("docker image prune -a -f failed: %w\noutput:\n%s", err, strings.TrimSpace(string(out)))
		}
		return nil
	}
}

type pruningExecutor struct {
	inner       engine.Executor
	coordinator *pruneCoordinator
}

func (e pruningExecutor) ExecuteInstance(
	ctx context.Context,
	run store.Run,
	inst store.Instance,
	updateState func(domain.InstanceState) error,
	updateResolvedImage func(string) error,
) (store.InstanceResult, []store.Artifact, error) {
	e.coordinator.beginInstance()
	defer e.coordinator.endInstance()
	return e.inner.ExecuteInstance(ctx, run, inst, updateState, updateResolvedImage)
}
