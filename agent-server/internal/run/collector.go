package run

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"

	"github.com/marginlab/margin-eval/agent-server/internal/agentruntime"
	"github.com/marginlab/margin-eval/agent-server/internal/config"
	"github.com/marginlab/margin-eval/agent-server/internal/fsutil"
	"github.com/marginlab/margin-eval/agent-server/internal/state"
	"github.com/marginlab/margin-eval/runner/runner-core/trajectory"
)

type collector struct {
	cfg     config.Config
	runtime *agentruntime.Runtime
}

type trajectoryWithPath struct {
	Payload []byte
	Path    string
}

func newCollector(cfg config.Config, runtime *agentruntime.Runtime) *collector {
	return &collector{
		cfg:     cfg,
		runtime: runtime,
	}
}

func (c *collector) collect(active *activeRun) (trajectoryWithPath, state.TrajectoryStatus, error) {
	path := trajectoryPath(active)
	if !supportsTrajectory(active.agent) {
		return trajectoryWithPath{Path: path}, state.TrajectoryStatusNone, nil
	}

	deadline := time.Now().Add(c.cfg.TrajectoryCollectTimeout)
	var lastErr error
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		raw, err := c.runtime.CollectTrajectory(ctx, active.agent, active.runContext)
		cancel()
		if err != nil {
			lastErr = fmt.Errorf("collect trajectory: %w", err)
		} else {
			trimmed := bytes.TrimSpace(raw)
			if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
				lastErr = fmt.Errorf("trajectory hook returned no payload")
			} else {
				if _, decodeErr := trajectory.Decode(trimmed); decodeErr == nil {
					return trajectoryWithPath{
						Payload: indentJSON(trimmed),
						Path:    path,
					}, state.TrajectoryStatusComplete, nil
				} else {
					lastErr = fmt.Errorf("validate ATIF trajectory: %w", decodeErr)
				}
			}
		}

		if time.Now().After(deadline) {
			if lastErr == nil {
				lastErr = fmt.Errorf("trajectory collection timed out")
			}
			return trajectoryWithPath{Path: path}, state.TrajectoryStatusFailed, lastErr
		}
		time.Sleep(c.cfg.TrajectoryPollInterval)
	}
}

func (t trajectoryWithPath) persist() error {
	if len(t.Payload) == 0 {
		return nil
	}
	if err := fsutil.WriteFileAtomic(t.Path, t.Payload, 0o644); err != nil {
		return fmt.Errorf("write trajectory: %w", err)
	}
	return nil
}

func trajectoryPath(active *activeRun) string {
	return filepath.Join(active.runContext.ArtifactsDir, "trajectory.json")
}

func trajectoryPathForRun(stateDir, runID string) string {
	return filepath.Join(stateDir, "runs", runID, "artifacts", "trajectory.json")
}

func indentJSON(raw []byte) []byte {
	var buf bytes.Buffer
	if err := json.Indent(&buf, raw, "", "  "); err != nil {
		return append([]byte(nil), raw...)
	}
	return buf.Bytes()
}

func supportsTrajectory(agent state.AgentRecord) bool {
	return agent.Definition != nil && agent.Definition.Snapshot.Manifest.Trajectory != nil
}
