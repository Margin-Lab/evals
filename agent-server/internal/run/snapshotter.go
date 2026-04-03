package run

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/creack/pty"
	"github.com/marginlab/margin-eval/agent-server/internal/agentruntime"
	"github.com/marginlab/margin-eval/agent-server/internal/apperr"
	"github.com/marginlab/margin-eval/agent-server/internal/config"
)

type snapshotter struct {
	cfg config.Config
}

type snapshotCaptureInput struct {
	ExecSpec agentruntime.ExecSpec
	PTY      PTYSize
}

type snapshotCaptureResult struct {
	Content   []byte
	Truncated bool
}

const (
	maxSnapshotTerminalPendingBytes = 128
	snapshotExitDrainTimeout        = 100 * time.Millisecond
)

var (
	cprResponse           = []byte("\x1b[1;1R")
	dsResponse            = []byte("\x1b[0n")
	daResponse            = []byte("\x1b[?1;2c")
	sdaResponse           = []byte("\x1b[>0;0;0c")
	kittyKeyboardResponse = []byte("\x1b[?0u")
	osc10Response         = []byte("\x1b]10;rgb:ffff/ffff/ffff\x1b\\")
	osc11Response         = []byte("\x1b]11;rgb:0000/0000/0000\x1b\\")
)

func newSnapshotter(cfg config.Config) *snapshotter {
	return &snapshotter{cfg: cfg}
}

