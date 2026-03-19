package ptyws

import "sync"

// ReplayBuffer stores the last N bytes for late websocket joiners.
type ReplayBuffer struct {
	maxBytes int

	mu   sync.Mutex
	data []byte
}

func NewReplayBuffer(maxBytes int) *ReplayBuffer {
	if maxBytes <= 0 {
		maxBytes = 1
	}
	return &ReplayBuffer{
		maxBytes: maxBytes,
		data:     make([]byte, 0, maxBytes),
	}
}

func (r *ReplayBuffer) Append(chunk []byte) {
	if len(chunk) == 0 {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.data = append(r.data, chunk...)
	if len(r.data) <= r.maxBytes {
		return
	}
	overflow := len(r.data) - r.maxBytes
	r.data = append([]byte(nil), r.data[overflow:]...)
}

func (r *ReplayBuffer) Bytes() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]byte, len(r.data))
	copy(out, r.data)
	return out
}
