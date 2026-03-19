package run

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"

	"github.com/marginlab/margin-eval/agent-server/internal/agentruntime"
	"github.com/marginlab/margin-eval/agent-server/internal/apperr"
	"github.com/marginlab/margin-eval/agent-server/internal/config"
)

func TestSnapshotterCaptureIdle(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("snapshot PTY tests are unix-only")
	}

	root := t.TempDir()
	scriptPath := filepath.Join(root, "snapshot.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\nprintf '\\033[31mhello\\033[0m'\nsleep 1\n"), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}

	s := newSnapshotter(config.Config{
		SnapshotMaxBytes:    2 * 1024 * 1024,
		SnapshotHardTimeout: 2 * time.Second,
		SnapshotIdleTimeout: 1 * time.Second,
		SnapshotStopGrace:   300 * time.Millisecond,
	})

	out, err := s.Capture(context.Background(), snapshotCaptureInput{
		ExecSpec: agentruntime.ExecSpec{
			Path: scriptPath,
			Dir:  root,
		},
		PTY: PTYSize{Cols: 100, Rows: 30},
	})
	if err != nil {
		t.Fatalf("Capture() error = %v", err)
	}
	if len(out.Content) == 0 {
		t.Fatalf("snapshot content should not be empty")
	}
	if out.Truncated {
		t.Fatalf("snapshot should not be truncated")
	}
}

func TestSnapshotterCaptureTruncatesAtMaxBytes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("snapshot PTY tests are unix-only")
	}

	root := t.TempDir()
	scriptPath := filepath.Join(root, "snapshot.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\nwhile :; do printf 'abcdefgh'; done\n"), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}

	s := newSnapshotter(config.Config{
		SnapshotMaxBytes:    64,
		SnapshotHardTimeout: 2 * time.Second,
		SnapshotIdleTimeout: 1 * time.Second,
		SnapshotStopGrace:   300 * time.Millisecond,
	})

	out, err := s.Capture(context.Background(), snapshotCaptureInput{
		ExecSpec: agentruntime.ExecSpec{
			Path: scriptPath,
			Dir:  root,
		},
		PTY: PTYSize{Cols: 100, Rows: 30},
	})
	if err != nil {
		t.Fatalf("Capture() error = %v", err)
	}
	if !out.Truncated {
		t.Fatalf("snapshot should be truncated")
	}
	if len(out.Content) != 64 {
		t.Fatalf("content size = %d, want 64", len(out.Content))
	}
}

func TestSnapshotterCaptureHardTimeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("snapshot PTY tests are unix-only")
	}

	root := t.TempDir()
	scriptPath := filepath.Join(root, "snapshot.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\nwhile :; do printf 'x'; sleep 0.05; done\n"), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}

	s := newSnapshotter(config.Config{
		SnapshotMaxBytes:    2 * 1024 * 1024,
		SnapshotHardTimeout: 220 * time.Millisecond,
		SnapshotIdleTimeout: 5 * time.Second,
		SnapshotStopGrace:   300 * time.Millisecond,
	})

	_, err := s.Capture(context.Background(), snapshotCaptureInput{
		ExecSpec: agentruntime.ExecSpec{
			Path: scriptPath,
			Dir:  root,
		},
		PTY: PTYSize{Cols: 100, Rows: 30},
	})
	if err == nil {
		t.Fatalf("Capture() expected timeout error")
	}

	apiErr, ok := apperr.As(mapRunError(err))
	if !ok {
		t.Fatalf("expected API error, got %T (%v)", err, err)
	}
	if apiErr.Code != apperr.CodeSnapshotTimeout {
		t.Fatalf("error code = %q, want %q", apiErr.Code, apperr.CodeSnapshotTimeout)
	}
}

func TestSnapshotTerminalEmulatorRespondsToCodexStartupQueries(t *testing.T) {
	emulator := newSnapshotTerminalEmulator()
	input := []byte(
		"\x1b[6n" +
			"\x1b[?u" +
			"\x1b[c" +
			"\x1b]10;?\x1b\\" +
			"\x1b]11;?\x1b\\",
	)

	responses := emulator.consume(input)
	want := [][]byte{
		cprResponse,
		kittyKeyboardResponse,
		daResponse,
		osc10Response,
		osc11Response,
	}

	if len(responses) != len(want) {
		t.Fatalf("response count = %d, want %d", len(responses), len(want))
	}
	for i := range want {
		if !bytes.Equal(responses[i], want[i]) {
			t.Fatalf("response[%d] = %q, want %q", i, responses[i], want[i])
		}
	}
}

func TestSnapshotTerminalEmulatorHandlesSplitSequences(t *testing.T) {
	emulator := newSnapshotTerminalEmulator()

	if got := emulator.consume([]byte("\x1b]10;?")); len(got) != 0 {
		t.Fatalf("unexpected OSC response before terminator: %q", got)
	}
	oscResponses := emulator.consume([]byte("\x1b\\"))
	if len(oscResponses) != 1 || !bytes.Equal(oscResponses[0], osc10Response) {
		t.Fatalf("OSC split response = %q, want %q", oscResponses, osc10Response)
	}

	if got := emulator.consume([]byte("\x1b[?")); len(got) != 0 {
		t.Fatalf("unexpected CSI response before final byte: %q", got)
	}
	csiResponses := emulator.consume([]byte("u"))
	if len(csiResponses) != 1 || !bytes.Equal(csiResponses[0], kittyKeyboardResponse) {
		t.Fatalf("CSI split response = %q, want %q", csiResponses, kittyKeyboardResponse)
	}
}

func TestIsBenignSnapshotReadError(t *testing.T) {
	if !isBenignSnapshotReadError(io.EOF) {
		t.Fatalf("io.EOF should be benign")
	}
	if !isBenignSnapshotReadError(os.ErrClosed) {
		t.Fatalf("os.ErrClosed should be benign")
	}
	if !isBenignSnapshotReadError(syscall.EIO) {
		t.Fatalf("syscall.EIO should be benign")
	}
	if isBenignSnapshotReadError(context.DeadlineExceeded) {
		t.Fatalf("unexpected benign error classification")
	}
}