func (s *snapshotter) Capture(ctx context.Context, input snapshotCaptureInput) (snapshotCaptureResult, error) {
	if strings.TrimSpace(input.ExecSpec.Path) == "" {
		return snapshotCaptureResult{}, invalidError(apperr.CodeSnapshotFailed, "snapshot command path is required", nil, nil)
	}
	if input.PTY.Cols <= 0 || input.PTY.Rows <= 0 {
		return snapshotCaptureResult{}, invalidError(apperr.CodeInvalidPTY, "pty cols/rows must be > 0", nil, nil)
	}

	cmd := exec.Command(input.ExecSpec.Path, input.ExecSpec.Args...)
	cmd.Env = input.ExecSpec.Env
	cmd.Dir = input.ExecSpec.Dir

	ptyFile, err := pty.StartWithSize(cmd, &pty.Winsize{
		Cols: uint16(input.PTY.Cols),
		Rows: uint16(input.PTY.Rows),
	})
	if err != nil {
		return snapshotCaptureResult{}, internalError(apperr.CodeSnapshotFailed, "start snapshot PTY process failed", map[string]any{"error": err.Error()}, err)
	}
	defer func() { _ = ptyFile.Close() }()

	readDone := make(chan error, 1)
	chunks := make(chan []byte, 64)
	stopRead := make(chan struct{})
	terminalEmulator := newSnapshotTerminalEmulator()
	go streamSnapshotChunks(ptyFile, chunks, readDone, stopRead, terminalEmulator)

	waitDone := make(chan error, 1)
	go func() {
		waitDone <- cmd.Wait()
	}()

	var idleTimer *time.Timer
	var idleTimerCh <-chan time.Time
	var exitDrainTimer *time.Timer
	var exitDrainTimerCh <-chan time.Time
	hardTimer := time.NewTimer(s.cfg.SnapshotHardTimeout)
	defer stopTimer(hardTimer)

	maxBytes := s.cfg.SnapshotMaxBytes
	if maxBytes <= 0 {
		maxBytes = 1
	}

	var out []byte
	var truncated bool
	var processExited bool
	var streamErr error
	var hardTimeout bool
	var ctxCanceled bool
	done := false

	for !done {
		select {
		case <-ctx.Done():
			ctxCanceled = true
			done = true
		case <-hardTimer.C:
			hardTimeout = true
			done = true
		case <-idleTimerCh:
			done = true
		case <-exitDrainTimerCh:
			if ptyFile != nil {
				_ = ptyFile.Close()
				ptyFile = nil
			}
			exitDrainTimerCh = nil
		case waitErr := <-waitDone:
			processExited = true
			if waitErr != nil {
				// Non-zero exit is expected when we interrupt snapshot TUIs.
			}
			waitDone = nil
			if readDone == nil {
				done = true
				continue
			}
			if exitDrainTimer == nil {
				exitDrainTimer = time.NewTimer(snapshotExitDrainTimeout)
			} else {
				resetTimer(exitDrainTimer, snapshotExitDrainTimeout)
			}
			exitDrainTimerCh = exitDrainTimer.C
		case chunk, ok := <-chunks:
			if !ok {
				chunks = nil
				if readDone == nil {
					done = true
				}
				continue
			}
			if len(chunk) == 0 {
				continue
			}
			var fullyWritten bool
			out, fullyWritten = appendBounded(out, chunk, maxBytes)
			if !fullyWritten {
				truncated = true
				done = true
				continue
			}
			if idleTimer == nil {
				idleTimer = time.NewTimer(s.cfg.SnapshotIdleTimeout)
				idleTimerCh = idleTimer.C
			} else {
				resetTimer(idleTimer, s.cfg.SnapshotIdleTimeout)
			}
			if processExited {
				if exitDrainTimer == nil {
					exitDrainTimer = time.NewTimer(snapshotExitDrainTimeout)
				} else {
					resetTimer(exitDrainTimer, snapshotExitDrainTimeout)
				}
				exitDrainTimerCh = exitDrainTimer.C
			}
		case err := <-readDone:
			if !isBenignSnapshotReadError(err) {
				streamErr = err
			}
			readDone = nil
			stopTimer(exitDrainTimer)
			exitDrainTimerCh = nil
			if chunks == nil {
				done = true
			}
		}
	}

	defer stopTimer(idleTimer)
	defer stopTimer(exitDrainTimer)
	close(stopRead)
	if ptyFile != nil {
		_ = ptyFile.Close()
	}

	if !processExited {
		if err := s.stopSnapshotProcess(waitDone, cmd.Process.Pid); err != nil {
			return snapshotCaptureResult{}, err
		}
	}

	if hardTimeout {
		return snapshotCaptureResult{}, unavailableError(apperr.CodeSnapshotTimeout, "snapshot capture timed out", map[string]any{
			"timeout": s.cfg.SnapshotHardTimeout.String(),
		}, nil)
	}
	if ctxCanceled {
		return snapshotCaptureResult{}, unavailableError(apperr.CodeSnapshotFailed, "snapshot capture canceled", map[string]any{"error": ctx.Err().Error()}, ctx.Err())
	}
	if streamErr != nil {
		return snapshotCaptureResult{}, internalError(apperr.CodeSnapshotFailed, "failed to read snapshot output", map[string]any{"error": streamErr.Error()}, streamErr)
	}

	return snapshotCaptureResult{
		Content:   out,
		Truncated: truncated,
	}, nil
}

func isBenignSnapshotReadError(err error) bool {
	return err == nil ||
		errors.Is(err, io.EOF) ||
		errors.Is(err, os.ErrClosed) ||
		errors.Is(err, syscall.EIO)
}

func (s *snapshotter) stopSnapshotProcess(waitDone <-chan error, pid int) error {
	if pid <= 0 {
		return internalError(apperr.CodeSnapshotFailed, "snapshot process PID is invalid", map[string]any{"pid": pid}, nil)
	}

	if waitDone == nil {
		return nil
	}
	if waitForExit(waitDone, 5*time.Millisecond) {
		return nil
	}

	if err := sendSnapshotSignal(pid, syscall.SIGINT); err != nil {
		return internalError(apperr.CodeSnapshotFailed, "failed to stop snapshot process with SIGINT", map[string]any{"error": err.Error()}, err)
	}
	if waitForExit(waitDone, s.cfg.SnapshotStopGrace) {
		return nil
	}

	if err := sendSnapshotSignal(pid, syscall.SIGKILL); err != nil {
		return internalError(apperr.CodeSnapshotFailed, "failed to stop snapshot process with SIGKILL", map[string]any{"error": err.Error()}, err)
	}
	if waitForExit(waitDone, s.cfg.SnapshotStopGrace) {
		return nil
	}

	return internalError(apperr.CodeSnapshotFailed, "snapshot process did not exit after SIGKILL", map[string]any{
		"grace_timeout": s.cfg.SnapshotStopGrace.String(),
	}, nil)
}

