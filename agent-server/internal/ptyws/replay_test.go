package ptyws

import "testing"

// TestNewReplayBufferEnforcesMinimumSize verifies zero/negative sizes are clamped to a one-byte buffer.
func TestNewReplayBufferEnforcesMinimumSize(t *testing.T) {
	buf := NewReplayBuffer(0)
	buf.Append([]byte("ab"))
	if got := string(buf.Bytes()); got != "b" {
		t.Fatalf("Bytes() = %q, want %q", got, "b")
	}
}

// TestReplayBufferSlidingWindow verifies appended output is truncated to the most recent max-bytes window.
func TestReplayBufferSlidingWindow(t *testing.T) {
	buf := NewReplayBuffer(5)
	buf.Append([]byte("abc"))
	buf.Append([]byte("def"))
	if got := string(buf.Bytes()); got != "bcdef" {
		t.Fatalf("Bytes() = %q, want %q", got, "bcdef")
	}
}

// TestReplayBufferIgnoresEmptyAppend verifies empty chunks do not change buffered replay state.
func TestReplayBufferIgnoresEmptyAppend(t *testing.T) {
	buf := NewReplayBuffer(4)
	buf.Append(nil)
	buf.Append([]byte{})
	if got := string(buf.Bytes()); got != "" {
		t.Fatalf("Bytes() = %q, want empty", got)
	}
}

// TestReplayBufferBytesReturnsCopy verifies Bytes returns a defensive copy that cannot mutate internal state.
func TestReplayBufferBytesReturnsCopy(t *testing.T) {
	buf := NewReplayBuffer(8)
	buf.Append([]byte("payload"))
	out := buf.Bytes()
	out[0] = 'X'

	if got := string(buf.Bytes()); got != "payload" {
		t.Fatalf("buffer was modified through returned slice: %q", got)
	}
}
