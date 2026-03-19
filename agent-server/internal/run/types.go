package run

import (
	"context"
	"os"
	"os/exec"
	"time"

	"github.com/marginlab/margin-eval/agent-server/internal/agentruntime"
	"github.com/marginlab/margin-eval/agent-server/internal/ptyws"
	"github.com/marginlab/margin-eval/agent-server/internal/state"
)

type PTYSize struct {
	Cols int `json:"cols"`
	Rows int `json:"rows"`
}

type AuthFile struct {
	RequiredEnv    string `json:"required_env"`
	SourcePath     string `json:"source_path"`
	RunHomeRelPath string `json:"run_home_rel_path"`
}

type StartRequest struct {
	CWD           string            `json:"cwd"`
	InitialPrompt string            `json:"initial_prompt"`
	Args          []string          `json:"args"`
	Env           map[string]string `json:"env"`
	AuthFiles     []AuthFile        `json:"auth_files,omitempty"`
	DryRun        bool              `json:"dry_run,omitempty"`
	PTY           PTYSize           `json:"pty"`
}

type StartResponse struct {
	RunID     string `json:"run_id"`
	State     string `json:"state"`
	PID       *int   `json:"pid,omitempty"`
	StartedAt string `json:"started_at"`
	Attach    struct {
		WSPath   string `json:"ws_path"`
		Protocol string `json:"protocol"`
	} `json:"attach"`
}

type SnapshotRequest struct {
	RunID string  `json:"run_id"`
	PTY   PTYSize `json:"pty"`
}

type SnapshotResponse struct {
	RunID      string
	Agent      string
	RunState   state.RunState
	CapturedAt time.Time
	Content    []byte
	Truncated  bool
}

type activeRun struct {
	runID      string
	agent      state.AgentRecord
	runContext agentruntime.RunContext

	cmd        *exec.Cmd
	ptyFile    *os.File
	ptyLogFile *os.File
	hub        *ptyws.Hub

	watchCancel context.CancelFunc

	finalizedCh chan struct{}
}