func sendSnapshotSignal(pid int, signal syscall.Signal) error {
	groupErr := killProcessGroup(pid, signal)
	if groupErr == nil || errors.Is(groupErr, syscall.ESRCH) {
		return nil
	}
	procErr := syscall.Kill(pid, signal)
	if procErr == nil || errors.Is(procErr, syscall.ESRCH) {
		return nil
	}
	return groupErr
}

func streamSnapshotChunks(ptyFile *os.File, chunks chan<- []byte, done chan<- error, stop <-chan struct{}, emulator *snapshotTerminalEmulator) {
	defer close(chunks)

	buf := make([]byte, 4096)
	for {
		n, err := ptyFile.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])

			if emulator != nil {
				responses := emulator.consume(chunk)
				for _, response := range responses {
					if len(response) == 0 {
						continue
					}
					if _, writeErr := ptyFile.Write(response); writeErr != nil {
						select {
						case done <- writeErr:
						default:
						}
						return
					}
				}
			}

			select {
			case <-stop:
				select {
				case done <- nil:
				default:
				}
				return
			case chunks <- chunk:
			}
		}

		if err != nil {
			select {
			case done <- err:
			default:
			}
			return
		}
	}
}

func appendBounded(base []byte, in []byte, maxBytes int) ([]byte, bool) {
	if len(base) >= maxBytes {
		return base, false
	}
	remaining := maxBytes - len(base)
	if len(in) <= remaining {
		return append(base, in...), true
	}
	return append(base, in[:remaining]...), false
}

func waitForExit(waitDone <-chan error, timeout time.Duration) bool {
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-waitDone:
		return true
	case <-timer.C:
		return false
	}
}

func stopTimer(timer *time.Timer) {
	if timer == nil {
		return
	}
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
}

func resetTimer(timer *time.Timer, duration time.Duration) {
	if timer == nil {
		return
	}
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	timer.Reset(duration)
}

type snapshotTerminalEmulator struct {
	pending []byte
}

type snapshotSequenceType int

const (
	snapshotSequenceUnknown snapshotSequenceType = iota
	snapshotSequenceCSI
	snapshotSequenceOSC
)

func newSnapshotTerminalEmulator() *snapshotTerminalEmulator {
	return &snapshotTerminalEmulator{}
}

func (e *snapshotTerminalEmulator) consume(chunk []byte) [][]byte {
	if len(chunk) == 0 {
		return nil
	}

	data := make([]byte, len(e.pending)+len(chunk))
	copy(data, e.pending)
	copy(data[len(e.pending):], chunk)
	e.pending = e.pending[:0]

	var responses [][]byte
	for i := 0; i < len(data); i++ {
		if data[i] != 0x1b {
			continue
		}
		end, sequenceType, ok := parseTerminalSequence(data, i)
		if !ok {
			e.setPending(data[i:])
			break
		}
		if end == i {
			continue
		}
		var response []byte
		switch sequenceType {
		case snapshotSequenceCSI:
			response = terminalCSIQueryResponse(data[i:end])
		case snapshotSequenceOSC:
			response = terminalOSCQueryResponse(data[i:end])
		default:
			response = nil
		}
		if len(response) > 0 {
			copyResp := make([]byte, len(response))
			copy(copyResp, response)
			responses = append(responses, copyResp)
		}
		i = end - 1
	}

	return responses
}

