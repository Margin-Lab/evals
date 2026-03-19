package run

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/marginlab/margin-eval/agent-server/internal/apperr"
	"github.com/marginlab/margin-eval/agent-server/internal/config"
	"github.com/marginlab/margin-eval/agent-server/internal/logutil"
	"syscall"
)

type supervisor struct {
	cfg config.Config
}

func newSupervisor(cfg config.Config) *supervisor {
	return &supervisor{cfg: cfg}
}

func (s *supervisor) streamPTYOutput(active *activeRun) {
	defer func() {
		_ = active.ptyLogFile.Sync()
	}()

	buf := make([]byte, 4096)
	for {
		n, err := active.ptyFile.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			_, _ = active.ptyLogFile.Write(chunk)
			active.hub.BroadcastOutput(chunk)
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return
			}
			return
		}
	}
}

func (s *supervisor) stop(ctx context.Context, active *activeRun) error {
	pid := active.cmd.Process.Pid
	logutil.Info("run.stop_sigint", map[string]any{"run_id": active.runID, "pid": pid})
	if err := killProcessGroup(pid, syscall.SIGINT); err != nil && !errors.Is(err, syscall.ESRCH) {
		return internalError(apperr.CodeRunStopFailed, "failed to send SIGINT", map[string]any{"error": err.Error()}, err)
	}

	graceTimer := time.NewTimer(s.cfg.StopGraceTimeout)
	defer graceTimer.Stop()

	select {
	case <-active.finalizedCh:
		return nil
	case <-ctx.Done():
		return internalError(apperr.CodeRunStopFailed, "run stop interrupted", map[string]any{"error": ctx.Err().Error()}, ctx.Err())
	case <-graceTimer.C:
	}

	logutil.Info("run.stop_sigkill", map[string]any{"run_id": active.runID, "pid": pid})
	if err := killProcessGroup(pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return internalError(apperr.CodeRunStopFailed, "failed to send SIGKILL", map[string]any{"error": err.Error()}, err)
	}

	select {
	case <-active.finalizedCh:
		return nil
	case <-ctx.Done():
		return internalError(apperr.CodeRunStopFailed, "run stop interrupted", map[string]any{"error": ctx.Err().Error()}, ctx.Err())
	}
}

func (s *supervisor) forceKill(active *activeRun) error {
	pid := active.cmd.Process.Pid
	if pid <= 0 {
		return nil
	}
	if err := killProcessGroup(pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return nil
}

func (s *supervisor) wait(active *activeRun) (int, *string, time.Time) {
	waitErr := active.cmd.Wait()
	exitCode, signal := processExitDetails(active.cmd.ProcessState, waitErr)
	endedAt := time.Now().UTC()
	return exitCode, signal, endedAt
}

func processExitDetails(processState *os.ProcessState, waitErr error) (int, *string) {
	if processState == nil {
		if waitErr != nil {
			code := 1
			return code, nil
		}
		code := 0
		return code, nil
	}

	ws, ok := processState.Sys().(syscall.WaitStatus)
	if !ok {
		code := processState.ExitCode()
		return code, nil
	}
	if ws.Signaled() {
		sig := ws.Signal().String()
		code := 128 + int(ws.Signal())
		return code, &sig
	}
	code := ws.ExitStatus()
	return code, nil
}

func killProcessGroup(pid int, signal syscall.Signal) error {
	if pid <= 0 {
		return fmt.Errorf("invalid pid: %d", pid)
	}
	return syscall.Kill(-pid, signal)
}