func (e *snapshotTerminalEmulator) setPending(data []byte) {
	if len(data) <= maxSnapshotTerminalPendingBytes {
		e.pending = append(e.pending[:0], data...)
		return
	}
	e.pending = append(e.pending[:0], data[len(data)-maxSnapshotTerminalPendingBytes:]...)
}

func parseTerminalSequence(data []byte, escIdx int) (int, snapshotSequenceType, bool) {
	if escIdx < 0 || escIdx >= len(data) || data[escIdx] != 0x1b {
		return escIdx, snapshotSequenceUnknown, true
	}
	if escIdx+1 >= len(data) {
		return 0, snapshotSequenceUnknown, false
	}

	switch data[escIdx+1] {
	case '[':
		end, ok := parseCSISequence(data, escIdx)
		return end, snapshotSequenceCSI, ok
	case ']':
		end, ok := parseOSCSequence(data, escIdx)
		return end, snapshotSequenceOSC, ok
	default:
		return escIdx + 2, snapshotSequenceUnknown, true
	}
}

func parseCSISequence(data []byte, escIdx int) (int, bool) {
	if escIdx < 0 || escIdx >= len(data) || data[escIdx] != 0x1b {
		return escIdx, true
	}
	if escIdx+1 >= len(data) {
		return 0, false
	}
	if data[escIdx+1] != '[' {
		return escIdx + 1, true
	}

	for i := escIdx + 2; i < len(data); i++ {
		if data[i] >= 0x40 && data[i] <= 0x7e {
			return i + 1, true
		}
	}
	return 0, false
}

func parseOSCSequence(data []byte, escIdx int) (int, bool) {
	if escIdx < 0 || escIdx >= len(data) || data[escIdx] != 0x1b {
		return escIdx, true
	}
	if escIdx+1 >= len(data) {
		return 0, false
	}
	if data[escIdx+1] != ']' {
		return escIdx + 1, true
	}

	for i := escIdx + 2; i < len(data); i++ {
		switch data[i] {
		case 0x07:
			return i + 1, true
		case 0x1b:
			if i+1 >= len(data) {
				return 0, false
			}
			if data[i+1] == '\\' {
				return i + 2, true
			}
		}
	}

	return 0, false
}

func terminalCSIQueryResponse(seq []byte) []byte {
	if len(seq) < 3 || seq[0] != 0x1b || seq[1] != '[' {
		return nil
	}
	final := seq[len(seq)-1]
	params := seq[2 : len(seq)-1]

	switch final {
	case 'n':
		if isCSIParam(params, "6") || isCSIParam(params, "?6") {
			return cprResponse
		}
		if isCSIParam(params, "5") {
			return dsResponse
		}
	case 'c':
		if len(params) > 0 && params[0] == '>' {
			return sdaResponse
		}
		return daResponse
	case 'u':
		if isCSIParam(params, "?") {
			return kittyKeyboardResponse
		}
	}

	return nil
}

func terminalOSCQueryResponse(seq []byte) []byte {
	body := oscBody(seq)
	switch body {
	case "10;?":
		return osc10Response
	case "11;?":
		return osc11Response
	default:
		return nil
	}
}

func oscBody(seq []byte) string {
	if len(seq) < 4 || seq[0] != 0x1b || seq[1] != ']' {
		return ""
	}
	if seq[len(seq)-1] == 0x07 {
		return string(seq[2 : len(seq)-1])
	}
	if len(seq) >= 2 && seq[len(seq)-2] == 0x1b && seq[len(seq)-1] == '\\' {
		return string(seq[2 : len(seq)-2])
	}
	return ""
}

func isCSIParam(params []byte, want string) bool {
	if len(params) != len(want) {
		return false
	}
	for i := 0; i < len(want); i++ {
		if params[i] != want[i] {
			return false
		}
	}
	return true
}
